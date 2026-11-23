# OVD // Highway — Operations Runbook

[English](./runbook.md) · [Русский](./runbook.ru.md) · [Español](./runbook.es.md) · [简体中文](./runbook.zh-CN.md)

Symptom-driven procedures for the running engine. All endpoints below live on the same HTTP server (`WS_ADDR`, default `:8080`) and are unauthenticated unless noted.

## First response: is it alive?

```bash
curl -s localhost:8080/healthz   # liveness — 200 {"status":"ok"}
curl -s localhost:8080/readyz    # readiness — 200 ready / 503 not_ready
curl -s localhost:8080/metrics   # Prometheus exposition (ovd_* metrics)
```

- `healthz` fails → the process is down or the port is unreachable. Check `docker compose ps`, `docker compose logs engine`, and restart.
- `readyz` returns `503` → read `checks`: `postgres` and `redis` report `"ok"` or the error text. The engine keeps running but cannot persist/dedup — treat as degraded.

## Reading /metrics

Key series (all `ovd_*`):

| Metric | Meaning |
|---|---|
| `ovd_uptime_seconds`, `ovd_goroutines`, `ovd_mem_*`, `ovd_gc_cycles_total` | Runtime health — watch for goroutine/memory growth. |
| `ovd_leads_dispatched_total` | Leads processed by the dispatcher. |
| `ovd_leads_saved_total` | Leads persisted + broadcast. |
| `ovd_leads_dropped_total` | Filtered by relevance/min_score — normal churn. |
| `ovd_dispatch_errors_total` | Persist/broadcast failures — investigate Postgres/WS. |
| `ovd_dlq_retries_scheduled_total`, `ovd_dlq_resolved_total` | DLQ activity. |
| `ovd_dlq_store_errors_total` | DLQ persistence failures — Postgres issue. |

A flat `ovd_leads_dispatched_total` while connectors run usually means the LLM qualifier is slow/failing — check DLQ growth next.

## Postgres down or degraded

Symptoms: `readyz.checks.postgres` != `"ok"`, `ovd_dispatch_errors_total` rising, `ovd_dlq_store_errors_total` rising.

1. Confirm the database: `docker compose ps`, check Postgres logs, disk space, and connections (`max 25` open conns from the pool).
2. The engine does **not** exit on DB loss — `SaveLead` errors surface via `dispatch_errors`. Fix Postgres; queued channel leads drain automatically once writes succeed.
3. If the outage was long, expect a DLQ backlog (see below).

## Redis down or degraded

Symptoms: `readyz.checks.redis` != `"ok"`; engine fails fast at boot (`redis ping`) if Redis is unreachable at startup — restart after Redis recovers.

Impact while running: dedup keys and DLQ dedup degrade; do not run long without Redis or duplicates will flow downstream.

## LLM API outage (429 / 5xx)

Symptoms: `ovd_leads_dispatched_total` stalls, `dead_letter_signals` grows, logs show qualifier errors.

1. Check provider status and `LLM_API_KEY`/`LLM_BASE_URL`/`LLM_MODEL`.
2. Signals failing evaluation are archived automatically — nothing is lost.
3. After the provider recovers, the DLQ worker retries due rows by itself (`next_retry_at` backoff). No manual re-ingest needed.

## Draining the DLQ

Table: `dead_letter_signals` (`raw_payload`, `error_reason`, `dedupe_key`, `retry_count`, `next_retry_at`).

Inspect backlog:

```sql
SELECT count(*) AS due_now
FROM dead_letter_signals
WHERE next_retry_at IS NULL OR next_retry_at <= NOW();

SELECT source, count(*), max(retry_count)
FROM dead_letter_signals
GROUP BY source;
```

- Worker picks due rows continuously; `retry_count` increments and `next_retry_at` is pushed out on each failure.
- Poison rows: if one signal keeps failing (e.g. permanently malformed payload), inspect `error_reason`, then either fix the root cause or delete the row manually:

  ```sql
  DELETE FROM dead_letter_signals WHERE id = '<uuid>';
  ```

- Mass requeue after an outage: rows become due automatically as `next_retry_at` passes; to force immediate retry, reset the schedule:

  ```sql
  UPDATE dead_letter_signals SET next_retry_at = NOW() WHERE next_retry_at > NOW();
  ```

Watch `ovd_dlq_resolved_total` vs `ovd_dlq_retries_scheduled_total` to confirm the queue is draining.

## Control plane issues

- `401` on `/api/v1/*` or `/ws` → wrong/missing `ADMIN_API_TOKEN` (or `?token=` for WS). The UI shows the Token Lock modal; clear `ovd_admin_token` in localStorage and re-enter.
- Sources not spawning → check `sources` table rows (`enabled = true`) and connector logs; `SourceManager` reconciles on every API mutation and at boot (`Hydrate`).
- Settings not applying → `PUT /api/v1/settings` persists to `engine_settings`; the qualifier picks changes up on the next signal.

## Restart procedure

```bash
docker compose restart engine     # graceful: SIGTERM drains errgroup, 5s HTTP shutdown
docker compose up -d --build      # after config/code change
```

Boot is idempotent: schema DDL uses `IF NOT EXISTS`, sources/settings hydrate from Postgres, and the MTProto session resumes from `session.json`.
