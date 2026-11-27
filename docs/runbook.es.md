# OVD // Highway — Runbook de operaciones

[English](./runbook.md) · [Русский](./runbook.ru.md) · [Español](./runbook.es.md) · [简体中文](./runbook.zh-CN.md)

Procedimientos orientados a síntomas para el motor en ejecución. Todos los endpoints viven en el mismo servidor HTTP (`WS_ADDR`, por defecto `:8080`) y no requieren autenticación salvo indicación contraria.

## Primera respuesta: ¿está vivo?

```bash
curl -s localhost:8080/healthz   # liveness — 200 {"status":"ok"}
curl -s localhost:8080/readyz    # readiness — 200 ready / 503 not_ready
curl -s localhost:8080/metrics   # exposición Prometheus (métricas ovd_*)
```

- `healthz` falla → el proceso está caído o el puerto no responde. Revisa `docker compose ps`, `docker compose logs engine` y reinicia.
- `readyz` devuelve `503` → lee `checks`: `postgres` y `redis` reportan `"ok"` o el texto del error. El motor sigue vivo pero no puede persistir/deduplicar — trátalo como degradado.

## Lectura de /metrics

Series clave (todas `ovd_*`):

| Métrica | Significado |
|---|---|
| `ovd_uptime_seconds`, `ovd_goroutines`, `ovd_mem_*`, `ovd_gc_cycles_total` | Salud del runtime — vigila el crecimiento de goroutines/memoria. |
| `ovd_leads_dispatched_total` | Leads procesados por el dispatcher. |
| `ovd_leads_saved_total` | Leads persistidos y difundidos. |
| `ovd_leads_dropped_total` | Filtrados por relevancia/min_score — churn normal. |
| `ovd_dispatch_errors_total` | Fallos de persistencia/difusión — investiga Postgres/WS. |
| `ovd_dlq_retries_scheduled_total`, `ovd_dlq_resolved_total` | Actividad de la DLQ. |
| `ovd_dlq_store_errors_total` | Fallos de persistencia de la DLQ — problema de Postgres. |

Un `ovd_leads_dispatched_total` plano con conectores activos suele indicar que el qualifier LLM va lento o falla — revisa el crecimiento de la DLQ.

## Postgres caído o degradado

Síntomas: `readyz.checks.postgres` != `"ok"`, `ovd_dispatch_errors_total` subiendo, `ovd_dlq_store_errors_total` subiendo.

1. Verifica la base: `docker compose ps`, logs de Postgres, espacio en disco y conexiones (el pool abre máx. 25).
2. El motor **no** se detiene al perder la BD — los errores de `SaveLead` se reflejan en `dispatch_errors`. Arregla Postgres; los leads encolados se drenan solos al recuperarse las escrituras.
3. Si la caída fue larga, espera una acumulación en la DLQ (ver abajo).

## Redis caído o degradado

Síntomas: `readyz.checks.redis` != `"ok"`; el engine falla al arrancar (`redis ping`) si Redis no responde — reinicia cuando Redis se recupere.

Impacto en caliente: dedup y dedup de DLQ degradados; no dejes el sistema mucho tiempo sin Redis o fluirán duplicados.

## Caída de la API LLM (429 / 5xx)

Síntomas: `ovd_leads_dispatched_total` se estanca, `dead_letter_signals` crece, logs con errores del qualifier.

1. Comprueba el estado del proveedor y `LLM_API_KEY`/`LLM_BASE_URL`/`LLM_MODEL`.
2. Las señales que fallan en la evaluación se archivan automáticamente — no se pierde nada.
3. Cuando el proveedor se recupere, el worker de DLQ reintentará las filas vencidas por sí solo (backoff `next_retry_at`). No hace falta reingesta manual.

## Drenar la DLQ

Tabla: `dead_letter_signals` (`raw_payload`, `error_reason`, `dedupe_key`, `retry_count`, `next_retry_at`).

Inspeccionar la acumulación:

```sql
SELECT count(*) AS due_now
FROM dead_letter_signals
WHERE next_retry_at IS NULL OR next_retry_at <= NOW();

SELECT source, count(*), max(retry_count)
FROM dead_letter_signals
GROUP BY source;
```

- El worker recoge filas vencidas continuamente; `retry_count` crece y `next_retry_at` se retrasa en cada fallo.
- Filas "venenosas": si una señal falla siempre (p. ej. payload malformado), revisa `error_reason`, corrige la causa raíz o borra la fila:

  ```sql
  DELETE FROM dead_letter_signals WHERE id = '<uuid>';
  ```

- Reencolado masivo tras una caída: las filas vencen solas según `next_retry_at`; para forzar reintento inmediato:

  ```sql
  UPDATE dead_letter_signals SET next_retry_at = NOW() WHERE next_retry_at > NOW();
  ```

Observa `ovd_dlq_resolved_total` frente a `ovd_dlq_retries_scheduled_total` para confirmar que la cola baja.

## Problemas del control plane

- `401` en `/api/v1/*` o `/ws` → `ADMIN_API_TOKEN` incorrecto/ausente (o `?token=` en WS). La UI muestra el modal Token Lock; borra `ovd_admin_token` de localStorage y vuelve a introducirlo.
- Las fuentes no arrancan → revisa las filas de `sources` (`enabled = true`) y los logs de conectores; `SourceManager` reconcilia en cada mutación API y al arrancar (`Hydrate`).
- Los ajustes no se aplican → `PUT /api/v1/settings` persiste en `engine_settings`; el qualifier los recoge en la siguiente señal.

## Procedimiento de reinicio

```bash
docker compose restart engine     # graceful: SIGTERM detiene el errgroup, shutdown HTTP de 5s
docker compose up -d --build      # tras cambiar config/código
```

El arranque es idempotente: el DDL del esquema usa `IF NOT EXISTS`, fuentes/ajustes se hidratan desde Postgres y la sesión MTProto se reanuda desde `session.json`.

## Checklist de smoke y soak para el release v1.0.0

Ejecutar antes de etiquetar `v1.0.0`. Requiere el engine en marcha (`docker compose up -d`) y `ADMIN_API_TOKEN`.

### Smoke (~5 min)

| Comprobación | Comando / paso | Criterio de pase |
|---|---|---|
| Liveness | `curl -s localhost:8080/healthz` | `200` `{"status":"ok"}` |
| Readiness | `curl -s localhost:8080/readyz` | `200`; `checks.postgres` y `checks.redis` en `"ok"` |
| Métricas | `curl -s localhost:8080/metrics` | expone `ovd_uptime_seconds`, `ovd_dlq_*`, `ovd_tg_*_dropped_total` |
| Auth API | `curl -s -o /dev/null -w '%{http_code}' localhost:8080/api/v1/sources` | `401` sin token; `200` con `Authorization: Bearer $ADMIN_API_TOKEN` |
| DLQ API | `curl -s -H "Authorization: Bearer $ADMIN_API_TOKEN" 'localhost:8080/api/v1/dlq?limit=5'` | `200` JSON con `count` e `items` |
| UI | abrir `http://localhost:8080` | el dashboard carga; badge DLQ visible en la barra de estado inferior |

### Soak en vivo de Telegram (30–60 min)

1. Verifica que haya una fila `sources` de tipo `tg` habilitada (`enabled = true`) apuntando a un canal real.
2. Espera (o publica) varios mensajes, incluido un álbum y una publicación solo con media.
3. En `/metrics`, `ovd_tg_empty_media_dropped_total` y `ovd_tg_album_duplicates_dropped_total` deben incrementarse con los descartes.
4. `ovd_leads_dispatched_total` debe crecer cuando el LLM responde; `GET /api/v1/dlq` debe mostrar `count: 0` o una cola que drena (`ovd_dlq_resolved_total` sube).

### Pass / fail

- **Pass**: todas las comprobaciones smoke en verde; el soak produce leads o descartes limpios; `ovd_dlq_store_errors_total` plano; `ovd_goroutines` estable tras el soak.
- **No etiquetar si**: cualquier 5xx en las sondas, DLQ creciendo sin progreso de `next_retry_at`, errores del qualifier >5 min, o la UI no carga.
