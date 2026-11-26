# OVD // Highway — Arquitectura

[English](./architecture.md) · [Русский](./architecture.ru.md) · [Español](./architecture.es.md) · [简体中文](./architecture.zh-CN.md)

OVD // Highway es un motor de ingesta multiprotocolo zero-blocking: los conectores emiten señales sin procesar a canales, un pipeline de lane las filtra y califica con un LLM, y un dispatcher persiste y difunde los leads calificados. Todas las etapas están desacopladas mediante canales Go con buffer, por lo que un consumidor lento nunca bloquea la ingesta.

## Flujo de datos

```
 Telegram (MTProto) ─┐
 Reddit (OAuth2)    ─┼─> rawStream ─> dedup (Redis, TTL 72h) ─> dedupStream
 HeadHunter API     ─┘                                             │
 Mock source (dev)                                                ▼
                                              qualifier (LLM_CONCURRENCY workers, timeout 60s)
                                              ├─ prefilter + LLM triage
                                              └─ en fallo ─> DLQ (dead_letter_signals)
                                                           │
                                              leadStream ──> dispatcher
                                                           ├─> PostgreSQL (highway_leads)
                                                           ├─> WebSocket hub ──> UI dashboard
                                                           └─> metrics (Prometheus)
                                              DLQ worker ──> reevalúa señales
                                              archivadas y devuelve los leads
                                              resueltos al dispatcher
```

## Componentes

### Onramps (`internal/onramp`)

Conectores de fuentes que implementan `Start(ctx, out chan<- domain.RawSignal)`:

| Conector | Protocolo | Notas |
|---|---|---|
| `mtproto.go` | Telegram userbot (MTProto vía gotd) | Sesión persistente en `session.json`; whitelist de canales desde `TG_TARGET_CHANNELS`; el control plane puede añadir/quitar objetivos en caliente. |
| `reddit.go` | Reddit OAuth2 | Requiere `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER`. |
| `hh.go` | API pública de HeadHunter | Consulta `api.hh.ru/vacancies`; protegido por circuit breaker. |
| `mock.go` | En proceso | Se activa con `MOCK_ONRAMP=1` para desarrollo local. |

Los conectores se crean por cada fila habilitada de la tabla `sources` mediante `controlplane.SourceManager`; el conector MTProto está siempre activo porque posee la única sesión de Telegram.

### Lane (`internal/lane`)

- **Deduplicator** — basado en Redis, TTL 72 h, clave por contenido de la señal.
- **Prefilter** — filtro heurístico barato antes de la llamada al LLM.
- **Qualifier** — `LLM_CONCURRENCY` workers (por defecto 3, rango 1–10), timeout de 60 s por señal (cliente HTTP y contexto alineados). Aplica `system_prompt` y `min_score` de `engine_settings` (recarga en caliente vía `GET/PUT /api/v1/settings`). Usa OpenRouter por defecto o cualquier endpoint compatible con OpenAI mediante `LLM_BASE_URL`/`LLM_MODEL`.
- **Failure handler** — las señales que fallan en la evaluación LLM (HTTP 429/5xx) se archivan en `dead_letter_signals` y sus claves de dedup se liberan para reintento.

### Dispatcher (`cmd/engine/main.go`)

`dispatchLead` es el único punto de fan-out para leads calificados:

1. `store.SaveLead` — upsert en `highway_leads` (`ON CONFLICT DO NOTHING`).
2. `wsHub.Broadcast` — envía el lead a todos los clientes `/ws`.
3. `internal/metrics` — incrementa los contadores `ovd_leads_*`.

Los leads por debajo de `min_score` o no relevantes se contabilizan como dropped y no se persisten.

### Control plane (`internal/controlplane`)

API REST bajo `/api/v1/*`, protegida por `BearerAuth` (`ADMIN_API_TOKEN`):

- `GET/POST /sources`, `PATCH/DELETE /sources/{id}` — ciclo de vida dinámico de conectores.
- `GET/PUT /settings` — `system_prompt`, `min_score`.
- `PATCH /leads/{id}` — triage: `status` (`new`/`contacted`/`archived`) y `draft_proposal`.

### Persistencia (`internal/store`)

Esquema PostgreSQL (creado de forma idempotente al arrancar, reflejado en `migrations/`):

- `highway_leads` — `id` (UUID = id de señal), `track`, `source`, `score`, `tech_stack`, `payload` (JSONB completo de `QualifiedLead`), `status`, `created_at`.
- `sources` — registro de conectores que alimenta `SourceManager`.
- `engine_settings` — fila única (`id = 1`) con prompt y umbral.
- `dead_letter_signals` — archivo DLQ con `dedupe_key`, `retry_count`, `next_retry_at`.

### DLQ worker (`internal/pipeline/dlq`)

Worker en segundo plano que consulta `dead_letter_signals` en busca de filas con `next_retry_at` vencido. Reevalúa la señal archivada a través del qualifier y gestiona el ciclo de vida de la fila: la elimina en caso de éxito, incrementa `retry_count`/`next_retry_at`/`error_reason` en caso de error. Las señales nunca vuelven a `rawStream`, por lo que el estado de backoff no se reinicia.

### API surface (`internal/api`)

Un único servidor HTTP (`WS_ADDR`, por defecto `:8080`), envuelto en `api.CORS`:

| Ruta | Auth | Propósito |
|---|---|---|
| `GET /healthz` | ninguna | Liveness — `{"status":"ok"}` |
| `GET /readyz` | ninguna | Readiness — ping a Postgres y Redis (timeout 2 s), `200`/`503` |
| `GET /metrics` | ninguna | Exposición Prometheus en texto (métricas `ovd_*`) |
| `GET /ws` | `?token=` | Stream WebSocket de leads; el primer frame es `init` con el historial |
| `/api/v1/*` | `Authorization: Bearer` | Control plane REST |

## Propiedades clave

- **Zero blocking** — cada etapa del pipeline es un canal con buffer; la contrapresión se absorbe con la profundidad de cola, sin detener los conectores.
- **Entrega at-least-once** — las claves de dedup se liberan cuando el LLM falla, permitiendo que los reintentos reingieran; `SaveLead` es idempotente por id de señal.
- **Configuración en caliente** — prompt, umbral de puntuación y lista de fuentes cambian sin reinicio.
- **Observable** — logs JSON estructurados (`LOG_LEVEL=debug` para verboso), métricas Prometheus y endpoints de probes para orquestadores.
