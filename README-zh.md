# Polaris Go

[![codecov](https://codecov.io/gh/polarismesh/polaris-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/polarismesh/polaris-go)
[![Go](https://github.com/polarismesh/polaris-go/actions/workflows/testing.yml/badge.svg?branch=main)](https://github.com/polarismesh/polaris-go/actions)
[![goproxy](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/polarismesh/polaris-go.svg)](https://pkg.go.dev/github.com/polarismesh/polaris-go)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/polarismesh/polaris-go?style=flat-square)](https://github.com/polarismesh/polaris-go)

[English](./README.md) | 简体中文

---

README：

- [介绍](#介绍)
- [如何使用](#如何使用)
- [使用示例](#使用示例)

## 介绍

polaris-go是北极星的Go语言嵌入式服务治理SDK。北极星是一个支持多种开发语言、兼容主流开发框架的服务治理中心。

polaris-go提供以下功能特性：

- 服务实例注册，心跳上报

  提供API接口供应用上下线时注册/反注册自身实例信息，并且可通过定时上报心跳来通知主调方自身健康状态。

- 服务发现

  提供多种API接口，通过API接口，用户可以获取服务下的全量服务实例，或者获取通过服务治理规则过滤后的一个服务实例，可供业务获取实例后马上发起调用。

- 故障熔断

  提供API接口供应用上报接口调用结果数据，并根据汇总数据快速对故障实例/分组进行隔离，以及在合适的时机进行探测恢复。

- 服务限流

  提供API接口供应用进行配额的检查及划扣，支持按服务级，以及接口级的限流策略。

- AI Agent、MCP 与 Skill 注册

  通过 Provider/Consumer 注册和发现实例并读写 Service Contract，提供无状态 MCP metadata helper，
  `SkillAPI` 查询、流式下载、持久化、安全解压和缓存 published Skill。AI Agent 管理、AgentCard
  解析与可选 Console Service CRUD 位于 `go-agent-naming-polarismesh`；Core 不提供
  `ServiceAdminAPI`、`AgentAPI` 或 `MCPAPI`。

## 如何使用

polaris-go通过go mod进行管理，用户可以在go.mod文件中引入polaris-go的依赖

```go
go get -u github.com/polarismesh/polaris-go
```

API的快速使用指南，可以参考：[QuickStart](examples/quickstart)

### AI Registry

Skill Registry 使用独立 gRPC 地址，Consumer 发现响应同时保留服务类型和扩展元数据。
可选 Console 管理由 `go-agent-naming-polarismesh` 显式启用，不属于 Core 配置。
Service Contract 通过 `ProviderAPI.ReportServiceContract` 上报、`ConsumerAPI.GetServiceContract`
查询；`provider.reportServiceContractEnable` 为 false 时两者均禁用。

```yaml
skill:
  enable: true
  connectorType: polaris
  addresses:
    - 127.0.0.1:8094
  connectTimeout: 1s
  messageTimeout: 5s
  downloadTimeout: 30s
  persistEnable: true
  persistDir: ./polaris/backup/skill
  persistMaxWriteRetry: 1
  persistMaxReadRetry: 0
  persistRetryInterval: 500ms
  fallbackToLocalCache: true

provider:
  reportServiceContractEnable: false
```

```go
cfg := config.NewDefaultConfiguration([]string{"127.0.0.1:8091"})
cfg.GetSkill().SetEnable(true)
cfg.GetSkill().SetAddresses([]string{"127.0.0.1:8094"})

ctx, err := api.InitContextByConfig(cfg)
if err != nil {
	panic(err)
}
defer ctx.Destroy()

consumer := api.NewConsumerAPIByContext(ctx)
provider := api.NewProviderAPIByContext(ctx)
skillAPI := api.NewSkillAPIByContext(ctx)

instances, err := consumer.GetAllInstances(&api.GetAllInstancesRequest{
	GetAllInstancesRequest: model.GetAllInstancesRequest{
		Namespace: "Production",
		Service:   "customer-agent",
	},
})
if err != nil {
	panic(err)
}
_ = instances.ServiceType
_ = instances.ServiceRevision
_ = instances.ExtendedMetadata
_, err = skillAPI.GetSkill(&api.GetSkillRequest{
	GetSkillRequest: model.GetSkillRequest{
		Namespace: "Production",
		Name:      "demo-skill",
		Version:   "1.0.0",
	},
})
if err != nil {
	panic(err)
}
skillList, err := skillAPI.GetSkillListResult(&api.GetSkillListRequest{
	GetSkillListRequest: model.GetSkillListRequest{
		Namespace: "Production",
		Limit:     20,
	},
})
if err != nil {
	panic(err)
}
_ = skillList.Total
_ = skillList.Resources
mcpMetadata, err := mcpAPI.BuildServerMetadata(&api.MCPServerMetadataRequest{
	MCPServerMetadataRequest: model.MCPServerMetadataRequest{
		ServerName:      "search-mcp",
		ProtocolVersion: "2025-06-18",
		Transport:       model.MCPTransportStreamableHTTP,
	},
})
if err != nil {
	panic(err)
}
_ = mcpMetadata
_, err = skillAPI.Install(&api.SkillInstallRequest{
	SkillInstallRequest: model.SkillInstallRequest{
		Namespace: "Production",
		Name:      "demo-skill",
		Version:   "1.0.0",
		CacheDir:  "./var/polaris-skills",
	},
})
_, err = provider.ReportServiceContract(&api.ReportServiceContractRequest{
	ServiceContract: &model.ServiceContract{
		GetServiceContractRequest: model.GetServiceContractRequest{
			Namespace: "Production",
			Service:   "demo-mcp",
			Name:      "demo-mcp",
			Protocol:  "mcp-streamable-http",
			Version:   "1.0.0",
		},
	},
})
```

Provider、Consumer 和 Skill API 应复用同一个 `SDKContext`。通过 `New...ByContext`
创建的 `SkillAPI` 只借用 Context，`Close`/`Destroy` 不会销毁它；通过配置创建的 API
拥有 Context，并且只关闭一次。Consumer 保留服务端实际类型与独立的扩展元数据快照；
`ServiceRevision` 只取身份匹配的服务资源投影，服务端缺失时保持为空，不使用实例列表 `Revision` 代替。

AI Agent 的 Ensure/Mount/Unmount/Delete 和 Skill Repository 装配由 `go-agent-naming-polarismesh` 完成。
其默认 Agent 服务读取走 ConsumerAPI，写入必须显式使用扩展层 `agentapi.WithConsole`，
未启用时返回 `ErrCodeServiceWriteUnsupported`。清空最后一个 Skill 仍依赖服务端在
HTTP protobuf 解码中保留 repeated 字段 presence；Core 注册和发现不需要 Console。

AgentCard 边界：

- Agent 实例的标准 metadata 严格为 `ai-agent-name`、`ai-agent-card`、`ai-a2a-version` 三项；
- Core 只按实例 metadata 原样透传，不解析 AgentCard schema；
- 0.3/1.0 严格解析、同 IP 校验、原始 JSON/签名保真和原子注册快照由
  `go-agent-naming-polarismesh` 提供。

Skill 安装会拒绝 ZIP Slip、绝对路径、符号链接、超过 64 MiB 的单文件和超过 256 MiB 的包；ZIP
最多包含 10000 个 entry，实际解压字节数同样受 256 MiB 限制。完整校验后发布不可变缓存代目录，
此前返回的安装路径在 Force 或修复安装期间仍可读取。缓存 tree digest 覆盖文件内容和权限位。
最后一次成功 Force 的选择会原子持久化，当前代目录损坏时重新下载修复，不会静默回退到旧缓存包。
当前验证范围见 `CHANGELOG.md`。

成功 Get/Download 还会持久化到 `get/`、`download/`、`active/`。只有网络错误允许回退该 payload 缓存；
NotFound、鉴权、协议和内容校验错误不会被掩盖。Skill 列表始终访问远端；`GetSkillListResult` 保留服务端
分页总数，原 `GetSkillList` 方法保持源码兼容。持久化异步且有容量上限，过载任务记录 WARN 后丢弃，
不会阻塞注册中心读请求。
Download 回退会校验持久化的请求 Tag；带 Tag 的下载不会替换未过滤 active 指针。
YAML 缓存 envelope 与解码后的 content 分别限界，64 MiB Markdown 持久化后仍可读取。

Core 提供无状态 helper 构建 `mcp-server-name`、`mcp-protocol-version`、`mcp-endpoint-path`。
MCP Tool/Resource/Prompt 使用通用 Service Contract feature `101/102/103` 表示，框架映射由
`go-agent-naming-polarismesh` 提供。`mcp-stdio` 只做本地校验，不注册网络实例；实例注册仍由
naming 插件负责。

服务调用 Prometheus 和 audit 输出新增受限维度 `callee_instance_type`，值为 `microservice`、`mcp` 或
`a2a`；零值和未知输入都归一为 `microservice`。

## 使用示例

为了演示功能如何使用，polaris-go 项目包含了一个子模块polaris-examples。此模块中提供了演示用的 example ，您可以阅读对应的 example 工程下的 README-zh 文档，根据里面的步骤来体验。

- [快速开始](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [路由示例](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [熔断示例](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [限流示例](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [配置中心示例](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)

