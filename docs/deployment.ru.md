# OVD // Highway — Развёртывание

[English](./deployment.md) · [Русский](./deployment.ru.md) · [Español](./deployment.es.md) · [简体中文](./deployment.zh-CN.md)

Это руководство описывает запуск движка через `docker compose`. Стеку нужны три компонента — PostgreSQL, Redis и сам engine (он же отдаёт WebSocket API, который использует дашборд `ui/`).

## Предварительные требования

- Docker Engine с плагином Compose (`docker compose version`).
- Файл `.env` в корне репозитория (см. ниже).
- При первом MTProto-логине engine записывает `session.json` рядом с рабочей директорией — подключите постоянный volume или скопируйте файл обратно после первого запуска.

## 1. Настройка окружения

```bash
cp .env.example .env
```

Заполните все обязательные переменные — без них engine не запустится:

| Переменная | Назначение |
|---|---|
| `DATABASE_URL` | DSN PostgreSQL (`postgres://user:pass@host:5432/db?sslmode=...`) |
| `REDIS_URL` | URL Redis (`redis://host:6379/0`) |
| `LLM_API_KEY` | Ключ модели-квалификатора (OpenRouter или OpenAI-совместимый endpoint) |
| `TG_API_ID`, `TG_API_HASH`, `TG_PHONE` | Учётные данные Telegram MTProto |
| `CONTROL_BOT_TOKEN`, `ADMIN_TG_ID` | Токен управляющего бота и id администратора |

Рекомендуется для продакшена:

- `ADMIN_API_TOKEN` — **обязательно** вне локальной разработки; защищает `/api/v1/*` и upgrade `/ws`.
- `WS_ALLOWED_ORIGINS` — дополнительные разрешённые origin для WebSocket (localhost-варианты разрешены всегда).
- `LLM_BASE_URL`, `LLM_MODEL` — направить квалификатор на self-hosted или альтернативный OpenAI-совместимый endpoint.
- `LLM_CONCURRENCY` — число параллельных LLM-воркеров квалификации (по умолчанию `3`, диапазон `1..10`).
- `/api/v1/dlq` (`GET`) и `/api/v1/dlq/retry` (`POST`) — просмотр и ручной перезапуск сигналов из dead-letter queue; выведены в нижней статус-строке дашборда.
- `REDDIT_CLIENT_ID`, `REDDIT_CLIENT_SECRET`, `REDDIT_USER` — включить Reddit-коннектор.
- `TG_TARGET_CHANNELS` — список Telegram-каналов через запятую.
- `LOG_LEVEL=debug` — подробные структурированные логи при диагностике.

Никогда не коммитьте `.env`; он указан в `.gitignore`.

## 2. Сборка и запуск

```bash
docker compose up -d --build
docker compose ps        # все сервисы должны быть healthy/running
docker compose logs -f engine
```

При первом старте engine создаёт схему (`highway_leads`, `sources`, `engine_settings`, `dead_letter_signals`), загружает включённые источники из таблицы `sources` и начинает слушать `WS_ADDR` (по умолчанию `:8080`).

## 3. Проверка

```bash
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ready","checks":{"postgres":"ok","redis":"ok"}}
curl http://localhost:8080/metrics   # Prometheus text (метрики ovd_*)
```

Или запустите прилагаемый smoke-скрипт из корня репозитория:

```powershell
./scripts/smoke.ps1                 # использует $env:ADMIN_API_TOKEN, если задан
./scripts/smoke.ps1 -Token <token>  # явный bearer-токен
```

Дашборд (`ui/`) запускается отдельно через `npm run dev` / `npm run build`; укажите `VITE_API_BASE` и `VITE_WS_URL` на развёрнутый engine.

## 4. Reverse-proxy в продакшене

Probe-эндпоинты намеренно не требуют авторизации, чтобы оркестраторы могли их опрашивать. В продакшене ограничьте их на уровне прокси:

- **`/metrics`** — раскрывает внутренние счётчики (`ovd_*`). Разрешите только мониторинговую сеть:

  ```nginx
  location = /metrics {
      allow 10.0.0.0/8;      # подсеть мониторинга
      deny all;
      proxy_pass http://engine:8080;
  }
  ```

- **`/healthz`, `/readyz`** — безопасно отдавать только health-check'ам балансировщика; при желании запретите публичный доступ.
- **`/api/v1/*` и `/ws`** — уже защищены (`ADMIN_API_TOKEN`); сохраните это и задайте `WS_ALLOWED_ORIGINS` на origin вашего дашборда.
- Завершайте TLS на прокси и прокидывайте заголовок `Authorization` без изменений; сам engine TLS не терминирует.

## 5. Обновления и откат

```bash
docker compose pull        # или сборка из тегированного checkout
docker compose up -d       # миграции схемы идемпотентны (IF NOT EXISTS)
```

Миграции из `migrations/` дублируются boot-time DDL движка, поэтому откат бинарника безопасен, пока более поздняя миграция не удалила колонку, нужную старой версии.
