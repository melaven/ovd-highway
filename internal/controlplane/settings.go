package controlplane

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
)

// SettingsStore is a thread-safe live cache of EngineSettings backed by
// SourceRepo. It implements lane.SettingsProvider so the Qualifier picks
// up system_prompt / min_score changes on the next incoming signal
// without a restart.
type SettingsStore struct {
	repo    SourceRepo
	log     *slog.Logger
	mu      sync.RWMutex
	current EngineSettings
}

// NewSettingsStore seeds the cache with defaults; call Hydrate to load
// the persisted row from the repository.
func NewSettingsStore(repo SourceRepo, defaults EngineSettings, log *slog.Logger) *SettingsStore {
	if log == nil {
		log = slog.Default()
	}
	return &SettingsStore{
		repo:    repo,
		log:     log.With("component", "settings_store"),
		current: defaults,
	}
}

// Hydrate loads persisted settings from the repository. When the
// engine_settings row is absent or fails validation the store keeps
// its defaults.
func (s *SettingsStore) Hydrate(ctx context.Context) error {
	stored, err := s.repo.GetSettings(ctx)
	if err != nil {
		return fmt.Errorf("hydrate engine settings: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if stored.SystemPrompt == "" && stored.MinScore == 0 {
		return nil
	}
	if err := stored.Validate(); err != nil {
		s.log.Warn("stored engine settings invalid, keeping defaults", "err", err)
		return nil
	}
	s.current = stored
	return nil
}

// Get returns a copy of the current settings.
func (s *SettingsStore) Get() EngineSettings {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.current
}

// SystemPrompt implements lane.SettingsProvider.
func (s *SettingsStore) SystemPrompt() string {
	return s.Get().SystemPrompt
}

// MinScoreThreshold implements lane.SettingsProvider. The control-plane
// min_score is a 0-100 slider value; the LLM scores on a 1-10 scale,
// so the threshold is min_score/10 clamped to [0, 10].
func (s *SettingsStore) MinScoreThreshold() float64 {
	t := float64(s.Get().MinScore) / 10
	if t < 0 {
		return 0
	}
	if t > 10 {
		return 10
	}
	return t
}

// Put validates, persists, and atomically publishes new settings.
func (s *SettingsStore) Put(ctx context.Context, in EngineSettings) (EngineSettings, error) {
	if err := in.Validate(); err != nil {
		return EngineSettings{}, err
	}
	out, err := s.repo.PutSettings(ctx, in)
	if err != nil {
		return EngineSettings{}, err
	}
	s.mu.Lock()
	s.current = out
	s.mu.Unlock()
	return out, nil
}
