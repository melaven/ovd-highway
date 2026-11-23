# OVD // Highway — Архитектура

[English](./architecture.md) · [Русский](./architecture.ru.md) · [Español](./architecture.es.md) · [简体中文](./architecture.zh-CN.md)

OVD // Highway — это zero-blocking движок мультипротокольного сбора данных: коннекторы-источники отправляют сырые сигналы в каналы, lane-пайплайн фильтрует и квалифицирует их с помощью LLM, а диспетчер сохраняет и рассылает квалифицированные лиды. Все стадии разделены буферизованными каналами Go, поэтому медленный потребитель никогда не блокирует ingestion.

## Поток данных

```
 Telegram (MTProto) ─┐
 Reddit (OAuth2)    ─┼─> rawStream ─> dedup (Redis, TTL 72ч) ─> dedupStream
 HeadHunter API     ─┘                                             │
 Mock source (dev)                                                ▼
                                              qualifier (4 воркера, таймаут 45с)
                                              ├─ prefilter + LLM triage
                                              └─ при ошибке ─> DLQ (dead_letter_signals)
                                                           │
                                              leadStream ──> dispatcher
                                                           ├─> PostgreSQL (highway_leads)
                                                           ├─> WebSocket hub ──> UI dashboard
                                                           └─> metrics (Prometheus)
                                              DLQ worker ──> повторно оценивает
                                              архивные сигналы и возвращает
                                              решённые лиды в dispatcher
```

## Компоненты

### Onramps (`internal/onramp`)

Коннекторы источников, реализующие `Start(ctx, out chan<- domain.RawSignal)`:

| Коннектор | Протокол | Примечания |
|---|---|---|
| `mtproto.go` | Telegram userbot (MTProto через gotd) | Персистентная сессия в `session.json`; whitelist каналов из `TG_TARGET_CHANNELS`; control plane может добавлять/удалять цели на лету через общий whitelist. |
| `reddit.go` | Reddit OAuth2 | Требуются `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER`. |
| `hh.go` | Публичный API HeadHunter | Опрашивает `api.hh.ru/vacancies`; защищён circuit breaker'ом. |
| `mock.go` | Внутрипроцессный | Включается `MOCK_ONRAMP=1` для локальной разработки. |

Коннекторы запускаются для каждой включённой записи таблицы `sources` менеджером `controlplane.SourceManager`; MTProto-коннектор работает всегда, так как владеет единственной Telegram-сессией.

### Lane (`internal/lane`)

- **Deduplicator** — на Redis, TTL 72 часа, ключ по содержимому сигнала.
- **Prefilter** — дешёвый эвристический фильтр перед вызовом LLM.
- **Qualifier** — 4 воркера, таймаут 45 с на сигнал. Применяет `system_prompt` и `min_score` из `engine_settings` (горячая перезагрузка через `GET/PUT /api/v1/settings`). По умолчанию OpenRouter либо любой OpenAI-совместимый endpoint через `LLM_BASE_URL`/`LLM_MODEL`.
- **Failure handler** — сигналы, упавшие на оценке LLM (HTTP 429/5xx), архивируются в `dead_letter_signals`, их dedup-ключи освобождаются для retry.

### Dispatcher (`cmd/engine/main.go`)

`dispatchLead` — единая точка fan-out для квалифицированных лидов:

1. `store.SaveLead` — upsert в `highway_leads` (`ON CONFLICT DO NOTHING`).
2. `wsHub.Broadcast` — отправка лида всем клиентам `/ws`.
3. `internal/metrics` — инкремент счётчиков `ovd_leads_*`.

Лиды ниже `min_score` или нерелевантные считаются dropped и не сохраняются.

### Control plane (`internal/controlplane`)

REST API под `/api/v1/*`, защищён `BearerAuth` (`ADMIN_API_TOKEN`):

- `GET/POST /sources`, `PATCH/DELETE /sources/{id}` — динамический жизненный цикл коннекторов.
- `GET/PUT /settings` — `system_prompt`, `min_score`.
- `PATCH /leads/{id}` — триаж: `status` (`new`/`contacted`/`archived`) и `draft_proposal`.

### Persistence (`internal/store`)

Схема PostgreSQL (создаётся идемпотентно при старте, продублирована в `migrations/`):

- `highway_leads` — `id` (UUID = id сигнала), `track`, `source`, `score`, `tech_stack`, `payload` (полный JSONB `QualifiedLead`), `status`, `created_at`.
- `sources` — реестр коннекторов, управляемый `SourceManager`.
- `engine_settings` — синглтон (`id = 1`) с промптом и порогом.
- `dead_letter_signals` — DLQ-архив с `dedupe_key`, `retry_count`, `next_retry_at`.

### DLQ worker (`internal/pipeline/dlq`)

Фоновый воркер, опрашивающий `dead_letter_signals` на строки с наступившим `next_retry_at`. Повторно оценивает архивный сигнал через qualifier и владеет жизненным циклом строки: удаляет при успехе, увеличивает `retry_count`/`next_retry_at`/`error_reason` при ошибке. Сигналы никогда не возвращаются в `rawStream`, поэтому состояние backoff не сбрасывается.

### API surface (`internal/api`)

Один HTTP-сервер (`WS_ADDR`, по умолчанию `:8080`), обёрнутый в `api.CORS`:

| Маршрут | Auth | Назначение |
|---|---|---|
| `GET /healthz` | нет | Liveness — `{"status":"ok"}` |
| `GET /readyz` | нет | Readiness — ping Postgres и Redis (таймаут 2 с), `200`/`503` |
| `GET /metrics` | нет | Prometheus text exposition (метрики `ovd_*`) |
| `GET /ws` | `?token=` | WebSocket-поток лидов; первый фрейм — `init` с историей |
| `/api/v1/*` | `Authorization: Bearer` | Control plane REST |

## Ключевые свойства

- **Zero blocking** — каждая стадия пайплайна — буферизованный канал; backpressure поглощается глубиной очереди, а не остановкой коннекторов.
- **At-least-once delivery** — dedup-ключи освобождаются при сбое LLM, поэтому retry может переобработать сигнал; `SaveLead` идемпотентен по id сигнала.
- **Горячая конфигурация** — промпт, порог оценки и список источников меняются без рестарта.
- **Наблюдаемость** — структурированные JSON-логи (`LOG_LEVEL=debug` для подробного вывода), Prometheus-метрики и probe-эндпоинты для оркестраторов.
