# Changelog

[TOC]

本项目所有重要的变更都必须记录在本文件中。

## [Unreleased]

### 添加的特性

#### 熔断（Circuit Breaking）

- **服务级 / 接口级熔断（`plugin/circuitbreaker/composite`）**：`composite`
  熔断器补齐服务（SERVICE）与接口（METHOD/INTERFACE）维度的规则匹配与状态
  机，新增 `rule_dictionary.go` 规则字典按资源级别索引规则、`default.go`
  默认熔断兜底，`breaker.go` / `counter.go` / `checker.go` 完成多级资源的
  统计、半开探活与状态流转。
- **熔断自定义降级响应（Fallback）**：规则命中 OPEN 时可按服务端配置注入
  自定义降级响应（HTTP 状态码 / Header / Body）。`counter.buildFallbackInfo`
  增加 `GetEnable()` 显式判断，避免 proto 零值 message 在 `enable=false`
  时误注入降级响应。
- **主动故障探测（Fault Detect）**：新增 `ResourceHealthChecker` 主动探测
  能力，OPEN 状态下按 `faultDetectConfig` 周期性探活，探活成功后驱动
  熔断器回到 CLOSE。支持 HTTP / TCP / UDP 三类探测器，并扩展协议 / 方法维度
  匹配（`targetService.api`）；探测上报走 `record=false`，避免探测流量自身
  扩充探测目标形成自循环。探测日志按「状态变化立即 INFO、连续失败 30s 定时
  INFO、连续成功静默」收敛，三类探测器 `lastErr` 加锁保证并发安全。
- **熔断事件上报与监控指标**：新增 `pkg/model/event/circuitbreaker.go`，
  在熔断状态转换（Open / HalfOpen / Close）时构造 `CircuitBreaker<Event>`
  事件上报到 EventReporter；`event_type` 采用 `"CircuitBreaker"+EventName`
  组合格式以对齐服务端 pushgateway 仅保留 `event_type` 的约束，规则名 / 资源
  类型 / 前后状态 / 错误率 / 慢调用阈值等打包进 `labels`。Prometheus
  Reporter 同步补齐熔断相关监控指标采集。
- **`examples/circuitbreaker/`**：完整重构为端到端验证套件，覆盖
  `newCircuitBreakerCaller` / `invokeHandlerCaller` 两种调用写法，含 10 个
  熔断用例（`verify_circuitbreaker.sh`）与 6 个主动探测用例
  （`verify_faultdetect.sh`），并提供 `mock_event_server.py` 校验事件上报、
  `test.md` / `fault-detect.md` 详尽用例文档（带源码行号引用）。

### 修复的 BUG

- **`ResourceHealthChecker` 主动探测两处阻断**：`checker.go` 补全 executor
  注入（修复 nil panic）与 `instanceExpireIntervalMill` 初始化；`rule.go`
  主动探测启动门控从 `fallbackConfig.enable` 修正为 `faultDetectConfig.enable`，
  并修复 `sortFaultDetectRules` 把规则 copy 到空 slice 导致丢数据的问题。
- **探测器日志 nil panic**：测试 / 未初始化场景下 `GetDetectLogger()` 返回
  nil，经 `ContextLogger` fallback 后触发 `nil.Logger.Debugf/Errorf` 的
  SIGSEGV。修复：HTTP / TCP / UDP 探测器统一加双层 nil guard。
- **`cleanInstances` 内存泄漏**：清理实例时同步清理
  `detectLastResult` / `detectLastReportTime` / `lastDetectErr`，避免残留。

### 兼容性说明

- **熔断 SERVICE / METHOD 级规则、Fallback、主动探测、事件上报 均为增量能力**，
  规则 `enable=false` 或服务端无下发时行为不变，存量仅使用实例级熔断的用户
  无感知。
- **`pkg/model/circuitbreaker.go` / `pkg/model/service.go` 结构体扩展字段**：
  均为新增字段，不改变既有字段语义，自定义 `CircuitBreaker` 实现需关注新增的
  Fallback / FaultDetect 相关接口方法。


## [v1.7.1-rc3] - 2026-06-11

### 添加的特性

#### 限流（Rate Limiting）

- **`plugin/ratelimiter/reject_concurrency`**：新增并发数限流插件，纯本地原子
  计数实现。规则 `resource=CONCURRENCY` 时由
  `pkg/flow/quota/window.go::resolveRateLimiterName` 强制路由到该插件，
  不受 `Rule.Action` 影响；`maxAmount<=0` 时保底为 1 并打印 Warn。
- **`QuotaResponse.AddRelease` 回调链**：合并多 window 的 release 函数后由
  `QuotaFutureImpl.Release()` 统一触发；幂等执行后清空回调链。并发数限流场景
  业务侧 `defer future.Release()` 即可正确归还配额。
- **`CALLER_METADATA` 维度**：新增 `model.ArgumentTypeCallerMetadata` +
  `BuildCallerMetadataArgument(key, value)`；
  `pkg/flow/data/object.toSpecArgument` / `pkg/flow/quota/assist.go`
  （`getLabelValue` / `getLabelEntry`）扩展识别 `CALLER_METADATA`。
- **统一 ratelimit logger**：`reject` / `reject_concurrency` / `unirate`
  三个限流插件均使用 `RateLimitLogger`，bucket 创建走 INFO（带 windowKey
  + ruleId + 关键阈值），hot path（passed/limited/queued/released）走 DEBUG +
  `IsLevelEnabled` 闸门。
- **`plugin/ratelimiter/common`**：抽出公共工具
  `RuleID` / `FormatRuleSummary` / `FormatCluster`，三个限流插件共享，避免
  byte-identical 副本（每个 ~50 行）扩散；`reject_concurrency.RuleID`
  顺带补上 `nil` 防御。
- **`QuotaResponse.Info` 协议化**：LIMITED 路径统一为
  `<resource>:<amount>/<duration>` 格式（如 `QPS:10/1s`），让运维 / 监控可
  解析；OK 路径新增三态语义 `Disabled` / `RuleNotExists` / `QuotaGranted`。
- **`QuotaResponse.WaitMs` 暴露**：`QuotaFutureImpl.Get()` 不再清零
  `WaitMs`；SDK 仍替业务 sleep（行为不变），业务可读真实排队时长用于观测/上报。
- **远端 init/acquire 错误日志限频**：`pkg/flow/quota/window.go` /
  `window_async.go` 新增 5s 限频窗口（`shouldLogRemoteErr`），FAILOVER_LOCAL
  退化时不再每 10ms 一行 Errorf（之前每分钟 600+ 条）；首次必打、
  跨过 5s 后带 `(suppressed N similar errors in last 5s)` 提示。

#### 泳道路由（Lane Router）

- **`plugin/servicerouter/lane`**：支持染色标签 `service-lane` 直接路由与
  `TrafficMatchRule` 流量染色，覆盖 `$method` / `Header` / `Query` / `Cookie` /
  `$path` / `$caller_ip` 六类匹配维度；支持 `STRICT` / `PERMISSIVE` 匹配模式与
  `baseLaneMode` 基线选取策略（`OnlyUntaggedInstance` / `ExcludeEnabledLaneInstance`）。
- **caller+callee 泳道规则合并**：同时拉取主被调侧规则并以 caller 优先去重，规避
  Polaris Server naming cache 按服务独立刷新的滞后问题。
- **`beforeChain` 路由链**：`consumer.serviceRouter.beforeChain` 用于在主路由链之前
  执行泳道路由，跨链同名插件自动去重。
- **`isEntry` 门禁首次染色（对齐 polaris-java）**：首次流量（无 stain label）
  只有当调用方是该泳道组的 `entries` 指定的流量入口时，才执行 percentage/warmup
  染色决策并写 stain label；非入口的中间服务直接走基线，避免误染色进泳道。
- **完整 stain label 透出（`InstancesResponse.RouteMetadata`）**：泳道路由决策
  成功后把完整格式 `{groupName}/{ruleName}` 写入 `RouteInfo.RouteMetadata`，
  引擎层在构建 `InstancesResponse` 时拷贝到 `RouteMetadata["service-lane"]`，
  业务网关/consumer 从中读出后以 HTTP Header 透传给下游 —— 下游 SDK 按精确匹配
  (`stainLabelIndex`) 直接命中，消除短格式 `gray` 在多同名 `defaultLabelValue`
  泳道组共存时的歧义。对齐 polaris-java `MessageMetadataContainer` +
  `TransitiveType.PASS_THROUGH` 的语义。

#### 服务鉴权（Authenticator）

- **`plugin/authenticator/blockallowlist`**：新增黑白名单鉴权插件，对齐
  polaris-java `BlockAllowListAuthenticator` 的 `checkAllow` 9 条语义
  （仅白名单/仅黑名单/混合策略），支持 `HEADER` / `QUERY` / `CALLER_SERVICE` /
  `CALLER_IP` / `CALLER_METADATA` / `CUSTOM` 六维 `MatchArgument` 匹配。
- **`AuthAPI` + 5 个工厂函数**：新增顶层入口 `NewAuthAPI` /
  `NewAuthAPIByConfig` / `NewAuthAPIByContext` / `NewAuthAPIByFile` /
  `NewAuthAPIByAddress`，对应 `AuthenticateRequest` / `AuthenticateResponse`。
- **`provider.auth` 配置段**：`enable` + `chain` + `plugin` 三字段，默认
  `enable=false`，未启用时 `SyncAuthenticate` 直接放行，对存量用户零开销。

#### 日志体系

- 新增三个独立日志通道：
  - `event/polaris-event.log`（`EventLogger`）
  - `route/polaris-route.log`（`RouteLogger`）
  - `ratelimit/polaris-ratelimit.log`（`RateLimitLogger`）
  - 对应 `api.SetXxxLogger` / `api.GetXxxLogger` / `api.ConfigXxxLogger`
    对外接口，`api.ConfigLoggers` 同步覆盖。

#### 示例与端到端验证

- **`examples/route/lane/`**：5 组件完整示例（provider / consumer / gateway /
  simple-consumer / simple-gateway），覆盖 `ProcessRouters` × `GetOneInstance`
  的 4 种链路组合。
- **`examples/route/{metadata,nearby,rule}/simple-consumer`**：基于
  `GetOneInstance` 的简化消费端，与已有 `consumer/`（手动三段式）形成对照。
- **`examples/loadbalancer/`**：按算法拆分子目录（`hash` / `ringhash` /
  `maglev` / `weightedRandom` / `l5cst`），补齐 `verify_weight.sh` 分布校验脚本。
- **`examples/ratelimit/`**：完整重构为端到端验证套件，含 6 个用例组（共
  18 个用例）：
  - `1.x` QPS reject（窗口内触发限流 / 新窗口再次生效）
  - `2.x` QPS unirate（匀速排队 / 队列丢弃 / 新窗口再次生效）
  - `3.x` 并发数限流（触发 / Release 后再次限流 / 低于上限放通）
  - `4.x` 自定义多维匹配（HEADER+QUERY+METHOD+CALLER_*，AND 关系）
  - `5.x` `regex_combine` 开关（false 各 path 独享 / true 多 path 共享）
  - `6.x` 分布式 GLOBAL 限流（多窗口聚合判定 / 跨窗口持续生效 / 多实例共享配额 /
    GLOBAL+regex_combine / FAILOVER_LOCAL 退化）
- **端到端验证脚本**：`lane-test.sh` / `lane-warmup-test.sh` + 3 个
  `verify_*_route.sh` + `verify_ratelimit.sh`；聚合入口 `run_all_tests.sh` /
  `cleanup_all.sh`。

#### 负载均衡单测

- 为 `weightedrandom`（含 `dynamic` 权重预热适配）/ `ringhash` / `maglev` /
  `hash` 四类负载均衡插件补齐表驱动单元测试，覆盖率显著提升。

### 修复的 BUG

- **`beforeChain` 触发 FilterOnly 兜底**：前置链尾部追加的 `FilterOnlyRouter`
  会置位 `IgnoreFilterOnlyOnEndChain`，导致主链（规则/就近/元数据路由）被跳过。
  修复：新增 `GetFilterClusterBefore` 入口，前置链不再追加 FilterOnly。
- **`convert()` 污染调用方 `SourceService.Metadata`**：原实现向用户 map 写入
  `$header.*` / `$query.*` 键，跨请求复用时遗留上一次数据。修复：改为 copy-on-write。
- **Prometheus `ReportStat` 熔断分支 nil-check 误判**：原代码在处理
  `CircuitBreakStat` 时错误地检查 `rateLimitCollector`，改为
  `circuitBreakerCollector`。
- **Prometheus 显式 `PortStr` 被覆盖**：`Config.SetDefault` 将 `PortStr` 解析
  写入 `port` 的逻辑放到了默认分支之外，导致显式端口被覆盖为默认值。
- **`zeroprotect` 日志 ns/svc 参数顺序颠倒**：搭车修复。
- **`provider-qps`/`provider-concurrency/polaris.yaml` `limiterNamespace`/
  `limiterService` 缩进错位**：原写在 `provider:` 顶层，YAML decode 时被忽略，
  `--limiter-service` 始终不生效。修复：嵌套到 `provider.rateLimit:` 下。

### 兼容性说明

本版本对存量用户保持完全向后兼容：

- **`laneRouter` / `auth` / 限流增强 均为新能力**，默认开关均为 `false`
  （或 `Enable()` 在无规则时返回 `false`），原有路由链 / 鉴权 / 限流行为不变。
- **`RouteInfo.EnvironmentVariables` 新增 Arguments 前缀 key**（`$header.*` /
  `$query.*` / `$cookie.*` / `$method` / `$caller_ip` / `$Path`）写入逻辑，
  此字段此前仅由 `rulebase` 内部使用，外部调用方不受影响。
- **`HasLimitedInstances` 机制、`RuleBasedRouter` / `NearbyBasedRouter` /
  `DstMetaRouter`** 等现有插件的路由行为均未改动，兄弟插件改动仅限日志通道
  迁移到 `RouteLogger`。
- **`QuotaResponse.WaitMs` 字段语义升级**：从"调用 `Get()` 后强制清零"改为
  "保留实际等待过的毫秒数"。SDK 仍在 `Get()` 内部 sleep，业务**不要**再次
  sleep；该字段仅作观测 / 指标上报用。
- **`QuotaResponse.Info` 字段格式微调**：LIMITED 路径统一为
  `<resource>:<amount>/<duration>`，OK 路径新增 `quota granted`。
  此字段历史上仅用于日志展示（外部 examples 也只 print/header），SDK 内部不
  做 `Info ==` 等值比较，不影响调用语义。


## [v1.7.1-rc2] - 2026-05-21

### 添加的特性

#### 无损上下线 & 权重预热（Lossless / Warmup）

- **无损上线**：新增 `plugin/lossless/losslessController` 插件与
  `ProviderAPI.LosslessRegister(...)` API。支持两种延迟注册策略：
  - `DELAY_BY_TIME`：按固定时长延迟上线（`delayRegisterInterval`，默认 30s）
  - `DELAY_BY_HEALTH_CHECK`：探测业务端就绪接口通过后再上线（`healthCheckInterval`，默认 5s）
- **无损下线**：通过 Admin HTTP Server（默认 `0.0.0.0:28080`）暴露
  `/offline` 端点，先反注册 + 等待实例从注册中心摘除，再退出业务进程，避免流量丢失。
- **权重预热（Warmup）**：新增 `plugin/weightadjuster/warmup`，在 `WeightedRandom`
  负载均衡器内按实例启动时间执行 logistic/linear/square 曲线的权重爬坡，避免新实例
  刚上线被打满。同步废弃 `plugin/weightadjuster/ratedelay`。
- **`provider.lossless` / `global.admin` / `consumer.weightAdjust` 配置段**：
  新增 `pkg/config/lossless.go` / `pkg/config/admin.go` / `pkg/config/weight_adjust.go`
  及默认值；`polaris.yaml` 同步追加注释化的默认模板。
- **Admin HTTP Server 插件**：`plugin/admin/httpServer` 提供统一的 SDK 运维
  HTTP 入口，允许其它插件通过 `AdminConfig.RegisterPath` 注册自定义路径。
- **Prometheus Pull 共享 Admin 端口**：Prometheus Pull 模式下，若
  `metrics.port == admin.port`，则 `/metrics` 路径自动注册到 Admin Server，
  避免额外监听端口。
- **`lossless/polaris-lossless.log` 日志通道**：新增 `LosslessLogger` 隔离
  无损上下线相关日志输出。
- **`examples/lossless/`**：`consumer` + `provider/timeDelay` +
  `provider/healthCheckDelay`，配合 `verify_offline.sh` /
  `verify_overload_protection.sh` / `verify_readiness.sh` /
  `verify_warmup.sh` 提供端到端回归脚本。

### 修复的 BUG

- **`fix(network)`**：未注册 `ClusterType` 的连接不强制 `lazyClose`。

### 兼容性说明

- **`LosslessRegister` / `warmup` / `admin` 均为新能力**，默认开关均为
  `false`（或 `Enable()` 在无规则时返回 `false`），原有 `Register` /
  `RegisterInstance` / 负载均衡行为不变。
- **`ProviderAPI` 接口新增 `LosslessRegister` 方法**：自定义 `ProviderAPI`
  实现（如测试 mock）需补实现该方法，SDK 内部实现已同步覆盖。
- **废弃 `weightadjuster/ratedelay`**：由 `weightadjuster/warmup` 取代；未启用
  `consumer.weightAdjust` 的用户不受影响。


## [v1.7.1-rc1] - 2026-03-27

### 添加的特性

- **支持服务治理规则拉取接口**：新增按规则类型批量拉取治理规则的接口。
- **配置中心优化（`config-uptimize`）**：配置加载与缓存路径优化。
- **支持日志上下文（`log-with-ctx`）**：日志输出可携带 trace context。

### 修复的 BUG

- **无损上下线规则获取适配服务端新字段**：服务端字段调整后客户端同步更新解析逻辑。


## [v1.7.0] - 2026-03-02

### 添加的特性

- **配置中心支持配置变更事件上报**：`ConfigFile` 变更可触发事件上报到监控/审计通道。
- **支持从缓存文件加载附近路由规则**：SDK 启动期间允许从本地缓存读取
  `nearby` 路由规则，降低对服务端的强依赖。
- **`ConfigAPI` 支持返回配置版本**：`ConfigFile` 增加版本号字段供业务侧做幂等控制。
- **支持默认实例熔断器**：当未匹配到具体熔断规则时，按默认实例策略熔断。
- **附近路由规则 v2 版本支持**：兼容服务端下发的 v2 协议规则。

### 修复的 BUG

- **文件配置持久化字段空值校验**：避免持久化空字段写坏文件。
- **熔断器多处 `panic` 修复**：包括 `CircuitBreaker.doReport` 在内的若干 race / nil 路径。
- **附近规则缓存刷新问题**。
- **Go 接口陷阱（underlying value is nil）修复**：消除典型的 `interface holds nil concrete value` 陷阱。
- **限流获取配额 `panic` 修复**。


## [v1.6.1-1] - 2025-12-23

> 将 `v1.6.1.1` 更名为 `v1.6.1-1`，以符合 go mod 规范。

### 修复的 BUG

- **`CircuitBreakerAPI.Report` `stat` 参数不应为 nil**：API 入参合法性补强。
- **Go 接口陷阱（underlying value is nil）修复**：与 v1.7.0 同步的 backport。


## [v1.6.1] - 2025-02-11（pre-release）

### 修复的 BUG

- **修复分布式限流场景下 RPC 连接泄漏的问题**。
- **修复 watch 泄漏问题**。
- **移除未使用的文件**。


## [v1.6.0] - 2025-01-21（pre-release）

### 添加的特性

- **新版本熔断规则**：支持服务端下发的新版熔断规则结构。
- **配置文件支持获取标签信息**：`ConfigFile` 增加 metadata/labels 读取。
- **支持用户自定义 SDK 标签**：`global.system.metadata` 可由用户写入。
- **连接 polaris 获取自身 IP 增加超时控制**：避免启动期 hang 在网络探测上。
- **禁用缓存持久化时不自动创建目录；提供禁用配置中心的 API**。
- **支持文件配置持久化**：`ConfigFile` 可落盘后跨重启恢复。

### 修复的 BUG

- **支持值 NotEqual 标签逻辑**。
- **修复熔断规则 `atomic.Value` 使用 panic 问题**。
- **修复 `LoadBalance` 无法选择处于半开的实例**。
- **修复客户端鉴权接口设置**（含配置中心客户端）。
- **修复 `RegisterInstance` 没有设置 `AutoHeartbeat`**。
- **修复监控数据上报无法清理数据问题**。
- **`push` 上报模式支持自动注销**。
- **修复 demo 配置不正确问题**。
- **修复 `config_flow` 递归加读锁导致的死锁问题**。
- **修正路由规则匹配目标服务时对星号的处理问题**。
- **修复服务列表监听可能导致 OOM 的问题**。
- **修复熔断器 `panic`**。
- **修复路由匹配失败默认转为返回全部实例**。


## [v1.5.9] - 2025-02-11

> 基于 `release-v1.5.x` 维护分支发布。

### 修复的 BUG

- **修复分布式限流场景下 RPC 连接泄漏的问题**：从主线 cherry-pick 回 1.5.x
  维护分支。


## [v1.5.8] - 2024-08-26

> 基于 `release-v1.5.x` 维护分支发布。

### 修复的 BUG

- **修复路由匹配失败默认转为返回全部实例的问题**。


## [0.9.0] - 2021-5-7

### 添加的特性

- 支持限流新版本控制台全部特性（规则配置集群，单机均摊模式）
- 支持客户端初始化接口
- 支持本地限流数据上报

### 修改的特性

- 服务发现：服务拉取超时策略的变更

### 修复的BUG

- 修复了健康检查关联到consumer的BUG
- 修复了规则路由可能引起的panic问题
