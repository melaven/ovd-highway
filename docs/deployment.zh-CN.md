# OVD // Highway — 部署指南

[English](./deployment.md) · [Русский](./deployment.ru.md) · [Español](./deployment.es.md) · [简体中文](./deployment.zh-CN.md)

本指南使用 `docker compose` 部署引擎。该技术栈需要三个后端服务——PostgreSQL、Redis 以及引擎本身（它同时提供 `ui/` 仪表盘所使用的 WebSocket API）。

## 前置条件

- 安装了 Compose 插件的 Docker Engine（`docker compose version`）。
- 仓库根目录下的 `.env` 文件（见下文）。
- 首次 MTProto 登录时，引擎会在工作目录下写入 `session.json`——请挂载持久卷，或在首次启动后将该文件拷回。

## 1. 配置环境变量

```bash
cp .env.example .env
```

填写所有必填变量——缺少它们引擎将拒绝启动：

| 变量 | 用途 |
|---|---|
| `DATABASE_URL` | PostgreSQL DSN（`postgres://user:pass@host:5432/db?sslmode=...`） |
| `REDIS_URL` | Redis URL（`redis://host:6379/0`） |
| `LLM_API_KEY` | 资质评估模型密钥（OpenRouter 或 OpenAI 兼容端点） |
| `TG_API_ID`、`TG_API_HASH`、`TG_PHONE` | Telegram MTProto 凭据 |
| `CONTROL_BOT_TOKEN`、`ADMIN_TG_ID` | 控制机器人 token 与管理员用户 ID |

生产环境建议配置：

- `ADMIN_API_TOKEN` — 本地开发之外**必须**设置；用于保护 `/api/v1/*` 和 `/ws` 升级握手。
- `WS_ALLOWED_ORIGINS` — 额外允许的 WebSocket 来源（localhost 变体始终允许）。
- `LLM_BASE_URL`、`LLM_MODEL` — 将资质评估指向自建或其他 OpenAI 兼容端点。
- `REDDIT_CLIENT_ID`、`REDDIT_CLIENT_SECRET`、`REDDIT_USER` — 启用 Reddit 连接器。
- `TG_TARGET_CHANNELS` — 逗号分隔的 Telegram 频道列表。
- `LOG_LEVEL=debug` — 排障时输出详细结构化日志。

切勿提交 `.env`；它已列入 `.gitignore`。

## 2. 构建并启动

```bash
docker compose up -d --build
docker compose ps        # 所有服务应为 healthy/running
docker compose logs -f engine
```

首次启动时，引擎会创建 schema（`highway_leads`、`sources`、`engine_settings`、`dead_letter_signals`），从 `sources` 表加载启用的信号源，并开始监听 `WS_ADDR`（默认 `:8080`）。

## 3. 验证

```bash
curl http://localhost:8080/healthz   # {"status":"ok"}
curl http://localhost:8080/readyz    # {"status":"ready","checks":{"postgres":"ok","redis":"ok"}}
curl http://localhost:8080/metrics   # Prometheus 文本（ovd_* 指标）
```

也可以在仓库根目录运行自带的冒烟脚本：

```powershell
./scripts/smoke.ps1                 # 已设置 $env:ADMIN_API_TOKEN 时会自动使用
./scripts/smoke.ps1 -Token <token>  # 显式指定 bearer token
```

仪表盘（`ui/`）单独运行：`npm run dev` / `npm run build`；将 `VITE_API_BASE` 和 `VITE_WS_URL` 指向已部署的引擎。

## 4. 生产环境反向代理注意事项

探针端点刻意不要求认证，以便编排器抓取。在生产环境中应在代理层限制它们：

- **`/metrics`** — 暴露内部计数器（`ovd_*`）。仅允许监控网段访问：

  ```nginx
  location = /metrics {
      allow 10.0.0.0/8;      # 监控子网
      deny all;
      proxy_pass http://engine:8080;
  }
  ```

- **`/healthz`、`/readyz`** — 只需暴露给负载均衡的健康检查路径；如有需要可拒绝公网访问。
- **`/api/v1/*` 和 `/ws`** — 已受保护（`ADMIN_API_TOKEN`）；保持现状，并将 `WS_ALLOWED_ORIGINS` 设为你的仪表盘来源。
- 在代理层终止 TLS，并原样转发 `Authorization` 头；引擎自身不终止 TLS。

## 5. 升级与回滚

```bash
docker compose pull        # 或从带标签的 checkout 构建
docker compose up -d       # schema 迁移是幂等的（IF NOT EXISTS）
```

`migrations/` 中的迁移与引擎启动时的 DDL 保持一致，因此只要较新的迁移没有删除旧二进制所需的列，回滚二进制版本就是安全的。
