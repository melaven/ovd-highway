package lane

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"sync"
	"testing"
	"time"

	"ovd/internal/domain"
)

// fakeSettingsProvider is a mutable lane.SettingsProvider for hot-reload
// tests. All accessors take the read lock; set takes the write lock.
type fakeSettingsProvider struct {
	mu        sync.RWMutex
	prompt    string
	threshold float64
}

func newFakeSettingsProvider(prompt string, threshold float64) *fakeSettingsProvider {
	return &fakeSettingsProvider{prompt: prompt, threshold: threshold}
}

func (f *fakeSettingsProvider) SystemPrompt() string {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.prompt
}

func (f *fakeSettingsProvider) MinScoreThreshold() float64 {
	f.mu.RLock()
	defer f.mu.RUnlock()
	return f.threshold
}

func (f *fakeSettingsProvider) set(prompt string, threshold float64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.prompt = prompt
	f.threshold = threshold
}

// captureCompleter records the system prompt of every call and returns a
// fixed valid qualification JSON.
type captureCompleter struct {
	mu         sync.Mutex
	lastSystem string
	calls      int
	response   string
}

func newCaptureCompleter(score int) *captureCompleter {
	return &captureCompleter{
		response: fmt.Sprintf(
			`{"is_relevant":true,"score":%d,"estimated_budget":"10k","pain_points":"pain","draft_proposal":"draft","tech_stack":["Go"],"detected_job_type":"freelance"}`,
			score,
		),
	}
}

func (c *captureCompleter) Complete(_ context.Context, system, _ string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.lastSystem = system
	c.calls++
	return c.response, nil
}

func (c *captureCompleter) last() (string, int) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.lastSystem, c.calls
}

func newTestQualifier(t *testing.T, completer Completer) *Qualifier {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	q, err := NewQualifier(completer, 1, time.Second, log)
	if err != nil {
		t.Fatalf("NewQualifier: %v", err)
	}
	return q
}

func TestQualifierUsesLiveSystemPrompt(t *testing.T) {
	completer := newCaptureCompleter(8)
	provider := newFakeSettingsProvider("prompt-v1", 7)
	q := newTestQualifier(t, completer)
	q.SetSettingsProvider(provider)

	if _, err := q.callOnce(context.Background(), "order text", "freelance"); err != nil {
		t.Fatalf("callOnce: %v", err)
	}
	system, _ := completer.last()
	if system != "prompt-v1" {
		t.Fatalf("system prompt = %q, want prompt-v1", system)
	}

	provider.set("prompt-v2", 7)
	if _, err := q.callOnce(context.Background(), "order text", "freelance"); err != nil {
		t.Fatalf("callOnce after update: %v", err)
	}
	system, calls := completer.last()
	if system != "prompt-v2" {
		t.Fatalf("system prompt = %q, want prompt-v2", system)
	}
	if calls != 2 {
		t.Fatalf("LLM calls = %d, want 2", calls)
	}
}

func TestQualifierFallsBackToDefaultPrompt(t *testing.T) {
	completer := newCaptureCompleter(8)
	q := newTestQualifier(t, completer)

	if _, err := q.callOnce(context.Background(), "order text", "freelance"); err != nil {
		t.Fatalf("callOnce: %v", err)
	}
	system, _ := completer.last()
	if system != systemPrompt {
		t.Fatalf("system prompt = %q, want built-in default", system)
	}
}

func TestQualifierLiveMinScoreThreshold(t *testing.T) {
	provider := newFakeSettingsProvider("p", 9)
	q := newTestQualifier(t, newCaptureCompleter(8))
	q.SetSettingsProvider(provider)

	sig := domain.RawSignal{ID: "s1", Source: "test", Text: passingPrefilterText}
	qual := domain.Qualification{IsRelevant: true, Score: 8, DetectedJobType: "freelance"}

	if _, ok := q.leadFrom(sig, qual); ok {
		t.Fatal("score 8 should be rejected at threshold 9")
	}

	provider.set("p", 7)
	lead, ok := q.leadFrom(sig, qual)
	if !ok {
		t.Fatal("score 8 should be accepted at threshold 7")
	}
	if lead.Signal.ID != "s1" {
		t.Fatalf("lead signal = %q", lead.Signal.ID)
	}
}

func TestQualifierNormalizeClearsDraftBelowThreshold(t *testing.T) {
	provider := newFakeSettingsProvider("p", 9)
	q := newTestQualifier(t, newCaptureCompleter(8))
	q.SetSettingsProvider(provider)

	qual, err := q.qualify(context.Background(), domain.RawSignal{
		ID:     "s1",
		Source: "test",
		Text:   passingPrefilterText,
	})
	if err != nil {
		t.Fatalf("qualify: %v", err)
	}
	if qual.DraftProposal != "" {
		t.Fatalf("draft_proposal = %q, want cleared below threshold", qual.DraftProposal)
	}
}

// TestQualifierSettingsConcurrentHotReload exercises concurrent reads of
// prompt/threshold while the provider is mutated. -race validates the
// SettingsStore/provider locking discipline end to end.
func TestQualifierSettingsConcurrentHotReload(t *testing.T) {
	completer := newCaptureCompleter(8)
	provider := newFakeSettingsProvider("prompt-0", 7)
	q := newTestQualifier(t, completer)
	q.SetSettingsProvider(provider)

	ctx := context.Background()

	var wg sync.WaitGroup
	stop := make(chan struct{})
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				if _, err := q.callOnce(ctx, "order", "freelance"); err != nil {
					return
				}
				_ = q.minScoreThresholdNow()
				_ = q.systemPromptNow()
				select {
				case <-stop:
					return
				default:
				}
			}
		}()
	}

	for i := 0; i < 200; i++ {
		provider.set(fmt.Sprintf("prompt-%d", i), float64(i%10))
	}
	close(stop)
	wg.Wait()

	_, calls := completer.last()
	if calls == 0 {
		t.Fatal("no LLM calls recorded")
	}
}

func TestQualifierNilProviderKeepsDefaults(t *testing.T) {
	completer := newCaptureCompleter(8)
	provider := newFakeSettingsProvider("p", 9)
	q := newTestQualifier(t, completer)
	q.SetSettingsProvider(provider)
	q.SetSettingsProvider(nil)

	if _, err := q.callOnce(context.Background(), "order", "freelance"); err != nil {
		t.Fatalf("callOnce: %v", err)
	}
	system, _ := completer.last()
	if system != systemPrompt {
		t.Fatalf("system prompt = %q, want built-in default after nil provider", system)
	}
}
