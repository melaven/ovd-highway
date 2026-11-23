# OVD // Highway — Despliegue

[English](./deployment.md) · [Русский](./deployment.ru.md) · [Español](./deployment.es.md) · [简体中文](./deployment.zh-CN.md)

Esta guía despliega el motor con `docker compose`. El stack necesita tres servicios: PostgreSQL, Redis y el propio engine (que también sirve la API WebSocket que consume el dashboard `ui/`).

## Prerrequisitos

- Docker Engine con el plugin Compose (`docker compose version`).
- Un archivo `.env` en la raíz del repositorio (ver abajo).
- En el primer login MTProto, el engine escribe `session.json` junto a su directorio de trabajo — monta un volumen persistente o copia el archivo de vuelta tras el primer arranque.

## 1. Configurar el entorno

```bash
cp .env.example .env
```

Rellena todas las variables obligatorias — el engine se niega a arrancar sin ellas:

| Variable | Propósito |
|---|---|
| `DATABASE_URL` | DSN de PostgreSQL (`postgres://user:pass@host:5432/db?sslmode=...`) |
| `REDIS_URL` | URL de Redis (`redis://host:6379/0`) |
| `LLM_API_KEY` | Clave del modelo de calificación (OpenRouter o endpoint compatible con OpenAI) |
| `TG_API_ID`, `TG_API_HASH`, `TG_PHONE` | Credenciales MTProto de Telegram |
| `CONTROL_BOT_TOKEN`, `ADMIN_TG_ID` | Token del bot de control e id de usuario admin |

Recomendado en producción:

- `ADMIN_API_TOKEN` — **obligatorio** fuera de desarrollo local; protege `/api/v1/*` y el upgrade de `/ws`.
- `WS_ALLOWED_ORIGINS` — orígenes WebSocket adicionales permitidos (las variantes localhost siempre están permitidas).
- `LLM_BASE_URL`, `LLM_MODEL` — apuntar el qualifier a un endpoint propio o alternativo compatible con OpenAI.
- `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER` — habilitar el conector de Reddit.
- `TG_TARGET_CHANNELS` — canales de Telegram separados por comas.
- `LOG_LEVEL=debug` — logs estructurados detallados durante el diagnóstico.

Nunca subas `.env` al repositorio; está en `.gitignore`.

## 2. Compilar y arrancar

```bash
docker compose up -d --build
docker compose ps        # todos los servicios deben estar healthy/running
docker compose logs -f engine
```

En el primer arranque el engine crea el esquema (`highway_leads`, `sources`, `engine_settings`, `dead_letter_signals`), hidrata las fuentes habilitadas desde la tabla `sources` y escucha en `WS_ADDR` (por defecto `:8080`).

## 3. Verificar

```bash
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ready","checks":{"postgres":"ok","redis":"ok"}}
curl http://localhost:8080/metrics   # texto Prometheus (métricas ovd_*)
```

O ejecuta el script de humo incluido desde la raíz del repo:

```powershell
./scripts/smoke.ps1                 # usa $env:ADMIN_API_TOKEN si está definido
./scripts/smoke.ps1 -Token <token>  # bearer token explícito
```

El dashboard (`ui/`) se ejecuta aparte con `npm run dev` / `npm run build`; apunta `VITE_API_BASE` y `VITE_WS_URL` al engine desplegado.

## 4. Notas de reverse proxy en producción

Los endpoints de probes son intencionadamente públicos para que los orquestadores puedan consultarlos. En producción, restríngelos en el proxy:

- **`/metrics`** — expone contadores internos (`ovd_*`). Permite solo tu red de monitorización:

  ```nginx
  location = /metrics {
      allow 10.0.0.0/8;      # subred de monitorización
      deny all;
      proxy_pass http://engine:8080;
  }
  ```

- **`/healthz`, `/readyz`** — es seguro exponerlos solo al health-check del balanceador; deniega el acceso público si lo prefieres.
- **`/api/v1/*` y `/ws`** — ya están protegidos (`ADMIN_API_TOKEN`); mantenlo así y define `WS_ALLOWED_ORIGINS` con el origen de tu dashboard.
- Termina TLS en el proxy y reenvía la cabecera `Authorization` sin cambios; el engine no termina TLS.

## 5. Actualizaciones y rollback

```bash
docker compose pull        # o build desde un checkout etiquetado
docker compose up -d       # las migraciones de esquema son idempotentes (IF NOT EXISTS)
```

Las migraciones de `migrations/` están reflejadas en el DDL de arranque del engine, por lo que volver a un binario anterior es seguro siempre que ninguna migración posterior haya eliminado una columna que el binario antiguo necesite.
