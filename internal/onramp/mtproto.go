package onramp

import (
	"bufio"
	"container/list"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"regexp"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	tg "github.com/gotd/td/tg"

	"ovd/internal/domain"
	"ovd/internal/metrics"
	"ovd/internal/pkg/breaker"
)

var (
	markdownLinkRe  = regexp.MustCompile(`\[.*?\]\((.*?)\)`)
	invisibleCharRe = regexp.MustCompile(`[\x00-\x1F\x7F]`)
)

func SanitizeChannelInput(raw string) string {
	cleaned := markdownLinkRe.ReplaceAllString(raw, "$1")
	cleaned = invisibleCharRe.ReplaceAllString(cleaned, "")
	return strings.TrimSpace(cleaned)
}

type MTProtoSource struct {
	apiID       int
	apiHash     string
	phone       string
	sessionFile string
	log         *slog.Logger

	allowedMu sync.RWMutex
	allowed   map[int64]string // channelID -> username
	tgAPI     atomic.Pointer[tg.Client]
	breaker   *breaker.Breaker
	grouped   *groupedSeenCache
}

// groupedSeenTTL and groupedSeenMax bound the album dedup cache: album
// frames arrive within seconds, so 10 minutes is generous, and 4096
// entries cap memory for 24/7 operation.
const (
	groupedSeenTTL = 10 * time.Minute
	groupedSeenMax = 4096
)

// groupedSeenCache remembers album GroupedIDs already emitted so only the
// first captioned frame of a Telegram album becomes a signal. Entries are
// ordered by last-seen time (LRU); expired entries form a suffix and are
// evicted on write, keeping the cache bounded without a background sweeper.
type groupedSeenCache struct {
	ttl   time.Duration
	max   int
	mu    sync.Mutex
	lru   *list.List // front = most recently seen
	items map[int64]*list.Element
}

type groupedSeenEntry struct {
	gid int64
	at  time.Time
}

func newGroupedSeenCache(ttl time.Duration, max int) *groupedSeenCache {
	return &groupedSeenCache{
		ttl:   ttl,
		max:   max,
		lru:   list.New(),
		items: make(map[int64]*list.Element),
	}
}

// checkAndRecord reports whether gid was already seen within the TTL
// window; otherwise it records gid and returns false.
func (c *groupedSeenCache) checkAndRecord(gid int64, now time.Time) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for back := c.lru.Back(); back != nil; back = c.lru.Back() {
		entry := back.Value.(*groupedSeenEntry)
		if now.Sub(entry.at) <= c.ttl {
			break
		}
		c.lru.Remove(back)
		delete(c.items, entry.gid)
	}

	if el, ok := c.items[gid]; ok {
		entry := el.Value.(*groupedSeenEntry)
		entry.at = now
		c.lru.MoveToFront(el)
		return true
	}

	if len(c.items) >= c.max {
		if back := c.lru.Back(); back != nil {
			entry := back.Value.(*groupedSeenEntry)
			c.lru.Remove(back)
			delete(c.items, entry.gid)
		}
	}
	c.items[gid] = c.lru.PushFront(&groupedSeenEntry{gid: gid, at: now})
	return false
}

func NewMTProtoSource(apiID int, apiHash, phone, sessionFile string, log *slog.Logger) *MTProtoSource {
	return &MTProtoSource{
		apiID:       apiID,
		apiHash:     apiHash,
		phone:       phone,
		sessionFile: sessionFile,
		log:         log.With("component", "mtproto_onramp"),
		allowed:     make(map[int64]string),
		breaker:     breaker.New(breaker.Options{}),
		grouped:     newGroupedSeenCache(groupedSeenTTL, groupedSeenMax),
	}
}

func (m *MTProtoSource) Name() string {
	return "telegram_mtproto"
}

func (m *MTProtoSource) Start(ctx context.Context, out chan<- domain.RawSignal) error {
	dispatcher := tg.NewUpdateDispatcher()

	dispatcher.OnBefore(func(ctx context.Context, _ tg.Entities, u tg.UpdateClass) error {
		m.log.Info("RAW UPDATE", "type", fmt.Sprintf("%T", u))
		return nil
	})

	dispatcher.OnNewMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewMessage) error {
		m.handleMessage(ctx, u.Message, out)
		return nil
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		m.log.Info("INCOMING CHANNEL EVENT", "type", fmt.Sprintf("%T", u.Message), "peer_id", peerID(u.Message))
		m.handleMessage(ctx, u.Message, out)
		return nil
	})

	client := telegram.NewClient(m.apiID, m.apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: m.sessionFile},
		UpdateHandler:  dispatcher,
	})

	m.log.Info("mtproto client starting", "session", m.sessionFile)

	return m.runWithBreaker(ctx, client, out)
}

// mtprotoReconnectDelay is the pause between session attempts after a
// failure or while the circuit breaker is open.
const mtprotoReconnectDelay = 10 * time.Second

// runWithBreaker keeps the MTProto session alive across transient
// failures. Each client.Run attempt is wrapped in the circuit breaker:
// repeated session failures open the circuit and reconnects are paused
// until the cooldown probe succeeds.
func (m *MTProtoSource) runWithBreaker(ctx context.Context, client *telegram.Client, out chan<- domain.RawSignal) error {
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := m.breaker.Execute(func() error {
			runErr := m.runSession(ctx, client, out)
			if errors.Is(runErr, context.Canceled) || errors.Is(runErr, context.DeadlineExceeded) {
				return nil
			}
			return runErr
		})
		if ctx.Err() != nil {
			return nil
		}
		if err != nil {
			if errors.Is(err, breaker.ErrOpen) {
				m.log.Warn("mtproto circuit open, pausing reconnects")
			} else {
				m.log.Error("mtproto session terminated", "err", err)
			}
		}
		// Always pace reconnect attempts: a fast nil return must not
		// spin the loop, and the open-circuit pause doubles as backoff.
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(mtprotoReconnectDelay):
		}
	}
}

func (m *MTProtoSource) runSession(ctx context.Context, client *telegram.Client, out chan<- domain.RawSignal) error {
	return client.Run(ctx, func(ctx context.Context) error {
		status, err := client.Auth().Status(ctx)
		if err != nil {
			return fmt.Errorf("check auth status: %w", err)
		}

		if !status.Authorized {
			m.log.Info("mtproto auth required, starting terminal auth flow", "phone", m.phone)
			flow := auth.NewFlow(
				m.newTerminalAuth(),
				auth.SendCodeOptions{},
			)
			if err := client.Auth().IfNecessary(ctx, flow); err != nil {
				return fmt.Errorf("auth flow failed: %w", err)
			}
			m.log.Info("mtproto auth completed")
		} else {
			m.log.Info("mtproto session authorized")
		}

		if _, err := client.Self(ctx); err != nil {
			return fmt.Errorf("fetch self: %w", err)
		}

		m.tgAPI.Store(client.API())
		if err := m.resolveTargets(ctx, client.API()); err != nil {
			return fmt.Errorf("resolve target channels: %w", err)
		}

		m.log.Info("mtproto updates listener started")

		<-ctx.Done()
		return nil
	})
}

func (m *MTProtoSource) handleMessage(ctx context.Context, msg tg.MessageClass, out chan<- domain.RawSignal) {
	m.log.Info("INCOMING EVENT", "type", fmt.Sprintf("%T", msg), "peer_id", peerID(msg))

	message, ok := msg.(*tg.Message)
	if !ok {
		return
	}

	if !m.allowedPeer(message.GetPeerID()) {
		return
	}

	text := message.GetMessage()
	if strings.TrimSpace(text) == "" {
		metrics.IncTGEmptyMediaDrop()
		return
	}

	// Album frames share GroupedID; only the first captioned frame is
	// emitted. Empty frames return above, so they never reserve the ID.
	if gid, ok := message.GetGroupedID(); ok && gid != 0 && m.grouped.checkAndRecord(gid, time.Now()) {
		metrics.IncTGAlbumDupDrop()
		m.log.Debug("album duplicate dropped", "grouped_id", gid)
		return
	}

	sig := domain.RawSignal{
		ID:         uuid.NewString(),
		Source:     "telegram_mtproto",
		URL:        messageURL(message),
		Text:       text,
		ReceivedAt: time.Now().UTC(),
		JobType:    "freelance",
	}

	select {
	case out <- sig:
		m.log.Debug("signal pushed", "id", sig.ID)
	case <-ctx.Done():
	}
}

// allowedPeer reports whether the peer is a whitelisted channel.
// Personal DMs (PeerUser) and basic groups (PeerChat) are always rejected.
func (m *MTProtoSource) allowedPeer(peer tg.PeerClass) bool {
	ch, ok := peer.(*tg.PeerChannel)
	if !ok || ch.ChannelID == 0 {
		return false
	}
	m.allowedMu.RLock()
	defer m.allowedMu.RUnlock()
	_, ok = m.allowed[ch.ChannelID]
	return ok
}

// AddTarget resolves username to a channel ID and whitelists it.
// Safe for concurrent use with the update handlers.
func (m *MTProtoSource) AddTarget(ctx context.Context, username string) (int64, error) {
	api := m.tgAPI.Load()
	if api == nil {
		return 0, fmt.Errorf("mtproto client is not ready yet")
	}
	return m.addTarget(ctx, api, normalizeTargetName(username))
}

func (m *MTProtoSource) addTarget(ctx context.Context, api *tg.Client, name string) (int64, error) {
	if name == "" {
		return 0, fmt.Errorf("empty target")
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: name})
	if err != nil {
		return 0, fmt.Errorf("resolve %q: %w", name, err)
	}
	ch, ok := res.Peer.(*tg.PeerChannel)
	if !ok || ch.ChannelID == 0 {
		return 0, fmt.Errorf("%q is not a channel", name)
	}
	m.allowedMu.Lock()
	m.allowed[ch.ChannelID] = name
	m.allowedMu.Unlock()
	m.log.Info("whitelisted channel", "username", name, "channel_id", ch.ChannelID)
	return ch.ChannelID, nil
}

// RemoveTarget resolves username and removes it from the whitelist.
func (m *MTProtoSource) RemoveTarget(ctx context.Context, username string) (bool, error) {
	api := m.tgAPI.Load()
	if api == nil {
		return false, fmt.Errorf("mtproto client is not ready yet")
	}
	name := normalizeTargetName(username)
	if name == "" {
		return false, fmt.Errorf("empty target")
	}
	res, err := api.ContactsResolveUsername(ctx, &tg.ContactsResolveUsernameRequest{Username: name})
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", name, err)
	}
	ch, ok := res.Peer.(*tg.PeerChannel)
	if !ok || ch.ChannelID == 0 {
		return false, fmt.Errorf("%q is not a channel", name)
	}
	m.allowedMu.Lock()
	_, existed := m.allowed[ch.ChannelID]
	delete(m.allowed, ch.ChannelID)
	m.allowedMu.Unlock()
	return existed, nil
}

// ListTargets returns sorted usernames of all whitelisted channels.
func (m *MTProtoSource) ListTargets() []string {
	m.allowedMu.RLock()
	defer m.allowedMu.RUnlock()
	names := make([]string, 0, len(m.allowed))
	for _, name := range m.allowed {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// resolveTargets reads TG_TARGET_CHANNELS (comma-separated usernames like
// @freelance_ru, golang_jobs or t.me/name) and resolves them to internal
// channel IDs via contacts.resolveUsername.
func (m *MTProtoSource) resolveTargets(ctx context.Context, api *tg.Client) error {
	names := parseTargetChannels(os.Getenv("TG_TARGET_CHANNELS"))
	if len(names) == 0 {
		m.log.Warn("TG_TARGET_CHANNELS is empty, all telegram updates will be dropped")
		return nil
	}

	resolved := 0
	for _, name := range names {
		if _, err := m.addTarget(ctx, api, name); err != nil {
			m.log.Warn("resolve target failed", "target", name, "err", err)
			continue
		}
		resolved++
	}

	m.log.Info("mtproto whitelist ready", "targets", len(names), "resolved", resolved)
	return nil
}

func parseTargetChannels(raw string) []string {
	seen := make(map[string]struct{})
	var names []string
	for _, part := range strings.FieldsFunc(raw, func(r rune) bool {
		return r == ',' || r == ' ' || r == '\n' || r == '\t' || r == ';'
	}) {
		name := normalizeTargetName(part)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		names = append(names, name)
	}
	return names
}

func normalizeTargetName(raw string) string {
	name := strings.ToLower(SanitizeChannelInput(raw))
	if i := strings.IndexAny(name, "?#"); i >= 0 {
		name = name[:i]
	}
	name = strings.TrimPrefix(name, "https://")
	name = strings.TrimPrefix(name, "http://")
	name = strings.TrimPrefix(name, "www.")
	for _, prefix := range []string{"t.me/s/", "telegram.me/s/", "t.me/", "telegram.me/"} {
		if strings.HasPrefix(name, prefix) {
			name = strings.TrimPrefix(name, prefix)
			break
		}
	}
	name = strings.TrimPrefix(name, "@")
	return strings.TrimSuffix(name, "/")
}

func peerID(msg tg.MessageClass) any {
	if ne, ok := msg.AsNotEmpty(); ok {
		return ne.GetPeerID()
	}
	return nil
}

func messageURL(msg *tg.Message) string {
	peer, ok := msg.GetPeerID().(*tg.PeerChannel)
	if !ok || peer.ChannelID == 0 {
		return ""
	}
	return fmt.Sprintf("https://t.me/c/%d/%d", peer.ChannelID, msg.GetID())
}

// terminalAuth implements auth.UserAuthenticator, prompting for code and
// 2FA password on stdin during the first (unauthorized) run.
type terminalAuth struct {
	phone string
	log   *slog.Logger
	in    *bufio.Reader
}

func (m *MTProtoSource) newTerminalAuth() *terminalAuth {
	return &terminalAuth{
		phone: m.phone,
		log:   m.log,
		in:    bufio.NewReader(os.Stdin),
	}
}

func (a *terminalAuth) Phone(ctx context.Context) (string, error) {
	return a.phone, nil
}

func (a *terminalAuth) Password(ctx context.Context) (string, error) {
	a.log.Info("2FA password required, enter it in console")
	return a.readLine("password")
}

func (a *terminalAuth) Code(ctx context.Context, sentCode *tg.AuthSentCode) (string, error) {
	a.log.Info("auth code sent, enter it in console")
	return a.readLine("code")
}

func (a *terminalAuth) AcceptTermsOfService(ctx context.Context, tos tg.HelpTermsOfService) error {
	return nil
}

func (a *terminalAuth) SignUp(ctx context.Context) (auth.UserInfo, error) {
	return auth.UserInfo{}, fmt.Errorf("sign up is not supported by mtproto onramp")
}

func (a *terminalAuth) readLine(prompt string) (string, error) {
	line, err := a.in.ReadString('\n')
	if err != nil && line == "" {
		return "", fmt.Errorf("read %s from stdin: %w", prompt, err)
	}
	return strings.TrimSpace(line), nil
}

var _ auth.UserAuthenticator = (*terminalAuth)(nil)
