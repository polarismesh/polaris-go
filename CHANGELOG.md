# Changelog

[TOC]

本项目所有重要的变更都必须记录在本文件中。

## [Unreleased]

### 修复的 BUG

- **路由链回归**：`beforeChain` 启用 `laneRouter` 后，规则路由 / 就近路由 / 元数据路由
  全部失效。根因是 `processServiceRouters` 在前置链尾部追加的 `FilterOnlyRouter` 会
  调用 `SetIgnoreFilterOnlyOnEndChain(true)`，使上层 `getServiceRoutedInstances`
  误判前置链已产出最终结果而跳过主链。修复：新增 `GetFilterClusterBefore` 入口，
  前置链不再追加 FilterOnly 兜底。
- **`ProcessRouters` / `GetOneInstance` / `GetInstances` 的 `convert()` 脏数据**：
  原实现直接向调用方传入的 `SourceService.Metadata` 写入 `$header.*` / `$query.*` 键，
  业务代码跨请求复用同一张 map 会看到上一次请求遗留的键。修复：改为复制一份再写。

### ⚠ 破坏性变更

- **泳道 STRICT 模式无可用实例时的返回语义**：
  - 旧行为：`lane router` 返回全量 cluster + `HasLimitedInstances=true`，下游有可能
    拿到任意实例继续发送请求（与 STRICT "严格隔离" 语义不符，实际上是缺陷）。
  - 新行为：返回**已按 lane metadata 过滤的空 cluster**，`LoadBalance`/`GetOneInstance`
    将直接返回 `ErrCodeAPIInstanceNotFound`。
  - 升级影响：曾依赖 `HasLimitedInstances` 在 STRICT 无实例时做兜底的调用方，需要改为
    捕获 `ErrCodeAPIInstanceNotFound`（SDK 错误码）并自行决定是否降级 / 返回 503。
    可参考 `examples/route/lane/gateway/main.go` 中的处理方式。

## [0.9.0] - 2021-5-7

### 添加的特性

- 支持限流新版本控制台全部特性（规则配置集群，单机均摊模式）
- 支持客户端初始化接口
- 支持本地限流数据上报

### 修改的特性

- 服务发现：服务拉取超时策略的变更

### 修复的BUG
