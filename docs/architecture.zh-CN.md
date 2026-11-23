# OVD // Highway — 架构

[English](./architecture.md) · [Русский](./architecture.ru.md) · [Español](./architecture.es.md) · [简体中文](./architecture.zh-CN.md)

OVD // Highway 是一个零阻塞的多协议采集引擎：各接入连接器将原始信号写入通道，lane 流水线对其进行过滤并借助 LLM 完成资质评估，dispatcher 负责持久化并广播合格的线索。所有阶段之间通过带缓冲的 Go channel 解耦，因此慢消费者永远不会阻塞采集。

## 数据流

```
 Telegram (MTProto) ─┐
 Reddit (OAuth2)    ─┼─> rawStream ─> dedup (Redis, TTL 72h) ─> dedupStream
 HeadHunter API     ─┘                                             │
 Mock source (dev)                                                ▼
                                              qualifier (4 workers, 45s 超时)
                                              ├─ prefilter + LLM triage
                                              └─ 失败时 ─> DLQ (dead_letter_signals)
                                                           │
                                              leadStream ──> dispatcher
                                                           ├─> PostgreSQL (highway_leads)
                                                           ├─> WebSocket hub ──> UI dashboard
                                                           └─> metrics (Prometheus)
                                              DLQ worker ──> 重新评估已归档
                                              信号，并将已解决的线索
                                              送回 dispatcher
```

## 组件

### Onramps（`internal/onramp`）

实现 `Start(ctx, out chan<- domain.RawSignal)` 的信号源连接器：

| 连接器 | 协议 | 说明 |
|---|---|---|
| `mtproto.go` | Telegram 用户客户端（经 gotd 的 MTProto） | 会话持久化在 `session.json`；频道白名单来自 `TG_TARGET_CHANNELS`；控制面可通过共享白名单在运行时增删目标。 |
| `reddit.go` | Reddit OAuth2 | 需要 `REDDIT_CLIENT_ID`、`REDDIT_CLIENT_SECRET`、`REDDIT_USER`。 |
| `hh.go` | HeadHunter 公开 API | 轮询 `api.hh.ru/vacancies`；带熔断器保护。 |
| `mock.go` | 进程内模拟源 | 通过 `MOCK_ONRAMP=1` 在本地开发时启用。 |

连接器由 `controlplane.SourceManager` 按 `sources` 表中每个启用行动态拉起；MTProto 连接器始终运行，因为它持有唯一的 Telegram 会话。

### Lane（`internal/lane`）

- **Deduplicator** — 基于 Redis，TTL 72 小时，按信号内容生成键。
- **Prefilter** — 在调用 LLM 之前的廉价启发式过滤。
- **Qualifier** — 4 个 worker，每条信号 45 秒超时。应用 `engine_settings` 中的 `system_prompt` 和 `min_score`（通过 `GET/PUT /api/v1/settings` 热更新）。默认使用 OpenRouter，也可通过 `LLM_BASE_URL`/`LLM_MODEL` 指向任意 OpenAI 兼容端点。
- **Failure handler** — LLM 评估失败（HTTP 429/5xx）的信号会被归档到 `dead_letter_signals`，其去重键被释放以便重试。

### Dispatcher（`cmd/engine/main.go`）

`dispatchLead` 是合格线索的唯一分发点：

1. `store.SaveLead` — 向 `highway_leads` 执行 upsert（`ON CONFLICT DO NOTHING`）。
2. `wsHub.Broadcast` — 将线索推送给所有 `/ws` 客户端。
3. `internal/metrics` — 递增 `ovd_leads_*` 计数器。

低于 `min_score` 或不相关的线索计为 dropped，不会持久化。

### 控制面（`internal/controlplane`）

位于 `/api/v1/*` 下的 REST API，由 `BearerAuth`（`ADMIN_API_TOKEN`）保护：

- `GET/POST /sources`、`PATCH/DELETE /sources/{id}` — 连接器的动态生命周期管理。
- `GET/PUT /settings` — `system_prompt`、`min_score`。
- `PATCH /leads/{id}` — 分诊：`status`（`new`/`contacted`/`archived`）和 `draft_proposal`。

### 持久化（`internal/store`）

PostgreSQL schema（启动时幂等创建，与 `migrations/` 中内容一致）：

- `highway_leads` — `id`（UUID = 信号 id）、`track`、`source`、`score`、`tech_stack`、`payload`（完整 `QualifiedLead` JSONB）、`status`、`created_at`。
- `sources` — 驱动 `SourceManager` 的连接器注册表。
- `engine_settings` — 单例行（`id = 1`），存放 prompt 与阈值。
- `dead_letter_signals` — DLQ 归档，含 `dedupe_key`、`retry_count`、`next_retry_at`。

### DLQ worker（`internal/pipeline/dlq`）

后台 worker 轮询 `next_retry_at` 已到期的 `dead_letter_signals` 行，通过 qualifier 重新评估归档信号，并负责行生命周期：成功则删除，失败则递增 `retry_count`/`next_retry_at`/`error_reason`。信号绝不会重新进入 `rawStream`，因此退避状态不会被重置。

### API 接口（`internal/api`）

同一 HTTP 服务器（`WS_ADDR`，默认 `:8080`），外层包裹 `api.CORS`：

| 路由 | 认证 | 用途 |
|---|---|---|
| `GET /healthz` | 无 | 存活探针 — `{"status":"ok"}` |
| `GET /readyz` | 无 | 就绪探针 — ping Postgres 与 Redis（各 2s 超时），`200`/`503` |
| `GET /metrics` | 无 | Prometheus 文本格式（`ovd_*` 指标） |
| `GET /ws` | `?token=` | WebSocket 线索流；首帧为 `init` 历史数据 |
| `/api/v1/*` | `Authorization: Bearer` | 控制面 REST |

## 关键特性

- **零阻塞** — 流水线每个阶段都是带缓冲的 channel；背压由队列深度吸收，而非停滞连接器。
- **至少一次投递** — LLM 失败时释放去重键，重试可重新摄入；`SaveLead` 按信号 id 幂等。
- **热配置** — prompt、评分阈值和信号源列表无需重启即可变更。
- **可观测** — 结构化 JSON 日志（`LOG_LEVEL=debug` 输出详细日志）、Prometheus 指标，以及供编排器使用的探针端点。
