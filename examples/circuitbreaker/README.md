# examples/circuitbreaker — 北极星（Polaris）故障熔断 Demo 集合

本目录提供 polaris-go SDK **三种熔断级别**（实例级 / 服务级 / 接口级）+ **三种编程模型**（装饰器 / InvokeHandler / 散装 Report）的端到端示例，并提供一键 E2E 测试脚本与清理脚本，方便快速验证新版 SDK 是否符合预期。

## 目录结构

```
examples/circuitbreaker/
├── callee/                              # 共用 Provider
│   ├── provider-a/                      #   /echo 默认返回 200，受 /switch?openError 控制
│   └── provider-b/                      #   /echo 默认返回 500，受 /switch?openError 控制
│   说明：两个 provider 都暴露 /switch?openError=true|false 用于运行时切换返回码
│         同时暴露 /order /info /slow 三个端点，配合 newCircuitBreakerCaller 验证多接口熔断
│         provider-a 额外暴露 TCP 探测端口 28091 / UDP 探测端口 28101（主动探测专用）
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
├── verify_circuitbreaker.sh             # 【主入口】被动熔断一键 E2E 测试脚本（10 个用例）
├── verify_faultdetect.sh                # 主动探测（fault detect）端到端验证脚本（6 个用例）
├── cleanup.sh                           # 进程与目录清理脚本
├── test.md                              # 被动熔断用例清单与验证原理（详细）
├── fault-detect.md                      # 主动探测用例清单与验证原理（详细）
└── README.md                            # 本文件
```

## 文档导航

- **本文（README.md）**：目录架构、demo 编写规范、验证脚本入口说明
- **[test.md](test.md)**：`verify_circuitbreaker.sh` 的全部 10 个熔断用例（Caller 写法 / 规则 / 验证步骤 / 通过条件 / 验证原理）
- **[fault-detect.md](fault-detect.md)**：`verify_faultdetect.sh` 的全部 6 个主动探测用例（同上结构）

## 前置条件

1. 已运行的北极星服务端（Polaris Server）：
   - gRPC: `<polaris-server>:8091`
   - HTTP 管控: `<polaris-server>:8090`
2. 本地安装 **Go**（与 `examples/circuitbreaker/*/go.mod` 兼容版本）、**python3**、`curl`、`lsof`
3. 子目录的 `go.mod` 已通过 `replace github.com/polarismesh/polaris-go => ../../../../` 指向本地源码

---

## 三种编程模型

polaris-go SDK 提供三种接入熔断的方式，按推荐程度排序。

### 一、装饰器写法（`newCircuitBreakerCaller`）—— 【主推】

通过 `MakeFunctionDecorator` 将"熔断检查 → 业务调用 → 熔断上报"封装为**一个 `DecoratorFunction`**。

**快速上手**：

```go
// 1. 定义 CodeConvert
type customCodeConvert struct{}
func (c *customCodeConvert) OnSuccess(val interface{}) string { return strconv.Itoa(resp.code) }
func (c *customCodeConvert) OnError(err error) string          { return "-1" }

// 2. 构造装饰器
decorator := svr.circuitBreaker.MakeFunctionDecorator(
    func(ctx context.Context, args interface{}) (interface{}, error) {
        instance, _ := svr.discoverInstance()
        model.GetInvokeContext(ctx).SetInstance(instance) // 触发实例级上报
        resp, err := http.Get(url)
        if err != nil || resp.StatusCode >= 500 {
            return nil, fmt.Errorf("upstream returned %d", resp.StatusCode) // → OnError → retCode="-1"
        }
        return commonResponse{code: resp.StatusCode, body: string(data)}, nil // → OnSuccess → 透传码
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

// 3. 调用
ret, abort, err := decorator(context.Background(), nil)
if abort != nil {
    // 被熔断拦截
    if abort.HasFallback() {
        rw.WriteHeader(abort.GetFallbackCode())
        rw.Write([]byte(abort.GetFallbackBody()))
    } else {
        rw.WriteHeader(http.StatusServiceUnavailable)
    }
    return
}
```

**生命周期（完全自动）**：

```
DecoratorFunction(ctx, nil)
  ├─ [自动] Check          ← 服务级 → 接口级 短路检查；熔断打开时返回 abort != nil
  ├─ [自动] customer func  ← 用户业务逻辑
  └─ [自动] Report         ← 按 ServiceResource → MethodResource → InstanceResource 三级上报
```

**覆盖级别**：Method 非空 + SetInstance → 服务级 + 接口级 + 实例级三级全覆盖。

**适用场景**：标准同步调用（HTTP RPC / gRPC Unary）、需要多级熔断覆盖、新项目开发。

### 二、InvokeHandler 手动编排写法（`invokeHandlerCaller`）

暴露 `AcquirePermission / OnSuccess / OnError` 三个生命周期钩子，调用方手动编排。

```go
// 1. 创建 handler（一次）
handler := svr.circuitBreaker.MakeInvokeHandler(&api.RequestContext{...})

// 2. 每次请求手动编排
pass, aborted, err := handler.AcquirePermission()
if !pass { /* 熔断拦截 */ }

// 业务调用
resp, err := http.Get(url)

// 手动上报
if err != nil {
    handler.OnError(&model.ResponseContext{Err: err, Instance: instance})
} else {
    handler.OnSuccess(&model.ResponseContext{Result: resp, Instance: instance})
}
```

**适用场景**：gRPC interceptor、自定义中间件、异步调用模型、需先 Check 再决定是否发请求。

### 三、旧版散装写法（`oldInstanceCircuitBreakerCaller`）—— 保留兼容

直接调用 `CircuitBreakerAPI.Report(InstanceResource)` 上报熔断结果 + `ConsumerAPI.UpdateServiceCallResult` 上报指标。

**核心差异**：无熔断检查（不会拦截请求），仅做上报统计。**仅覆盖实例级熔断**。

**适用场景**：存量代码零改动兼容；新项目不建议使用。

### 选型速查

| 你的场景 | 推荐写法 | 目录 |
|---|---|---|
| 标准 HTTP / gRPC 同步调用 | **装饰器** | `newCircuitBreakerCaller` |
| gRPC interceptor / 自定义 middleware | **InvokeHandler** | `invokeHandlerCaller` |
| 异步调用（MQ 消费 / 定时任务） | **InvokeHandler** | `invokeHandlerCaller` |
| 存量代码兼容（仅实例级） | 旧版 Report | `oldInstanceCircuitBreakerCaller` |
| 新项目 / 重构 | **装饰器** | `newCircuitBreakerCaller` |

---

## 5xx vs 4xx 的分流约定

熔断判定看的是 `retCode`，由 demo 按 HTTP 状态码分流决定走哪个路径：

```
customer func 返回 (result, err)
       │
       ├─ err != nil  ──→ OnError(err) → retCode="-1" → SDK 哨兵直接判 RetFail（不走 RET_CODE 匹配）
       │
       └─ err == nil  ──→ OnSuccess(result) → retCode=真实状态码 → 用规则 ErrorCondition 匹配
```

**约定**：

| 场景 | customer func 返回 | retCode | 熔断效果 |
|------|-------------------|---------|---------|
| HTTP 5xx | `return nil, fmt.Errorf(...)` | `"-1"` | SDK 哨兵直接计失败 |
| 网络错（连接失败） | `return nil, err` | `"-1"` | SDK 哨兵直接计失败 |
| HTTP 4xx | `return (commonResponse{code:403}, nil)` | `"403"` | 不命中 `RANGE 500~599`，不计入熔断 |
| HTTP 2xx | `return (commonResponse{code:200}, nil)` | `"200"` | 不计入熔断 |

> **retCode="-1" 哨兵**：`parseRetStatus`（`block_counter.go:124`）对 `"-1"` 直接判 `RetFail`
> 而不经 MatchString，因此 5xx 和网络错都能可靠触发熔断。

---

## 熔断验证 — `verify_circuitbreaker.sh`

一键 E2E 测试脚本，覆盖**被动熔断**的完整链路（业务请求触发 OPEN → 半开 → 恢复）。

**共 10 个用例**：`instance` / `service` / `interface` / `old_instance` /
`http_status` / `default_rule` / `modify_rule` / `protocol_method` / `pathtype` /
`all_dead_fallback`。

覆盖三级熔断（INSTANCE/SERVICE/METHOD）、三种编程模型（Decorator/InvokeHandler/Bare Report）、
HTTP 状态码区分、默认规则兜底、规则热更新、协议/方法/路径多维度匹配、全死全活兜底策略、
自定义降级响应（fallback）等核心能力。

> 用例详细说明见 **[test.md](test.md)**，含每个用例的 Caller 写法、规则配置、验证步骤、
> 通过条件、验证原理（带源码行号引用）和失败诊断。

### 基本用法

```bash
chmod +x verify_circuitbreaker.sh
./verify_circuitbreaker.sh                                              # 跑全部用例
./verify_circuitbreaker.sh --polaris-server 10.0.0.1                    # 指定北极星地址
./verify_circuitbreaker.sh --only instance                              # 仅跑实例级
./verify_circuitbreaker.sh --only service,interface,http_status         # 多个用例组合
```

### 关键选项

| 参数 | 说明 |
|------|------|
| `--polaris-server <地址>` | 北极星服务端地址（默认 `127.0.0.1`） |
| `--polaris-token <令牌>` | 北极星鉴权令牌 |
| `--only <列表>` | 仅运行指定用例，逗号分隔（可用值见上方用例列表） |
| `--trigger-count <次数>` | 触发阶段请求次数（默认 15） |
| `--wait-half-open <秒数>` | 等待 sleepWindow 进入半开的秒数（默认 8） |
| `--debug` | 开启 Polaris SDK DEBUG 日志 |

### 退出码
- `0` = 全部 PASS
- `>0` = FAIL 数量

### 日志位置
- 主日志：`examples/circuitbreaker/.logs/verify_circuitbreaker-<时间戳>.log`
- Provider：`.logs/provider_a.log` / `provider_b.log`
- Consumer：`.logs/<name>_consumer.log`
- SDK 内部日志：`.build/<name>_run/polaris/log/circuitbreaker/polaris-circuitbreaker.log`

---

## 主动探测验证 — `verify_faultdetect.sh`

独立脚本，验证 **SDK 在熔断打开后周期性主动探活下游实例并据此恢复** 的能力。

**共 6 个用例**：SERVICE 级 HTTP / METHOD 级 HTTP / INSTANCE 级 HTTP / TCP 探测 / UDP 探测 /
协议方法维度 + Headers。

覆盖三级（SERVICE/METHOD/INSTANCE）探测闭环、TCP/UDP 协议探测、`faultDetectConfig.enable`
门控 toggle 启停、探测规则参数热更新（interval）、`selectFaultDetectRules` method 过滤、
`generateHttpRequest` headers 传递等能力。

> 用例详细说明见 **[fault-detect.md](fault-detect.md)**，含每个用例的 Caller 写法、规则配置、
> 验证步骤、通过条件、验证原理（带源码行号引用）和失败诊断。

### 基本用法

```bash
chmod +x verify_faultdetect.sh
POLARIS_SERVER=10.0.0.1 POLARIS_TOKEN=<token> ./verify_faultdetect.sh
POLARIS_SERVER=10.0.0.1 ./verify_faultdetect.sh --debug                 # 开启 SDK DEBUG 日志
RUN_FD_CASES=method,instance POLARIS_SERVER=10.0.0.1 ./verify_faultdetect.sh   # 只跑部分用例
```

### 关键参数（环境变量）

| 变量 | 含义 | 默认值 |
|------|------|--------|
| `RUN_FD_CASES` | 选跑用例子集（逗号分隔：`service,method,instance,tcp,udp,proto_method`） | 全部 |
| `FD_TRIGGER_COUNT` | 触发熔断的 burst 次数 | 30 |
| `FD_CONSECUTIVE_ERROR` | 熔断规则连续错误阈值 | 5 |
| `FD_SLEEP_WINDOW` | 熔断 sleepWindow（秒） | 6 |
| `FD_PROBE_INTERVAL` | 主动探测间隔（秒） | 2 |
| `WAIT_KEEP_OPEN_SECONDS` | 维持 OPEN 阶段等待（> sleepWindow） | 12 |
| `WAIT_RECOVER_SECONDS` | 恢复阶段等待 | 16 |

---

## 一键清理 — `cleanup.sh`

```bash
chmod +x cleanup.sh
./cleanup.sh             # 交互式（先展示再确认）
./cleanup.sh -f          # 强制清理，不询问
./cleanup.sh --dry-run   # 仅展示残留项，不执行清理
```

清理范围：按端口匹配并终止 provider/consumer 进程，删除 `.build/` 与 `.logs/` 目录。

> 熔断规则在脚本退出时已自动删除，`cleanup.sh` 不会再删规则。若脚本被 `kill -9` 强制终止
> 导致规则未清理，需手动登录北极星控制台搜索 `cb-instance-*` / `cb-service-*` / `cb-interface-*` 删除。

---

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

1. **用例编号严格顺序递增**：脚本中步骤编号与文档编号一致
2. **文档同步**：修改验证脚本后须同步更新 `test.md` 和/或 `fault-detect.md`
3. **清理同步**：新增进程/端口/产物须同步更新 `cleanup.sh`
4. **验证脚本变更优先改脚本**：`.build/` 下生成文件由脚本 heredoc 每次重写，不要直接改
