# examples/ratelimit 端到端测试方案

> 本文档与 `verify_ratelimit.sh` 保持一致，新增/修改用例时同步更新本文件。

## 总览

`verify_ratelimit.sh` 端到端验证 polaris-go 限流 SDK 的以下能力：

| 用例组 | 验证能力 | 限流类型 |
|--------|----------|----------|
| 1.x | QPS reject（快速失败） | LOCAL |
| 2.x | QPS unirate（匀速排队） | LOCAL |
| 3.x | 并发数限流 | LOCAL |
| 4.x | 多维 AND 匹配 | LOCAL |
| 5.x | regex_combine 合并阈值开关 | LOCAL |
| 6.x | 分布式集群限流 | GLOBAL |
| 7.x | CustomResponse 自定义返回 | LOCAL + GLOBAL |
| 8.x | Prometheus 监控指标 | LOCAL |
| 9.x | 限流事件上报（RateLimitStart/End） | LOCAL |

## 前置条件

| 依赖 | 说明 |
|------|------|
| Polaris 服务端 | gRPC `:8091`、HTTP `:8090` 可达 |
| Go 工具链 | 编译 provider / consumer |
| `python3` | 拼接 JSON 请求体 |
| `curl` | HTTP 请求 |

## 运行方式

```bash
# 全部用例
./verify_ratelimit.sh

# 仅运行指定用例组（逗号分隔）
./verify_ratelimit.sh --only qps
./verify_ratelimit.sh --only qps,metrics
./verify_ratelimit.sh --only custom-response

# 跳过指定用例组
./verify_ratelimit.sh --skip concurrency,global
```

### 用例组名称对照

| `--only` / `--skip` 名称 | 对应用例 | 说明 |
|---------------------------|----------|------|
| `qps` | 1.x | QPS reject 限流 |
| `unirate` | 2.x | QPS unirate 匀速排队 |
| `concurrency` | 3.x | 并发数限流 |
| `custom` | 4.x | 多维 AND 匹配 |
| `regex` | 5.x | regex_combine 合并阈值 |
| `global` | 6.x | GLOBAL 分布式集群限流 |
| `custom-response` | 7.x | CustomResponse 自定义返回 |
| `metrics` | 8.x | Prometheus 监控指标验证 |
| `events` | 9.x | 限流事件上报验证 |

### 其他参数

| 参数 | 说明 |
|------|------|
| `--polaris-server <ip>` | Polaris 服务端地址（默认 `127.0.0.1`） |
| `--polaris-token <token>` | 鉴权 token（开启鉴权时必填） |
| `--limiter-service <svc>` | 覆盖 polaris.limiter 服务名（本地 limiter 场景） |
| `--keep` | 测试结束后保留 provider/consumer 进程和日志 |
| `--debug` | 所有 binary 开启 Polaris SDK DEBUG 日志 |

## 公共配置

### Provider — Prometheus Metrics（pull 模式）

每个 **provider** 实例的 metrics 端口 = **业务端口 + 10000**，通过环境变量 `${POLARIS_METRICS_PORT}` 注入 `polaris.yaml`：

```yaml
# provider-qps/polaris.yaml & provider-concurrency/polaris.yaml
global:
  statReporter:
    enable: true
    chain: [prometheus]
    plugin:
      prometheus:
        type: pull
        metricHost: 127.0.0.1
        metricPort: "${POLARIS_METRICS_PORT}"
```

### Consumer — Prometheus Metrics（push 模式）

所有 **consumer** 实例使用 push 模式，不暴露 /metrics 端口：

```yaml
# consumer/polaris.yaml
global:
  statReporter:
    enable: true
    chain: [prometheus]
    plugin:
      prometheus:
        type: push
        address: ${POLARIS_SERVER}:9091
        interval: 10s
```

### 事件上报（EventReporter）

```yaml
global:
  eventReporter:
    enable: true
    chain: [pushgateway]
    plugin:
      pushgateway:
        address: "${POLARIS_EVENT_ADDR}"  # 占位符，未设置时走服务发现
```

限流状态切换（UNLIMITED↔LIMITED）时 SDK 自动构造 `RateLimitStart` / `RateLimitEnd` 事件并 POST 到：
- `${POLARIS_EVENT_ADDR}` 设置时 → 直接发到该地址（用例 9.1/9.2 指向本地 mock server）
- 未设置时 → 服务发现 `Polaris/polaris.pushgateway`（用例 9.3 走真实链路）

**关键流程**（`plugin/events/pushgateway/reporter.go`）：

1. `ReportEvent` 入队 `reqChan`（容量 513）
2. 后台 goroutine 每秒 flush 一次（`time.Ticker(1s)`）或队列满立即 flush
3. Flush 失败（`SyncGetOneInstance` 拿不到实例 / HTTP timeout）→ **整个 batch 丢弃**，无缓存重试
4. Provider 进程退出时 SDK `Destroy()` 触发同步 flush，把队列残留事件发出（最多 3 次重试）

⚠️ 本地开发环境下 `polaris.pushgateway` 实例的注册 IP 可能是云端内网（如 30.x.x.x），客户端不可达时大量 batch 会被丢弃；用例 9.1/9.2 通过 mock server 规避这个问题，9.3 用于真实链路演示。

---

## 用例 1.x — QPS 限流（reject 策略）

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-qps-rule` |
| service / namespace | `QpsRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / validDuration=1s` |
| action | `REJECT` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18180 | 28180 |
| consumer | `consumer` | 18190 | —（push 模式） |

### 请求链路

```
curl → consumer:18190/echo → (polaris 服务发现) → provider:18180/echo → LimitAPI.GetQuota
```

### 用例 1.1 — QPS 限流触发

| 项 | 内容 |
|-----|------|
| 操作 | 串行 6 次 GET `http://127.0.0.1:18190/echo` |
| 预期 | `200 ≈ 2`、`429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

### 用例 1.2 — 新窗口重新放通 + 再次限流

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s 跨窗口 → 单发 1 次（验证放通）→ 再串行 6 次（验证再次限流） |
| 预期 | 单发 200；后续 `429 ≥ 2 && other == 0` |
| 判定 | 单发 200 且突发 `429 ≥ 2` → PASS |

---

## 用例 2.x — QPS 限流（unirate 匀速排队策略）

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-unirate-rule` |
| service / namespace | `UnirateRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=4 / validDuration=2s`（即 2 QPS） |
| action | `UNIRATE` |
| max_queue_delay | `1s` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18182 | 28182 |
| consumer | `consumer` | 18192 | —（push 模式） |

### 请求链路

```
curl → consumer:18192/echo → provider:18182/echo → LimitAPI.GetQuota (unirate 排队)
```

### 用例 2.1 — 匀速排队全部通过

| 项 | 内容 |
|-----|------|
| 操作 | **串行** 3 次 GET |
| 预期 | 全部 200，总耗时 ≥ 700ms（SDK 排队 sleep） |
| 判定 | `200 == 3 && 耗时 ≥ 700ms` → PASS |

### 用例 2.2 — 排队超 maxQueueDelay 触发丢弃

| 项 | 内容 |
|-----|------|
| 操作 | **并发** 6 次 GET（并发让排队全部堆积，后发请求 wait > 1s） |
| 预期 | `429 ≥ 2`，无 other |
| 判定 | `limited ≥ 2 && other == 0` → PASS |

### 用例 2.3 — 冷却后规则持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 3s 冷却 → 再次**并发** 6 次 |
| 预期 | 与 2.2 一致 |
| 判定 | `limited ≥ 2 && ok ≥ 1 && other == 0` → PASS |

---

## 用例 3.x — 并发数限流

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-concurrency-rule` |
| service / namespace | `ConcurrencyEchoServer` / `default` |
| resource | `CONCURRENCY` |
| type | `LOCAL` |
| method | EXACT `/slow` |
| concurrencyAmount.maxAmount | `2` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-concurrency` | 18181 | 28181 |
| consumer | `consumer` | 18191 | —（push 模式） |

### 请求链路

```
curl → consumer:18191/slow?ms=N → provider:18181/slow (sleep N ms, defer future.Release())
```

### 用例 3.1 — 并发超限触发限流

| 项 | 内容 |
|-----|------|
| 操作 | **并发** 5 次 GET `/slow?ms=1500` |
| 预期 | `200 ≤ 2`、`429 ≥ 3` |
| 判定 | `ok ≤ 2 && limited ≥ 3 && other == 0` → PASS |

### 用例 3.2 — Release 归还后放通 + 再次限流

| 项 | 内容 |
|-----|------|
| 操作 | 等 3s（上批请求结束、Release 归还）→ 单发 `/slow?ms=200`（验证归还）→ 并发 5 次 `/slow?ms=1500` |
| 预期 | 单发 200；后续 `ok ≤ 2 && 429 ≥ 3` |
| 判定 | 单发 200 且突发满足条件 → PASS |

### 用例 3.3 — 低于上限全放通（反向用例）

| 项 | 内容 |
|-----|------|
| 操作 | 并发 2 次 `/slow?ms=600`（≤ 上限） |
| 预期 | `200 == 2 && 429 == 0` |
| 判定 | 全部通过 → PASS |

---

## 用例 4.x — 多维 AND 匹配

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-custom-match-rule` |
| service / namespace | `CustomMatchEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / 1s` |
| action | `REJECT` |
| arguments（AND） | HEADER `x-tenant=gold` + QUERY `region=cn-east` + CALLER_SERVICE `default/CustomCallerService` + CALLER_IP `10.0.0.1` + CALLER_METADATA `env=prod` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 额外参数 |
|------|------|------|-------------|---------|
| provider | `provider-qps` | 18183 | 28183 | — |
| consumer | `consumer` | 18193 | —（push 模式） | `--caller-service=default/CustomCallerService --caller-ip=10.0.0.1 --caller-metadata=env=prod` |

### 请求链路（维度注入）

| 维度 | 来源 |
|------|------|
| HEADER `x-tenant` | curl `-H 'x-tenant: gold'` |
| QUERY `region` | curl `?region=cn-east` |
| METHOD | provider `quotaReq.SetMethod("/echo")` |
| CALLER_SERVICE | consumer `--caller-service` → `X-Polaris-Caller-Service` header |
| CALLER_IP | consumer `--caller-ip` → `X-Polaris-Caller-IP` header |
| CALLER_METADATA | consumer `--caller-metadata` → `X-Polaris-Caller-Metadata-env` header |

### 用例 4.1 — 全维度命中触发限流

| 项 | 内容 |
|-----|------|
| 操作 | 串行 5 次 GET `/echo?region=cn-east` + header `x-tenant: gold` |
| 预期 | `429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

### 用例 4.2 — 单维不命中全放通（反向验证 AND）

| 项 | 内容 |
|-----|------|
| 操作 | 同 4.1 但 query 改为 `?region=cn-west` |
| 预期 | 全部 200 |
| 判定 | `200 == 5 && 429 == 0` → PASS |

### 用例 4.3 — 新窗口持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s → 再次全命中模式 5 次 |
| 预期 | `429 ≥ 2` |
| 判定 | `429 ≥ 2 && other == 0` → PASS |

---

## 用例 5.x — regex_combine 合并阈值

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-regex-combine-rule` |
| service / namespace | `RegexCombineEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | **REGEX** `/users/.*/orders` |
| amounts | `maxAmount=4 / 1s` |
| action | `REJECT` |
| regex_combine | 初始 `false`；用例 5.2 通过 PUT 翻为 `true` |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18184 | 28184 |
| consumer | `consumer` | 18194 | —（push 模式） |

### 用例 5.1 — regex_combine=false 各 path 独享

| 项 | 内容 |
|-----|------|
| 操作 | 并发打 `/users/100/orders` × 5 + `/users/200/orders` × 5（共 10） |
| 预期 | 两条 path 各独享 4/1s → 总 `429 ≥ 2`、`200 ≤ 10` |
| 判定 | `limited ≥ 2 && ok ≤ 10 && other == 0` → PASS |

### 用例 5.2 — regex_combine=true 共享阈值

| 项 | 内容 |
|-----|------|
| 操作 | PUT 翻转 `regex_combine=true` → 等 3s → 同 5.1 请求 |
| 预期 | 两条 path 共享 4/1s → 总 `200 ≈ 4`、`429 ≈ 6` |
| 判定 | `limited ≥ 2 && ok ≤ 8 && other == 0` → PASS |
| 收尾 | 自动 PUT 恢复 `regex_combine=false` |

---

## 用例 6.x — GLOBAL 分布式集群限流

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-global-rule` |
| service / namespace | `GlobalRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | **`GLOBAL`** |
| method | EXACT `/echo` |
| amounts | `maxAmount=4 / 1s` |
| action | `REJECT` |
| failover | `FAILOVER_LOCAL` |
| 远端集群 | `Polaris/polaris.limiter`（可由 `--limiter-service` 覆盖） |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 说明 |
|------|------|------|-------------|------|
| provider A | `provider-qps` | 18185 | 28185 | 实例 A |
| provider B | `provider-qps` | 18186 | 28186 | 实例 B |
| provider (failover) | `provider-qps` | 18187 | 28187 | 用例 6.5 专用 |
| consumer | `consumer` | 18195 | —（push 模式） | 6.1/6.2 使用 |
| consumer (failover) | `consumer` | 18197 | —（push 模式） | 用例 6.5 专用 |

### 用例 6.0 — polaris.limiter 探测（前置条件）

| 项 | 内容 |
|-----|------|
| 操作 | GET `/naming/v1/instances?service=polaris.limiter&namespace=Polaris` |
| 判定 | 有健康实例 → 继续 6.1~6.5；无实例 → SKIP |

### 用例 6.1 — 多窗口聚合触发限流

| 项 | 内容 |
|-----|------|
| 操作 | 连续 4 个 1s 窗口，每窗口经 consumer 并发 8 次（共 32 次） |
| 预期 | `limited ≥ 4` |
| 判定 | `limited ≥ 4 && other == 0` → PASS |

### 用例 6.2 — 新窗口持续生效

| 项 | 内容 |
|-----|------|
| 操作 | 等 1 窗口（让 limiter quota 自然重置）→ 再连续 4 窗口并发 8 次 |
| 原理 | 验证规则不是"用一次就废"，sleep 后 limiter quota 重置，前 1~2 窗口 burst 全过，后续窗口必触发限流 |
| 预期 | `limited ≥ 2`（4 窗口里至少 2 个事件证明规则未静音）|
| 判定 | `limited ≥ 2 && other == 0 && SDK 日志含 type=GLOBAL` → PASS |
| 备注 | 阈值从原 `limited ≥ 4`（每窗口至少 1）放宽到 `limited ≥ 2`（4 窗口里至少 2），容忍前期 burst |

### 用例 6.3 — 多实例共享配额（核心语义）

| 项 | 内容 |
|-----|------|
| 操作 | 直接打 A:18185 并发 5 + B:18186 并发 5，连续 **8 窗口**（共 80 次） |
| 原理 | 分布式限流"先消费后结算"：每个 SDK 实例本地 atomic 扣减 tokenLeft，异步上报 limiter；多实例并发时各自基于过期视图放行 → 单窗口短暂超 1~3 倍阈值（**polaris-limiter 服务端 stat 日志亲自记录** passed=8~12 而 threshold=4）|
| 预期 | GLOBAL：`ok ≤ 60`（LOCAL 下界 64 - MAX=4，留余量）、`limited ≥ 8` |
| 反例 | LOCAL 退化：`ok = 8*2*MAX = 64`（确定值），明显高于 GLOBAL 实测的 30~56 |
| 判定 | `ok ≤ 60 && limited ≥ 8 && other == 0 && SDK 日志含 type=GLOBAL` → PASS |
| 二次校验 | grep `provider-qps-global-{a,b}/polaris/log/ratelimit/polaris-ratelimit.log` 中的 `type=GLOBAL` 行（INFO 级别，无需 --debug），结构性证明规则按 GLOBAL 加载 |
| 备注 | `ok ≥ 64`：完全退化为 LOCAL，几乎可断定 polaris.limiter 没真正接入；`60 < ok < 64`：灰区，limiter 推送延迟过大 |
| 设计依据 | 不再要求 `ok ≤ N*MAX`（严格上界），因为分布式限流允许预消费导致瞬时超限；改为"GLOBAL 至少节省 MAX 个请求 + 日志结构校验"双重判定 |

### 用例 6.3.5 — GLOBAL 稳态精准验证（对照 6.3 的 burst 场景）

| 项 | 内容 |
|-----|------|
| 操作 | 两实例并发，但实例内**串行** + 每个请求间隔 60ms（每实例 8 次请求 ≈ 480ms ≈ 3 个窗口） |
| 原理 | 60ms 间隔 > SDK acquire 周期（30~50ms）> limiter push 延迟，每发 1 个请求 SDK 都有时间上报 used + 接收 push，**burst 几乎为 0** |
| 预期 | 稳态 ok ≤ 16（≈ 3*MAX + 1*MAX burst）、limited ≥ 3 |
| 判定 | `ok ≤ 16 && limited ≥ 3 && other == 0` → PASS（数值精度高于 6.3） |
| 与 6.3 对比 | 6.3 测**burst 容忍**（必然超限，宽松判定）；6.3.5 测**稳态精准**（请求间隔贴合同步周期，严格判定） |
| 失败诊断 | `ok ≥ 21`：远端配额完全未节流；`16 < ok < 21`：limiter 同步异常 |

### 用例 6.4 — GLOBAL + regex_combine 共享

| 项 | 内容 |
|-----|------|
| 操作 | PUT regex 规则切到 `GLOBAL+regex_combine=true` → 打 provider:18184 两条 path × 4 窗口 |
| 前提 | 必须先跑过 5.x（复用 regex provider） |
| 预期 | `ok ≤ 24`、`limited ≥ 4` |
| 收尾 | 自动 PUT 恢复 `LOCAL+regex_combine=false` |

### 用例 6.5 — 远端不可达降级（FAILOVER_LOCAL）

| 项 | 内容 |
|-----|------|
| 操作 | `POLARIS_LIMITER_SVC=不存在的服务` 启动新 provider+consumer → 4 窗口并发 8 次 |
| 预期 | 本地配额生效：`limited ≥ 4` |
| 判定 | `limited ≥ 4 && other == 0` → PASS |

---

## 用例 7.x — CustomResponse 自定义返回

### 规则（LOCAL）

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-custom-response-rule` |
| service / namespace | `CustomResponseEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / 1s` |
| action | `REJECT` |
| customResponse.body | `{"code":429,"reason":"verify_ratelimit e2e custom body","trace":"verify"}` |

### 规则（GLOBAL）

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-global-custom-response-rule` |
| service / namespace | `GlobalCustomResponseEchoServer` / `default` |
| type | `GLOBAL` |
| failover | `FAILOVER_LOCAL` |
| 其余 | 同 LOCAL 版 |

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 | 用于 |
|------|------|------|-------------|------|
| provider (LOCAL) | `provider-qps` | 18188 | 28188 | 用例 7.1 |
| consumer (LOCAL) | `consumer` | 18198 | —（push 模式） | 用例 7.1 |
| provider (GLOBAL) | `provider-qps` | 18189 | 28189 | 用例 7.2 |
| consumer (GLOBAL) | `consumer` | 18199 | —（push 模式） | 用例 7.2 |

### 用例 7.1 — LOCAL 自定义返回

| 项 | 内容 |
|-----|------|
| 链路 | curl → consumer:18198 → provider:18188 |
| 操作 | 串行 6 次 GET /echo（前 5 次预热 + 最后 1 次抓取 body/header，同一 1s 窗口内完成） |
| 断言 1 | `status == 429` |
| 断言 2 | body == 规则配置的 `customResponse.body`（字面量精确匹配） |
| 断言 3 | 响应头 `X-Polaris-RateLimit-Rule` == `ratelimit-e2e-custom-response-rule` |
| 判定 | 3 个断言全 PASS → PASS |
| 原理 | `QuotaResponse.GetActiveRule().GetCustomResponse().GetBody()` 透传规则 body；`GetActiveRuleName()` 写入响应头 |

### 用例 7.2 — GLOBAL 自定义返回

| 项 | 内容 |
|-----|------|
| 链路 | curl → consumer:18199 → provider:18189 |
| 操作 | 同 7.1（合并策略） |
| 断言 | 同 7.1，规则名为 `ratelimit-e2e-global-custom-response-rule` |
| 前提 | polaris.limiter 可用（不可用 → SKIP） |
| 原理 | 验证 GLOBAL / FAILOVER_LOCAL 路径下 customResponse 仍能透传（静态属性，不依赖远端） |

---

## 用例 8.x — 限流监控指标验证

### 规则

| 字段 | 值 |
|------|-----|
| name | `ratelimit-e2e-metrics-rule` |
| service / namespace | `MetricsRatelimitEchoServer` / `default` |
| resource | `QPS` |
| type | `LOCAL` |
| method | EXACT `/echo` |
| amounts | `maxAmount=2 / validDuration=1s` |
| action | `REJECT` |

> **专用服务名 `MetricsRatelimitEchoServer`**：与 1.x（`QpsRatelimitEchoServer`）等用例隔离，让 consumer 服务发现只能命中本用例启动的那一个 provider 实例，不会被 LB 分散到其他用例的同服务 provider 上。

### 启动服务

| 角色 | 源码 | 端口 | metrics 端口 |
|------|------|------|-------------|
| provider | `provider-qps` | 18200 | **28200** |
| consumer | `consumer` | 18201 | —（push 模式） |

### 请求链路

```
curl → consumer:18201/echo → (polaris 服务发现) → provider:18200/echo → LimitAPI.GetQuota
```

### 用例 8.1 — /metrics 指标存在性 + 数值校验

| 项 | 内容 |
|-----|------|
| 操作 | 经 consumer:18201 串行 6 次 GET `/echo` → 等聚合 → curl `http://127.0.0.1:28200/metrics` |
| 断言 1 | `/metrics` 端口可达（HTTP 200） |
| 断言 2 | `ratelimit_rq_total` / `ratelimit_rq_pass` / `ratelimit_rq_limit` 三个指标存在 |
| 断言 3 | label 包含 `callee_namespace` / `callee_service`（**值精确匹配 `MetricsRatelimitEchoServer`**） / `callee_method` / `rule_name` |
| 断言 4 | `pass > 0` 且 `limit > 0`（阈值 2/1s → pass≈2 limit≈4） |
| 断言 5 | `total == pass + limit` |
| 判定 | 5 个断言全 PASS → PASS |

**为什么使用独立服务名？** 用 `MetricsRatelimitEchoServer` 而非复用 `QpsRatelimitEchoServer`，可以让 consumer 的服务发现只能拉到本用例的一个 provider 实例（同服务下只有一个 provider）。否则 consumer 会通过 LB 把请求分散到 1.x 等用例残留的同服务 provider 上，导致本 provider 的 /metrics 只能看到部分数据，limit 维度容易为 0。这种设计让 8.x 既能验证完整的 `curl → consumer → provider` 链路，又能保证监控数据完整聚合在一个实例。

**metrics 时间语义**：Prometheus pull 模式下指标不带时间戳；聚合周期 30s 内的 ReportStat 数据刷入 registry，连续 60s 无新数据后 GC 清除。脚本控制了"先发请求 → 再抓 /metrics"的时序，数据必然属于那 6 次请求。

---

## 用例 9.x — 限流事件上报验证

### 规则

复用 `ratelimit-e2e-qps-rule`（QPS LOCAL，maxAmount=2/1s，作用于 `QpsRatelimitEchoServer` 的 `/echo`）

### 启动服务

| 角色 | 源码/工具 | 端口 | 说明 |
|------|-----------|------|------|
| mock event server | `mock_event_server.py` | 19090 | 模拟 pushgateway，捕获事件到 JSON 文件（用例 9.1/9.2 使用） |
| provider (mock) | `provider-qps` | 18202 | 用例 9.1/9.2 用，POLARIS_EVENT_ADDR=127.0.0.1:19090 |
| provider (remote) | `provider-qps` | 18203 | 用例 9.3 用，**不**设 POLARIS_EVENT_ADDR → 走服务发现到真实 polaris.pushgateway |

### 配置

provider 的 `polaris.yaml` 中 `eventReporter.enable: true`，pushgateway 插件 `address: "${POLARIS_EVENT_ADDR}"`：

| 环境变量 | 设置 | 行为 |
|----------|------|------|
| `POLARIS_EVENT_ADDR=127.0.0.1:19090` | 用例 9.1/9.2 | SDK 直接 POST 到 mock server，绕过服务发现 |
| `POLARIS_EVENT_ADDR=`（空串） | 用例 9.3 | SDK 走服务发现拉 `Polaris/polaris.pushgateway` 实例，推到真实云端 |

### 事件触发原理

```
请求 → AllocateQuota() → atomic.SwapInt64(&lastCode, currentCode)
                               ↓ (previousCode != currentCode)
                         reportRateLimitEvent()
                               ↓
                         BuildRateLimitEvent() → {event_name, rule_name, ...}
                               ↓
                         EventReporter.ReportEvent() → 队列 → 每秒 batch flush
                               ↓
                         HTTP POST → mock_event_server → captured_events.json
```

### 用例 9.1 — RateLimitStart 事件

| 项 | 内容 |
|-----|------|
| 操作 | 直接向 provider:18202/echo 发 6 次请求（触发 UNLIMITED→LIMITED） → 等 3s batch flush |
| 断言 | `captured_events.json` 中存在 `event_name=RateLimitStart` 且 `rule_name=ratelimit-e2e-qps-rule` |
| 判定 | 事件存在且字段匹配 → PASS |

### 用例 9.2 — RateLimitEnd 事件

| 项 | 内容 |
|-----|------|
| 操作 | 等 2s 窗口恢复 → 发 1 次请求（触发 LIMITED→UNLIMITED） → 等 3s flush |
| 断言 | `captured_events.json` 中存在 `event_name=RateLimitEnd` 且 `rule_name=ratelimit-e2e-qps-rule` |
| 判定 | 事件存在且字段匹配 → PASS |

### 用例 9.3 — 远程 pushgateway 事件推送（不 mock）

| 项 | 内容 |
|-----|------|
| 目的 | 与 9.1/9.2 互补：让 SDK 走服务发现真实推送到云端 pushgateway，便于在 `/data/pushgateway-data/polaris-client-event.log` 看到本次测试产生的事件，做端到端对账 |
| 操作 | provider:18203 发 6 次请求触发限流 → 等 2s → 发 1 次触发恢复 → 等 4s 让 SDK flush |
| 必过断言 | SDK 端 `polaris/log/event/polaris-event.log` 中观察到至少 1 条 `ReportEvent called.*RateLimitStart` 和 1 条 `ReportEvent called.*RateLimitEnd` |
| 告知信息 | grep 同一日志的 `request success` 与 `request failed` 行数：成功表示远程 pushgateway 收到事件；失败（本地 macOS 常见）只打印告知，不影响判定 |
| 判定 | SDK 端触发了 ReportEvent → PASS（即使 Flush 失败也判 PASS，因为这是环境问题非代码问题） |

> **设计权衡**：9.3 不强制要求事件到达远程，因为 `polaris.pushgateway` 实例的注册 IP 在客户端通常是云端内网不可达。如果运行环境（如同一 K8s 集群内）实际能连通，PASS 描述会显示"Flush 成功 N 次"，可登录云端 pushgateway 查看事件日志。

---

## 判定与汇总

```
✅ [编号 名称] PASS - 详情
❌ [编号 名称] FAIL - 详情
⏭️  [编号 名称] SKIP - 详情（环境不具备前置条件，不计入失败）
```

退出码：全部 PASS → 0；N 个 FAIL → N

## 失败排查

| 现象 | 排查方向 |
|------|----------|
| `HTTP=403002` | Polaris 开启鉴权，用 `--polaris-token` 传入 token |
| `provider 30 秒内未就绪` | 查 `.logs/<provider>.log`，常见为 polaris 不可达 |
| 2.1 实际 429 | `max_queue_delay` 太小 → 控制台调大 |
| 2.1 耗时 < 700ms | 规则 action 不是 `UNIRATE`（被当 reject 处理） |
| 3.1 出现 `200 > 2` | SDK 尚未拉到规则（脚本预留了 4s，检查网络） |
| 3.2 单发 429 | 上批请求未 Release → 检查 `defer future.Release()` |
| 6.3 `ok > 24` | polaris.limiter 不可达 → FAILOVER_LOCAL 退化为各自独享 |
| 7.1 status=200 | 窗口跨秒（已用合并策略修复）；或规则不存在 |
| 8.1 `callee_namespace=""` | label supplier 误用 `EmptyInstanceGauge.GetNamespace()`（已修复为 `val.Namespace`） |
| 8.1 `limit=0` | 请求被 LB 分散到其他用例的同服务 provider（已修复为独立服务名 `MetricsRatelimitEchoServer`） |
| 8.1 端口不可达 | 需至少一次 GetQuota 调用后 SDK 才 prepare registry |

调用 `--keep` 保留进程和日志便于排查；`./cleanup.sh -f` 一键清理。

## 与单元测试的关系

本脚本验证**完整链路**（HTTP → SDK → 限流插件 → 计数 → 释放），单元测试验证组件正确性：

- `plugin/ratelimiter/reject_concurrency/bucket_concurrency_test.go`
- `pkg/flow/quota/resolver_test.go`
- `pkg/flow/quota/window_concurrency_test.go`
- `pkg/model/quota_test.go`（ActiveRule nil 安全）
- `pkg/model/event/ratelimit_test.go`（事件状态变化）
- `pkg/flow/sync_flow_test.go`（监控 Labels 格式化）
