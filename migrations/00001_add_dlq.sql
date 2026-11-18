-- Dead Letter Queue for signals that failed LLM processing (HTTP 429 / 5xx).
-- Rows are picked up by a background worker once next_retry_at is reached.

CREATE TABLE IF NOT EXISTS dead_letter_signals (
    id            UUID PRIMARY KEY DEFAULT gen_random_uuid(),
    source        VARCHAR(100) NOT NULL,
    raw_payload   JSONB        NOT NULL,
    error_reason  TEXT         NOT NULL,
    retry_count   INT          NOT NULL DEFAULT 0,
    next_retry_at TIMESTAMP WITH TIME ZONE,
    created_at    TIMESTAMP WITH TIME ZONE NOT NULL DEFAULT NOW()
);

-- Supports the retry worker's "give me due rows" scan.
CREATE INDEX IF NOT EXISTS idx_dead_letter_signals_next_retry
    ON dead_letter_signals (next_retry_at)
    WHERE next_retry_at IS NOT NULL;
