# OVD // Highway — 运维手册

[English](./runbook.md) · [Русский](./runbook.ru.md) · [Español](./runbook.es.md) · [简体中文](./runbook.zh-CN.md)

针对运行中引擎的症状驱动操作流程。以下端点均位于同一 HTTP 服务器（`WS_ADDR`，默认 `:8080`），除非另有说明否则无需认证。

## 首要响应：服务是否存活？

```bash
curl -s localhost:8080/healthz   # 存活 — 200 {"status":"ok"}
curl -s localhost:8080/readyz    # 就绪 — 200 ready / 503 not_ready
curl -s localhost:8080/metrics   # Prometheus 导出（ovd_* 指标）
```

- `healthz` 失败 → 进程已停止或端口不可达。检查 `docker compose ps`、`docker compose logs engine` 并重启。
- `readyz` 返回 `503` → 查看 `checks`：`postgres` 和 `redis` 分别报告 `"ok"` 或错误文本。引擎仍在运行但无法持久化/去重——按降级处理。

## 解读 /metrics

关键序列（均为 `ovd_*`）：

| 指标 | 含义 |
|---|---|
| `ovd_uptime_seconds`、`ovd_goroutines`、`ovd_mem_*`、`ovd_gc_cycles_total` | 运行时健康度——关注 goroutine/内存增长。 |
| `ovd_leads_dispatched_total` | dispatcher 已处理的线索数。 |
| `ovd_leads_saved_total` | 已持久化并广播的线索数。 |
| `ovd_leads_dropped_total` | 因相关性/min_score 被过滤——属正常损耗。 |
| `ovd_dispatch_errors_total` | 持久化/广播失败——排查 Postgres/WS。 |
| `ovd_dlq_retries_scheduled_total`、`ovd_dlq_resolved_total` | DLQ 活动量。 |
| `ovd_dlq_store_errors_total` | DLQ 持久化失败——Postgres 问题。 |

连接器正常运行但 `ovd_leads_dispatched_total` 持平，通常意味着 LLM 资质评估缓慢或失败——接着查看 DLQ 是否增长。

## Postgres 宕机或降级

症状：`readyz.checks.postgres` != `"ok"`，`ovd_dispatch_errors_total` 上升，`ovd_dlq_store_errors_total` 上升。

1. 确认数据库状态：`docker compose ps`、Postgres 日志、磁盘空间和连接数（连接池最多 25 个）。
2. 引擎在数据库丢失时**不会**退出——`SaveLead` 错误通过 `dispatch_errors` 呈现。修复 Postgres；写入恢复后，通道中的线索会自动排空。
3. 若中断时间较长，预期会出现 DLQ 积压（见下文）。

## Redis 宕机或降级

症状：`readyz.checks.redis` != `"ok"`；Redis 不可达时引擎启动即失败（`redis ping`）——待 Redis 恢复后重启。

运行中影响：去重键与 DLQ 去重退化；不要让系统长时间无 Redis 运行，否则下游会出现重复数据。

## LLM API 故障（429 / 5xx）

症状：`ovd_leads_dispatched_total` 停滞，`dead_letter_signals` 增长，日志中 qualifier 报错。

1. 检查服务商状态以及 `LLM_API_KEY`/`LLM_BASE_URL`/`LLM_MODEL`。
2. 评估失败的信号会被自动归档——不会丢失任何数据。
3. 服务商恢复后，DLQ worker 会自行重试到期行（`next_retry_at` 退避）。无需手动重新摄入。

## 排空 DLQ

表：`dead_letter_signals`（`raw_payload`、`error_reason`、`dedupe_key`、`retry_count`、`next_retry_at`）。

查看积压：

```sql
SELECT count(*) AS due_now
FROM dead_letter_signals
WHERE next_retry_at IS NULL OR next_retry_at <= NOW();

SELECT source, count(*), max(retry_count)
FROM dead_letter_signals
GROUP BY source;
```

- Worker 持续领取到期行；每次失败 `retry_count` 递增，`next_retry_at` 顺延。
- 毒丸行：若某条信号持续失败（如 payload 损坏），检查 `error_reason`，修复根因或手动删除该行：

  ```sql
  DELETE FROM dead_letter_signals WHERE id = '<uuid>';
  ```

- 故障后批量重排：行会随 `next_retry_at` 到期自动变为可处理；如需立即重试：

  ```sql
  UPDATE dead_letter_signals SET next_retry_at = NOW() WHERE next_retry_at > NOW();
  ```

对比 `ovd_dlq_resolved_total` 与 `ovd_dlq_retries_scheduled_total` 以确认队列正在下降。

## 控制面问题

- `/api/v1/*` 或 `/ws` 返回 `401` → `ADMIN_API_TOKEN` 错误/缺失（WS 使用 `?token=`）。UI 会弹出 Token Lock 模态框；清除 localStorage 中的 `ovd_admin_token` 后重新输入。
- 信号源不启动 → 检查 `sources` 表中的行（`enabled = true`）及连接器日志；`SourceManager` 在每次 API 变更和启动时（`Hydrate`）进行 reconcile。
- 设置不生效 → `PUT /api/v1/settings` 持久化到 `engine_settings`；qualifier 在下一条信号时生效。

## 重启流程

```bash
docker compose restart engine     # 优雅停机：SIGTERM 停止 errgroup，HTTP 5s 关闭
docker compose up -d --build      # 配置/代码变更后使用
```

启动是幂等的：schema DDL 使用 `IF NOT EXISTS`，信号源/设置从 Postgres 水合，MTProto 会话从 `session.json` 恢复。

## v1.0.0 发布 smoke 与 soak 检查清单

在打 `v1.0.0` 标签前执行。需要运行中的 engine（`docker compose up -d`）和 `ADMIN_API_TOKEN`。

### Smoke（约 5 分钟）

| 检查 | 命令 / 步骤 | 通过标准 |
|---|---|---|
| Liveness | `curl -s localhost:8080/healthz` | `200` `{"status":"ok"}` |
| Readiness | `curl -s localhost:8080/readyz` | `200`；`checks.postgres` 与 `checks.redis` 为 `"ok"` |
| 指标 | `curl -s localhost:8080/metrics` | 暴露 `ovd_uptime_seconds`、`ovd_dlq_*`、`ovd_tg_*_dropped_total` |
| API 鉴权 | `curl -s -o /dev/null -w '%{http_code}' localhost:8080/api/v1/sources` | 无 token 返回 `401`；带 `Authorization: Bearer $ADMIN_API_TOKEN` 返回 `200` |
| DLQ API | `curl -s -H "Authorization: Bearer $ADMIN_API_TOKEN" 'localhost:8080/api/v1/dlq?limit=5'` | `200` JSON，含 `count` 与 `items` |
| UI | 打开 `http://localhost:8080` | 仪表盘正常加载；底部状态栏可见 DLQ 徽标 |

### Telegram 实时 soak（30–60 分钟）

1. 确认 `sources` 表中有一条启用的 `tg` 类型记录（`enabled = true`），指向真实频道。
2. 等待（或发布）多条消息，包括一个相册和一条仅含媒体的帖子。
3. 在 `/metrics` 上，`ovd_tg_empty_media_dropped_total` 与 `ovd_tg_album_duplicates_dropped_total` 在丢弃时应递增。
4. LLM 正常响应时 `ovd_leads_dispatched_total` 应增长；`GET /api/v1/dlq` 应显示 `count: 0` 或队列正在排空（`ovd_dlq_resolved_total` 上升）。

### Pass / fail

- **Pass**：所有 smoke 检查通过；soak 产生线索或干净丢弃；`ovd_dlq_store_errors_total` 不增长；soak 后 `ovd_goroutines` 保持稳定。
- **出现以下情况不要打标签**：探针出现任何 5xx、DLQ 增长且 `next_retry_at` 无进展、qualifier 错误持续超过 5 分钟、或 UI 无法加载。
