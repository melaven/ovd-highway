package dlq

import (
	"context"
	"errors"
	"testing"
	"time"
)

type fakeArchiveStore struct {
	entries []DeadLetterEntry
	err     error
	calls   int
}

func (f *fakeArchiveStore) Archive(ctx context.Context, entry DeadLetterEntry) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.entries = append(f.entries, entry)
	return nil
}

type fakeDedupStore struct {
	deleted []string
	err     error
	calls   int
}

func (f *fakeDedupStore) DeleteKey(ctx context.Context, key string) error {
	f.calls++
	if f.err != nil {
		return f.err
	}
	f.deleted = append(f.deleted, key)
	return nil
}

func newTestManager(t *testing.T, archive ArchiveStore, dedup DedupStore) *Manager {
	t.Helper()
	m, err := NewManager(archive, dedup, Options{})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	return m
}

func TestHandleLLMFailureSuccess(t *testing.T) {
	archive := &fakeArchiveStore{}
	dedup := &fakeDedupStore{}
	m := newTestManager(t, archive, dedup)

	payload := []byte(`{"id":"sig-1","text":"need a go dev"}`)
	err := m.HandleLLMFailure(context.Background(), "reddit", payload, "dedup:reddit:sig-1", "openrouter HTTP 429")
	if err != nil {
		t.Fatalf("HandleLLMFailure: %v", err)
	}

	if archive.calls != 1 {
		t.Fatalf("archive calls = %d, want 1", archive.calls)
	}
	entry := archive.entries[0]
	if entry.Source != "reddit" {
		t.Errorf("entry.Source = %q, want reddit", entry.Source)
	}
	if string(entry.Payload) != string(payload) {
		t.Errorf("entry.Payload = %s, want %s", entry.Payload, payload)
	}
	if entry.ErrorReason != "openrouter HTTP 429" {
		t.Errorf("entry.ErrorReason = %q", entry.ErrorReason)
	}
	if entry.RetryCount != 0 {
		t.Errorf("entry.RetryCount = %d, want 0", entry.RetryCount)
	}
	if entry.NextRetryAt.IsZero() {
		t.Error("entry.NextRetryAt is zero, want set")
	}

	if dedup.calls != 1 || len(dedup.deleted) != 1 || dedup.deleted[0] != "dedup:reddit:sig-1" {
		t.Fatalf("dedup deleted = %v calls=%d, want [dedup:reddit:sig-1] x1", dedup.deleted, dedup.calls)
	}
}

func TestHandleLLMFailureDBFailureSkipsRedis(t *testing.T) {
	archive := &fakeArchiveStore{err: errors.New("db down")}
	dedup := &fakeDedupStore{}
	m := newTestManager(t, archive, dedup)

	err := m.HandleLLMFailure(context.Background(), "hh", []byte(`{"id":"x"}`), "dedup:hh:x", "HTTP 500")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	if dedup.calls != 0 {
		t.Fatalf("dedup calls = %d, want 0 (key must survive when archive fails)", dedup.calls)
	}
}

func TestHandleLLMFailureRedisFailureAfterArchive(t *testing.T) {
	archive := &fakeArchiveStore{}
	dedup := &fakeDedupStore{err: errors.New("redis down")}
	m := newTestManager(t, archive, dedup)

	err := m.HandleLLMFailure(context.Background(), "reddit", []byte(`{"id":"x"}`), "dedup:reddit:x", "HTTP 429")
	if err == nil {
		t.Fatal("expected error, got nil")
	}
	// Archive succeeded; only the dedupe release failed.
	if archive.calls != 1 {
		t.Fatalf("archive calls = %d, want 1", archive.calls)
	}
	if dedup.calls != 1 {
		t.Fatalf("dedup calls = %d, want 1", dedup.calls)
	}
}

func TestHandleLLMFailureValidatesInput(t *testing.T) {
	archive := &fakeArchiveStore{}
	dedup := &fakeDedupStore{}
	m := newTestManager(t, archive, dedup)

	cases := []struct {
		name    string
		payload []byte
	}{
		{"empty payload", nil},
		{"invalid json", []byte(`{not json`)},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := m.HandleLLMFailure(context.Background(), "reddit", tc.payload, "k", "reason")
			if err == nil {
				t.Fatal("expected validation error")
			}
		})
	}
	if archive.calls != 0 || dedup.calls != 0 {
		t.Fatalf("stores touched on invalid input: archive=%d dedup=%d", archive.calls, dedup.calls)
	}
}

func TestHandleLLMFailureEmptyDedupeKeySkipsRedis(t *testing.T) {
	archive := &fakeArchiveStore{}
	dedup := &fakeDedupStore{}
	m := newTestManager(t, archive, dedup)

	if err := m.HandleLLMFailure(context.Background(), "hh", []byte(`{"id":"x"}`), "", "HTTP 500"); err != nil {
		t.Fatalf("HandleLLMFailure: %v", err)
	}
	if dedup.calls != 0 {
		t.Fatalf("dedup calls = %d, want 0 for empty key", dedup.calls)
	}
}

func TestHandleLLMFailureRetryDelay(t *testing.T) {
	fixed := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	archive := &fakeArchiveStore{}
	dedup := &fakeDedupStore{}
	m, err := NewManager(archive, dedup, Options{
		RetryDelay: 5 * time.Minute,
		Now:        func() time.Time { return fixed },
	})
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}

	if err := m.HandleLLMFailure(context.Background(), "reddit", []byte(`{"id":"x"}`), "k", "r"); err != nil {
		t.Fatalf("HandleLLMFailure: %v", err)
	}
	want := fixed.Add(5 * time.Minute)
	if !archive.entries[0].NextRetryAt.Equal(want) {
		t.Errorf("NextRetryAt = %v, want %v", archive.entries[0].NextRetryAt, want)
	}
}

func TestNewManagerRequiresStores(t *testing.T) {
	if _, err := NewManager(nil, &fakeDedupStore{}, Options{}); err == nil {
		t.Error("nil archive accepted")
	}
	if _, err := NewManager(&fakeArchiveStore{}, nil, Options{}); err == nil {
		t.Error("nil dedup accepted")
	}
}
