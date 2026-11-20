package controlplane

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
)

// SourceRepo is the persistence boundary for the control plane.
// *pgStore satisfies it; tests use an in-memory fake.
type SourceRepo interface {
	ListSources(ctx context.Context) ([]Source, error)
	CreateSource(ctx context.Context, p CreateSourcePayload) (Source, error)
	SetSourceEnabled(ctx context.Context, id string, enabled bool) (Source, error)
	DeleteSource(ctx context.Context, id string) error
	GetSettings(ctx context.Context) (EngineSettings, error)
	PutSettings(ctx context.Context, s EngineSettings) (EngineSettings, error)
}

// LeadRepo is the persistence boundary for lead triage updates.
type LeadRepo interface {
	UpdateLead(ctx context.Context, id string, p UpdateLeadPayload) (LeadPatchResult, error)
}

// Store is the full control-plane persistence boundary.
type Store interface {
	SourceRepo
	LeadRepo
}

type pgStore struct {
	db *sql.DB
}

// NewPGStore wraps an initialized *sql.DB as a Store.
func NewPGStore(db *sql.DB) Store {
	return &pgStore{db: db}
}

func scanSource(row interface{ Scan(...any) error }) (Source, error) {
	var s Source
	var typ string
	var createdAt time.Time
	if err := row.Scan(&s.ID, &typ, &s.Target, &s.Enabled, &createdAt); err != nil {
		return Source{}, err
	}
	s.Type = SourceType(typ)
	s.CreatedAt = createdAt.UTC()
	return s, nil
}

func (p *pgStore) ListSources(ctx context.Context) ([]Source, error) {
	rows, err := p.db.QueryContext(ctx,
		`SELECT id, type, target, enabled, created_at FROM sources ORDER BY created_at ASC, id ASC`)
	if err != nil {
		return nil, fmt.Errorf("list sources: %w", err)
	}
	defer rows.Close()

	sources := make([]Source, 0)
	for rows.Next() {
		s, err := scanSource(rows)
		if err != nil {
			return nil, fmt.Errorf("scan source: %w", err)
		}
		sources = append(sources, s)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate sources: %w", err)
	}
	return sources, nil
}

func (p *pgStore) CreateSource(ctx context.Context, payload CreateSourcePayload) (Source, error) {
	if err := payload.Validate(); err != nil {
		return Source{}, err
	}
	id := uuid.NewString()
	target := strings.TrimSpace(payload.Target)
	var s Source
	var typ string
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO sources (id, type, target, enabled) VALUES ($1, $2, $3, TRUE)
		 RETURNING id, type, target, enabled, created_at`,
		id, string(payload.Type), target,
	).Scan(&s.ID, &typ, &s.Target, &s.Enabled, &s.CreatedAt)
	if err != nil {
		return Source{}, fmt.Errorf("insert source: %w", err)
	}
	s.Type = SourceType(typ)
	s.CreatedAt = s.CreatedAt.UTC()
	return s, nil
}

func (p *pgStore) SetSourceEnabled(ctx context.Context, id string, enabled bool) (Source, error) {
	var s Source
	var typ string
	err := p.db.QueryRowContext(ctx,
		`UPDATE sources SET enabled = $2 WHERE id = $1
		 RETURNING id, type, target, enabled, created_at`,
		id, enabled,
	).Scan(&s.ID, &typ, &s.Target, &s.Enabled, &s.CreatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return Source{}, ErrNotFound
	}
	if err != nil {
		return Source{}, fmt.Errorf("update source %q: %w", id, err)
	}
	s.Type = SourceType(typ)
	s.CreatedAt = s.CreatedAt.UTC()
	return s, nil
}

func (p *pgStore) DeleteSource(ctx context.Context, id string) error {
	res, err := p.db.ExecContext(ctx, `DELETE FROM sources WHERE id = $1`, id)
	if err != nil {
		return fmt.Errorf("delete source %q: %w", id, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		return fmt.Errorf("delete source %q rows: %w", id, err)
	}
	if n == 0 {
		return ErrNotFound
	}
	return nil
}

// UpdateLead applies a validated patch to a highway_leads row. The
// status column is authoritative for triage; draft_proposal is written
// into the payload JSONB at the same path SaveLead marshals it to
// (qualification.draft_proposal).
func (p *pgStore) UpdateLead(ctx context.Context, id string, payload UpdateLeadPayload) (LeadPatchResult, error) {
	if _, err := uuid.Parse(id); err != nil {
		return LeadPatchResult{}, ErrInvalidLeadID
	}

	args := []any{id}
	var sets []string
	var payloadExpr string

	if payload.Status != nil {
		args = append(args, *payload.Status)
		n := len(args)
		sets = append(sets, fmt.Sprintf("status = $%d", n))
		payloadExpr = fmt.Sprintf("jsonb_set(payload, '{status}', to_jsonb($%d::text), true)", n)
	}
	if payload.DraftProposal != nil {
		args = append(args, strings.TrimSpace(*payload.DraftProposal))
		n := len(args)
		if payloadExpr == "" {
			payloadExpr = fmt.Sprintf("jsonb_set(payload, '{qualification,draft_proposal}', to_jsonb($%d::text), true)", n)
		} else {
			payloadExpr = fmt.Sprintf("jsonb_set(%s, '{qualification,draft_proposal}', to_jsonb($%d::text), true)", payloadExpr, n)
		}
	}
	if payloadExpr != "" {
		sets = append(sets, "payload = "+payloadExpr)
	}

	query := fmt.Sprintf(
		`UPDATE highway_leads SET %s WHERE id = $1::uuid
		 RETURNING id::text, status, COALESCE(payload->'qualification'->>'draft_proposal', '')`,
		strings.Join(sets, ", "),
	)
	var res LeadPatchResult
	err := p.db.QueryRowContext(ctx, query, args...).Scan(&res.ID, &res.Status, &res.DraftProposal)
	if errors.Is(err, sql.ErrNoRows) {
		return LeadPatchResult{}, ErrLeadNotFound
	}
	if err != nil {
		return LeadPatchResult{}, fmt.Errorf("update lead %q: %w", id, err)
	}
	return res, nil
}

func (p *pgStore) GetSettings(ctx context.Context) (EngineSettings, error) {
	var s EngineSettings
	err := p.db.QueryRowContext(ctx,
		`SELECT system_prompt, min_score FROM engine_settings WHERE id = 1`,
	).Scan(&s.SystemPrompt, &s.MinScore)
	if errors.Is(err, sql.ErrNoRows) {
		return EngineSettings{}, nil
	}
	if err != nil {
		return EngineSettings{}, fmt.Errorf("get engine settings: %w", err)
	}
	return s, nil
}

func (p *pgStore) PutSettings(ctx context.Context, s EngineSettings) (EngineSettings, error) {
	if err := s.Validate(); err != nil {
		return EngineSettings{}, err
	}
	var out EngineSettings
	err := p.db.QueryRowContext(ctx,
		`INSERT INTO engine_settings (id, system_prompt, min_score, updated_at)
		 VALUES (1, $1, $2, NOW())
		 ON CONFLICT (id) DO UPDATE
		   SET system_prompt = EXCLUDED.system_prompt,
		       min_score     = EXCLUDED.min_score,
		       updated_at    = NOW()
		 RETURNING system_prompt, min_score`,
		s.SystemPrompt, s.MinScore,
	).Scan(&out.SystemPrompt, &out.MinScore)
	if err != nil {
		return EngineSettings{}, fmt.Errorf("put engine settings: %w", err)
	}
	return out, nil
}
