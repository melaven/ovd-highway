package controlplane

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"

	"ovd/internal/domain"
	"ovd/internal/onramp"
)

func discardLog() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// ---- fakeRepo: in-memory SourceRepo ----

type fakeRepo struct {
	mu       sync.Mutex
	sources  map[string]Source
	settings EngineSettings
}

func newFakeRepo() *fakeRepo {
	return &fakeRepo{sources: make(map[string]Source)}
}

func (r *fakeRepo) ListSources(context.Context) ([]Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]Source, 0, len(r.sources))
	for _, s := range r.sources {
		out = append(out, s)
	}
	return out, nil
}

func (r *fakeRepo) CreateSource(_ context.Context, p CreateSourcePayload) (Source, error) {
	if err := p.Validate(); err != nil {
		return Source{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	s := Source{
		ID:        uuid.NewString(),
		Type:      p.Type,
		Target:    strings.TrimSpace(p.Target),
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}
	r.sources[s.ID] = s
	return s, nil
}

func (r *fakeRepo) SetSourceEnabled(_ context.Context, id string, enabled bool) (Source, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sources[id]
	if !ok {
		return Source{}, ErrNotFound
	}
	s.Enabled = enabled
	r.sources[id] = s
	return s, nil
}

func (r *fakeRepo) DeleteSource(_ context.Context, id string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if _, ok := r.sources[id]; !ok {
		return ErrNotFound
	}
	delete(r.sources, id)
	return nil
}

func (r *fakeRepo) GetSettings(context.Context) (EngineSettings, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.settings, nil
}

func (r *fakeRepo) PutSettings(_ context.Context, s EngineSettings) (EngineSettings, error) {
	if err := s.Validate(); err != nil {
		return EngineSettings{}, err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.settings = s
	return s, nil
}

func (r *fakeRepo) seed(s Source) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.sources[s.ID] = s
}

func (r *fakeRepo) get(id string) (Source, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	s, ok := r.sources[id]
	return s, ok
}

// ---- fakeConnector / fakeFactory ----

type fakeConnector struct {
	name    string
	started chan struct{}
	stopped chan struct{}
}

func (f *fakeConnector) Name() string { return f.name }

func (f *fakeConnector) Start(ctx context.Context, _ chan<- domain.RawSignal) error {
	close(f.started)
	<-ctx.Done()
	close(f.stopped)
	return nil
}

type fakeFactory struct {
	mu         sync.Mutex
	connectors []*fakeConnector
	err        error
}

func (f *fakeFactory) build(src Source) (onramp.Connector, error) {
	if f.err != nil {
		return nil, f.err
	}
	c := &fakeConnector{
		name:    string(src.Type) + ":" + src.Target,
		started: make(chan struct{}),
		stopped: make(chan struct{}),
	}
	f.mu.Lock()
	f.connectors = append(f.connectors, c)
	f.mu.Unlock()
	return c, nil
}

// latestByName returns the most recently built connector with name.
func (f *fakeFactory) latestByName(name string) *fakeConnector {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := len(f.connectors) - 1; i >= 0; i-- {
		if f.connectors[i].name == name {
			return f.connectors[i]
		}
	}
	return nil
}

func (f *fakeFactory) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.connectors)
}

// ---- fakeTG ----

// fakeTG simulates the shared MTProto whitelist. failNext makes the
// first N AddTarget calls fail so the retry path is exercised.
type fakeTG struct {
	mu       sync.Mutex
	calls    int
	failNext int
	added    chan string
	removed  chan string
}

func newFakeTG() *fakeTG {
	return &fakeTG{
		added:   make(chan string, 16),
		removed: make(chan string, 16),
	}
}

func (f *fakeTG) AddTarget(_ context.Context, username string) (int64, error) {
	f.mu.Lock()
	f.calls++
	fail := f.calls <= f.failNext
	f.mu.Unlock()
	if fail {
		return 0, errors.New("mtproto client is not ready yet")
	}
	select {
	case f.added <- username:
	default:
	}
	return 1, nil
}

func (f *fakeTG) RemoveTarget(_ context.Context, username string) (bool, error) {
	select {
	case f.removed <- username:
	default:
	}
	return true, nil
}

// ---- helpers ----

func waitChan(t *testing.T, ch <-chan struct{}, what string) {
	t.Helper()
	select {
	case <-ch:
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
	}
}

func waitValue[T any](t *testing.T, ch <-chan T, what string) T {
	t.Helper()
	select {
	case v := <-ch:
		return v
	case <-time.After(2 * time.Second):
		t.Fatalf("timeout waiting for %s", what)
		var zero T
		return zero
	}
}

func testSource(id string, typ SourceType, target string, enabled bool) Source {
	return Source{
		ID:        id,
		Type:      typ,
		Target:    target,
		Enabled:   enabled,
		CreatedAt: time.Now().UTC(),
	}
}

// ---- tests ----

func TestSourceManagerHydrateLifecycle(t *testing.T) {
	oldInterval := tgTargetRetryInterval
	tgTargetRetryInterval = time.Millisecond
	defer func() { tgTargetRetryInterval = oldInterval }()

	repo := newFakeRepo()
	repo.seed(testSource("hh-1", SourceTypeHH, "golang", true))
	repo.seed(testSource("rd-1", SourceTypeReddit, "forhire", false))
	repo.seed(testSource("tg-1", SourceTypeTG, "@chan", true))

	tg := newFakeTG()
	factory := &fakeFactory{}
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, tg, factory.build, discardLog())
	defer mgr.Shutdown()

	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}

	hhConn := factory.latestByName("hh:golang")
	if hhConn == nil {
		t.Fatal("hh connector was not created")
	}
	waitChan(t, hhConn.started, "hh connector start")

	if rd := factory.latestByName("reddit:forhire"); rd != nil {
		t.Error("disabled reddit source should not have a connector")
	}

	if got := waitValue(t, tg.added, "tg target registration"); got != "@chan" {
		t.Fatalf("tg added target = %q, want @chan", got)
	}

	// Pause: connector stops, row stays disabled.
	src, err := mgr.SetEnabled(context.Background(), "hh-1", false)
	if err != nil {
		t.Fatalf("SetEnabled(false): %v", err)
	}
	if src.Enabled {
		t.Error("SetEnabled returned enabled source")
	}
	waitChan(t, hhConn.stopped, "hh connector stop")

	// Resume: a fresh connector instance starts.
	if _, err := mgr.SetEnabled(context.Background(), "hh-1", true); err != nil {
		t.Fatalf("SetEnabled(true): %v", err)
	}
	if factory.count() != 2 {
		t.Fatalf("factory builds = %d, want 2", factory.count())
	}
	hhConn2 := factory.latestByName("hh:golang")
	if hhConn2 == hhConn {
		t.Fatal("resume reused the stopped connector instance")
	}
	waitChan(t, hhConn2.started, "hh connector restart")

	// Delete: row removed and connector stopped.
	if err := mgr.Delete(context.Background(), "hh-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if _, ok := repo.get("hh-1"); ok {
		t.Error("deleted source still present in repo")
	}
	waitChan(t, hhConn2.stopped, "hh connector stop after delete")

	// Deleting again reports not found.
	if err := mgr.Delete(context.Background(), "hh-1"); !errors.Is(err, ErrNotFound) {
		t.Fatalf("second Delete err = %v, want ErrNotFound", err)
	}
}

func TestSourceManagerCreateStartsConnector(t *testing.T) {
	repo := newFakeRepo()
	factory := &fakeFactory{}
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, newFakeTG(), factory.build, discardLog())
	defer mgr.Shutdown()

	src, err := mgr.Create(context.Background(), CreateSourcePayload{Type: SourceTypeHH, Target: "devops"})
	if err != nil {
		t.Fatalf("Create: %v", err)
	}
	if !src.Enabled {
		t.Error("created source should be enabled")
	}
	conn := factory.latestByName("hh:devops")
	if conn == nil {
		t.Fatal("connector was not built for created source")
	}
	waitChan(t, conn.started, "created connector start")
}

func TestSourceManagerCreateRollbackOnStartFailure(t *testing.T) {
	repo := newFakeRepo()
	factory := &fakeFactory{err: errors.New("boom")}
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, newFakeTG(), factory.build, discardLog())
	defer mgr.Shutdown()

	_, err := mgr.Create(context.Background(), CreateSourcePayload{Type: SourceTypeHH, Target: "x"})
	if err == nil {
		t.Fatal("Create should fail when connector cannot start")
	}
	if sources, _ := repo.ListSources(context.Background()); len(sources) != 0 {
		t.Fatalf("repo has %d sources after rollback, want 0", len(sources))
	}
}

func TestSourceManagerTGRetryThenRemove(t *testing.T) {
	oldInterval := tgTargetRetryInterval
	tgTargetRetryInterval = time.Millisecond
	defer func() { tgTargetRetryInterval = oldInterval }()

	repo := newFakeRepo()
	repo.seed(testSource("tg-1", SourceTypeTG, "@slowchan", true))

	tg := newFakeTG()
	tg.failNext = 3
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, tg, (&fakeFactory{}).build, discardLog())
	defer mgr.Shutdown()

	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if got := waitValue(t, tg.added, "tg target registration after retries"); got != "@slowchan" {
		t.Fatalf("tg added target = %q, want @slowchan", got)
	}

	if err := mgr.Delete(context.Background(), "tg-1"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if got := waitValue(t, tg.removed, "tg target removal"); got != "@slowchan" {
		t.Fatalf("tg removed target = %q, want @slowchan", got)
	}
}

func TestSourceManagerShutdownStopsEverything(t *testing.T) {
	repo := newFakeRepo()
	repo.seed(testSource("hh-1", SourceTypeHH, "a", true))
	repo.seed(testSource("hh-2", SourceTypeHH, "b", true))
	repo.seed(testSource("rd-1", SourceTypeReddit, "c", true))

	factory := &fakeFactory{}
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, newFakeTG(), factory.build, discardLog())

	if err := mgr.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for factory.count() < 3 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if factory.count() != 3 {
		t.Fatalf("connectors built = %d, want 3", factory.count())
	}

	mgr.Shutdown()
	mgr.Shutdown() // idempotent

	for _, c := range factory.connectors {
		select {
		case <-c.stopped:
		default:
			t.Errorf("connector %s still running after Shutdown", c.name)
		}
	}
}

func TestSourceManagerTGRequiresWhitelist(t *testing.T) {
	repo := newFakeRepo()
	out := make(chan domain.RawSignal, 8)
	mgr := NewSourceManager(context.Background(), repo, out, nil, (&fakeFactory{}).build, discardLog())
	defer mgr.Shutdown()

	_, err := mgr.Create(context.Background(), CreateSourcePayload{Type: SourceTypeTG, Target: "@x"})
	if err == nil {
		t.Fatal("Create tg source without whitelist should fail")
	}
}
