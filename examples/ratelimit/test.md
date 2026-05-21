# examples/ratelimit 端到端测试方案

> 本文档与 `verify_ratelimit.sh` 保持一致，新增/修改用例时同步更新本文件。

## 总览

`verify_ratelimit.sh` 端到端验证 polaris-go 的两类限流能力 + 多维度匹配规则：

- **QPS 限流（请求数）**：`Rule.Resource=QPS`
  - **reject** 策略（快速失败）：超出阈值立即返回 HTTP 429（`reject` 插件）
  - **unirate** 策略（匀速排队）：超出速率的请求被 SDK 排队等待，仅当排队超过 `maxQueueDelay` 才拒绝（`unirate` 插件）
- **并发数限流**：`Rule.Resource=CONCURRENCY`，由 `concurrency` 插件实现，**纯本地模式**
- **多维匹配（AND）**：规则的 `arguments` 之间是 AND 关系，支持 6 类维度——HEADER / QUERY / METHOD / CALLER_SERVICE / CALLER_IP / CALLER_METADATA

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
| QPS unirate | `127.0.0.1:18182` | `127.0.0.1:18192` | `UnirateRatelimitEchoServer` | 用例 2.x |
| 并发数 | `127.0.0.1:18181` | `127.0.0.1:18191` | `ConcurrencyEchoServer` | 用例 3.x |
| 自定义匹配 | `127.0.0.1:18183` | `127.0.0.1:18193` | `CustomMatchEchoServer` | 用例 4.x（多维 AND） |
| regex_combine | `127.0.0.1:18184` | `127.0.0.1:18194` | `RegexCombineEchoServer` | 用例 5.x（合并阈值开关） |
| 命名空间 | `default` | | | |

> 端口选自 `18180-18194` 段，避开 `examples/route` 占用的 `18080-18099` 区间。
> consumer 实例与 provider 实例一一对应：`consumer --service` 指向同一个服务，由 SDK 服务发现路由到对应 provider。
> 自定义匹配用例的 consumer 额外通过 `--caller-service / --caller-ip / --caller-metadata` flag 注入主调身份 header.

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
| `max_queue_delay` | `1`（秒） | 单个请求允许等待的最长时间；超过此值即拒绝（用例 2.2 验证丢弃） |

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

### QPS 规则 - 自定义多维匹配（`ratelimit-e2e-custom-match-rule`）

| 字段 | 值 | 含义 |
| --- | --- | --- |
| `name` | `ratelimit-e2e-custom-match-rule` | 规则名 |
| `service` / `namespace` | `CustomMatchEchoServer` / `default` | 与其他规则用不同服务，便于隔离演示 |
| `resource` | `QPS` | reject 策略 |
| `type` | `LOCAL` | |
| `method` | EXACT `/echo` | |
| `amounts` | 1 条：`maxAmount=2 / 1s` | |
| `action` | `REJECT` | |
| `arguments` | 5 条 EXACT 匹配（AND 关系）：HEADER `x-tenant=gold` + QUERY `region=cn-east` + CALLER_SERVICE `default/CustomCallerService` + CALLER_IP `10.0.0.1` + CALLER_METADATA `env=prod` | 多维度组合限流 |

**配置效果**：6 类匹配维度（`method` + 5 条 `arguments`）**全部命中**时规则才生效，超阈值后 429；任一维度不命中（例如 query 改成 `cn-west`）整条规则跳过，请求全部放行。这是验证 polaris ratelimit AND 语义的核心用例。

**链路细节**：

| 维度 | 来源（curl → consumer → provider） |
| --- | --- |
| HEADER `x-tenant` | curl `-H 'x-tenant: gold'` |
| QUERY `region` | curl `?region=cn-east` |
| METHOD | provider 内 `quotaReq.SetMethod("/echo")` |
| CALLER_SERVICE | consumer `--caller-service=default/CustomCallerService` → 注入 `X-Polaris-Caller-Service` header → provider 解析 |
| CALLER_IP | consumer `--caller-ip=10.0.0.1` → 注入 `X-Polaris-Caller-IP` header → provider 解析 |
| CALLER_METADATA | consumer `--caller-metadata=env=prod` → 注入 `X-Polaris-Caller-Metadata-env` header → provider 解析 |

### QPS 规则 - regex_combine 开关（`ratelimit-e2e-regex-combine-rule`）

| 字段 | 值 | 含义 |
| --- | --- | --- |
| `name` | `ratelimit-e2e-regex-combine-rule` | 规则名 |
| `service` / `namespace` | `RegexCombineEchoServer` / `default` | 与其他规则用不同服务 |
| `resource` | `QPS` | reject 策略 |
| `type` | `LOCAL` | |
| `method` | **REGEX `/users/.*/orders`** | 正则匹配，多条实际 path 都命中 |
| `amounts` | 1 条：`maxAmount=4 / 1s` | |
| `action` | `REJECT` | |
| `regex_combine` | **首次创建为 `false`；用例 5.2 通过 PUT 翻为 `true`** | 合并阈值开关 |

**配置效果（脚本会动态翻转测试）**：

- `regex_combine=false`（默认 / 5.1）：每条命中 REGEX 的实际 path（如 `/users/100/orders`、`/users/200/orders`）拥有**独立 token bucket**，各自独享 `4/1s`
- `regex_combine=true`（5.2 翻转后）：所有命中 REGEX 的 path **共享同一 token bucket**，合计 `4/1s`

5.2 结束后脚本自动把 `regex_combine` 翻回 `false`，让规则状态干净。

## 用例编号

> ⚠️ 新增用例时必须**接续编号**（不跳号、不复用），并同步更新 `verify_ratelimit.sh`、本文件、聚合脚本（如适用）。

### [用例 1.1] QPS 限流触发

| 项 | 内容 |
| --- | --- |
| 操作 | 串行向 `http://127.0.0.1:18190/echo`（consumer-qps-reject）发起 5 次 GET（无并发） |
| 原理 | provider 在请求处理前调用 `LimitAPI.GetQuota` 命中 QPS 规则；窗口（1s）内放过 2 个，剩余被 reject 插件拒绝 |
| 预期 | `200 ≈ 2`、`429 ≥ 2`（容忍 SDK 拉规则 1s 时延） |
| 判定 | 限流次数 ≥ `total - maxAmount - 1 = 2`，且无其它状态码 → PASS；否则 FAIL |

### [用例 1.2] QPS 新窗口重新放通 + 再次触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `validDuration + 1s = 2s`（跨过当前窗口）→ 先发 1 次 `/echo`（验证放通）→ 紧接再串行发 5 次 GET（验证再次限流） |
| 原理 | QPS reject 按时间窗口计数；新窗口开始后配额清零，第 1 次请求 200；后续突发再次超过阈值 `2/1s` → 429。这是端到端验证规则"持续生效"，避免出现"用一次就废"的退化 |
| 预期 | 单发 200；后续突发中 `429 ≥ 2`、`other == 0` |
| 判定 | 单发 200 且突发 `429 ≥ 2 && other == 0` → PASS |

### [用例 2.1] unirate 匀速排队（QPS 限流的另一种策略）

| 项 | 内容 |
| --- | --- |
| 操作 | **串行**向 `http://127.0.0.1:18192/echo`（consumer-qps-unirate）发起 3 次 GET |
| 原理 | unirate 让 SDK 把超出速率的请求**排队等待**（`QuotaFutureImpl.Get()` 内部 sleep 至下一个配额到期）；速率 = `4/2s` = 2 QPS，每个请求间隔约 500ms。3 个请求最大等待 ≈ 1000ms ≤ `max_queue_delay=1s`，全部排队成功 |
| 预期 | 3 个全部 200；总耗时 ≈ `(3-1) * 500ms = 1000ms`，脚本设容忍下限 `≥ 700ms` |
| 判定 | `200 == 3 && 429 == 0 && other == 0 && 总耗时 ≥ 700ms`（耗时下限确认 SDK 真的把请求排队了，而不是直接放过） |

> **核心对比**：reject（用例 1.1）超出阈值的请求**立即变成 429**；unirate（用例 2.1）超出速率的请求**被排队后仍是 200，但耗时被拉长**——这是两种策略最直观的差异。

### [用例 2.2] unirate 队列等待超 maxQueueDelay 触发丢弃（429）

| 项 | 内容 |
| --- | --- |
| 操作 | **并发**（同时启动）向 `http://127.0.0.1:18192/echo` 发起 6 次 GET |
| 原理 | 第 i 个请求的等待 ≈ `(i-1)*500ms`：i=1..3 ≤ 1000ms 仍排队成功；i=4..6 等待 ≥ 1500ms > `max_queue_delay=1s`，SDK 直接返回 RateLimit 错误 → consumer 透传 HTTP 429。**必须并发触发**：unirate 串行调用时 SDK 会 sleep 等到下一个配额时刻，第二次 GetQuota 调用看到的 currentTime 已追上 expectedTime，waitMs 始终 ≈ 一个 costDuration（≤ maxQueueDelay），永远不会触发丢弃 |
| 预期 | `200 + 429 == 6` 且 `429 ≥ 2`，无 other |
| 判定 | `limited ≥ 2 && other == 0 && (200 + 429) == 6` → PASS（验证 unirate 在排队超阈值后会主动丢弃） |

### [用例 2.3] unirate 新窗口（队列消散后）再次按规则限流

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `≈3s`（让上一轮排队彻底耗尽，`lastGrantTime` 自然回归当前时间），再次**并发**打 6 次 GET |
| 原理 | unirate 不是"用一次就废"——`lastGrantTime` 是滑动量，冷却后新一轮请求会被当作首批处理；但规则本身的速率/队列上限不变 |
| 预期 | 与用例 2.2 行为一致：前几个 200、靠后几个 429 |
| 判定 | `limited ≥ 2 && ok ≥ 1 && other == 0 && (200 + 429) == 6` → PASS（验证规则持续生效，不会"全直通"或"全拒绝"的异常状态） |

### [用例 3.1] 并发数触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | **并发**（同时启动）向 `http://127.0.0.1:18191/slow?ms=1500`（consumer-concurrency）发起 5 次 GET |
| 原理 | `/slow` 接口 sleep 1.5s 模拟长耗时业务；`maxAmount=2` 表示同时只允许 2 个请求处理中；超出 2 立即被拒 |
| 预期 | 200 数量 ≤ 2，429 数量 ≥ 3 |
| 判定 | `200 ≤ 2 && 429 ≥ 3 && other == 0` → PASS |

### [用例 3.2] Release 归还后放通 + 再次触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `slowMs + 1.5s ≈ 3s`（让 3.1 中所有 in-flight `/slow` 自然结束并触发 Release）→ 先发 1 次 `/slow?ms=200`（验证 Release 归还）→ 等首发结束后再并发 5 个 `/slow?ms=1500`（验证再次限流） |
| 原理 | provider `main.go` 中 `defer future.Release()` 在请求结束时把并发计数 -1；正确实现下计数回到 0；新一轮并发突发再次超过 `maxAmount=2` 时仍应触发 429。同时验证两件事：①Release 回调链正常；②规则在归还后持续生效 |
| 预期 | 单发 200；后续并发 5 次中 `ok ≤ 2 && 429 ≥ 3 && other == 0` |
| 判定 | 单发 200 且突发 `ok ≤ 2 && 429 ≥ 3 && other == 0` → PASS（若 Release 没归还，单发就 429；若规则失效，并发就 5 个全 200） |

### [用例 3.3] 低于上限全放通

| 项 | 内容 |
| --- | --- |
| 操作 | 并发 2 个 `/slow?ms=600`（≤ 上限 2） |
| 原理 | 反向用例：并发数低于上限时，所有请求都应正常通过；确保限流不会误伤合法请求 |
| 预期 | 全部 200，0 个 429，0 个其它 |
| 判定 | `200 == 2 && 429 == 0 && other == 0` → PASS |

### [用例 4.1] 自定义多维匹配规则全条件命中触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | 串行向 `http://127.0.0.1:18193/echo?region=cn-east` 发 5 次 GET，并带 `-H 'x-tenant: gold'`；consumer 已通过 `--caller-*` flag 注入 `X-Polaris-Caller-Service`/`-IP`/`-Metadata-env` 三组 header |
| 原理 | 6 类维度（method + 5 个 arguments）**全部命中**：`method=/echo`、`HEADER x-tenant=gold`、`QUERY region=cn-east`、`CALLER_SERVICE=default/CustomCallerService`、`CALLER_IP=10.0.0.1`、`CALLER_METADATA env=prod`；规则生效后 QPS reject |
| 预期 | 200 ≈ 2，429 ≥ 2 |
| 判定 | `429 ≥ total - maxAmount - 1 = 2` 且无非 200/429 状态码 → PASS |

### [用例 4.2] 自定义匹配规则单维不命中（反向验证 AND 语义）

| 项 | 内容 |
| --- | --- |
| 操作 | 同 4.1 但 query 改为 `?region=cn-west`（其它 4 维度仍命中） |
| 原理 | 规则的 5 个 arguments 之间是 AND 关系；query 一个维度不命中 → 整条规则跳过 → 请求全部放行 |
| 预期 | 全部 200，0 个 429 |
| 判定 | `200 == 5 && 429 == 0 && other == 0` → PASS（说明 AND 语义正确，单维度足以决定整条规则的命中） |

> **核心对比**：4.1 验证"多维度全部命中时限流生效"；4.2 反向验证"AND 语义"——任一维度不命中整条规则就跳过。两者一起证明 polaris ratelimit 规则匹配的精确性。

### [用例 4.3] 自定义匹配规则在新窗口仍能触发限流

| 项 | 内容 |
| --- | --- |
| 操作 | 等待 `validDuration + 1s ≈ 2s`（跨过 4.1 的限流窗口），再次串行向 `http://127.0.0.1:18193/echo?region=cn-east` 发 5 次（同 4.1 的全命中模式） |
| 原理 | 自定义匹配规则底层走 QPS reject 限流，按时间窗口计数；新窗口配额清零后，命中规则的请求继续按 `2/1s` 限流。与用例 1.2/2.3/3.2 形成完整对照：**每种限流方案都覆盖"新窗口再次生效"语义** |
| 预期 | `429 ≥ 2`，无非 200/429 状态码 |
| 判定 | `429 ≥ 2 && other == 0` → PASS（验证规则在新窗口持续生效，不会"用一次就废"） |

### [用例 5.1] regex_combine=false 多 path 各自独享配额

| 项 | 内容 |
| --- | --- |
| 操作 | 并发向 `http://127.0.0.1:18194/users/100/orders` 和 `http://127.0.0.1:18194/users/200/orders` 各发 5 次 GET（共 10 次），规则 method 为 REGEX `/users/.*/orders` |
| 原理 | `regex_combine=false`（默认）时，SDK 用"实际请求 path"作为 token bucket 维度——`/users/100/orders` 与 `/users/200/orders` 落到不同 bucket，各自独享 `4/1s` 阈值；每条路径理论限 `5-4=1` 个 |
| 预期 | 两条 path 各 通过 ≈4 / 限流 ≈1，总 `429 ≥ 2`、`200 ≤ 10`；脚本判定下界容忍跨 2 窗口 |
| 判定 | `limited ≥ 2 && ok ≤ 10 && other == 0` → PASS（验证默认行为不会合并阈值） |

### [用例 5.2] regex_combine=true 多 path 共享同一阈值（合并阈值生效）

| 项 | 内容 |
| --- | --- |
| 操作 | 通过 PUT `/naming/v1/ratelimits` 翻转 `regex_combine=true`，等 SDK 拉新规则（3s）后重复 5.1 的并发请求 |
| 原理 | `regex_combine=true` 时，SDK 改用"规则配置的 REGEX 字符串"作为 bucket 维度——所有命中 `/users/.*/orders` 的请求落到同一个 bucket，**合计共享** `4/1s`；总放过 ≈4，限流 ≈6 |
| 预期 | 总 `200 ≈ 4`、`429 ≈ 6`；与 5.1 形成强对比（5.1 通过数 ≈8、5.2 通过数 ≈4） |
| 判定 | `limited ≥ total - 2*MAX_AMOUNT && ok ≤ 2*MAX_AMOUNT && other == 0` → PASS（具体：`limited ≥ 2 && ok ≤ 8`） |

> **核心对比**：5.1 / 5.2 用同样的请求量、同样的规则阈值，唯一变化是 `regex_combine` 字段。两者通过数差异（8 vs 4）直观证明合并阈值的语义。脚本在 5.2 结束后会自动 PUT 把 `regex_combine` 翻回 `false`，让规则恢复初始状态，避免下次跑 5.1 时偏差。

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
| 用例 3.1 出现 `200 > 2` | 检查规则是否生效（SDK 拉规则需 1-2 秒，脚本预留了 4s 等待） |
| 用例 3.2 实际为 429 | 上一批请求未真正释放配额 → 检查 `provider-concurrency/main.go` 是否有 `defer future.Release()` |
| 用例 3.3 出现 429 | 并发数桶状态未清零（可能上一批 sleep 太长未结束） → 增大 `recover_wait_ms` |
| 用例 2.1 实际为 429 | unirate 规则的 `max_queue_delay` 太小，请求等不到下一个配额就超时 → 控制台调大此值；或改成全部立即变 200，说明规则没生效（按 reject 跑） |
| 用例 2.1 总耗时 < 700ms | unirate 没生效——规则被识别为 reject 策略，所有请求秒级返回。检查 polaris 控制台规则的 `action` 字段是否为 `UNIRATE` |
| 用例 2.2 实际 `429 == 0` | `max_queue_delay` 被改大或脚本常量 `UNIRATE_MAX_QUEUE_DELAY_SEC` 与控制台不一致，导致所有突发请求都能排队成功 |
| 用例 2.3 全部 200 或全部 429 | unirate `lastGrantTime` 状态异常；冷却时间不够（< 3s）会让旧值残留，反过来如果 SDK 端 bug 也会全拒。增大 `cooldown_sec` 后还出现请提 issue |
| 升级脚本后 2.2/2.3 不通过 | 控制台规则 `max_queue_delay` 与脚本期望不一致；脚本会自动检测并 PUT 更新（看日志中 `[unirate 规则] 已自动更新` 行）。如果自动更新失败（HTTP 非 200），请检查 polaris token 权限或手动到控制台删除规则后重跑 |

调用 `--keep` 可保留 provider 进程和日志便于人工排查；之后用 `./cleanup.sh -f` 一键清理进程与日志。
> 限流规则始终保留，与 `--keep` 无关——下次跑脚本时直接复用既有规则。

## 与单元测试的关系

本端到端脚本验证的是**完整链路**：HTTP → SDK 框架 → 限流插件 → 计数 → 释放。
而单元测试位于：

- `plugin/ratelimiter/reject_concurrency/bucket_concurrency_test.go`：验证 `ConcurrencyQuotaBucket` 计数与并发安全性
- `pkg/flow/quota/resolver_test.go`：验证 `Resource → 插件名` 路由
- `pkg/flow/quota/window_concurrency_test.go`：验证 CONCURRENCY 规则强制本地模式

两者互补，单测保证组件正确，端到端脚本保证集成行为。
