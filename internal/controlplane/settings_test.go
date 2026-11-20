package controlplane

import (
	"context"
	"fmt"
	"sync"
	"testing"
)

func TestSettingsStoreDefaultsBeforeHydrate(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "built-in", MinScore: 70}, discardLog())

	got := s.Get()
	if got.SystemPrompt != "built-in" || got.MinScore != 70 {
		t.Fatalf("defaults = %+v", got)
	}
	if s.SystemPrompt() != "built-in" {
		t.Error("SystemPrompt() did not return default")
	}
	if th := s.MinScoreThreshold(); th != 7 {
		t.Fatalf("MinScoreThreshold() = %v, want 7", th)
	}
}

func TestSettingsStoreHydrateLoadsPersisted(t *testing.T) {
	repo := newFakeRepo()
	repo.settings = EngineSettings{SystemPrompt: "stored prompt", MinScore: 55}
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "built-in", MinScore: 70}, discardLog())

	if err := s.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	got := s.Get()
	if got.SystemPrompt != "stored prompt" || got.MinScore != 55 {
		t.Fatalf("hydrated = %+v", got)
	}
}

func TestSettingsStoreHydrateKeepsDefaultsOnEmpty(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "built-in", MinScore: 70}, discardLog())

	if err := s.Hydrate(context.Background()); err != nil {
		t.Fatalf("Hydrate: %v", err)
	}
	if got := s.Get(); got.SystemPrompt != "built-in" {
		t.Fatalf("empty repo should keep defaults, got %+v", got)
	}
}

func TestSettingsStorePutUpdatesCacheAndRepo(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "built-in", MinScore: 70}, discardLog())

	out, err := s.Put(context.Background(), EngineSettings{SystemPrompt: "live", MinScore: 90})
	if err != nil {
		t.Fatalf("Put: %v", err)
	}
	if out.SystemPrompt != "live" || out.MinScore != 90 {
		t.Fatalf("Put returned %+v", out)
	}
	if got := s.Get(); got.SystemPrompt != "live" || got.MinScore != 90 {
		t.Fatalf("cache = %+v", got)
	}
	if stored := repo.settings; stored.SystemPrompt != "live" {
		t.Fatalf("repo settings = %+v", stored)
	}
	if th := s.MinScoreThreshold(); th != 9 {
		t.Fatalf("MinScoreThreshold() = %v, want 9", th)
	}
}

func TestSettingsStorePutValidation(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "built-in", MinScore: 70}, discardLog())

	for _, in := range []EngineSettings{
		{SystemPrompt: "", MinScore: 50},
		{SystemPrompt: "ok", MinScore: 101},
		{SystemPrompt: "ok", MinScore: -5},
	} {
		if _, err := s.Put(context.Background(), in); err == nil {
			t.Errorf("Put(%+v) should fail validation", in)
		}
	}
	if got := s.Get(); got.SystemPrompt != "built-in" {
		t.Fatal("failed Put mutated the cache")
	}
}

func TestSettingsStoreMinScoreThresholdClamps(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "p", MinScore: 0}, discardLog())
	if th := s.MinScoreThreshold(); th != 0 {
		t.Fatalf("min_score 0 -> %v, want 0", th)
	}
	s.Put(context.Background(), EngineSettings{SystemPrompt: "p", MinScore: 100})
	if th := s.MinScoreThreshold(); th != 10 {
		t.Fatalf("min_score 100 -> %v, want 10", th)
	}
}

// TestSettingsStoreConcurrentHotReload hammers Get/Put/SystemPrompt from
// multiple goroutines; -race validates the RWMutex discipline.
func TestSettingsStoreConcurrentHotReload(t *testing.T) {
	repo := newFakeRepo()
	s := NewSettingsStore(repo, EngineSettings{SystemPrompt: "p0", MinScore: 50}, discardLog())
	ctx := context.Background()

	var wg sync.WaitGroup
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < 50; j++ {
				_ = s.Get()
				_ = s.SystemPrompt()
				_ = s.MinScoreThreshold()
			}
		}()
	}
	for i := 0; i < 4; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			for j := 0; j < 25; j++ {
				_, _ = s.Put(ctx, EngineSettings{
					SystemPrompt: fmt.Sprintf("prompt-%d-%d", i, j),
					MinScore:     (i*10 + j) % 100,
				})
			}
		}(i)
	}
	wg.Wait()
}
