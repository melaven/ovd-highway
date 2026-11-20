package controlplane

import (
	"errors"
	"strings"
	"time"
)

// SourceType mirrors ui/src/types/controlPlane.ts.
type SourceType string

const (
	SourceTypeTG     SourceType = "tg"
	SourceTypeHH     SourceType = "hh"
	SourceTypeReddit SourceType = "reddit"
)

// Source is the JSON contract returned by /api/v1/sources.
type Source struct {
	ID        string     `json:"id"`
	Type      SourceType `json:"type"`
	Target    string     `json:"target"`
	Enabled   bool       `json:"enabled"`
	CreatedAt time.Time  `json:"created_at"`
}

// CreateSourcePayload is the POST /api/v1/sources body.
type CreateSourcePayload struct {
	Type   SourceType `json:"type"`
	Target string     `json:"target"`
}

// UpdateSourcePayload is the PATCH /api/v1/sources/{id} body.
type UpdateSourcePayload struct {
	Enabled *bool `json:"enabled"`
}

// EngineSettings is the JSON contract for /api/v1/settings.
// MinScore uses the 0-100 scale exposed by the UI slider.
type EngineSettings struct {
	SystemPrompt string `json:"system_prompt"`
	MinScore     int    `json:"min_score"`
}

// ErrNotFound is returned by repo/manager operations on unknown IDs.
var ErrNotFound = errors.New("source not found")

// ValidateCreate checks the POST payload against the locked contract.
func (p CreateSourcePayload) Validate() error {
	switch p.Type {
	case SourceTypeTG, SourceTypeHH, SourceTypeReddit:
	default:
		return errors.New("type must be one of tg, hh, reddit")
	}
	if strings.TrimSpace(p.Target) == "" {
		return errors.New("target must not be empty")
	}
	return nil
}

// Validate checks PUT /api/v1/settings payload rules.
func (s EngineSettings) Validate() error {
	if strings.TrimSpace(s.SystemPrompt) == "" {
		return errors.New("system_prompt must not be empty")
	}
	if s.MinScore < 0 || s.MinScore > 100 {
		return errors.New("min_score must be in [0, 100]")
	}
	return nil
}
