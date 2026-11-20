-- Lead triage status for the Control Plane (PATCH /api/v1/leads/{id}).
-- Existing rows default to 'new'; the engine never writes this column
-- directly, so idempotent re-runs are safe.

ALTER TABLE highway_leads
    ADD COLUMN IF NOT EXISTS status VARCHAR(20) NOT NULL DEFAULT 'new';
