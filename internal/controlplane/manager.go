package controlplane

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"ovd/internal/domain"
	"ovd/internal/onramp"
)

const tgTargetRemoveTimeout = 10 * time.Second

// tgTargetRetryInterval is a var so tests can shrink the backoff.
var tgTargetRetryInterval = 5 * time.Second

// TGWhitelist manages Telegram channel targets on the shared MTProto
// session. *onramp.MTProtoSource satisfies it.
type TGWhitelist interface {
	AddTarget(ctx context.Context, username string) (int64, error)
	RemoveTarget(ctx context.Context, username string) (bool, error)
}

// ConnectorFactory builds a signal-emitting connector for a persisted
// source row. It is used for 'hh' and 'reddit' rows; 'tg' rows are
// handled through TGWhitelist instead.
type ConnectorFactory func(src Source) (onramp.Connector, error)

// DefaultConnectorFactory maps source types to their onramp connectors.
func DefaultConnectorFactory() ConnectorFactory {
	return func(src Source) (onramp.Connector, error) {
		switch src.Type {
		case SourceTypeHH:
			return onramp.NewHHConnectorFor(src.Target), nil
		case SourceTypeReddit:
			return onramp.NewRedditConnectorFor(src.Target), nil
		default:
			return nil, fmt.Errorf("no connector factory for source type %q", src.Type)
		}
	}
}

// runningSource tracks one live connector goroutine.
type runningSource struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// SourceManager reconciles the sources table with running connector
// goroutines. Every mutation (Hydrate/Create/SetEnabled/Delete) is
// serialized through mu for the running map; connector goroutines
// themselves are only ever cancelled, never touched concurrently.
type SourceManager struct {
	ctx     context.Context
	cancel  context.CancelFunc
	repo    SourceRepo
	out     chan<- domain.RawSignal
	tg      TGWhitelist
	factory ConnectorFactory
	log     *slog.Logger

	mu      sync.Mutex
	running map[string]*runningSource
	wg      sync.WaitGroup
	stopped bool
}

// NewSourceManager creates a manager bound to parent. tg may be nil,
// which disables 'tg' sources; factory may be nil to use
// DefaultConnectorFactory.
func NewSourceManager(parent context.Context, repo SourceRepo, out chan<- domain.RawSignal, tg TGWhitelist, factory ConnectorFactory, log *slog.Logger) *SourceManager {
	if factory == nil {
		factory = DefaultConnectorFactory()
	}
	if log == nil {
		log = slog.Default()
	}
	ctx, cancel := context.WithCancel(parent)
	return &SourceManager{
		ctx:     ctx,
		cancel:  cancel,
		repo:    repo,
		out:     out,
		tg:      tg,
		factory: factory,
		log:     log.With("component", "source_manager"),
		running: make(map[string]*runningSource),
	}
}

// Hydrate starts every enabled source found in the repository. Per-source
// failures are logged and skipped so one bad row cannot block startup.
func (m *SourceManager) Hydrate(ctx context.Context) error {
	sources, err := m.repo.ListSources(ctx)
	if err != nil {
		return fmt.Errorf("hydrate sources: %w", err)
	}
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if err := m.start(src); err != nil {
			m.log.Error("hydrate: failed to start source", "id", src.ID, "type", src.Type, "err", err)
		}
	}
	return nil
}

// Create persists a new source and starts it when enabled (new rows are
// always created enabled). On start failure the row is rolled back.
func (m *SourceManager) Create(ctx context.Context, p CreateSourcePayload) (Source, error) {
	src, err := m.repo.CreateSource(ctx, p)
	if err != nil {
		return Source{}, err
	}
	if src.Enabled {
		if err := m.start(src); err != nil {
			if derr := m.repo.DeleteSource(ctx, src.ID); derr != nil {
				m.log.Error("rollback delete failed", "id", src.ID, "err", derr)
			}
			return Source{}, fmt.Errorf("start source %q: %w", src.ID, err)
		}
	}
	return src, nil
}

// SetEnabled toggles a source and starts/stops its connector accordingly.
// On start failure the row is reverted to disabled.
func (m *SourceManager) SetEnabled(ctx context.Context, id string, enabled bool) (Source, error) {
	src, err := m.repo.SetSourceEnabled(ctx, id, enabled)
	if err != nil {
		return Source{}, err
	}
	if enabled {
		if err := m.start(src); err != nil {
			if _, rerr := m.repo.SetSourceEnabled(ctx, id, false); rerr != nil {
				m.log.Error("revert disable failed", "id", id, "err", rerr)
			}
			return Source{}, fmt.Errorf("start source %q: %w", id, err)
		}
	} else {
		m.stop(id)
	}
	return src, nil
}

// Delete removes the row and stops the connector if running.
func (m *SourceManager) Delete(ctx context.Context, id string) error {
	if err := m.repo.DeleteSource(ctx, id); err != nil {
		return err
	}
	m.stop(id)
	return nil
}

// Shutdown cancels every running connector and waits for all goroutines
// to exit. It is idempotent.
func (m *SourceManager) Shutdown() {
	m.mu.Lock()
	if !m.stopped {
		m.stopped = true
		m.cancel()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

// start registers and launches the connector goroutine for src.
// It is a no-op when the source is already running.
func (m *SourceManager) start(src Source) error {
	var conn onramp.Connector
	if src.Type == SourceTypeTG {
		if m.tg == nil {
			return errors.New("tg source requires a telegram whitelist")
		}
	} else {
		c, err := m.factory(src)
		if err != nil {
			return fmt.Errorf("build connector: %w", err)
		}
		conn = c
	}

	m.mu.Lock()
	if m.stopped {
		m.mu.Unlock()
		return errors.New("source manager is shut down")
	}
	if _, ok := m.running[src.ID]; ok {
		m.mu.Unlock()
		return nil
	}
	ctx, cancel := context.WithCancel(m.ctx)
	rs := &runningSource{cancel: cancel, done: make(chan struct{})}
	m.running[src.ID] = rs
	m.wg.Add(1)
	m.mu.Unlock()

	go m.runSource(ctx, rs, src, conn)
	return nil
}

// stop cancels the connector for id and waits for its goroutine to exit.
func (m *SourceManager) stop(id string) {
	m.mu.Lock()
	rs := m.running[id]
	if rs != nil {
		delete(m.running, id)
	}
	m.mu.Unlock()
	if rs == nil {
		return
	}
	rs.cancel()
	<-rs.done
}

func (m *SourceManager) runSource(ctx context.Context, rs *runningSource, src Source, conn onramp.Connector) {
	defer m.wg.Done()
	defer close(rs.done)

	log := m.log.With("source_id", src.ID, "source_type", src.Type, "target", src.Target)
	if src.Type == SourceTypeTG {
		m.runTGSource(ctx, log, src)
		return
	}
	log.Info("connector started", "connector", conn.Name())
	if err := conn.Start(ctx, m.out); err != nil && ctx.Err() == nil {
		log.Error("connector terminated", "connector", conn.Name(), "err", err)
	}
}

// runTGSource registers the source target on the shared MTProto
// whitelist. The MTProto session may still be handshaking, so
// registration retries until it succeeds or the source is stopped.
// On stop the target is removed best-effort.
func (m *SourceManager) runTGSource(ctx context.Context, log *slog.Logger, src Source) {
	target := strings.TrimSpace(src.Target)
	for {
		_, err := m.tg.AddTarget(ctx, target)
		if err == nil {
			log.Info("tg target whitelisted", "target", target)
			break
		}
		if ctx.Err() != nil {
			return
		}
		log.Warn("tg target registration pending", "target", target, "err", err)
		select {
		case <-ctx.Done():
			return
		case <-time.After(tgTargetRetryInterval):
		}
	}

	<-ctx.Done()

	rmCtx, cancel := context.WithTimeout(context.Background(), tgTargetRemoveTimeout)
	defer cancel()
	if _, err := m.tg.RemoveTarget(rmCtx, target); err != nil {
		log.Warn("tg target removal failed", "target", target, "err", err)
	}
}
