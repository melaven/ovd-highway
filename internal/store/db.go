package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	_ "github.com/jackc/pgx/v5/stdlib"

	"ovd/internal/domain"
)

const pingTimeout = 5 * time.Second

var (
	initOnce sync.Once
	db       *sql.DB
	initErr  error
)

// Init opens, verifies, and initializes the schema for the PostgreSQL connection pool.
func Init(ctx context.Context, databaseURL string) (*sql.DB, error) {
	if ctx == nil {
		return nil, errors.New("database context is required")
	}
	if strings.TrimSpace(databaseURL) == "" {
		return nil, errors.New("DATABASE_URL is required")
	}

	initOnce.Do(func() {
		db, initErr = sql.Open("pgx", databaseURL)
		if initErr != nil {
			initErr = fmt.Errorf("open PostgreSQL connection: %w", initErr)
			return
		}
		db.SetMaxOpenConns(25)
		db.SetMaxIdleConns(10)
		db.SetConnMaxLifetime(5 * time.Minute)

		pingCtx, cancel := context.WithTimeout(ctx, pingTimeout)
		defer cancel()
		if err := db.PingContext(pingCtx); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("ping PostgreSQL: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}

		createTableQuery := `
		CREATE TABLE IF NOT EXISTS highway_leads (
			id UUID PRIMARY KEY,
			track VARCHAR(50) NOT NULL,
			source VARCHAR(50) NOT NULL,
			score INT NOT NULL,
			tech_stack JSONB,
			payload JSONB,
			created_at TIMESTAMP WITH TIME ZONE DEFAULT NOW()
		)
		`
		if _, err := db.ExecContext(ctx, createTableQuery); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create highway_leads table: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}

		createIndexQuery := `CREATE INDEX IF NOT EXISTS idx_leads_created_at ON highway_leads(created_at DESC)`
		if _, err := db.ExecContext(ctx, createIndexQuery); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create highway_leads created_at index: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}

		alterStatusQuery := `ALTER TABLE highway_leads ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'new'`
		if _, err := db.ExecContext(ctx, alterStatusQuery); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("add highway_leads status column: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}

		deadLetterDDL := `
		CREATE TABLE IF NOT EXISTS dead_letter_signals (
			id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
			source        VARCHAR(100) NOT NULL,
			raw_payload   JSONB        NOT NULL,
			error_reason  TEXT         NOT NULL,
			dedupe_key    VARCHAR(255),
			retry_count   INT          NOT NULL DEFAULT 0,
			next_retry_at TIMESTAMP WITH TIME ZONE,
			created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`
		if _, err := db.ExecContext(ctx, deadLetterDDL); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create dead_letter_signals table: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}
		if _, err := db.ExecContext(ctx, `CREATE UNIQUE INDEX IF NOT EXISTS uq_dead_letter_signals_dedupe_key ON dead_letter_signals (dedupe_key) WHERE dedupe_key IS NOT NULL`); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create dead_letter_signals dedupe index: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}
		if _, err := db.ExecContext(ctx, `CREATE INDEX IF NOT EXISTS idx_dead_letter_signals_next_retry ON dead_letter_signals (next_retry_at) WHERE next_retry_at IS NOT NULL`); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create dead_letter_signals retry index: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
		}
		sourcesDDL := `
		CREATE TABLE IF NOT EXISTS sources (
			id         VARCHAR(64) PRIMARY KEY,
			type       VARCHAR(20) NOT NULL CHECK (type IN ('tg', 'hh', 'reddit')),
			target     TEXT         NOT NULL,
			enabled    BOOLEAN      NOT NULL DEFAULT TRUE,
			created_at TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`
		if _, err := db.ExecContext(ctx, sourcesDDL); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create sources table: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
			return
		}

		settingsDDL := `
		CREATE TABLE IF NOT EXISTS engine_settings (
			id            SMALLINT PRIMARY KEY CHECK (id = 1),
			system_prompt TEXT         NOT NULL,
			min_score     INT          NOT NULL,
			updated_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
		)`
		if _, err := db.ExecContext(ctx, settingsDDL); err != nil {
			closeErr := db.Close()
			db = nil
			initErr = fmt.Errorf("create engine_settings table: %w", err)
			if closeErr != nil {
				initErr = errors.Join(initErr, fmt.Errorf("close failed connection: %w", closeErr))
			}
		}

	})

	if initErr != nil {
		return nil, initErr
	}
	return db, nil
}

// SaveLead persists a qualified lead before it is sent to downstream consumers.
func SaveLead(ctx context.Context, db *sql.DB, lead domain.QualifiedLead) error {
	if ctx == nil {
		return errors.New("database context is required")
	}
	if db == nil {
		return errors.New("database connection is required")
	}

	status := lead.Status
	if !domain.ValidLeadStatus(status) {
		status = domain.LeadStatusNew
	}
	lead.Status = status

	techStack, err := json.Marshal(lead.Qualification.TechStack)
	if err != nil {
		return fmt.Errorf("marshal lead tech stack: %w", err)
	}
	payload, err := json.Marshal(lead)
	if err != nil {
		return fmt.Errorf("marshal lead payload: %w", err)
	}

	const query = `
		INSERT INTO highway_leads (id, track, source, score, tech_stack, payload, status)
		VALUES ($1::uuid, $2, $3, $4, $5::jsonb, $6::jsonb, $7)
		ON CONFLICT (id) DO NOTHING
	`
	if _, err := db.ExecContext(ctx, query,
		lead.Signal.ID,
		lead.JobType,
		lead.Signal.Source,
		lead.Qualification.Score,
		string(techStack),
		string(payload),
		status,
	); err != nil {
		return fmt.Errorf("insert qualified lead %q: %w", lead.Signal.ID, err)
	}
	return nil
}

// GetRecentLeads returns the most recently created leads first.
func GetRecentLeads(ctx context.Context, limit int) ([]domain.QualifiedLead, error) {
	if ctx == nil {
		return nil, errors.New("database context is required")
	}
	if db == nil {
		if initErr != nil {
			return nil, fmt.Errorf("database is not initialized: %w", initErr)
		}
		return nil, errors.New("database is not initialized")
	}

	rows, err := db.QueryContext(ctx,
		`SELECT id, track, source, score, tech_stack, payload, created_at, status
		 FROM highway_leads ORDER BY created_at DESC LIMIT $1`,
		limit,
	)
	if err != nil {
		return nil, fmt.Errorf("query recent leads: %w", err)
	}
	defer rows.Close()

	leads := make([]domain.QualifiedLead, 0)
	for rows.Next() {
		var id, track, source string
		var score int
		var techStackJSON, payloadJSON []byte
		var createdAt sql.NullTime
		var status string
		if err := rows.Scan(&id, &track, &source, &score, &techStackJSON, &payloadJSON, &createdAt, &status); err != nil {
			return nil, fmt.Errorf("scan recent lead: %w", err)
		}

		lead, err := decodeRecentLead(id, track, source, score, techStackJSON, payloadJSON, createdAt, status)
		if err != nil {
			return nil, err
		}
		leads = append(leads, lead)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate recent leads: %w", err)
	}
	return leads, nil
}

func decodeRecentLead(
	id, track, source string,
	score int,
	techStackJSON, payloadJSON []byte,
	createdAt sql.NullTime,
	status string,
) (domain.QualifiedLead, error) {
	var lead domain.QualifiedLead
	if err := json.Unmarshal(payloadJSON, &lead); err != nil {
		return domain.QualifiedLead{}, fmt.Errorf("decode lead %q payload: %w", id, err)
	}

	techStack := []string{}
	if len(techStackJSON) > 0 {
		if err := json.Unmarshal(techStackJSON, &techStack); err != nil {
			return domain.QualifiedLead{}, fmt.Errorf("decode lead %q tech stack: %w", id, err)
		}
	}

	lead.Signal.ID = id
	lead.Signal.Source = source
	lead.JobType = track
	lead.Qualification.Score = score
	lead.Qualification.TechStack = techStack
	lead.Status = status
	if !domain.ValidLeadStatus(lead.Status) {
		lead.Status = domain.LeadStatusNew
	}
	if lead.QualifiedAt.IsZero() && createdAt.Valid {
		lead.QualifiedAt = createdAt.Time
	}
	return lead, nil
}
