# examples/circuitbreaker — 北极星（Polaris）故障熔断 Demo 集合

本目录提供 polaris-go SDK **三种熔断级别**（实例级 / 服务级 / 接口级）+ **三种编程模型**（装饰器 / InvokeHandler / 散装 Report）的端到端示例，并提供一键 E2E 测试脚本与清理脚本，方便快速验证新版 SDK 是否符合预期。

## 目录结构

```
examples/circuitbreaker/
├── callee/                              # 共用 Provider
│   ├── provider-a/                      #   /echo 默认返回 200
│   └── provider-b/                      #   /echo 默认返回 500
│   说明：两个 provider 都暴露 /switch?openError=true|false 用于运行时切换返回码
│         同时暴露 /order /info /slow 三个端点，配合 newCircuitBreakerCaller 验证多接口熔断
├── newCircuitBreakerCaller/             # 【主推】统一装饰器写法
│   └── consumer/                        #   一份 demo 同时覆盖 SERVICE/METHOD/INSTANCE 三种级别
│                                        #   - MakeFunctionDecorator + RequestContext.Method
│                                        #   - customer func 内 SetInstance 让实例级也走装饰器
├── invokeHandlerCaller/                 # InvokeHandler 手动编排写法
│   └── consumer/                        #   暴露 AcquirePermission / OnSuccess / OnError 三阶段钩子
│                                        #   适用 gRPC interceptor / 自定义中间件 / 异步模型
├── oldInstanceCircuitBreakerCaller/     # 旧版散装写法（保留兼容）
│   └── consumer/                        #   CircuitBreakerAPI.Report + ConsumerAPI.UpdateServiceCallResult
│                                        #   仅覆盖实例级熔断
├── verify_circuitbreaker.sh             # 【主入口】一键 E2E 测试脚本
├── cleanup.sh                           # 进程与目录清理脚本
├── test.md                              # 用例清单
└── README.md                            # 本文件
```

## 三种编程模型详解

polaris-go SDK 提供三种接入熔断的方式，按推荐程度排序：

### 一、统一装饰器写法（`newCircuitBreakerCaller`）—— 【主推】

通过 `CircuitBreakerAPI.MakeFunctionDecorator` 将"熔断检查 → 业务调用 → 熔断上报"封装为**一个 `DecoratorFunction`**，业务只需写好 customer func 即可。

**核心调用链**：

```go
decorator := svr.circuitBreaker.MakeFunctionDecorator(
    // ① 业务回调：服务发现 + HTTP 调用 + 回填 Instance
    func(ctx context.Context, args interface{}) (interface{}, error) {
        instance, _ := svr.discoverInstance()
        model.GetInvokeContext(ctx).SetInstance(instance)  // ← 关键：触发实例级
        resp, err := http.Get(url)
        // 5xx 返回 error → 走 OnError；2xx/4xx 返回 commonResponse → 走 OnSuccess
        ...
    },
    // ② RequestContext：Method 非空 → 启用接口级
    &api.RequestContext{
        RequestContext: model.RequestContext{
            Caller: &model.ServiceKey{...},
            Callee: &model.ServiceKey{...},
            CodeConvert: &customCodeConvert{},  // ← 从 commonResponse 抽取 retCode
            Method: "/echo",                    // ← 触发接口级
        },
    },
)
// ③ 调用 decorator：一行代码完成"检查→执行→上报"
ret, abort, err := decorator(context.Background(), nil)
```

**生命周期（完全自动）**：

```
DecoratorFunction()
  ├─ [自动] Check          ← 服务级 → 接口级 短路检查
  ├─ [自动] customer func  ← 用户业务逻辑
  └─ [自动] Report         ← 按 ServiceResource → MethodResource → InstanceResource 三级上报
```

**覆盖熔断级别**：

| RequestContext.Method | ic.SetInstance() | 覆盖级别 |
|---|---|---|
| 为空 | 不调用 | **仅服务级** — 最轻量 |
| 非空 | 不调用 | 服务级 + 接口级 |
| 非空 | 调用 | **服务级 + 接口级 + 实例级** — demo 默认用法 |

**适用场景**：

- **标准同步调用**（HTTP RPC / gRPC Unary）：天然适合，一行 `decorator(ctx, nil)` 搞定
- **只需服务级熔断**：Method 留空 + 不调 `SetInstance`，零额外开销
- **同时要服务级 + 接口级**：填 Method 即可，多接口隔离
- **要对齐 4xx/5xx 语义**：`5xx → return error`（走 OnError → SDK 用 retCode="-1" 哨兵命中规则），`2xx/4xx → return (commonResponse, nil)`（走 OnSuccess → retCode 透传真实状态码，由规则 ErrorCondition 决定）

**示例代码**：`newCircuitBreakerCaller/consumer/main.go:216-274`

---

### 二、InvokeHandler 手动编排写法（`invokeHandlerCaller`）

通过 `CircuitBreakerAPI.MakeInvokeHandler` 获取 `InvokeHandler`，它暴露 **三个生命周期钩子**，由调用方自行编排检查 → 执行 → 上报的顺序。

**核心调用链**：

```go
handler := svr.circuitBreaker.MakeInvokeHandler(&api.RequestContext{...})

// ── 第一步：熔断检查 ──
pass, aborted, err := handler.AcquirePermission()
if !pass { /* 被熔断拦截，处理 fallback 或返回 503 */ }

// ── 第二步：业务调用（服务发现 + HTTP）──
instance, _ := svr.discoverInstance()
resp, err := http.Get(url)

// ── 第三步：熔断上报 ──
if err != nil {
    handler.OnError(&model.ResponseContext{
        Duration: delay, Err: err, Instance: instance,
    })
} else {
    handler.OnSuccess(&model.ResponseContext{
        Duration: delay,
        Result:   commonResponse{code: resp.StatusCode, body: string(data)},
        Instance: instance,
    })
}

// ── 附加：调用结果指标上报（Prometheus 等）──
// 注意：与 LB 权重 / 健康检查 / 熔断均无关；
//   LB 动态权重由 weightAdjuster（warmup）管理，
//   健康检查由 healthCheck 插件链独立周期探测，
//   熔断由 CircuitBreakerAPI.Report() 上报。
svr.consumer.UpdateServiceCallResult(...)
```

**生命周期（手动控制）**：

```
【你控制】 handler.AcquirePermission()  ← 服务级 → 接口级 短路检查
【你控制】 服务发现 + HTTP 调用
【你控制】 handler.OnSuccess() 或 OnError()
```

**与装饰器写法的关键差异**：

| 维度 | 装饰器（`MakeFunctionDecorator`） | InvokeHandler（`MakeInvokeHandler`） |
|---|---|---|
| 编程模型 | 闭包 + 回调 | 三阶段钩子 |
| 自动上报 | ✅ 检查/执行/上报全自动 | ❌ 需手动调用 `OnSuccess/OnError` |
| 代码量 | 最少（约 40 行） | 较多（约 80 行） |
| 熔断检查时机 | 只能在装饰器入口 | **可在业务调用前任意时刻** |
| 熔断上报时机 | 装饰器结束时自动 | **可在业务调用完成后任意时刻** |
| 同一 handler 复用 | 不支持（每个 decorator 绑定一个 customer func） | **支持**（同一个 handler 可被多次 Acquire→Report 循环复用） |

**适用场景**：

- **gRPC interceptor**：UnaryServerInterceptor 中先 `AcquirePermission()` → `handler(ctx, req)` → 根据结果调 `OnSuccess/OnError`，与 gRPC 生命周期天然对齐
- **自定义中间件**：Go 标准库 `http.Handler` middleware 中在 `next.ServeHTTP` 前后分别做检查/上报
- **异步调用模型**：消息消费、定时任务等，需要先检查再异步执行，最后异步回调上报
- **需要先预检再决定是否发起请求**：比如资源有限时先 `AcquirePermission` 再 `GetOneInstance`

**示例代码**：`invokeHandlerCaller/consumer/main.go:223-340`

---

### 三、旧版散装写法（`oldInstanceCircuitBreakerCaller`）—— 保留兼容

通过 `CircuitBreakerAPI.Report()` + `ConsumerAPI.UpdateServiceCallResult()` 手动构造 `InstanceResource` 上报。

**核心调用链**：

```go
// ── 服务发现 ──
instance, _ := svr.consumer.GetOneInstance(...)

// ── HTTP 调用 ──
resp, err := http.Get(...)

// ── 调用结果指标上报（Prometheus 等）──
svr.consumer.UpdateServiceCallResult(...)

// ── 熔断上报（手动构造 Resource）──
insRes, _ := model.NewInstanceResource(
    &model.ServiceKey{Namespace: calleeNs, Service: calleeSvc},
    &model.ServiceKey{Namespace: callerNs, Service: callerSvc},
    "http", instance.GetHost(), instance.GetPort())
svr.circuitBreaker.Report(&model.ResourceStat{
    Delay:     time.Since(start),
    RetStatus: retStatus,
    RetCode:   retCode,
    Resource:  insRes,
})
```

**与装饰器 / InvokeHandler 的关键差异**：

| 维度 | 装饰器 / InvokeHandler | 旧版散装 Report |
|---|---|---|
| 覆盖级别 | SERVICE / METHOD / INSTANCE 三级 | **仅 INSTANCE**（实例级） |
| 熔断检查 | SDK 自动 Check | **无**（只能上报，不会主动拦截） |
| 半开配额管理 | SDK 自动管理 | **不参与**（上报即计入，无配额限制下的放行/拒绝逻辑） |
| Resource 构造 | SDK 内部自动 | **手动 NewInstanceResource** |
| 代码量 | 一/三个钩子 | 需完整拼接 ServiceKey + host:port + caller |

> **本质差异**：装饰器 / InvokeHandler 走的是 `Check → Execute → Report` 完整闭环，而旧版 `Report` 只做"上报统计"这一步，没有"熔断检查"能力。因此旧版写法**收到 500 后仍能继续发请求**，不会像装饰器那样主动拦截返回 `CallAborted`。

**适用场景**：

- **存量代码零改动兼容**：历史上用 `Report` 的代码不用改，新项目不建议使用
- **仅需实例级熔断**的极简场景（但装饰器写法也可以做到，且更简洁）
- **定制化 Check 逻辑**：需要自己实现检查策略而非用 SDK 内置 Check 时（极少见）

**示例代码**：`oldInstanceCircuitBreakerCaller/consumer/main.go:235-267`

---

### 四、选型速查表

| 你的场景 | 推荐写法 | 对应目录 |
|---|---|---|
| 标准 HTTP / gRPC Unary 同步调用 | **装饰器** | `newCircuitBreakerCaller` |
| 需要同时覆盖服务级 + 接口级 + 实例级 | **装饰器** | `newCircuitBreakerCaller` |
| 只需服务级（无需关心实例） | **装饰器**（Method 留空） | `newCircuitBreakerCaller` |
| gRPC interceptor / 自定义 middleware | **InvokeHandler** | `invokeHandlerCaller` |
| 异步调用（MQ 消费 / 定时任务） | **InvokeHandler** | `invokeHandlerCaller` |
| 需要先 Check 再决定是否发请求 | **InvokeHandler** | `invokeHandlerCaller` |
| 存量代码兼容（仅实例级） | 旧版 Report | `oldInstanceCircuitBreakerCaller` |
| 新项目 / 重构 | **装饰器** | `newCircuitBreakerCaller` |

---

## 三种熔断级别速览

| 级别       | proto Level     | 资源标识                          | 触发后行为                                |
|------------|------------------|-----------------------------------|--------------------------------------------|
| 实例级     | `Level_INSTANCE` | `host:port + service + caller`    | 单实例被摘除，流量转移到其他健康实例       |
| 服务级     | `Level_SERVICE`  | `service + caller`                | 整个服务被熔断，请求返回 `call aborted`    |
| 接口级     | `Level_METHOD`   | `service + protocol/method/path`  | 仅特定接口被熔断，其他接口不受影响         |

> **优先级**：服务级 > 接口级 > 实例级。`CircuitBreakerAPI.Check` 在 `DefaultInvokeHandler` 中按 SERVICE → METHOD 顺序短路。
>
> 实例级熔断的状态会写回 `Instance.CircuitBreakerStatus`，可通过 `GetInstances()` 看到；服务级 / 接口级状态不写回 Instance。

## 半开配额精确管理（HalfOpenStatus）

当资源熔断打开后会经过 `sleepWindow` 才进入半开态。半开态下放行请求受**精确配额**控制：

- **发放配额**：`HalfOpenStatus.AcquirePermission` 用 atomic 计数，并发请求按 `RecoverCondition.ConsecutiveSuccess` 数量逐次放行；
- **配额耗尽**：超额请求会被 SDK 直接拦截返回 `*model.CallAborted`，对应 demo 中 `writeResult` 的 `abort != nil` 分支；
- **归集判定**：所有放行结果归集后，全部成功 → 切回 Close；任一失败 → 立即重新 Open；
- **demo 行为**：`abort.HasFallback() == false` 时（半开配额耗尽路径默认无 fallback），demo 统一返回 `503 + "circuit breaker open: ..."` 文案，便于通过 curl/脚本观察拒绝路径。

## 块级独立错误条件（BlockConfig）

一条规则中的多个 `BlockConfig`（块）相互**独立计数**：

- 每个块持有自己的 `ErrorConditions`：块自身非空时使用块的；为空则回退到规则顶层；都空则透传 `stat.RetStatus`；
- 每个块按 `BlockConfig × TriggerCondition` 笛卡尔积建立独立 trigger counter；
- 任一块的任一 trigger 触发即让整个资源进入 Open——块**不维护**自己的状态机，资源级状态机统一调度。

## 4xx vs 5xx：三条独立的路径

处理 HTTP 状态码时需要区分三条**完全独立**的路径：

| 路径 | 入口 | 谁来决定"是否失败" | 4xx 行为 |
|---|---|---|---|
| **熔断器** | `customCodeConvert` → `commonReport` → `BlockConfig.ErrorConditions` | **完全由你配置的规则决定**（`RANGE 500~599` / `EXACT` / `IN/NOT_IN` / `REGEX` / `DELAY`） | 默认规则配 `500~599` 时不计入；用户可显式配 `IN [400,500]` 把 4xx 也纳入 |
| **调用结果指标** | `ConsumerAPI.UpdateServiceCallResult` 的 `RetStatus` / `RetCode` / `Delay` | demo 自己决定（仅用于 Prometheus 等指标打点，**不影响 LB 权重和健康检查**） | demo 默认**只把 5xx 标 `RetFail`**；4xx 视为成功（4xx 通常是请求侧参数 / 鉴权问题，不应计入失败率） |
| **LB 权重 / 健康检查** | SDK 自管理（`weightAdjuster` 链 + `healthCheck` 插件链） | SDK 内部自动处理 | 与本 demo 无关，调用方无需感知 |

**约定**：

1. demo 中 `customCodeConvert.OnSuccess` 透传 HTTP 状态码字符串给 SDK，让规则决定是否纳入熔断；
2. demo 中 `customCodeConvert.OnError` 返回 `"-1"` 这类**明显非 HTTP 状态码**的值，避免被 `RANGE 500~599` 等规则误命中或漏命中；
3. demo 中 `reportServiceCallResult` 只在 `StatusCode >= 500` 时标 `RetFail`，4xx 一律 `RetSuccess`；
4. 验证脚本 `verify_circuitbreaker.sh` 默认规则下发的 `ErrorCondition` 同样是 `RANGE 500~599`，与 demo 行为对齐。

## 前置条件

1. 已运行的北极星服务端（Polaris Server）：
   - gRPC: `<polaris-server>:8091`
   - HTTP 管控: `<polaris-server>:8090`
2. 本地安装 **Go**（与 `examples/circuitbreaker/*/go.mod` 兼容版本）、**python3**、`curl`、`lsof`
3. 子目录的 `go.mod` 已通过 `replace github.com/polarismesh/polaris-go => ../../../../` 指向本地源码，因此 `verify_circuitbreaker.sh` 编译时使用的就是当前仓库的 SDK 改动

## 一键 E2E 测试 — `verify_circuitbreaker.sh`

脚本会：
1. 编译 `provider-a` / `provider-b` 与三种 consumer
2. 启动 Provider 集群（注册到 `CircuitBreakerCallee` 服务）
3. 通过 Polaris HTTP API 自动创建 `Level=INSTANCE/SERVICE/METHOD` 三条熔断规则
4. 启动对应 consumer，并发请求触发熔断
5. 通过 `/switch` 翻转 provider 状态验证恢复链路
6. 退出时自动删除所创建的熔断规则并清理进程

### 基本用法

```bash
chmod +x verify_circuitbreaker.sh
./verify_circuitbreaker.sh                                              # 跑全部用例（默认 9 个）
./verify_circuitbreaker.sh --polaris-server 10.0.0.1                    # 指定北极星地址
./verify_circuitbreaker.sh --only instance                              # 仅跑实例级
./verify_circuitbreaker.sh --only service,interface,http_status         # 多个用例组合
./verify_circuitbreaker.sh --only default_rule                          # 仅跑默认规则兜底用例
./verify_circuitbreaker.sh --only protocol_method,pathtype              # 仅跑高级匹配维度用例
```

### 关键选项

| 参数                        | 说明                                                               |
|-----------------------------|--------------------------------------------------------------------|
| `--polaris-server <地址>`   | 北极星服务端地址（默认 `127.0.0.1`）                               |
| `--polaris-token <令牌>`    | 北极星鉴权令牌                                                     |
| `--namespace <ns>`          | 命名空间（默认 `default`）                                         |
| `--service <name>`          | 被调服务名（默认 `CircuitBreakerCallee`）                          |
| `--only <列表>`             | 仅运行指定用例：`instance`, `service`, `interface`, `old_instance`, `http_status`, `default_rule`, `modify_rule`, `protocol_method`, `pathtype` |
| `--trigger-count <次数>`    | 触发阶段请求次数（默认 15，越大越容易触发熔断）                    |
| `--recovery-count <次数>`   | 验证 / 恢复阶段请求次数（默认 10）                                 |
| `--wait-half-open <秒数>`   | 服务级用例等待 sleepWindow 进入半开的秒数（默认 35）               |
| `--debug`                   | 透传 `--debug=true` 给所有 provider/consumer 子进程，开启 Polaris SDK DEBUG 日志；demo 自身日志默认携带 `文件:行号` |

### 输出示例

```
╔════════════════════════════════════════════════════════════╗
║   故障熔断验证结果汇总                                    ║
╚════════════════════════════════════════════════════════════╝

  用例         结果
  ------------ ----------
  instance     PASS
  service      PASS
  interface    PASS

╔════════════════════════════════════════════════════════════╗
║  验证结论: ✅ 全部熔断用例通过                            ║
╚════════════════════════════════════════════════════════════╝
```

### 退出码

- `0` = 全部 PASS
- `>0` = FAIL 数量（便于 CI 快速判定）

### 日志位置

- 主日志：`examples/circuitbreaker/.logs/verify_circuitbreaker-<时间戳>.log`
- Provider：`examples/circuitbreaker/.logs/provider_a.log` / `provider_b.log`
- Consumer：`examples/circuitbreaker/.logs/instance_consumer.log` 等

## 一键清理 — `cleanup.sh`

```bash
chmod +x cleanup.sh
./cleanup.sh             # 交互式（先展示再确认）
./cleanup.sh -f          # 强制清理，不询问
./cleanup.sh --dry-run   # 仅展示残留项，不执行清理
```

清理范围：
1. 按命令行匹配 `examples/circuitbreaker/.build/(provider_a|provider_b|*_consumer)` 的进程
2. 删除 `.build/` 与 `.logs/` 目录（询问后）

> 熔断规则在 `verify_circuitbreaker.sh` 退出时已自动删除，`cleanup.sh` 不会再删一次规则。
> 若脚本被 `kill -9` 强制终止导致规则未清理，可手动登录北极星控制台搜索 `cb-instance-*` / `cb-service-*` / `cb-interface-*` 删除。

## 用例细节

详见 [test.md](test.md)。

## 高级匹配维度（用例 8 / 用例 10）

`BlockConfig.api` 的三字段（protocol / method / path）每字段都支持独立匹配，验证 SDK 能否按业务侧元数据精确隔离熔断范围。

| 维度 | 字段 | 验证目标 | 用例编号 |
|---|---|---|---|
| 接口协议 + HTTP 方法合并 | `BlockConfig.api.protocol` + `BlockConfig.api.method` | 1 条规则 13 个 BC：4 个 protocol + 9 个 HTTP method 各自独立命中；`protocol="http"` 不会命中 `protocol="dubbo"` 的 BC | 用例 8 `case_protocol_method` |
| 路径匹配方式 | `BlockConfig.api.path`（5 种 MatchString）| EXACT（精确）/ REGEX（正则）/ NOT_EQUALS（不等于）/ IN（包含）/ NOT_IN（不包含）各自正向+反向匹配行为 | 用例 10 `case_pathtype` |

**consumer 端**：`newCircuitBreakerCaller` 在 `makeDecorator` 内通过 `inferProtocolMethod` 根据 path 段（`/api/protocol/{proto}` / `/api/method/{method}` / `/api/pathtype/...`）自动填 `RequestContext.Protocol` / `HTTPMethod`，无需在业务代码里硬编码。

**SDK 匹配逻辑**（`pkg/flow/circuitbreaker_flow.go::buildMethodResource`）：Path 非空时构造 (protocol, httpMethod, path) 三元组 MethodResource，逐字段用 `matchProtocolOrMethod` + `MatchString` 与 BlockConfig.api 三元组匹配，**任一字段不匹配即该 BC 不适用**（独立 trigger counter，不影响其他 BC 统计）。

**对应 `_gen_rule.py` 扩展**：4 个新环境变量 `BC_API_PROTOCOL` / `BC_API_METHOD` / `BC_API_PATH_TYPE` / `BC_API_PATH_VALUE[_LIST]` 支持任意 (protocol, method, path type) 组合；旧的 `BC_API_PATH`（EXACT 单值）继续可用，向后兼容。

## 子 demo 独立运行

每个子目录也可独立运行（参考各子目录的 `README.md`）：

```bash
# 1. 统一装饰器写法（推荐，一份 demo 同时覆盖 SERVICE/METHOD/INSTANCE 三种级别）
cd callee/provider-a && make run                    # 终端 1
cd callee/provider-b && make run                    # 终端 2
cd newCircuitBreakerCaller/consumer && make run     # 终端 3
# 在控制台手工创建熔断规则后，curl http://127.0.0.1:18080/<echo|order|info|slow> 即可观察

# 2. InvokeHandler 手动编排写法（gRPC interceptor / 自定义 middleware / 异步模型）
cd invokeHandlerCaller/consumer && make run         # 终端 3（端口 18081，和装饰器不冲突）

# 3. 旧版散装写法（保留兼容；仅实例级）
cd oldInstanceCircuitBreakerCaller/consumer && make run
```

## 故障排查

| 症状 | 可能原因 |
|------|---------|
| `Go 未安装` / `python3 未安装` | 安装对应工具 |
| `bind: address already in use` | 端口占用，先 `./cleanup.sh -f` 再重试 |
| 触发阶段一直返回 200 | provider-b 的 `/switch?openError=true` 没生效，或 SDK 未拉取到规则；查看 `.logs/*_consumer.log` |
| 服务级用例的 `恢复阶段` 失败 | sleepWindow 时间不够，可加大 `--wait-half-open` |
| 接口级用例不熔断 | 检查 SDK 是否已应用本仓库的 metadata 改造（`MethodResource.Path` 字段、`matchMethodWithAPI`、`BlockConfig` 处理） |
| 创建规则报 `code != 200000/200001` | 北极星鉴权未通过、规则字段不合法或服务不存在；查看脚本输出的 HTTP 响应 |
| 创建的规则未被删掉 | 脚本被 `kill -9` 强杀；登录控制台搜索 `cb-instance-*`/`cb-service-*`/`cb-interface-*` 手工删除 |

## CI 集成示例

```yaml
- name: Run polaris-go circuitbreaker E2E
  run: |
    cd examples/circuitbreaker
    chmod +x verify_circuitbreaker.sh cleanup.sh
    ./verify_circuitbreaker.sh --polaris-server ${{ env.POLARIS_HOST }} \
                               --polaris-token ${{ secrets.POLARIS_TOKEN }}
- name: Cleanup
  if: always()
  run: examples/circuitbreaker/cleanup.sh -f
```

## 约定与规范

参考 `examples/route/README.md` 章节"约定与规范"：
1. **用例编号严格顺序递增**：脚本中 `用例N.M` 标识与文档编号一致
2. **文档同步**：新增用例须同步更新 `test.md`
3. **清理同步**：新增进程/端口/产物须同步更新 `cleanup.sh`
