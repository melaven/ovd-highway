# OVD // Highway — Deployment

[English](./deployment.md) · [Русский](./deployment.ru.md) · [Español](./deployment.es.md) · [简体中文](./deployment.zh-CN.md)

This guide deploys the engine with `docker compose`. The stack needs three backing services — PostgreSQL, Redis, and the engine itself (which also serves the WebSocket API consumed by the `ui/` dashboard).

## Prerequisites

- Docker Engine with the Compose plugin (`docker compose version`).
- A `.env` file at the repository root (see below).
- For the first MTProto login, the engine writes `session.json` next to its working directory — mount a persistent volume or copy the file back after first boot.

## 1. Configure environment

```bash
cp .env.example .env
```

Fill in every required variable — the engine refuses to start without them:

| Variable | Purpose |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN (`postgres://user:pass@host:5432/db?sslmode=...`) |
| `REDIS_URL` | Redis URL (`redis://host:6379/0`) |
| `LLM_API_KEY` | Qualifier model key (OpenRouter or OpenAI-compatible) |
| `TG_API_ID`, `TG_API_HASH`, `TG_PHONE` | Telegram MTProto credentials |
| `CONTROL_BOT_TOKEN`, `ADMIN_TG_ID` | Control bot token and admin user id |

Optional but recommended for production:

- `ADMIN_API_TOKEN` — **must** be set outside local dev; it protects `/api/v1/*` and the `/ws` upgrade.
- `WS_ALLOWED_ORIGINS` — extra allowed WebSocket origins (localhost variants are always allowed).
- `LLM_BASE_URL`, `LLM_MODEL` — point the qualifier at a self-hosted or alternative OpenAI-compatible endpoint.
- `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER` — enable the Reddit connector.
- `TG_TARGET_CHANNELS` — comma-separated Telegram channels to ingest.
- `LOG_LEVEL=debug` — verbose structured logs while troubleshooting.

Never commit `.env`; it is listed in `.gitignore`.

## 2. Build and start

```bash
docker compose up -d --build
docker compose ps        # all services should be healthy/running
docker compose logs -f engine
```

On first boot the engine creates the schema (`highway_leads`, `sources`, `engine_settings`, `dead_letter_signals`), hydrates enabled sources from the `sources` table, and starts listening on `WS_ADDR` (default `:8080`).

## 3. Verify

```bash
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ready","checks":{"postgres":"ok","redis":"ok"}}
curl http://localhost:8080/metrics   # Prometheus text (ovd_* metrics)
```

Or run the bundled smoke script from the repo root:

```powershell
./scripts/smoke.ps1                 # uses $env:ADMIN_API_TOKEN when set
./scripts/smoke.ps1 -Token <token>  # explicit bearer token
```

The dashboard (`ui/`) runs separately with `npm run dev` / `npm run build`; point `VITE_API_BASE` and `VITE_WS_URL` at the deployed engine.

## 4. Production reverse-proxy notes

The probe endpoints are intentionally unauthenticated so orchestrators can scrape them. In production, restrict them at the proxy layer:

- **`/metrics`** — exposes internal counters (`ovd_*`). Allow only your monitoring network:

  ```nginx
  location = /metrics {
      allow 10.0.0.0/8;      # monitoring subnet
      deny all;
      proxy_pass http://engine:8080;
  }
  ```

- **`/healthz`, `/readyz`** — safe to expose to the load balancer's health-check path only; deny public access if you prefer.
- **`/api/v1/*` and `/ws`** — already protected (`ADMIN_API_TOKEN`); keep them that way and set `WS_ALLOWED_ORIGINS` to your dashboard origin.
- Terminate TLS at the proxy and forward `Authorization` headers unchanged; the engine does not terminate TLS itself.

## 5. Upgrades and rollback

```bash
docker compose pull        # or build from a tagged checkout
docker compose up -d       # schema migrations are idempotent (IF NOT EXISTS)
```

Migrations in `migrations/` are mirrored by the engine's boot-time DDL, so rolling back the binary is safe as long as no later migration removed a column the old binary needs.
