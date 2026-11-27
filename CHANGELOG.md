# Changelog

All notable changes to OVD // Highway are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `LLM_CONCURRENCY` env var controls parallel LLM qualification workers (default `3`, clamped to `1..10`); the resolved value is logged at startup and can be set in `docker-compose.yml` (`${LLM_CONCURRENCY:-3}`).
- `GET /api/v1/dlq` lists dead-letter queue rows (`count` + latest items with payload preview) and accepts an optional `?limit=N` parameter (default `50`, clamped to `1..100`); `POST /api/v1/dlq/retry` re-queues all unresolved rows for immediate reprocessing.
- Dashboard bottom status bar shows a `DLQ: N` counter with a popover of recent failures and a "Retry all" action.
- Telegram target normalization now accepts `www.t.me/`, `telegram.me/`, `www.telegram.me/`, `t.me/s/` preview URLs and query/trailing-slash variants.
- Prometheus counters `ovd_tg_empty_media_dropped_total` and `ovd_tg_album_duplicates_dropped_total` track Telegram posts dropped before qualification.
- Engine container healthcheck on `/healthz` in `docker-compose.yml`.
- v1.0.0 release smoke-check and soak-test runbook in `docs/runbook*.md`.

### Changed

- LLM HTTP client and qualifier context timeouts aligned to 60s so slow OpenAI-compatible models no longer get cut at 15s.

### Fixed

- DLQ admin SQL hardened: `List` clamps `limit` to `1..100`, selected columns are `COALESCE`-guarded against NULLs, and empty result sets serialize as `[]`.
- `DLQBadge` polling lifecycle cleaned up (no synchronous `setState` in effects, interval/timeout cleanup on unmount, retry button guarded against double-clicks, API errors surfaced in the popover).
- Media-only Telegram posts (empty text) are dropped before LLM qualification.
- Album posts sharing `GroupedID` are deduplicated: only the first captioned frame becomes a signal, and empty leading frames no longer reserve the group ID.

## [1.0.0] — 2026-11-23

First stable release of the zero-blocking multi-protocol ingestion and AI qualification engine.

### Added

#### Web Control Plane

- React 19 + Vite + Tailwind dashboard (`ui/`) with a live WebSocket lead stream, inspector, and track/source/score/status filters.
- Control Plane REST API under `/api/v1/*` protected by `Authorization: Bearer` (`ADMIN_API_TOKEN`): dynamic source lifecycle (`GET/POST/PATCH/DELETE /sources`) and hot-reloadable engine settings (`GET/PUT /settings`).
- Lead triage via `PATCH /api/v1/leads/{id}` — `status` (`new`/`contacted`/`archived`) and editable `draft_proposal` persisted into the `payload` JSONB.
- "Copy Pitch & Open" workflow in the inspector: opens the source post, copies the pitch, and marks the lead `contacted` in one action; archive/restore quick actions included.
- WebSocket auth via `?token=` query param with close-code `4401` signaling auth-required to the UI Token Lock modal.

#### Multi-Protocol Dispatch

- Onramp connectors for Telegram (MTProto userbot via gotd, persistent `session.json`, runtime channel whitelist), Reddit (OAuth2), HeadHunter public API, and an opt-in mock source (`MOCK_ONRAMP=1`).
- `controlplane.SourceManager` reconciles enabled `sources` rows with running connector goroutines on every mutation and at boot.
- Buffered-channel pipeline: onramps → dedup (Redis, 72h TTL) → prefilter → LLM qualifier (4 workers, 45s timeout, OpenRouter or OpenAI-compatible via `LLM_BASE_URL`) → dispatcher.
- Dispatcher fans out each qualified lead to PostgreSQL (`highway_leads`), the WebSocket hub, and Prometheus metrics; `min_score` threshold applied before persistence.
- PostgreSQL persistence with idempotent boot-time schema (`highway_leads`, `sources`, `engine_settings`, `dead_letter_signals`) and SQL migrations in `migrations/`.

#### Observability

- Prometheus metrics endpoint `GET /metrics` (`ovd_*` series: uptime, goroutines, memory, GC, lead dispatch/save/drop/error counters, DLQ retry/resolve/store-error counters).
- Probe endpoints `GET /healthz` (liveness) and `GET /readyz` (postgres + redis checks, 2s timeout, `200`/`503`).
- Structured JSON logs with `LOG_LEVEL=debug` support; CORS wrapper for local development.

#### Dead Letter Queue

- Failed LLM evaluations (HTTP 429/5xx) archived to `dead_letter_signals` with dedup keys released for retry.
- Background DLQ worker re-evaluates due rows (`next_retry_at` backoff, `retry_count`) and owns row lifecycle; resolved leads re-enter the dispatcher.

#### Docker packaging

- `Dockerfile`, `docker-compose.yml`, and `.dockerignore` for containerized deployment; `session.json` persistence for the MTProto session.
- `Makefile` targets (`build`, `test`, `run`, `fmt`, `docker-up`, `docker-down`, `smoke`) and `scripts/smoke.ps1` stack verification script.
- Documentation: `docs/architecture.md`, `docs/deployment.md`, `docs/runbook.md` localized in English, Русский, Español, and 简体中文.

### Security

- Bearer-token auth on all `/api/v1/*` routes and `/ws` upgrades; constant-time token comparison.
- WebSocket origin allowlist (`WS_ALLOWED_ORIGINS`) with localhost defaults.
- `.env.example` documents every supported environment variable; secrets never committed.
