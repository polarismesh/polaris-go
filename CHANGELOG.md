# Changelog

[TOC]

本项目所有重要的变更都必须记录在本文件中。

## [Unreleased]

### 添加的特性

- **泳道路由（Lane Router）**：新增 `plugin/servicerouter/lane`，支持染色标签
  `service-lane` 直接路由与 `TrafficMatchRule` 流量染色，覆盖 `$method` / `Header` /
  `Query` / `Cookie` / `$path` / `$caller_ip` 六类匹配维度；支持 `STRICT` / `PERMISSIVE`
  匹配模式与 `baseLaneMode` 基线选取策略（`OnlyUntaggedInstance` / `ExcludeEnabledLaneInstance`）。
- **caller+callee 泳道规则合并**：同时拉取主被调侧规则并以 caller 优先去重，规避
  Polaris Server naming cache 按服务独立刷新的滞后问题。
- **`beforeChain` 路由链**：`consumer.serviceRouter.beforeChain` 用于在主路由链之前
  执行泳道路由，跨链同名插件自动去重。
- **`examples/route/lane/`**：新增 5 组件完整示例（provider / consumer / gateway /
  simple-consumer / simple-gateway），覆盖 `ProcessRouters` × `GetOneInstance` 的 4 种链路组合。
- **`examples/route/{metadata,nearby,rule}/simple-consumer`**：基于 `GetOneInstance`
  的简化消费端，与已有 `consumer/`（手动三段式）形成对照。
- **端到端验证脚本**：`lane-test.sh` / `lane-warmup-test.sh` + 3 个 `verify_*.sh`；
  聚合入口 `run_all_tests.sh` / `cleanup_all.sh`。
- **`RouteLogger`**：独立路由日志通道 `route/polaris-route.log`。

### 修复的 BUG

- **`beforeChain` 触发 FilterOnly 兜底**：前置链尾部追加的 `FilterOnlyRouter`
  会置位 `IgnoreFilterOnlyOnEndChain`，导致主链（规则/就近/元数据路由）被跳过。
  修复：新增 `GetFilterClusterBefore` 入口，前置链不再追加 FilterOnly。
- **`convert()` 污染调用方 `SourceService.Metadata`**：原实现向用户 map 写入
  `$header.*` / `$query.*` 键，跨请求复用时遗留上一次数据。修复：改为 copy-on-write。
- **泳道路由 6 类流量匹配维度互撞 / 失效**：`Arguments` 合并到 `EnvironmentVariables`
  时用短 key 导致 METHOD/CALLER_IP/PATH（`Key()` 为空）互相覆盖，HEADER/QUERY/COOKIE
  同名 key 串扰。修复：统一改用 `Argument.ToLabels` 的前缀 key（`$header.xxx` /
  `$query.xxx` / `$cookie.xxx` / `$method` / `$caller_ip` / `$Path`），6 维完全独立。
- **`zeroprotect` 日志 ns/svc 参数顺序颠倒**：搭车修复。

### ⚠ 破坏性变更

- **泳道 STRICT 模式无实例时的返回语义**：旧行为返回全量 cluster +
  `HasLimitedInstances=true`；新行为返回空 cluster，`GetOneInstance` 直接返回
  `ErrCodeAPIInstanceNotFound`。依赖 `HasLimitedInstances` 兜底的调用方需改为捕获
  错误码并自行决策（参考 `examples/route/lane/gateway/main.go`）。
- **`RouteInfo.EnvironmentVariables` key 约定变更**：从 `arg.Key()` 短 key
  改为 `Argument.ToLabels` 的前缀 key。直接读取该 map 的自定义路由需改用带前缀的 key；
  普通调用方（仅通过 `AddArguments` 传参）无需修改。


## [0.9.0] - 2021-5-7

### 添加的特性

- 支持限流新版本控制台全部特性（规则配置集群，单机均摊模式）
- 支持客户端初始化接口
- 支持本地限流数据上报

### 修改的特性

- 服务发现：服务拉取超时策略的变更

### 修复的BUG
