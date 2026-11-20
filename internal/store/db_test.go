package store

import (
	"database/sql"
	"testing"
	"time"
)

func TestDecodeRecentLead(t *testing.T) {
	createdAt := time.Date(2026, time.November, 17, 10, 0, 0, 0, time.UTC)
	lead, err := decodeRecentLead(
		"signal-id",
		"freelance",
		"telegram",
		8,
		[]byte(`["Go","PostgreSQL"]`),
		[]byte(`{"signal":{"text":"Build an API"},"qualification":{"is_relevant":true},"qualified_at":"2026-11-17T09:00:00Z"}`),
		sql.NullTime{Time: createdAt, Valid: true},
		"contacted",
	)
	if err != nil {
		t.Fatalf("decodeRecentLead: %v", err)
	}

	if lead.Signal.ID != "signal-id" || lead.Signal.Source != "telegram" ||
		lead.JobType != "freelance" || lead.Qualification.Score != 8 {
		t.Errorf("decoded lead metadata = %+v", lead)
	}
	if len(lead.Qualification.TechStack) != 2 ||
		lead.Qualification.TechStack[0] != "Go" ||
		lead.Qualification.TechStack[1] != "PostgreSQL" {
		t.Errorf("decoded tech stack = %v", lead.Qualification.TechStack)
	}
	if want := time.Date(2026, time.November, 17, 9, 0, 0, 0, time.UTC); !lead.QualifiedAt.Equal(want) {
		t.Errorf("QualifiedAt = %s, want %s", lead.QualifiedAt, want)
	}
	if lead.Status != "contacted" {
		t.Errorf("Status = %q, want contacted", lead.Status)
	}
}

func TestDecodeRecentLeadFallsBackToCreatedAt(t *testing.T) {
	createdAt := time.Date(2026, time.November, 17, 10, 0, 0, 0, time.UTC)
	lead, err := decodeRecentLead(
		"signal-id",
		"vacancy",
		"habr",
		7,
		nil,
		[]byte(`{}`),
		sql.NullTime{Time: createdAt, Valid: true},
		"",
	)
	if err != nil {
		t.Fatalf("decodeRecentLead: %v", err)
	}
	if !lead.QualifiedAt.Equal(createdAt) {
		t.Errorf("QualifiedAt = %s, want %s", lead.QualifiedAt, createdAt)
	}
	if lead.Qualification.TechStack == nil {
		t.Error("TechStack is nil, want an empty slice")
	}
	if lead.Status != "new" {
		t.Errorf("Status = %q, want new", lead.Status)
	}
}
