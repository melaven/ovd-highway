# OVD // Highway — Architecture

[English](./architecture.md) · [Русский](./architecture.ru.md) · [Español](./architecture.es.md) · [简体中文](./architecture.zh-CN.md)

OVD // Highway is a zero-blocking, multi-protocol ingestion engine: onramp connectors emit raw signals into channels, a lane pipeline filters and qualifies them with an LLM, and a dispatcher persists and broadcasts qualified leads. All stages are decoupled by buffered Go channels, so a slow consumer never blocks ingestion.

## High-level data flow

```
 Telegram (MTProto) ─┐
 Reddit (OAuth2)    ─┼─> rawStream ─> dedup (Redis, 72h TTL) ─> dedupStream
 HeadHunter API     ─┘                                             │
 Mock source (dev)                                                ▼
                                              qualifier (LLM_CONCURRENCY workers, 60s timeout)
                                              ├─ prefilter + LLM triage
                                              └─ on failure ─> DLQ (dead_letter_signals)
                                                           │
                                              leadStream ──> dispatcher
                                                           ├─> PostgreSQL (highway_leads)
                                                           ├─> WebSocket hub ──> UI dashboard
                                                           └─> metrics (Prometheus)
                                              DLQ worker ──> re-evaluates archived
                                              signals and feeds resolved leads
                                              back through the dispatcher
```

## Components

### Onramps (`internal/onramp`)

Source connectors implementing `Start(ctx, out chan<- domain.RawSignal)`:

| Connector | Protocol | Notes |
|---|---|---|
| `mtproto.go` | Telegram userbot (MTProto via gotd) | Persistent session in `session.json`; channel whitelist from `TG_TARGET_CHANNELS`; the control plane can add/remove targets at runtime through the shared whitelist. |
| `reddit.go` | Reddit OAuth2 | Requires `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER`. |
| `hh.go` | HeadHunter public API | Polls `api.hh.ru/vacancies`; circuit-breaker protected. |
| `mock.go` | In-process | Enabled with `MOCK_ONRAMP=1` for local development. |

Connectors are spawned per enabled row in the `sources` table by `controlplane.SourceManager`; the MTProto connector is always-on because it owns the single Telegram session.

### Lane (`internal/lane`)

- **Deduplicator** — Redis-backed, 72h TTL, keyed by signal content.
- **Prefilter** — cheap heuristic gate before the LLM call.
- **Qualifier** — `LLM_CONCURRENCY` workers (default 3, clamped 1–10), 60s per-signal timeout (HTTP client and context aligned). Applies `system_prompt` + `min_score` from `engine_settings` (hot-reloaded via `GET/PUT /api/v1/settings`). Uses OpenRouter by default or any OpenAI-compatible endpoint via `LLM_BASE_URL`/`LLM_MODEL`.
- **Failure handler** — signals that fail LLM evaluation (HTTP 429/5xx) are archived to `dead_letter_signals` and their dedup keys released for retry.

### Dispatcher (`cmd/engine/main.go`)

`dispatchLead` is the single fan-out point for qualified leads:

1. `store.SaveLead` — upsert into `highway_leads` (`ON CONFLICT DO NOTHING`).
2. `wsHub.Broadcast` — pushes the lead to all connected `/ws` clients.
3. `internal/metrics` — increments `ovd_leads_*` counters.

Leads below `min_score` or not relevant are counted as dropped, not persisted.

### Control plane (`internal/controlplane`)

REST API under `/api/v1/*`, protected by `BearerAuth` (`ADMIN_API_TOKEN`):

- `GET/POST /sources`, `PATCH/DELETE /sources/{id}` — dynamic connector lifecycle.
- `GET/PUT /settings` — `system_prompt`, `min_score`.
- `PATCH /leads/{id}` — triage: `status` (`new`/`contacted`/`archived`) and `draft_proposal`.

### Persistence (`internal/store`)

PostgreSQL schema (created idempotently on boot, mirrored in `migrations/`):

- `highway_leads` — `id` (UUID = signal id), `track`, `source`, `score`, `tech_stack`, `payload` (full `QualifiedLead` JSONB), `status`, `created_at`.
- `sources` — connector registry driving `SourceManager`.
- `engine_settings` — singleton row (`id = 1`) with prompt and threshold.
- `dead_letter_signals` — DLQ archive with `dedupe_key`, `retry_count`, `next_retry_at`.

### DLQ worker (`internal/pipeline/dlq`)

Background worker polling `dead_letter_signals` for rows whose `next_retry_at` is due. It re-evaluates the archived signal through the qualifier and owns row lifecycle: deletes on success, bumps `retry_count`/`next_retry_at`/`error_reason` on failure. Signals are never re-enqueued into `rawStream`, so backoff state cannot reset.

### API surface (`internal/api`)

Same HTTP server (`WS_ADDR`, default `:8080`), wrapped in `api.CORS`:

| Route | Auth | Purpose |
|---|---|---|
| `GET /healthz` | none | Liveness — `{"status":"ok"}` |
| `GET /readyz` | none | Readiness — pings Postgres and Redis (2s timeout each), `200`/`503` |
| `GET /metrics` | none | Prometheus text exposition (`ovd_*` metrics) |
| `GET /ws` | `?token=` | WebSocket lead stream; first frame is `init` with recent history |
| `/api/v1/*` | `Authorization: Bearer` | Control plane REST |

## Key properties

- **Zero blocking** — every pipeline stage is a buffered channel; backpressure is absorbed by queue depth, not by stalling connectors.
- **At-least-once delivery** — dedup keys are released on LLM failure so retries can re-ingest; `SaveLead` is idempotent on signal id.
- **Hot configuration** — prompt, score threshold, and source list change without restart.
- **Observable** — structured JSON logs (`LOG_LEVEL=debug` for verbose), Prometheus metrics, and probe endpoints for orchestrators.
