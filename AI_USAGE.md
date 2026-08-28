# AI 使用说明

> 本项目包含 Go、Elixir 两个实现版本（见[根 README](./README.md)）。本文档覆盖整个项目：从需求分析、两版实现，到测试与 CI 的加固。

---

## 一、AI 提供了帮助的地方

**需求分析阶段**

用 AI 梳理了问题的核心矛盾（低延迟提交 vs 可靠投递），以及常见坑点：4xx/5xx 区别处理、退避 Jitter、入口幂等性、`processing` 状态的崩溃恢复。这些问题分散在实际工程经验中，AI 帮助做了系统性的整理。

**Go 版实现**

- PostgreSQL `SELECT FOR UPDATE SKIP LOCKED` 的 GORM 写法（`clause.Locking`）
- GORM 与 JSONB、BYTEA 的自定义 Scanner/Valuer 接口（`HeadersMap` 类型）
- Gin handler 的结构和 binding tag
- Dockerfile 多阶段构建
- GitHub Actions CI 的 postgres service container 配置

**Elixir 版实现**

- Oban 的 worker 定义、unique jobs、`backoff/1` callback 的 API 用法
- Plug.Router + Bandit 的启动骨架，Ecto schema 与 migration
- `mix release` 的 release 配置与 release 内迁移（`RcNotification.Release.migrate/0`）

**测试与 CI 骨架**

- ExUnit + Mox（HTTP client mock）以及基于 Bandit 的真实 TCP E2E 测试结构
- GitHub Actions 的 `erlef/setup-beam`、postgres service container、多步门禁编排

---

## 二、AI 给出过但没采纳的建议

**引入 Kafka / RabbitMQ**

AI 在讨论可靠性时建议使用独立 MQ。没有采纳。理由：这个服务的规模下 DB queue 完全够用，引入 MQ 增加了部署和运维复杂度，解决的是一个目前不存在的问题。`Queue` interface 已为未来替换留好口子。

**透传原始请求（不做 Vendor 配置层）**

AI 最初建议简化设计：业务系统直接传入 `target_url`、`method`、`headers`、`body`，本服务只做可靠投递，不持有供应商信息。评估后没有采纳。理由：题目要求"抹平第三方 API 之间的格式差距"，透传设计只是把格式差异从 HTTP 层移到了 body 层，并没有真正解决问题；供应商配置统一持有、Body 模板渲染才是题目的核心价值。

**Circuit Breaker**

AI 建议对每个 target 域名维护熔断状态。没有采纳。理由：MVP 阶段过度设计。指数退避已经有效缓解了对故障外部系统的压力，熔断的收益要到规模更大时才体现。

**Elixir 版直接用 Oban 内建默认退避**

AI 建议省掉 `backoff/1`，直接用 Oban 默认退避。没有采纳——为了让两版**行为可直接对比**，显式实现了与 Go 版一致的 `1m → 2m → … → 24h cap`、±20% jitter。牺牲一点代码量，换来"两版语义等价"这个更重要的性质。

---

## 三、关键决策是自己做出的

**Vendor 配置 + Body 模板方案**

起初 AI 倾向于透传设计（职责更单一）。通过分析题目"统一各供应商 API 格式差异"的核心要求，判断供应商配置层是必要的，不是额外复杂度。具体设计：Vendor CRUD 管理目标 URL / Header / Body 模板，入队时快照配置，避免配置变更影响在途任务。

**幂等键放在 Header，不放在 Body**

API 设计中幂等键通过 `Idempotency-Key` Header 传入，而非 request body 字段。理由：幂等控制是传输层关注点，与业务 payload 分离更清晰；与 Stripe 等主流 API 风格一致；重复请求返回原任务 ID 而非静默忽略。去重范围是 `(vendor_name, idempotency_key)`，同一业务事件可以安全地分别通知 CRM、库存等多个供应商。

**Header 合并优先级：供应商覆盖调用方**

调用方可传 `extra_headers` 扩展，但供应商预设的 headers（含鉴权信息）始终优先，调用方无法覆盖。这保证了鉴权凭证的安全性。

**429 / 408 不进 DLQ**

AI 最初建议所有 4xx 直接进 DLQ。经过分析，429 Too Many Requests 和 408 Request Timeout 是临时性错误，应该重试而非永久放弃。最终只有配置类错误（400、401、403、404 等）才视为永久失败。

**语言选择 Go，但用 Elixir 重写来验证判断**

在 Go / Rust / Elixir 之间权衡后选 Go：这个服务要长期稳定运行，不是极致性能、也不是复杂并发状态管理，Go 的投入产出比最好。同时也判断 Elixir 的 OTP 模型在设计上更契合（supervisor tree 天然做 crash recovery）。

关键在于：这句"更契合"没有停留在文档断言，而是**用 Elixir 把同一需求重写了一遍来验证**——结论是可量化的（同样功能 Go worker 307 行 vs Elixir 58 行、崩溃恢复 63 行手写 vs 0 行）。详见 [`COMPARISON.md`](./COMPARISON.md)。工程判断的质量在于识别问题结构、再选契合的工具，而不是默认选最熟的那个。

**根据依赖审计从 Cowboy 迁移到 Bandit**

AI 帮助定位到 Cowboy 间接引入的 Cowlib 2.19.0 存在 3 个安全公告，并对比了“保留并说明可达性”“等待上游修复”和“迁移 Bandit”三种方案。没有把“换包”当成自动正确的答案，而是继续核对 Bandit 自身的历史公告、当前修复版本和项目实际调用路径。

最终自主决定迁移到 Bandit 1.12.5：当前项目只使用标准 Plug 接口，迁移成本低；迁移时锁定的 1.12.5 不落入当时已公开公告的受影响版本；而 Cowlib 暂无可直接升级的修复版本。迁移后又发现测试依赖 Bypass 仍会间接引入 Cowboy/Cowlib，因此没有只做表面上的生产依赖替换，而是将 E2E 目标服务器也改为 Bandit，最终让完整依赖审计通过。这个决策的依据是可验证的依赖树、`mix hex.audit`、普通/E2E 测试和真实 release 构建，而不是对某个库的主观偏好。

**不采信"测试全绿"，验证测试是否真的跑过**

这是对 AI 产出判断力体现得最集中的一处。

AI 为 Elixir 版生成了一整套测试（单元 + e2e）和 CI，表面结构完整、且看起来"通过"。但没有直接采信这个"全绿"信号。实际逐层验证后发现，**这套测试与 CI 从未真正执行过**：

- e2e 文件的 `@moduletag` 写在模块外，是编译错误 → 整个 e2e 从没编译过
- 测试缺 `import Plug.Conn`，`put_req_header/3` 未定义 → 编译不过
- 5xx 重试用例的断言用了 `assert_enqueued`，它匹配不到失败后的 `retryable` 状态 → 就算能跑也会 fail
- **应用在生产模式下根本起不来**：`Oban.Plugins.Stager` 在新版 Oban 已被移除。而 Oban 在 `testing: :manual` 下会强制清空 `plugins` 再校验，导致这个 boot 崩溃在普通测试里**永远暴露不出来**
- CI 里名为 "release gate" 的步骤只做 `mix release`（组装），**从不启动** → 拦不住任何 boot 崩溃
- 另外 `config/prod.exs`、`.formatter.exs` 缺失，`credo --strict` 一堆未过项——都是"没跑过"的旁证

由此做出的修正与决策：

- **加一条回归测试**（`test/rc_notification/oban_config_test.exs`）：用 `Oban.Config.new/1` 校验**去掉 `testing:` 覆盖后**的真实生产配置。它能精确复现上面那个 boot 崩溃——把"只有上线才炸"的问题拉回到单元测试里。
- **把 release gate 改成真正启动 release**（`scripts/release_smoke.sh`）：构建 → 迁移 → 以 daemon 启动 → 打 `/healthz` → 真实 vendor+notification 冒烟。只有真的 boot 起来才算数。
- 让 `:integration` 默认排除，测试分层清晰（快单元 / e2e / release gate 三层）。
- 把两套 workflow 移到仓库根目录 `.github/workflows/`；子目录中的 workflow 不会被 GitHub Actions 发现。
- 删除 `EEx.eval_string/2`：Vendor 模板改为只识别 `<%= @field %>` 的受限替换器，既不执行代码，也不把任意业务字段转成永久 Atom。

判断依据：AI 生成的测试覆盖的是**逻辑分支**，但覆盖不了"这套测试、这个服务本身能不能跑起来"。对 AI 产出的代码，"能编译""测试绿"都不等于"真的被验证过"——最容易骗过人的恰恰是那种**结构看起来很完整、却从没被执行过**的测试。
