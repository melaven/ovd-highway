# OVD // Highway — Операционный регламент

[English](./runbook.md) · [Русский](./runbook.ru.md) · [Español](./runbook.es.md) · [简体中文](./runbook.zh-CN.md)

Процедуры по симптомам для работающего движка. Все эндпоинты ниже живут на одном HTTP-сервере (`WS_ADDR`, по умолчанию `:8080`) и не требуют авторизации, если не указано иное.

## Первая реакция: жив ли сервис?

```bash
curl -s localhost:8080/healthz   # liveness — 200 {"status":"ok"}
curl -s localhost:8080/readyz    # readiness — 200 ready / 503 not_ready
curl -s localhost:8080/metrics   # Prometheus-выгрузка (метрики ovd_*)
```

- `healthz` падает → процесс мёртв или порт недоступен. Проверьте `docker compose ps`, `docker compose logs engine`, перезапустите.
- `readyz` возвращает `503` → смотрите `checks`: `postgres` и `redis` сообщают `"ok"` или текст ошибки. Движок продолжает работать, но не может сохранять/дедуплицировать — считайте это деградацией.

## Чтение /metrics

Ключевые серии (все `ovd_*`):

| Метрика | Значение |
|---|---|
| `ovd_uptime_seconds`, `ovd_goroutines`, `ovd_mem_*`, `ovd_gc_cycles_total` | Здоровье рантайма — следите за ростом горутин/памяти. |
| `ovd_leads_dispatched_total` | Лиды, обработанные диспетчером. |
| `ovd_leads_saved_total` | Лиды, сохранённые и разосланные. |
| `ovd_leads_dropped_total` | Отфильтрованные по релевантности/min_score — нормальный churn. |
| `ovd_dispatch_errors_total` | Ошибки сохранения/рассылки — investigate Postgres/WS. |
| `ovd_dlq_retries_scheduled_total`, `ovd_dlq_resolved_total` | Активность DLQ. |
| `ovd_dlq_store_errors_total` | Ошибки персистентности DLQ — проблема Postgres. |

Плоский `ovd_leads_dispatched_total` при работающих коннекторах обычно означает, что LLM-квалификатор тормозит или падает — смотрите рост DLQ.

## Postgres недоступен или деградировал

Симптомы: `readyz.checks.postgres` != `"ok"`, растёт `ovd_dispatch_errors_total`, растёт `ovd_dlq_store_errors_total`.

1. Проверьте базу: `docker compose ps`, логи Postgres, место на диске, соединения (пул — максимум 25 открытых).
2. Движок **не** завершается при потере БД — ошибки `SaveLead` видны через `dispatch_errors`. Почините Postgres; лиды в каналах догонятся автоматически после восстановления записей.
3. При долгом сбое ожидайте накопления DLQ (см. ниже).

## Redis недоступен или деградировал

Симптомы: `readyz.checks.redis` != `"ok"`; при старте engine падает сразу (`redis ping`), если Redis недоступен — перезапустите после восстановления Redis.

Влияние во время работы: деградация dedup-ключей и DLQ dedup; не оставляйте систему надолго без Redis — вниз по потоку пойдут дубликаты.

## Сбой LLM API (429 / 5xx)

Симптомы: `ovd_leads_dispatched_total` замирает, растёт `dead_letter_signals`, в логах ошибки квалификатора.

1. Проверьте статус провайдера и `LLM_API_KEY`/`LLM_BASE_URL`/`LLM_MODEL`.
2. Сигналы, упавшие на оценке, архивируются автоматически — ничего не теряется.
3. После восстановления провайдера DLQ-воркер сам повторит подошедшие строки (`next_retry_at` backoff). Ручная переотправка не нужна.

## Разгребание DLQ

Таблица: `dead_letter_signals` (`raw_payload`, `error_reason`, `dedupe_key`, `retry_count`, `next_retry_at`).

Оценка бэклога:

```sql
SELECT count(*) AS due_now
FROM dead_letter_signals
WHERE next_retry_at IS NULL OR next_retry_at <= NOW();

SELECT source, count(*), max(retry_count)
FROM dead_letter_signals
GROUP BY source;
```

- Воркер непрерывно забирает подошедшие строки; при каждой ошибке `retry_count` растёт, а `next_retry_at` отодвигается.
- «Ядовитые» строки: если сигнал падает стабильно (например, битый payload), смотрите `error_reason`, затем чините первопричину или удаляйте строку вручную:

  ```sql
  DELETE FROM dead_letter_signals WHERE id = '<uuid>';
  ```

- Массовая requeue после сбоя: строки становятся due автоматически по мере истечения `next_retry_at`; чтобы форсировать немедленный retry:

  ```sql
  UPDATE dead_letter_signals SET next_retry_at = NOW() WHERE next_retry_at > NOW();
  ```

Следите за `ovd_dlq_resolved_total` против `ovd_dlq_retries_scheduled_total`, чтобы убедиться, что очередь убывает.

## Проблемы control plane

- `401` на `/api/v1/*` или `/ws` → неверный/отсутствующий `ADMIN_API_TOKEN` (или `?token=` для WS). UI показывает Token Lock modal; очистите `ovd_admin_token` в localStorage и введите заново.
- Источники не запускаются → проверьте строки в `sources` (`enabled = true`) и логи коннекторов; `SourceManager` сверяется при каждой API-мутации и при старте (`Hydrate`).
- Настройки не применяются → `PUT /api/v1/settings` пишет в `engine_settings`; qualifier подхватывает изменения на следующем сигнале.

## Процедура перезапуска

```bash
docker compose restart engine     # graceful: SIGTERM останавливает errgroup, 5s HTTP shutdown
docker compose up -d --build      # после смены конфига/кода
```

Запуск идемпотентен: DDL схемы использует `IF NOT EXISTS`, источники/настройки гидрируются из Postgres, а MTProto-сессия продолжается из `session.json`.
