# Changelog

[TOC]

本项目所有重要的变更都必须记录在本文件中。

## [Unreleased]

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
  避免额外监听端口。同步修复 `ReportStat` 中 `rateLimitCollector` 误判熔断指标
  collector 的 BUG。

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
  - `lossless/polaris-lossless.log`（`LosslessLogger`）
  - `route/polaris-route.log`（`RouteLogger`）
  - 对应 `api.SetXxxLogger` / `api.GetXxxLogger` / `api.ConfigXxxLogger`
  对外接口，`api.ConfigLoggers` 同步覆盖。

#### 示例与端到端验证

- **`examples/route/lane/`**：5 组件完整示例（provider / consumer / gateway /
  simple-consumer / simple-gateway），覆盖 `ProcessRouters` × `GetOneInstance`
  的 4 种链路组合。
- **`examples/route/{metadata,nearby,rule}/simple-consumer`**：基于
  `GetOneInstance` 的简化消费端，与已有 `consumer/`（手动三段式）形成对照。
- **`examples/lossless/`**：`consumer` + `provider/timeDelay` +
  `provider/healthCheckDelay`，配合 `verify_offline.sh` /
  `verify_overload_protection.sh` / `verify_readiness.sh` /
  `verify_warmup.sh` 提供端到端回归脚本。
- **`examples/loadbalancer/`**：按算法拆分子目录（`hash` / `ringhash` /
  `maglev` / `weightedRandom` / `l5cst`），补齐 `verify_weight.sh` 分布校验脚本。
- **端到端验证脚本**：`lane-test.sh` / `lane-warmup-test.sh` + 3 个
  `verify_*_route.sh`；聚合入口 `run_all_tests.sh` / `cleanup_all.sh`。

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

### 兼容性说明

本版本对存量用户保持完全向后兼容：

- **`LosslessRegister` / `laneRouter` / `warmup` / `admin` 均为新能力**，默认开关
  均为 `false`（或 `Enable()` 在无规则时返回 `false`），原有 `Register` /
  `RegisterInstance` / 路由链 / 负载均衡行为不变。
- **`ProviderAPI` 接口新增 `LosslessRegister` 方法**：自定义 `ProviderAPI`
  实现（如测试 mock）需补实现该方法，SDK 内部实现已同步覆盖。
- **`RouteInfo.EnvironmentVariables` 新增 Arguments 前缀 key** （`$header.*` /
  `$query.*` / `$cookie.*` / `$method` / `$caller_ip` / `$Path`）写入逻辑，
  此字段此前仅由 `rulebase` 内部使用，外部调用方不受影响。
- **`HasLimitedInstances` 机制、`RuleBasedRouter` / `NearbyBasedRouter` /
  `DstMetaRouter`** 等现有插件的路由行为均未改动，兄弟插件改动仅限日志通道
  迁移到 `RouteLogger`。
- **废弃 `weightadjuster/ratedelay`**：由 `weightadjuster/warmup` 取代；未启用
  `consumer.weightAdjust` 的用户不受影响。


## [0.9.0] - 2021-5-7

### 添加的特性

- 支持限流新版本控制台全部特性（规则配置集群，单机均摊模式）
- 支持客户端初始化接口
- 支持本地限流数据上报

### 修改的特性

- 服务发现：服务拉取超时策略的变更

### 修复的BUG
