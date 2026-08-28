> **Go 实现版本。** 为什么同时存在 Elixir 版本，见[根目录 README](../README.md)。
> 与 Elixir 版本的逐点技术对比见 [`COMPARISON.md`](../COMPARISON.md)。
> AI 使用说明见 [`AI_USAGE.md`](../AI_USAGE.md)。

# rc_LingHeChen — API Notification Delivery Service（Go 版本）

一个内部 HTTP 通知投递服务。业务系统提交通知请求后立即返回，服务在后台负责可靠地将请求投递到目标外部 API。

---

## 问题理解

本质上这是一个**异步可靠投递**问题，核心矛盾是：

- 业务系统要求**低延迟**（提交即返回）
- 外部 API 要求**可靠到达**（对端可能随时不可用）

解决方法是解耦：业务系统写队列，worker 异步投递。同时，不同外部供应商的 URL、Header、Body 格式差异很大，本服务通过**供应商配置**统一持有这些信息，业务系统只需传入语义数据。

---

## 架构

```
业务系统
  │
  │ POST /notifications/:vendor_name
  │ Header: Idempotency-Key: <key>
  ▼
┌─────────────┐     写入      ┌──────────────────────┐
│  HTTP 接收层 │ ──────────▶ │   PostgreSQL 队列      │
│   (Gin)     │  返回 202    │  (notification_jobs)  │
└─────────────┘             └──────────┬───────────┘
                                       │  SELECT FOR UPDATE SKIP LOCKED
                                       ▼
                            ┌──────────────────────┐
                            │    Worker Pool        │
                            │  (5 goroutines)       │
                            └──────────┬───────────┘
                                       │
                          ┌────────────┴────────────┐
                          │                         │
                     成功 Ack                   失败 Nack
                     status=done           指数退避重试
                                          超限 → DLQ (status=dead)

供应商配置（vendor_configs 表）在入队时快照，变更不影响在途任务。
```

---

## 关键设计决策

### 1. 投递语义：At-least-once

选择**至少一次**。Exactly-once 需要对端支持幂等，无法单方面保证。  
接受的代价：worker 在 Ack 前崩溃时可能重复投递，外部系统应设计为幂等。

### 2. 供应商配置 + Body 模板

供应商的 `target_url`、`method`、`headers` 统一在本服务配置，由 `/vendors` CRUD 管理。  
业务系统只传入语义 body，本服务按 `body_tpl` 模板渲染后转发。  
Header 合并策略：调用方的 `extra_headers` 为基础，供应商 headers 覆盖（鉴权信息不可被调用方覆盖）。

### 3. DB 作为队列，不引入独立 MQ

使用 PostgreSQL `SELECT FOR UPDATE SKIP LOCKED` 实现队列语义。  
DB 已是依赖项，零额外基础设施；吞吐量瓶颈出现前完全够用。`Queue` interface 为未来替换留好接口。

### 4. 4xx / 5xx 区别对待

- `5xx` / 网络超时 / `429 Too Many Requests` / `408 Request Timeout`：临时故障，**重试**
- 其他 `4xx`：请求配置错误，重试无意义，**直接进 DLQ**

### 5. 退避 + Jitter

重试延迟加 ±20% 随机抖动（1m → 2m → … → 24h cap），防止大量任务同时打到已恢复的外部服务。

### 6. 崩溃恢复

`processing` 状态超过 5 分钟未更新，视为 worker 崩溃，定期重置为 `pending`。

### 7. 外部系统长期不可用

每个任务最多尝试 **10 次**。临时故障按照指数退避和 ±20% jitter 重试；达到上限仍未成功时，任务进入 DLQ（`status=dead`），保留最后一次错误供排查。永久性 4xx 不消耗无意义的重试次数，直接进入 DLQ。

MVP 不提供 DLQ 重投 UI。运维人员通过结构化日志和数据库记录定位失败原因，确认供应商恢复或配置修正后再人工处理。若失败任务数量开始影响日常运营，再增加告警、批量重投接口和带审计记录的管理界面。

### 8. 当前可观测性取舍

当前提供 `/healthz` 存活检查、结构化日志，以及 PostgreSQL 中可查询的任务状态、尝试次数和最后错误。`/healthz` 只表示 HTTP 进程可以响应，不等同于数据库和外部供应商全部健康。

MVP 暂不增加 `/metrics`。在实例数量少、流量未知时，自建内存计数器会带来多实例聚合和进程重启丢失等问题。需要建立 SLO 或自动告警时，再接入 Prometheus/OpenTelemetry，重点暴露 enqueue 数量、投递成功率、重试率、DLQ 数量、队列积压和投递延迟。

---

## 系统边界

**解决：**
- 接收通知请求并持久化（防丢失）
- 供应商配置统一管理，入队时快照
- Body 模板渲染（`text/template`）
- 异步投递 + 指数退避重试 + DLQ
- 入口幂等（按 `vendor_name + Idempotency-Key` 去重，重复请求返回原任务 ID）
- Crash recovery

**明确不解决（MVP 范围外）：**
- 消息顺序保证
- Rate limiting per vendor（演进时按需加）
- DLQ 重新投递 UI（需人工介入时查库处理）
- 实时监控大盘和 `/metrics`（MVP 先用任务表、存活检查和结构化日志；达到需要 SLO/自动告警的阶段再接入）
- 公网级认证与任意 URL 防护：MVP 假定部署在已认证的内部网关后，`/vendors` 仅对管理员开放；生产化时应增加目标域名 allowlist、凭证加密与响应脱敏

---

## 演进路径

| 触发条件 | 演进方向 |
|---------|---------|
| 写入吞吐达到 DB 瓶颈 | `Queue` interface 换 Redis / MQ 实现，业务代码不变 |
| 需要可观测性 | 接入 Prometheus metrics（enqueue rate、DLQ rate、latency） |
| 供应商鉴权复杂化 | 在 vendor config 增加签名、OAuth token 刷新等字段 |

---

## API

### 供应商配置

```
POST   /vendors              创建供应商
GET    /vendors              列出所有供应商
GET    /vendors/:name        获取供应商
PUT    /vendors/:name        更新供应商
DELETE /vendors/:name        删除供应商
```

**创建供应商 Request**
```json
{
  "name": "crm",
  "target_url": "https://api.crm.com/webhook",
  "method": "POST",
  "headers": { "Authorization": "Bearer secret", "Content-Type": "application/json" },
  "body_tpl": "{\"crm_id\":\"{{.user_id}}\",\"event\":\"{{.event}}\"}"
}
```

`body_tpl` 为空时原样转发调用方 body。模板语法为 Go `text/template`，数据来自调用方的 `body` 字段（需为 JSON object）。

### 发送通知

```
POST /notifications/:vendor_name
Header: Idempotency-Key: <key>   // 可选，省略则自动生成
```

**Request body**
```json
{
  "body": { "event": "user.registered", "user_id": "u42" },
  "extra_headers": { "X-Trace-Id": "abc" }
}
```

**Response** `202 Accepted`
```json
{
  "id": "550e8400-e29b-41d4-a716-446655440000",
  "idempotency_key": "order-paid-12345"
}
```

同一供应商下重复 `Idempotency-Key` 请求返回原任务的 `id`，不会重复入队；不同供应商可以复用同一个业务幂等键。

### GET /healthz

健康检查，返回 `200 OK`。

---

## 本地运行

**依赖：** Docker, Docker Compose

```bash
docker compose up --build
```

单独运行（需要本地 PostgreSQL）：

```bash
# 创建表（按顺序执行所有迁移）
for migration in migrations/*.sql; do
  psql "$DATABASE_URL" -f "$migration"
done

# 启动服务
DATABASE_URL=postgres://notify:notify@localhost:5432/notify?sslmode=disable \
  go run ./cmd/server
```

**完整流程示例：**

```bash
# 1. 注册供应商
curl -X POST http://localhost:8080/vendors \
  -H "Content-Type: application/json" \
  -d '{
    "name": "crm",
    "target_url": "https://httpbin.org/post",
    "headers": {"Content-Type": "application/json"},
    "body_tpl": "{\"crm_id\":\"{{.user_id}}\"}"
  }'

# 2. 发送通知
curl -X POST http://localhost:8080/notifications/crm \
  -H "Content-Type: application/json" \
  -H "Idempotency-Key: order-paid-001" \
  -d '{"body": {"user_id": "u42"}}'
```

---

## 测试

```bash
# 单元测试（含竞态检测）
go test -race ./internal/...

# 集成测试（需要 DATABASE_URL）
DATABASE_URL=postgres://notify:notify@localhost:5432/notify?sslmode=disable \
  go test -race -tags=integration ./tests/...
```

---

## 环境变量

| 变量 | 默认值 | 说明 |
|------|--------|------|
| `DATABASE_URL` | `postgres://notify:notify@localhost:5432/notify?sslmode=disable` | PostgreSQL 连接串 |
| `PORT` | `8080` | HTTP 监听端口 |
| `GIN_MODE` | `debug` | `release` 关闭调试日志 |
