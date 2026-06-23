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
├── verify_circuitbreaker.sh             # 【主入口】被动熔断一键 E2E 测试脚本
├── verify_faultdetect.sh                # 主动探测（fault detect）端到端验证脚本
├── cleanup.sh                           # 进程与目录清理脚本
├── test.md                              # 被动熔断用例清单
├── fault-detect.md                      # 主动探测验证说明
└── README.md                            # 本文件
```

## customer func 返回值如何决定是否熔断

这是理解三种用法的核心。熔断判定链如下：

```
customer func 返回 (result, err)
       │
       ├─ err != nil  ──→ 调用 CodeConvert.OnError(err) → 得到 retCode（demo 返回 "-1"）
       │
       └─ err == nil  ──→ 调用 CodeConvert.OnSuccess(result) → 得到 retCode（demo 返回状态码字符串）
                               │
                               └─ 然后 SDK 内部用这个 retCode 去匹配规则的 BlockConfig.ErrorCondition.RET_CODE：
                                  · 命中 → 该次调用计为失败，累加 failRatio/consecutiveError
                                  · 未命中 → 计为成功（即使 HTTP 状态码是 400）
```

**关键设计意图**：

| 场景 | customer func 返回 | 走哪个 CodeConvert | retCode 值 | 默认 RANGE 500~599 是否命中 |
|------|-------------------|-------------------|------------|--------------------------|
| 网络错（TCP 断连 / DNS 失败） | `return nil, err` | `OnError(err)` | `"-1"` | **否**（-1 不在 500~599 范围） |
| HTTP 5xx（如 500 / 502 / 503） | `return nil, err` | `OnError(err)` | `"-1"` | **否**（-1 不在 500~599 范围） |
| HTTP 4xx（如 400 / 403 / 404） | `return commonResponse{code: 403}, nil` | `OnSuccess(resp)` | `"403"` | **否**（403 不在 500~599 范围） |
| HTTP 2xx（如 200 / 201） | `return commonResponse{code: 200}, nil` | `OnSuccess(resp)` | `"200"` | **否**（200 不在 500~599 范围） |

> **问题来了**：既然 5xx 返回 error → OnError → retCode="-1"，而 "-1" 也不在 RANGE 500~599 内，那 **5xx 是如何触发熔断的**？

**答案**：SDK 内部对 retCode="-1" 有**特殊处理**。当 `OnError` 返回的 retCode 以 "-1" 开头（即 `block_counter.parseRetStatus` 逻辑），SDK 会**直接将该次调用计为 RetFail**，不依赖 RET_CODE 类条件匹配。这是"网络错 / 服务端故障"的兜底路径。

**完整判定链（修正版）**：

```
customer func 返回 (result, err)
       │
       ├─ err != nil
       │    └─ OnError(err) → retCode = "-1"
       │         └─ SDK 看到 retCode="-1" → 直接计为 RetFail（走兜底路径，不依赖 RET_CODE 条件）
       │
       └─ err == nil
            └─ OnSuccess(result) → retCode = 真实状态码字符串（如 "200"、"403"、"500"）
                 └─ SDK 用 retCode 匹配规则 ErrorCondition.RET_CODE：
                    · 命中 → 计为 RetFail
                    · 未命中 → 计为 RetSuccess
```

**两种写法下 retCode 的来源对比**：

| 写法 | OnError retCode 来源 | OnSuccess retCode 来源 |
|------|---------------------|----------------------|
| 装饰器 + InvokeHandler | `customCodeConvert.OnError(err)` | `customCodeConvert.OnSuccess(result)` |
| 旧版散装 Report | 直接传字符串给 `ResourceStat.RetCode` | 直接传字符串给 `ResourceStat.RetCode` |

**实践建议**：

- **让 5xx 触发熔断**：customer func 对 5xx 返回 `error`，OnError 返回 "-1"，SDK 自动计为失败
- **让 4xx 不触发熔断**：customer func 对 4xx 返回 `(commonResponse, nil)`，OnSuccess 透传状态码，默认规则不命中 4xx
- **让特定 4xx（如 429）也触发熔断**：customer func 对 429 返回 `error`，OnError 返回 "-1"；或返回 `(commonResponse, nil)`，然后规则配 `IN ["429"]`
- **让网络错触发熔断**：无需特殊处理，return error 自然走 OnError → "-1" 兜底路径

---

## 三种编程模型详解

polaris-go SDK 提供三种接入熔断的方式，按推荐程度排序：

### 一、统一装饰器写法（`newCircuitBreakerCaller`）—— 【主推】

通过 `CircuitBreakerAPI.MakeFunctionDecorator` 将"熔断检查 → 业务调用 → 熔断上报"封装为**一个 `DecoratorFunction`**，业务只需写好 customer func 即可。

#### 快速上手步骤

**第 1 步**：定义返回值类型和 CodeConvert

```go
// 自定义返回值，CodeConvert 从中抽取 retCode
type commonResponse struct {
    code int
    body string
}

type customCodeConvert struct{}

// 成功路径：透传 HTTP 状态码
func (c *customCodeConvert) OnSuccess(val interface{}) string {
    resp, _ := val.(commonResponse)
    return strconv.Itoa(resp.code)
}

// 失败路径：返回 "-1" 哨兵值
func (c *customCodeConvert) OnError(err error) string {
    return "-1"
}
```

**第 2 步**：构造装饰器

```go
decorator := svr.circuitBreaker.MakeFunctionDecorator(
    // customer func：服务发现 + 业务调用 + 回填 Instance
    func(ctx context.Context, args interface{}) (interface{}, error) {
        instance, _ := svr.discoverInstance()
        // 关键：SetInstance 触发实例级熔断
        model.GetInvokeContext(ctx).SetInstance(instance)
        resp, err := http.Get(url)
        if err != nil {
            // 网络错 → return error → OnError → retCode="-1" → 兜底计为失败
            return nil, err
        }
        defer resp.Body.Close()
        data, _ := io.ReadAll(resp.Body)
        if resp.StatusCode >= 500 {
            // 5xx → return error → OnError → retCode="-1" → 兜底计为失败
            return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
        }
        // 4xx/2xx → return (commonResponse, nil) → OnSuccess → retCode 透传状态码
        return commonResponse{code: resp.StatusCode, body: string(data)}, nil
    },
    &api.RequestContext{
        RequestContext: model.RequestContext{
            Caller:      &model.ServiceKey{Namespace: "default", Service: "myApp"},
            Callee:      &model.ServiceKey{Namespace: "default", Service: "downstream"},
            CodeConvert: &customCodeConvert{},
            Method:      "/echo",   // 非空 → 启用接口级熔断
        },
    },
)
```

**第 3 步**：在 handler 中调用

```go
// 一行代码完成"熔断检查 → 执行业务 → 熔断上报"
ret, abort, err := decorator(context.Background(), nil)

// 处理三种返回
if err != nil {
    // 业务执行失败（网络错 / 5xx），不是熔断拦截
    rw.WriteHeader(http.StatusInternalServerError)
    return
}
if abort != nil {
    // 被熔断拦截：abort.HasFallback() 判断是否有 fallback 响应
    if abort.HasFallback() {
        // 透传服务端下发的 fallback
        rw.WriteHeader(abort.GetFallbackCode())
        rw.Write([]byte(abort.GetFallbackBody()))
    } else {
        // 无 fallback：返回 503
        rw.WriteHeader(http.StatusServiceUnavailable)
        rw.Write([]byte("circuit breaker open"))
    }
    return
}
// 正常响应：透传 provider 的返回
resp := ret.(commonResponse)
rw.WriteHeader(resp.code)
rw.Write([]byte(resp.body))
```

**生命周期（完全自动）**：

```
DecoratorFunction(ctx, nil)
  ├─ [自动] Check          ← 服务级 → 接口级 短路检查；熔断打开时返回 abort != nil
  ├─ [自动] customer func  ← 用户业务逻辑；返回 (result, error)
  └─ [自动] Report         ← 按 ServiceResource → MethodResource → InstanceResource 三级上报
       │
       ├─ customer func 返回 error → 调 OnError → retCode="-1" → 计为失败
       └─ customer func 返回 (result, nil) → 调 OnSuccess → retCode 透传 → 规则匹配
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
- **要对齐 4xx/5xx 语义**：`5xx → return error`（走 OnError → SDK 用 retCode="-1" 哨兵兜底计为失败），`2xx/4xx → return (commonResponse, nil)`（走 OnSuccess → retCode 透传真实状态码，由规则 ErrorCondition 决定）

**示例代码**：`newCircuitBreakerCaller/consumer/main.go:247-316`

---

### 二、InvokeHandler 手动编排写法（`invokeHandlerCaller`）

通过 `CircuitBreakerAPI.MakeInvokeHandler` 获取 `InvokeHandler`，它暴露 **三个生命周期钩子**，由调用方自行编排检查 → 执行 → 上报的顺序。

#### 快速上手步骤

**第 1 步**：创建 InvokeHandler（初始化时一次性完成）

```go
// 每个 path 独立一个 handler
handler := svr.circuitBreaker.MakeInvokeHandler(&api.RequestContext{
    RequestContext: model.RequestContext{
        Caller:      &model.ServiceKey{Namespace: "default", Service: "myApp"},
        Callee:      &model.ServiceKey{Namespace: "default", Service: "downstream"},
        CodeConvert: &customCodeConvert{},   // 同装饰器写法
        Method:      "/echo",                // 非空 → 启用接口级熔断
    },
})
```

**第 2 步**：在每次请求中手动编排三步

```go
// ── 第一步：熔断检查（可在业务调用前任意时刻）──
pass, aborted, err := handler.AcquirePermission()
if err != nil {
    // SDK 内部异常
    rw.WriteHeader(http.StatusInternalServerError)
    return
}
if !pass {
    // 被熔断拦截：aborted.HasFallback() 判断是否有 fallback
    if aborted.HasFallback() {
        rw.WriteHeader(aborted.GetFallbackCode())
        rw.Write([]byte(aborted.GetFallbackBody()))
    } else {
        rw.WriteHeader(http.StatusServiceUnavailable)
        rw.Write([]byte("circuit breaker open"))
    }
    return
}

// ── 第二步：业务调用（服务发现 + HTTP）──
instance, _ := svr.discoverInstance()
resp, err := http.Get(url)

// ── 第三步：熔断上报 ──
// Instance 字段传入实际命中的实例 → 触发实例级熔断统计
if err != nil {
    handler.OnError(&model.ResponseContext{
        Duration: delay,
        Err:      err,       // ← 非 nil → SDK 内部调 CodeConvert.OnError → retCode="-1"
        Instance: instance,
    })
} else {
    defer resp.Body.Close()
    data, _ := io.ReadAll(resp.Body)
    if resp.StatusCode >= 500 {
        // 5xx：走 OnError → SDK 调 OnError → retCode="-1" → 兜底计为失败
        handler.OnError(&model.ResponseContext{
            Duration: delay,
            Err:      fmt.Errorf("upstream returned %d", resp.StatusCode),
            Instance: instance,
        })
    } else {
        // 4xx/2xx：走 OnSuccess → SDK 调 OnSuccess → retCode 透传状态码
        handler.OnSuccess(&model.ResponseContext{
            Duration: delay,
            Result:   commonResponse{code: resp.StatusCode, body: string(data)},
            Instance: instance,
        })
    }
}
```

**生命周期（手动控制）**：

```
【你控制】 handler.AcquirePermission()  ← 服务级 → 接口级 短路检查；熔断打开时返回 pass=false
【你控制】 服务发现 + HTTP 调用
【你控制】 handler.OnSuccess() 或 OnError()
           ├─ OnError(ResponseContext{Err: err, ...}) → SDK 调 CodeConvert.OnError(err) → retCode="-1" → 计为失败
           └─ OnSuccess(ResponseContext{Result: resp, ...}) → SDK 调 CodeConvert.OnSuccess(resp) → retCode 透传 → 规则匹配
```

**OnError 与 OnSuccess 的返回值如何决定熔断**：

与装饰器写法完全一致：
- `OnError` 中 `ResponseContext.Err` 非 nil → SDK 调 `CodeConvert.OnError(err)` → retCode="-1" → 兜底计为失败
- `OnSuccess` 中 `ResponseContext.Result` 非 nil → SDK 调 `CodeConvert.OnSuccess(result)` → retCode 透传 → 规则匹配

区别仅在于：装饰器中 customer func 返回 `error` 即自动触发 OnError；InvokeHandler 中你**显式调用** `handler.OnError()` 来触发。

**适用场景**：

- **gRPC interceptor**：UnaryServerInterceptor 中先 `AcquirePermission()` → `handler(ctx, req)` → 根据结果调 `OnSuccess/OnError`
- **自定义中间件**：`http.Handler` middleware 中在 `next.ServeHTTP` 前后分别做检查/上报
- **异步调用模型**：消息消费、定时任务，先检查再异步执行，最后异步回调上报
- **需要先预检再决定是否发起请求**：资源有限时先 `AcquirePermission` 再 `GetOneInstance`

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

#### 快速上手步骤

**第 1 步**：服务发现 + HTTP 调用（无熔断检查）

```go
// 注意：旧版写法没有熔断检查，即使实例已被熔断，请求仍会发出
instance, _ := svr.consumer.GetOneInstance(...)
resp, err := http.Get(url)
```

**第 2 步**：上报服务调用结果（LB / 健康检查路径）

```go
svr.consumer.UpdateServiceCallResult(&polaris.ServiceCallResult{
    ServiceCallResult: model.ServiceCallResult{
        CalledInstance: instance,
        RetStatus:      retStatus,   // RetSuccess 或 RetFail
    },
})
```

**第 3 步**：手动构造 InstanceResource 上报熔断

```go
insRes, _ := model.NewInstanceResource(
    &model.ServiceKey{Namespace: calleeNs, Service: calleeSvc},
    &model.ServiceKey{Namespace: callerNs, Service: callerSvc},
    "http", instance.GetHost(), instance.GetPort())
svr.circuitBreaker.Report(&model.ResourceStat{
    Delay:     time.Since(start),
    RetStatus: retStatus,   // RetFail / RetSuccess
    RetCode:   retCode,     // 直接传字符串，无 CodeConvert
    Resource:  insRes,
})
```

**retCode 如何决定熔断（无 CodeConvert，直接传字符串）**：

```go
// 网络错：直接传 "-1" → SDK 兜底计为失败
svr.reportCircuitBreak(instance, model.RetFail, "-1", start)

// HTTP 5xx：RetFail + 真实状态码字符串
svr.reportCircuitBreak(instance, model.RetFail, "500", start)

// HTTP 4xx：RetSuccess + 真实状态码 → 规则不命中 → 不计入熔断
svr.reportCircuitBreak(instance, model.RetSuccess, "403", start)

// HTTP 2xx：RetSuccess + 真实状态码 → 规则不命中 → 计为成功
svr.reportCircuitBreak(instance, model.RetSuccess, "200", start)
```

**核心差异**：

| 维度 | 装饰器 / InvokeHandler | 旧版散装 Report |
|---|---|---|
| 熔断检查 | SDK 自动 Check | **无**（只能上报，不会主动拦截） |
| 半开配额管理 | SDK 自动管理 | **不参与** |
| 覆盖级别 | SERVICE / METHOD / INSTANCE 三级 | **仅 INSTANCE**（实例级） |
| retCode 来源 | CodeConvert.OnSuccess/OnError 转换 | **直接传字符串**给 ResourceStat.RetCode |
| Resource 构造 | SDK 内部自动 | **手动 NewInstanceResource** |
| 代码量 | 一/三个钩子 | 需完整拼接 ServiceKey + host:port + caller |

> **本质差异**：装饰器 / InvokeHandler 走的是 `Check → Execute → Report` 完整闭环，而旧版 `Report` 只做"上报统计"这一步，没有"熔断检查"能力。因此旧版写法**收到 500 后仍能继续发请求**，不会像装饰器那样主动拦截返回 `CallAborted`。

**适用场景**：

- **存量代码零改动兼容**：历史上用 `Report` 的代码不用改，新项目不建议使用
- **仅需实例级熔断**的极简场景（但装饰器写法也可以做到，且更简洁）
- **定制化 Check 逻辑**：需要自己实现检查策略而非用 SDK 内置 Check 时（极少见）

**示例代码**：`oldInstanceCircuitBreakerCaller/consumer/main.go:263-295`

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

## 三种写法中 retCode 的转换链对比

| 步骤 | 装饰器（newCircuitBreakerCaller） | InvokeHandler（invokeHandlerCaller） | 旧版散装 Report |
|------|----------------------------------|--------------------------------------|-----------------|
| 业务调用结果 | customer func 返回 `(result, error)` | 业务代码自行判断 | 业务代码自行判断 |
| retCode 获取方式 | 自动：err != nil → `OnError(err)`；err == nil → `OnSuccess(result)` | 手动：调 `OnError(ResponseContext{Err: ...})` 或 `OnSuccess(ResponseContext{Result: ...})` | 手动：直接传字符串给 `ResourceStat.RetCode` |
| OnError 触发条件 | customer func `return nil, err` | 显式调用 `handler.OnError(...)` 且 `ResponseContext.Err != nil` | 无 OnError 概念 |
| OnSuccess 触发条件 | customer func `return result, nil` | 显式调用 `handler.OnSuccess(...)` | 无 OnSuccess 概念 |
| "-1" 哨兵含义 | "非 HTTP 状态码"，SDK 直接计为失败 | 同左 | 同左 |
| 是否自动上报 | ✅ 装饰器结束自动上报 | ❌ 需手动调用 OnSuccess/OnError | ❌ 需手动调用 Report |

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
3. 通过 Polaris HTTP API 自动创建 `Level=INSTANCE/SERVICE/METHOD` 各级熔断规则
4. 启动对应 consumer，并发请求触发熔断
5. 通过 `/switch` 翻转 provider 状态验证恢复链路
6. 退出时自动删除所创建的熔断规则并清理进程

共 10 个用例（详见 `test.md`）：`instance` / `service` / `interface` / `old_instance` /
`http_status` / `default_rule` / `modify_rule` / `protocol_method` / `pathtype` /
`all_dead_fallback`。其中用例 2/3 末尾各追加了 fallback 子阶段，通过 PUT 更新现有规则切换
`fallbackConfig.enable` 验证自定义降级响应：正向（`enable=true` 返回 `code=599`/`body`/`headers`）
和反向（`enable=false` 回退 503），无需创建独立规则。

> 提速说明：规则 `sleepWindow` 已从 12s 收紧到 6s，`--wait-half-open` 默认 8s、
> `--wait-rule-ready` 默认 5s，全量用例耗时显著下降。

### 基本用法

```bash
chmod +x verify_circuitbreaker.sh
./verify_circuitbreaker.sh                                              # 跑全部用例（默认 10 个）
./verify_circuitbreaker.sh --polaris-server 10.0.0.1                    # 指定北极星地址
./verify_circuitbreaker.sh --only instance                              # 仅跑实例级
./verify_circuitbreaker.sh --only service,interface,http_status         # 多个用例组合
./verify_circuitbreaker.sh --only default_rule                          # 仅跑默认规则兜底用例
./verify_circuitbreaker.sh --only protocol_method,pathtype              # 仅跑高级匹配维度用例
./verify_circuitbreaker.sh --only all_dead_fallback                    # 仅跑全死全活兜底用例
```

### 关键选项

| 参数                        | 说明                                                               |
|-----------------------------|--------------------------------------------------------------------|
| `--polaris-server <地址>`   | 北极星服务端地址（默认 `127.0.0.1`）                               |
| `--polaris-token <令牌>`    | 北极星鉴权令牌                                                     |
| `--namespace <ns>`          | 命名空间（默认 `default`）                                         |
| `--service <name>`          | 被调服务名（默认 `CircuitBreakerCallee`）                          |
| `--only <列表>`             | 仅运行指定用例：`instance`, `service`, `interface`, `old_instance`, `http_status`, `default_rule`, `modify_rule`, `protocol_method`, `pathtype`, `all_dead_fallback` |
| `--trigger-count <次数>`    | 触发阶段请求次数（默认 15，越大越容易触发熔断）                    |
| `--recovery-count <次数>`   | 验证 / 恢复阶段请求次数（默认 10）                                 |
| `--wait-half-open <秒数>`   | 服务级用例等待 sleepWindow 进入半开的秒数（默认 8，规则 sleepWindow=6s + 2s buffer）   |
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

## 主动探测验证 — `verify_faultdetect.sh`

`verify_circuitbreaker.sh` 验证的是**业务请求触发的被动熔断**；主动探测（Fault Detect /
Active Health Check）则是 SDK 在熔断打开后**周期性主动探活下游实例**、据此推动恢复的能力。
这条链路由独立脚本 `verify_faultdetect.sh` 验证，与上面的熔断脚本解耦、互不干扰。

### 验证的闭环

脚本覆盖 **SERVICE（服务级）/ METHOD（接口级）/ INSTANCE（实例级）三个 HTTP 探测级别 + TCP 探测 +
UDP 探测 + **协议/方法维度验证**共 **6 个用例**，各用独立 caller + 独立 consumer 端口（18095/18096/18097/18098/18099）+
**独立被调服务**（CircuitBreakerFDSvcCallee / FDMethodCallee / FDInstanceCallee / FDTcpCallee /
FDUdpCallee，provider 通过 `--service` 逗号分隔同时注册到五个服务）+ 独立规则名（`cb-fd-<LEVEL>-<caller>`）。
独立被调服务可避免被 verify_circuitbreaker.sh 残留的 `source=*/*` catch-all 规则按 id 字典序抢占
（详见 fault-detect.md）。

> **TCP/UDP 探测用例**：均为 SERVICE 级熔断规则（abort 型），探测规则 `protocol=TCP`(2)/`UDP`(3)，
> 探测打 provider-a 的独立探测端口（TCP=28091 / UDP=28101，`tcp_config`/`udp_config` 的 `send="ping"` +
> `receive="tcp-ok"`/`"udp-ok"` 内容匹配判健康）。因 `FaultDetectRule.port` 单值、同机两进程不能绑同一
> TCP/UDP 端口，TCP/UDP 探测端口仅 provider-a 启动，故只用单实例 provider-a（SERVICE 级闭环不依赖多实例）。
> 业务故障仍用 HTTP `/echo` 的 `openError` 触发熔断；探测故障用 `openTcpError`/`openUdpError` 让探测端口
> 不回包（维持 OPEN 阶段需业务 500 + 探测端口故障双重保证，因 SERVICE 级 half-open 会放行业务探活）。

SERVICE / METHOD 级闭环（都经 AcquirePermission，OPEN 时 abort）：
```
业务请求触发熔断 OPEN（abort）
  → provider 仍故障，主动探测 GET /echo 也失败 → 维持 OPEN（半开探测失败立即回 OPEN，abort）
  → 恢复 provider → 主动探测探活成功，推动 HALF_OPEN → CLOSE（ok）
```

INSTANCE 级闭环（不经 AcquirePermission，OPEN 实例被路由摘除、不 abort）：
```
仅 provider-b 故障 → 触发 b 实例熔断 OPEN
  → b 被服务路由层摘除，业务请求落到健康的 a（ok，不是 abort）
  → 恢复 provider-b → b 实例 half-open → close 重新可选（ok）
```

> **探测端点为何选 `/echo`**：provider 的 `/health` 固定返回 200，无法反映实例健康变化；
> 一旦熔断进入半开就会被它立即拉回 CLOSE，验证不出探测与恢复的因果。`/echo` 受 provider
> `needErr` 开关控制，挂时探测失败、恢复时探测成功，闭环可被稳定观察。

### 触发条件

主动探测的启动门控为「熔断规则 `faultDetectConfig.enable=true` 且存在匹配的 FaultDetectRule」。
三个级别用例各自下发一条熔断规则（顶层带 `faultDetectConfig.enable=true`，仅含 `CONSECUTIVE_ERROR`、
**不叠加 ERROR_RATE** 以避免恢复抖动）+ 一条 HTTP FaultDetectRule（探 `/echo`，`interval=2s`，`port=0`
表示使用被探测实例自身端口）。METHOD 级额外把熔断规则 `destination.method`、`BlockConfig.api.path`
与探测规则 `target_service.method` 都限定到 `/echo`。

### 基本用法

```bash
chmod +x verify_faultdetect.sh
POLARIS_SERVER=10.0.0.1 POLARIS_TOKEN=<token> ./verify_faultdetect.sh
POLARIS_SERVER=10.0.0.1 ./verify_faultdetect.sh --debug                 # 打开 SDK DEBUG 日志便于排查
RUN_FD_CASES=method,instance POLARIS_SERVER=10.0.0.1 ./verify_faultdetect.sh   # 只跑部分级别
```

### 关键参数（环境变量）

| 变量                     | 含义                                       | 默认值 |
| ------------------------ | ------------------------------------------ | ------ |
| `RUN_FD_CASES`           | 选跑用例子集（逗号分隔 service/method/instance/tcp/udp/proto_method），未选标 SKIP | service,method,instance,tcp,udp,proto_method |
| `FD_TRIGGER_COUNT`       | 触发熔断的 burst 次数                      | 30     |
| `FD_CONSECUTIVE_ERROR`   | 熔断规则连续错误阈值                       | 5      |
| `FD_SLEEP_WINDOW`        | 熔断 sleepWindow（秒），进入半开的等待窗口 | 6      |
| `FD_PROBE_INTERVAL`      | 主动探测间隔（秒）                         | 2      |
| `FD_PROBE_INTERVAL_UPDATED` | D 段 interval 热更新验证的目标探测间隔（秒） | 5  |
| `WAIT_KEEP_OPEN_SECONDS` | 维持 OPEN 阶段等待（> sleepWindow）        | 12     |
| `WAIT_RECOVER_SECONDS`   | 恢复阶段等待（sleepWindow + 多个探测周期） | 16     |

### 判定指标（按用例）

| 阶段 | SERVICE / METHOD / TCP / UDP（abort 型） | INSTANCE（ok 型，单实例故障） |
|------|------|------|
| 触发 | `abort >= 1`（熔断已 OPEN） | `fail >= 1`（b 被选中时 500，不要求 abort） |
| 维持 OPEN | `abort >= 1`（探测失败不恢复） | `ok >= N-1`（b 被摘除，业务落 a） |
| 探活 CLOSE | `ok == FD_VERIFY_COUNT` | `ok >= N-1`（b 恢复可选） |
| 日志佐证 | 探测调度=yes 且 状态切换=yes | 探测调度=yes 且 **INSTANCE 级** half-open→close=yes |

### D 段：规则动态启停与热更新验证

SERVICE 和 METHOD 用例在 A-C 段之后增加 D 段子步骤，验证规则变更是否实时生效，
**复用已有 consumer、不增加进程启动**：

| 用例 | D 段步骤 | 操作 | 日志铁证 |
|------|----------|------|----------|
| SERVICE | [7] 关闭探测 | PUT `faultDetectConfig.enable=false` | `[FaultDetect] health check ... is disabled, now stop` |
| SERVICE | [8] 重新开启 | PUT `faultDetectConfig.enable=true` | `schedule task` 行数增加 |
| METHOD | [7] 修改间隔 | PUT 探测规则 `interval=5s`（原 2s） | `schedule task.*interval=5s` 出现 |

> D 段失败不影响 A-C 段判定结果（但整体仍 FAIL，指标表单独标注）。

> TCP/UDP 用例与 SERVICE 同为 abort 型，判定指标完全一致；区别仅在探测协议（TCP/UDP）、探测端口
> （provider-a 的 28091/28101）与探测故障开关（`openTcpError`/`openUdpError`）。

> 日志佐证 grep 的是各 consumer 独立的 SDK 日志文件
> `.build/<consumer_name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log`（非 consumer stdout）。
> 探测调度铁证用 `[CircuitBreaker] schedule task`（探测周期任务真正注册），**不用**宽松的
> `[FaultDetect]`/`health check`（会被"is disabled, now stop the previous checker"关停日志误判）。

> 退出时：**熔断规则删除**；**探测规则保留不删**（FaultDetectRule 无 enable 字段，改为跨运行幂等复用，
> 下次存在则 PUT 更新复用）；停止 provider/consumer 进程。

### 协议/方法维度验证（proto_method）

用例 `proto_method` 验证 `selectFaultDetectRules` 对 `target_service.method` 的过滤正确性，
以及 `generateHttpRequest` 对 `http_config.headers` 的设置正确性。分三段，**复用已有 SERVICE/METHOD consumer，不新增进程**：

| 段 | 验证目标 | 操作 |
|----|---------|------|
| A | SERVICE 级 method 过滤 | 非通配 method 被拒绝、通配 method 被接受 |
| B | METHOD 级 method 过滤 + 闭环 | 仅匹配的 method 被选中，走完整 trigger→maintain→recover |
| C | HTTP headers 传递 | `X-Health-Probe=true` header 出现在探测请求中 |

> 段 A/C 复用 SERVICE consumer（18095），段 B 复用 METHOD consumer（18096）。

## 用例细节

- 被动熔断用例清单：[test.md](test.md)
- 主动探测验证说明：[fault-detect.md](fault-detect.md)

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

每个子目录可独立编译运行。以下是三种写法的启动步骤：

### 1. 装饰器写法（推荐，同时覆盖 SERVICE/METHOD/INSTANCE 三级）

```bash
# 终端 1：启动 provider-a（默认返回 200）
cd callee/provider-a && make run

# 终端 2：启动 provider-b（默认返回 500）
cd callee/provider-b && make run

# 终端 3：启动 consumer（端口 18080）
cd newCircuitBreakerCaller/consumer && make run

# 在北极星控制台创建熔断规则后：
curl http://127.0.0.1:18080/echo      # 正常请求，走装饰器自动 Check→Execute→Report
curl http://127.0.0.1:18080/order     # 不同接口，独立熔断统计
curl http://127.0.0.1:18080/info      # 同上
curl http://127.0.0.1:18080/slow      # 同上
```

### 2. InvokeHandler 手动编排写法

```bash
# 前提：provider-a 和 provider-b 已启动（同上）

# 终端 3：启动 consumer（端口 18081，和装饰器不冲突）
cd invokeHandlerCaller/consumer && make run

# 手动编排三阶段：AcquirePermission → 业务调用 → OnSuccess/OnError
curl http://127.0.0.1:18081/echo
```

### 3. 旧版散装写法（仅实例级）

```bash
# 前提：provider-a 和 provider-b 已启动

# 终端 3：启动 consumer（端口 18080，注意与装饰器端口冲突，不要同时启动）
cd oldInstanceCircuitBreakerCaller/consumer && make run

# 只有实例级上报，没有熔断检查
curl http://127.0.0.1:18080/echo
```

### 对比验证

同时启动装饰器写法（端口 18080）和 InvokeHandler 写法（端口 18081），对同一个被调服务发请求，观察：

```bash
# 观察装饰器行为：熔断打开后直接返回 abort，不会实际发请求
curl http://127.0.0.1:18080/echo

# 观察 InvokeHandler 行为：熔断打开后 AcquirePermission 返回 pass=false，同样拦截
curl http://127.0.0.1:18081/echo
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
