package onramp

import (
	"bufio"
	"context"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/gotd/td/session"
	"github.com/gotd/td/telegram"
	"github.com/gotd/td/telegram/auth"
	tg "github.com/gotd/td/tg"

	"ovd/internal/domain"
)

type MTProtoSource struct {
	apiID       int
	apiHash     string
	phone       string
	sessionFile string
	log         *slog.Logger
}

func NewMTProtoSource(apiID int, apiHash, phone, sessionFile string, log *slog.Logger) *MTProtoSource {
	return &MTProtoSource{
		apiID:       apiID,
		apiHash:     apiHash,
		phone:       phone,
		sessionFile: sessionFile,
		log:         log.With("component", "mtproto_onramp"),
	}
}

func (m *MTProtoSource) Start(ctx context.Context, out chan<- domain.RawSignal) error {
	dispatcher := tg.NewUpdateDispatcher()

	dispatcher.OnNewMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewMessage) error {
		m.handleMessage(ctx, u.Message, out)
		return nil
	})

	dispatcher.OnNewChannelMessage(func(ctx context.Context, _ tg.Entities, u *tg.UpdateNewChannelMessage) error {
		m.handleMessage(ctx, u.Message, out)
		return nil
	})

	client := telegram.NewClient(m.apiID, m.apiHash, telegram.Options{
		SessionStorage: &session.FileStorage{Path: m.sessionFile},
		UpdateHandler:  dispatcher,
	})

	m.log.Info("mtproto client starting", "session", m.sessionFile)

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

		m.log.Info("mtproto updates listener started")

		<-ctx.Done()
		return nil
	})
}

func (m *MTProtoSource) handleMessage(ctx context.Context, msg tg.MessageClass, out chan<- domain.RawSignal) {
	message, ok := msg.(*tg.Message)
	if !ok {
		return
	}

	text := message.GetMessage()
	if text == "" {
		return
	}

	sig := domain.RawSignal{
		ID:         uuid.NewString(),
		Source:     "telegram_mtproto",
		URL:        messageURL(message),
		Text:       text,
		ReceivedAt: time.Now().UTC(),
	}

	select {
	case out <- sig:
		m.log.Debug("signal pushed", "id", sig.ID)
	case <-ctx.Done():
	}
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
