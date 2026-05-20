# examples/ratelimit 端到端测试方案

> 本文档与 `verify_ratelimit.sh` 保持一致，新增/修改用例时同步更新本文件。

## 总览

`verify_ratelimit.sh` 端到端验证 polaris-go 的两类限流能力：

- **QPS 限流（请求数）**：`Rule.Resource=QPS`
  - **reject** 策略（快速失败）：超出阈值立即返回 HTTP 429（`reject` 插件）
  - **unirate** 策略（匀速排队）：超出速率的请求被 SDK 排队等待，仅当排队超过 `maxQueueDelay` 才拒绝（`unirate` 插件）
- **并发数限流**：`Rule.Resource=CONCURRENCY`，由 `concurrency` 插件实现，**纯本地模式**

脚本通过 Polaris 控制面 HTTP API（`/naming/v1/ratelimits`）**按需创建**两条限流规则：
按 `(name, service, namespace)` 三元组定位，未存在才创建；规则一旦创建后**永久保留**，下次跑直接复用。
启动本地 provider 后用 `curl` 串行/并发打请求，统计 200 / 429 比例与预期对比。

> 如需重置阈值或清理规则，请到 Polaris 控制台手动操作，脚本不会主动删除任何规则。
> 因为按 `(name, service)` 三元组查询，所以**不同服务可以复用同名规则名**而不冲突。

## 前置条件

| 依赖 | 说明 |
| --- | --- |
| Polaris 服务端 | gRPC `:8091`、HTTP `:8090` 可达 |
| Go 工具链 | 用于编译 provider |
| `python3` | 用于拼接 JSON 请求体 |
| `curl` | HTTP 请求 |

可执行命令前置：

```bash
chmod +x verify_ratelimit.sh cleanup.sh
```

## 端口与服务名

链路：**curl → consumer → provider**。consumer 通过 polaris 服务发现选 provider 实例，HTTP 转发请求；provider 内 `LimitAPI.GetQuota` 命中规则，返回的状态码（包含 429）由 consumer 透传给 curl。

| 资源 | provider 端口 | consumer 端口 | 服务名 | 验证 |
| --- | --- | --- | --- | --- |
| QPS reject | `127.0.0.1:18180` | `127.0.0.1:18190` | `QpsRatelimitEchoServer` | 用例 1.x |
| QPS unirate | `127.0.0.1:18182` | `127.0.0.1:18192` | `UnirateRatelimitEchoServer` | 用例 3.x |
| 并发数 | `127.0.0.1:18181` | `127.0.0.1:18191` | `ConcurrencyEchoServer` | 用例 2.x |
| 命名空间 | `default` | | | |

> 端口选自 `18180-18192` 段，避开 `examples/route` 占用的 `18080-18099` 区间。
> consumer 实例与 provider 实例一一对应：`consumer --service` 指向同一个服务，由 SDK 服务发现路由到对应 provider。

## 限流规则

### QPS 规则（`ratelimit-e2e-qps-rule`）

| 字段 | 值 | 含义 |
| --- | --- | --- |
| `name` | `ratelimit-e2e-qps-rule` | 规则名（与服务、命名空间一起作为唯一身份） |
| `service` / `namespace` | `QpsRatelimitEchoServer` / `default` | 规则作用的服务身份 |
| `resource` | `QPS` | 资源类型 = QPS（请求数限流） |
| `type` | `LOCAL` | 单机限流，不依赖远程 |
| `method` | EXACT `/echo` | 仅作用于 `/echo` 接口；其它接口（如 `/health`）不受此规则影响 |
| `amounts` | 1 条：`maxAmount=2 / 1s` | 1 秒窗口内最多放过 2 次 |
| `action` | `REJECT` | 超出阈值时直接拒绝（`reject` 插件；SDK 内部统一小写匹配，写大写为约定） |

**配置效果**：单机视角下，每秒最多放过 2 个 `/echo` 请求；超出部分由 SDK 直接返回限流（provider 内会反映为 HTTP 429）；窗口（1s）结束后配额自动重置。

### QPS 规则 - unirate 策略（`ratelimit-e2e-unirate-rule`）

| 字段 | 值 | 含义 |
| --- | --- | --- |
| `name` | `ratelimit-e2e-unirate-rule` | 规则名 |
| `service` / `namespace` | `UnirateRatelimitEchoServer` / `default` | 与 reject 规则用不同服务，便于对比两种策略 |
| `resource` | `QPS` | 资源类型仍是 QPS |
| `type` | `LOCAL` | 单机限流 |
| `method` | EXACT `/echo` | 仅作用于 `/echo` 接口 |
| `amounts` | 1 条：`maxAmount=4 / 2s` | 即 2 QPS（每个请求间隔约 500ms） |
| `action` | `UNIRATE` | **匀速排队**（与 REJECT 的关键差异） |
| `max_queue_delay` | `10`（秒） | 单个请求允许等待的最长时间；超过此值才拒绝 |

**配置效果**：与 reject 不同，超出速率的请求**不会立即拒绝**，而是被 SDK 排队等待（`QuotaFutureImpl.Get()` 内部 sleep 直到下个配额到期）。只有等待时间超过 `max_queue_delay` 时才返回限流（HTTP 429）。

从用户视角的现象差异：
- `reject`：发起 5 个并发请求 → 2 个 200 + 3 个**立即** 429
- `unirate`：发起 5 个串行请求 → 5 个 200，但**总耗时被拉长**到约 (5-1)/rate ≈ 2s

### 并发数规则（`ratelimit-e2e-concurrency-rule`）

| 字段 | 值 | 含义 |
| --- | --- | --- |
| `name` | `ratelimit-e2e-concurrency-rule` | 规则名 |
| `service` / `namespace` | `ConcurrencyEchoServer` / `default` | 规则作用的服务身份 |
| `resource` | `CONCURRENCY` | 资源类型 = CONCURRENCY（并发数限流） |
| `type` | `LOCAL` | 写 `LOCAL` 即可；即便控制面下发 `GLOBAL`，SDK 也会强制按本地处理（`buildRemoteConfigMode` 短路） |
| `method` | EXACT `/slow` | 仅作用于 `/slow` 接口；`/slow` 由 provider 实现为 sleep N 毫秒，模拟长耗时业务 |
| `concurrencyAmount.maxAmount` | `2` | 同时处理中的请求数上限 |

**配置效果**：同时只允许 2 个 `/slow` 请求处于"处理中"状态；超出部分立即拒绝（HTTP 429）；前一批请求完成后，并发计数回落，新请求即可进入。

**前提**：provider 必须 `defer future.Release()` 归还配额（见 `provider-concurrency/main.go`），否则计数只增不减，最终全部请求被拒。

## 用例编号

> ⚠️ 新增用例时必须**接续编号**（不跳号、不复用），并同步更新 `verify_ratelimit.sh`、本文件、聚合脚本（如适用）。

### [用例 1.1] QPS 限流触发

| 项 | 内容 |
| --- | --- |
| 操作 | 串行向 `http://127.0.0.1:18190/echo`（consumer-qps-reject）发起 5 次 GET（无并发） |
| 原理 | provider 在请求处理前调用 `LimitAPI.GetQuota` 命中 QPS 规则；窗口（1s）内放过 2 个，剩余被 reject 插件拒绝 |
| 预期 | `200 ≈ 2`、`429 ≥ 2`（容忍 SDK 拉规则 1s 时延） |
| 判定 | 限流次数 ≥ `total - maxAmount - 1 = 2`，且无其它状态码 → PASS；否则 FAIL |

### [用例 1.2] QPS 窗口重置后放通

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `validDuration + 0.5s = 1.5s`（跨过当前窗口）后再发 1 次 `/echo` |
| 原理 | QPS 规则按时间窗口计数；新窗口开始后配额重置 |
| 预期 | HTTP 200 |
| 判定 | 状态码必须是 200；若仍 429 说明窗口未正确重置 |

### [用例 3.1] unirate 匀速排队（QPS 限流的另一种策略）

| 项 | 内容 |
| --- | --- |
| 操作 | **串行**向 `http://127.0.0.1:18192/echo`（consumer-qps-unirate）发起 5 次 GET |
| 原理 | unirate 让 SDK 把超出速率的请求**排队等待**（`QuotaFutureImpl.Get()` 内部 sleep 至下一个配额到期）；速率 = `4/2s` = 2 QPS，每个请求间隔约 500ms |
| 预期 | 5 个全部 200（不会因为速率超限而立即拒绝）；总耗时 ≈ `(5-1) * 500ms = 2000ms`，脚本设容忍区间 `[1400ms, 4000ms]` |
| 判定 | `200 == 5 && 429 == 0 && other == 0 && 总耗时 ≥ 1400ms`（耗时下限确认 SDK 真的把请求排队了，而不是直接放过） |

> **核心对比**：reject（用例 1.1）超出阈值的请求**立即变成 429**；unirate（用例 3.1）超出速率的请求**被排队后仍是 200，但耗时被拉长**——这是两种策略最直观的差异。

### [用例 2.1] 并发数触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | **并发**（同时启动）向 `http://127.0.0.1:18191/slow?ms=1500`（consumer-concurrency）发起 5 次 GET |
| 原理 | `/slow` 接口 sleep 1.5s 模拟长耗时业务；`maxAmount=2` 表示同时只允许 2 个请求处理中；超出 2 立即被拒 |
| 预期 | 200 数量 ≤ 2，429 数量 ≥ 3 |
| 判定 | `200 ≤ 2 && 429 ≥ 3 && other == 0` → PASS |

### [用例 2.2] Release 归还后放通

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `slowMs + 1.5s ≈ 3s`（让 2.1 中所有 in-flight `/slow` 自然结束并触发 Release），再发 1 次 `/slow?ms=200` |
| 原理 | provider `main.go` 中 `defer future.Release()` 在请求结束时把并发计数 -1；正确实现下并发数应回到 0 |
| 预期 | HTTP 200 |
| 判定 | 状态码必须是 200。若仍 429，则说明 Release 没归还配额（怀疑 main.go 漏写 defer 或 SDK 回调链断裂——这是验证并发数限流"释放"语义的关键用例） |

### [用例 2.3] 低于上限全放通

| 项 | 内容 |
| --- | --- |
| 操作 | 并发 2 个 `/slow?ms=600`（≤ 上限 2） |
| 原理 | 反向用例：并发数低于上限时，所有请求都应正常通过；确保限流不会误伤合法请求 |
| 预期 | 全部 200，0 个 429，0 个其它 |
| 判定 | `200 == 2 && 429 == 0 && other == 0` → PASS |

## 判定与汇总

- 每个用例打印：
  - `✅ [编号 名称] PASS - 详情`
  - `❌ [编号 名称] FAIL - 详情`
  - `⚠️ [编号 名称] WARN - 详情`
- 退出前打印一行**结论行**供聚合脚本识别：
  - `验证结论: ✅ 全部 N 个用例通过` → 退出码 0
  - `验证结论: ❌ K 个用例失败` → 退出码 K
  - `验证结论: ⚠️ ...` → 仅当无任何用例运行时

## 失败排查

| 现象 | 排查方向 |
| --- | --- |
| `创建 QPS 规则失败 HTTP=403002` | Polaris 开启了鉴权，使用 `--polaris-token` 传入 token |
| `provider-* 30 秒内未就绪` | 查看 `.logs/<provider>.log`，常见为 polaris 不可达或服务名冲突 |
| 用例 2.1 出现 `200 > 2` | 检查规则是否生效（SDK 拉规则需 1-2 秒，脚本预留了 4s 等待） |
| 用例 2.2 实际为 429 | 上一批请求未真正释放配额 → 检查 `provider-concurrency/main.go` 是否有 `defer future.Release()` |
| 用例 2.3 出现 429 | 并发数桶状态未清零（可能上一批 sleep 太长未结束） → 增大 `recover_wait_ms` |
| 用例 3.1 实际为 429 | unirate 规则的 `max_queue_delay` 太小，请求等不到下一个配额就超时 → 控制台调大此值；或改成全部立即变 200，说明规则没生效（按 reject 跑） |
| 用例 3.1 总耗时 < 1400ms | unirate 没生效——规则被识别为 reject 策略，所有请求秒级返回。检查 polaris 控制台规则的 `action` 字段是否为 `UNIRATE` |

调用 `--keep` 可保留 provider 进程和日志便于人工排查；之后用 `./cleanup.sh -f` 一键清理进程与日志。
> 限流规则始终保留，与 `--keep` 无关——下次跑脚本时直接复用既有规则。

## 与单元测试的关系

本端到端脚本验证的是**完整链路**：HTTP → SDK 框架 → 限流插件 → 计数 → 释放。
而单元测试位于：

- `plugin/ratelimiter/reject_concurrency/bucket_concurrency_test.go`：验证 `ConcurrencyQuotaBucket` 计数与并发安全性
- `pkg/flow/quota/resolver_test.go`：验证 `Resource → 插件名` 路由
- `pkg/flow/quota/window_concurrency_test.go`：验证 CONCURRENCY 规则强制本地模式

两者互补，单测保证组件正确，端到端脚本保证集成行为。
