# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## 项目简介

polaris-go 是 [Polaris](https://github.com/polarismesh/polaris)（腾讯开源服务治理平台）的 Go SDK，为微服务提供服务发现、路由、熔断、限流、配置管理和鉴权能力。

模块名：`github.com/polarismesh/polaris-go`
最低 Go 版本：1.15（CI 测试覆盖 1.19–1.24）

## 方案设计工作流

**大型需求开发或非平凡 bugfix 在动手写代码前必须先出方案。** 触发条件（满足任一即视为「大型」）：

- 跨 2 个及以上模块的改动
- 新增插件、新增 API、改动公开接口
- 涉及并发模型、缓存、连接器协议、版本兼容等架构面影响
- bug 根因不清晰、需要先调研复现路径

流程：

1. 使用 superpowers 提供的相关 skill 完成方案设计：
   - `superpowers:brainstorming` — 探索用户意图、需求边界与设计空间（创意/新功能场景）
   - `superpowers:systematic-debugging` — 系统化定位 bug 根因（疑难 bug 场景）
   - `superpowers:writing-plans` — 把方案写成可执行的多步实施计划
2. 方案文档及相关材料保存到 `specs/<YYYY-MM-DD-short-topic>/` 子目录下，按需求/bugfix 一个子目录隔离：
   - `specs/<YYYY-MM-DD-short-topic>/design.md` — 主设计文档，包含：背景、目标、非目标、方案对比、最终选型、影响面、实施步骤、回滚策略、测试计划
   - 同目录下放置该需求相关的其他材料：调研笔记、时序图、抓包/日志样本、PoC 代码片段、参考资料链接等
   - 例：`specs/2026-05-29-ratelimit-multi-windows/design.md`、`specs/2026-05-29-ratelimit-multi-windows/benchmark.md`
   - 注：`specs/` 已在 `.gitignore` 中（仅本地 / AI 工作目录使用，不入库），无需也不要提交到远程仓库
3. 与用户确认方案后，再用 `superpowers:executing-plans` 或 `superpowers:test-driven-development` 进入实现阶段。

小改动（typo、单文件局部修复、纯文档调整）可直接进入实现。

## 构建与测试命令

### 快速开发流程

```bash
# 开发前：安装依赖
go mod download

# 快速构建验证（查看是否有编译错误）
go build -v ./...

# 快速运行单个测试套件（需要本地 Polaris 服务端运行在 127.0.0.1:8091）
cd ./test
export SDK_SUIT_TEST=ConsumerTestingSuite
go test -timeout=120m -v

# 格式化代码和 import（提交前必须执行）
bash import-format.sh

# 版权头和其他检查（提交前必须执行）
bash vert.sh
```

### 完整构建与测试

```bash
# 构建整个项目（包括 plugin code generation）
# 如果修改了 plugin.cfg，必须先执行此命令
go generate && go build -v ./...

# 运行完整的单个测试套件（需要连接 Polaris 服务端）
# 测试按套件组织，通过 SDK_SUIT_TEST 环境变量指定运行哪个套件
cd ./test
export SDK_SUIT_TEST=ConsumerTestingSuite
go test -timeout=120m -v -covermode=count -coverprofile=coverage.txt \
  -coverpkg=github.com/polarismesh/polaris-go/api,github.com/polarismesh/polaris-go/pkg,github.com/polarismesh/polaris-go/plugin

# 运行所有套件（遍历 test/suit.txt）
cd ./test
for line in $(cat suit.txt); do
  export SDK_SUIT_TEST=${line}
  go test -timeout=120m -v -covermode=count -coverprofile=coverage_${line}.txt \
    -coverpkg=github.com/polarismesh/polaris-go/api,github.com/polarismesh/polaris-go/pkg,github.com/polarismesh/polaris-go/plugin
done

# 代码检查（可用于 CI 或提交前检查）
golangci-lint run          # 主 linter（配置见 .golangci.yml）
revive -config revive.toml # 辅助 linter

# 版权头、import 排序、go mod tidy 一体化检查
bash vert.sh               # 检查会进行如下验证：
                           # - 所有 .go 文件是否包含版权头
                           # - import 排序和 go fmt
                           # - go mod tidy 状态
                           # - 禁止导入 x/net/context
                           # - 禁止 math/rand 在非测试代码中使用
```

### 常见开发场景

```bash
# 场景 1：修改了单个文件，想快速验证是否有编译错误和格式问题
go build -v ./... && bash import-format.sh

# 场景 2：写了新的测试，想快速验证测试通过
cd ./test
export SDK_SUIT_TEST=ConsumerTestingSuite
go test -timeout=120m -v -run TestXxx  # 只运行名为 TestXxx 的测试

# 场景 3：修改了插件配置（plugin.cfg）
# 必须重新生成注册代码，然后重新编译
go generate && go build -v ./...

# 场景 4：修改了 go.mod，需要更新依赖
go mod tidy
go mod download
```

### 可用测试套件

所有可用套件名称列在 `test/suit.txt` 中：
`ConsumerTestingSuite`、`ProviderTestingSuite`、`LBTestingSuite`、`CircuitBreakSuite`、`HealthCheckTestingSuite`、`HealthCheckAlwaysTestingSuite`、`NearbyTestingSuite`、`RuleRoutingTestingSuite`、`DstMetaTestingSuite`、`SetDivisionTestingSuite`、`CanaryTestingSuite`、`CacheTestingSuite`、`ServiceUpdateSuite`、`ServerSwitchSuite`、`DefaultServerSuite`、`CacheFastUpdateSuite`、`ServerFailOverSuite`、`EventSubscribeSuit`、`InnerServiceLBTestingSuite`、`LocalNormalTestingSuite`、`RuleChangeTestingSuite`、`RemoteNormalTestingSuite`

## 架构说明

### 三层架构

代码库采用三层架构：

**1. API 层（`api/` + 根目录 `api_*.go`）**
对外暴露的接口和工厂函数。核心类型：
- `ConsumerAPI` — 服务发现：`GetOneInstance`、`GetInstances`、`WatchService`
- `ProviderAPI` — 服务注册：`Register`、`Deregister`、`Heartbeat`
- `ConfigAPI` — 配置管理：`GetConfigFile`、`FetchConfigFile`、`CreateConfigFile`
- `CircuitBreakerAPI` — 熔断：`Check`、`Report`
- `LimitAPI` — 限流：`GetQuota`
- `RouterAPI` — 路由：`ProcessRouters`、`ProcessLoadBalance`
- `AuthAPI` — 鉴权：`Authenticate`
- `ConfigGroupAPI` — 配置分组：`GetConfigGroup`、`FetchConfigGroup`

所有 API 类型均为薄封装，最终委托给 `pkg/sdk.Engine` 执行。

**2. 核心逻辑层（`pkg/`）**
- `pkg/model/` — 所有数据结构及核心类型定义。`EventType` 枚举、`ServiceKey`、`RegistryValue`、`ControlParam` 等
- `pkg/sdk/` — `Engine` 接口（`pkg/sdk/engine.go`）是所有 SDK 操作的统一编排入口；`ValueContext` 提供线程安全的上下文 KV 存储和地域信息管理
- `pkg/config/` — SDK 配置类型及默认值；配置从 `polaris.yaml` 加载
- `pkg/flow/` — `Engine` 接口的具体实现。请求流将各插件串联：
  - `sync_flow.go` 处理同步调用
  - `router_flow.go` 处理路由流程
  - `circuitbreaker_flow.go` 处理熔断流程
  - `quota/` 处理限流子流程
  - `configuration/` 处理配置管理子流程
  - `lossless_flow.go` 处理无损上下线流程
- `pkg/plugin/` — 插件管理器（`manage.go`）。插件按类型注册，`plugin.cfg` 驱动代码生成，生命周期为 `Init → Start → Destroy`
- `pkg/network/` — 与 Polaris 服务端的 gRPC 连接管理

**3. 插件层（`plugin/`）**
按类别组织的可扩展实现：
- `serverconnector/grpc` — 连接 Polaris 服务端的 gRPC 连接器
- `localregistry/inmemory` — 内存服务缓存
- `servicerouter/` — 路由策略：`rulebase`、`nearbybase`、`setdivision`、`dstmeta`、`filteronly`、`canary`、`lane`、`zeroprotect`
- `loadbalancer/` — 负载均衡算法：`weightedrandom`、`ringhash`、`hash`、`maglev`
- `healthcheck/` — 主动健康检查：`tcp`、`http`、`udp`
- `circuitbreaker/composite` — 组合熔断器（含 consecutive、err_rate 等 trigger）
- `ratelimiter/` — `reject`（本地 QPS 限流）、`unirate`（漏桶限流）、`reject_concurrency`（并发限流）
- `weightadjuster/warmup` — 权重调整（预热）
- `authenticator/blockallowlist` — 鉴权（黑白名单）
- `location/` — 地理位置感知：`local`、`remotehttp`、`remoteservice`
- `configconnector/polaris` — 配置中心连接器
- `configfilter/crypto/` — 配置加解密过滤：`aes`、`rsa`
- `logger/zaplog` — 基于 zap 的日志
- `metrics/prometheus` — Prometheus 指标上报
- `events/pushgateway` — Pushgateway 事件上报
- `admin/httpServer` — 管理 HTTP 服务
- `lossless/losslessController` — 无损上下线控制器

### 核心设计模式

**Engine 作为核心枢纽：** 所有 API 对象均通过 `SDKContext` 创建，`SDKContext` 持有 `Engine` 实例。`Engine`（`pkg/sdk/engine.go`）是所有 SDK 操作的统一编排入口，负责管理插件生命周期、协调请求流程。共享同一个 `SDKContext` 的多个 API 对象复用同一连接和缓存。

**插件架构：** 功能以插件形式实现，注册至 `PluginManager`（`pkg/plugin/manage.go`）。插件通过 `plugin.cfg` 配置驱动代码生成（`go generate`），以 `init()` + blank import 方式注册到 `pkg/plugin/register/plugins.go`。每个插件实现 `Plugin` 接口（`Type`、`ID`、`Name`、`Init`、`Start`、`Destroy`、`IsEnable`）。

**Flow 流水线处理：** 运行时调用（如 `GetOneInstance`）经过 `pkg/flow/` 中的 Flow 类，按顺序调用插件链：缓存查询 → 连接器同步 → 路由器过滤 → 负载均衡 → 熔断检查。

**ValueContext 线程安全上下文：** `ValueContext`（`pkg/sdk/context.go`）提供线程安全的 KV 存储、地域信息管理和时钟抽象，在 Engine 和各插件间传递共享状态。

### 典型 API 使用方式

```go
// 通过 NewSDKContext 创建上下文，再从上下文获取各种 API
sdkCtx, err := api.NewSDKContextByAddress("127.0.0.1:8091")
if err != nil {
    panic(err)
}
defer sdkCtx.Destroy()

consumer := api.NewConsumerAPIByContext(sdkCtx)
provider := api.NewProviderAPIByContext(sdkCtx)
limit := api.NewLimitAPIByContext(sdkCtx)

// 也可以通过编程式配置创建
cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
sdkCtx, err := api.NewSDKContext(cfg)
```

`SDKContext` 在 `Destroy()` 时统一释放插件和网络连接，推荐在长生命周期组件持有它，并在应用退出时统一销毁，避免重复创建导致连接/缓存重复初始化。

### 配置

默认从工作目录的 `polaris.yaml` 加载配置。也可通过代码方式配置：`config.NewDefaultConfiguration([]string{"host:port"})`。插件行为通过 YAML 配置控制。

### SDKContext 生命周期与最佳实践

**重点：`SDKContext` 的创建开销很大（初始化插件、建立网络连接），销毁也很重要（释放资源）。**

```go
// ✅ 推荐：在应用启动时创建，应用退出时销毁
var globalSDKContext api.SDKContext

func init() {
    var err error
    globalSDKContext, err = api.NewSDKContextByAddress("127.0.0.1:8091")
    if err != nil {
        panic(err)
    }
}

func main() {
    defer globalSDKContext.Destroy()
    
    // 从同一个 SDKContext 获取的所有 API 对象复用同一个 Engine、连接和缓存
    consumer := api.NewConsumerAPIByContext(globalSDKContext)
    provider := api.NewProviderAPIByContext(globalSDKContext)
    
    // 使用 consumer 和 provider...
}

// ❌ 错误做法：频繁创建销毁 SDKContext
func badExample() {
    for i := 0; i < 100; i++ {
        ctx, _ := api.NewSDKContextByAddress("127.0.0.1:8091")
        // 每次都重新初始化插件、建立连接 - 性能灾难
        defer ctx.Destroy()
    }
}
```

关键点：
- 共享同一个 `SDKContext` 的多个 API 对象复用同一个 `Engine`、连接池和服务缓存。
- `Destroy()` 调用会关闭所有 gRPC 连接、停止健康检查、释放插件资源，必须确保最后调用。
- 如果应用分层架构中多个组件都需要 SDK，推荐在全局单例或 DI 容器中持有 `SDKContext`。

### 新增插件的关键步骤

1. 在 `plugin/` 下对应类别目录中创建子目录（如 `plugin/myservice/mymodule/`）
2. 实现插件接口（`Plugin` 及对应的类型接口如 `ServerConnector`、`ServiceRouter` 等）
3. 在 `plugin.cfg` 中添加一行：`myPluginName : myservice/mymodule`
4. 执行 `go generate && go build`，自动生成注册代码到 `pkg/plugin/register/plugins.go`

### Go 版本兼容性

源码最低版本为 Go 1.15（`go.mod` 中声明）。CI 在 Go 1.19、1.20、1.21、1.22、1.23、1.24 上测试。

### 依赖管理

```bash
# 添加新依赖
go get -u <module-path>@<version>

# 整理 go.mod 和 go.sum（移除未使用的依赖）
go mod tidy

# 下载依赖（用于 CI 或初始化环境）
go mod download

# 验证依赖完整性和安全性
go mod verify
```

**提交前必须执行 `go mod tidy`，`vert.sh` 会检查此状态。**

## 测试结构（`test/`）

测试使用 `gopkg.in/check.v1`（gocheck），按功能分套件存放于子目录：
- `test/discover/` — 服务消费者和提供者套件
- `test/loadbalance/` — 负载均衡套件
- `test/serviceroute/` — 路由策略套件
- `test/stability/` — 缓存、服务端切换、故障转移套件
- `test/ratelimit/` — 限流套件
- `test/subscribe/` — 事件订阅套件

入口文件 `test/all_suite_test.go` 读取 `SDK_SUIT_TEST`（未设置时回退读取 `suit.txt`）来决定注册哪些套件。

## 代码风格

项目通过 golangci-lint（`.golangci.yml`）和 revive（`revive.toml`）强制执行编码规范，关键约束如下：

- **版权头**：每个 `.go` 文件必须包含腾讯 BSD-3-Clause 版权声明头，`vert.sh` 脚本会对此进行检查
- **import 排序**：通过 `import-format.sh` 调用 `goimports-reviser`，按标准库 → 外部依赖 → 内部包的顺序分组
- **行长度限制**：revive 配置行最大长度为 180 字符
- **导出命名**：遵循 Go 导出命名规范，常见缩写保持一致（如 `ID`、`URL`、`IP`、`API`）
- **插件顺序**：`plugin.cfg` 中的插件执行顺序有意义，插件按声明顺序依次生效
- **禁止通配符导入 x/net/context**：`vert.sh` 检查禁止在非 `.pb.go` 文件中导入 `x/net/context`
- **配置方式**：SDK 行为由工作目录下的 `polaris.yaml` 控制，也可通过代码方式配置

### 提交前检查清单

每次提交前应执行：

```bash
# 1. 格式化 import 和代码
bash import-format.sh

# 2. 运行全面检查（包括版权头、lint、go mod tidy）
bash vert.sh

# 3. 如果修改了 plugin.cfg，需要重新生成代码
go generate && go build -v ./...

# 4. 提交时使用 -s flag 自动添加 Signed-off-by，并包含 Co-Authored-By
# 格式参考全局 CLAUDE.md 的 Commit 规范
git commit -s
```

## License 文件头

所有新 Go 文件必须以如下内容开头：

```go
/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */
```

> 注意：`vert.sh` 脚本会通过 `git grep -L` 检查所有 `.go` 文件是否包含版权声明。新增文件即使不在已有路径，也必须按上述格式手工添加 license 头。

## CI

GitHub Actions 在 Go 1.19、1.20、1.21、1.22、1.23、1.24 多个版本上运行测试（`ubuntu-latest`）。PR 需通过所有检查后方可合并。贡献代码请提交至 `main` 分支。

CI 流程包括：
1. 下载依赖（`go mod download`）
2. 编译验证（`go build -v ./...`）
3. 遍历 `test/suit.txt` 中的所有测试套件运行集成测试
4. 覆盖率上报（Codecov）

另外还有 golangci-lint 和 revive 的独立 CI 工作流。

## Pull Request 规范

创建 PR 时必须提交到上游仓库 `polarismesh/polaris-go`，而不是 fork 仓库。使用以下命令：

```bash
gh pr create --repo polarismesh/polaris-go --title "..." --body "..."
```

PR 模板要求标注影响的领域（Configuration、Docs、Performance、Naming、HealthCheck、Test and Release）以及是否涉及用户可见变更。

## Git 提交规范

- 每次提交必须包含 `Signed-off-by` 行，使用 `git commit -s`
