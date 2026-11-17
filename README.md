# Polaris Go

[![codecov](https://codecov.io/gh/polarismesh/polaris-go/branch/main/graph/badge.svg)](https://app.codecov.io/gh/polarismesh/polaris-go)
[![Go](https://github.com/polarismesh/polaris-go/actions/workflows/testing.yml/badge.svg?branch=main)](https://github.com/polarismesh/polaris-go/actions)
[![goproxy](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/polarismesh/polaris-go.svg)](https://pkg.go.dev/github.com/polarismesh/polaris-go)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/polarismesh/polaris-go?style=flat-square)](https://github.com/polarismesh/polaris-go)

English | [简体中文](./README-zh.md)

README：

- [Introduction](#introduction)
- [How to use](#how-to-use)
- [Examples](#examples)

## Introduction

Polaris-go is golang SDK for Polaris. Polaris is an operation centre that supports multiple programming languages, with high compatibility to different
application framework.

Polaris-go provide features listed as below:

- Service instance registration, and health check

  Provides API on/offline registration instance information, with regular report to inform caller server's healthy
  status.

- Service discovery

  Provides multiple API, for users to get a full list of server instance, or get one server instance after route rule
  filtering and loadbalancing, which can be applied to srevice invocation soon.

- Service circuitbreaking

  Provide API to report the invocation result, and conduct circuit breaker instance/group insolation based on collected
  data, eventually recover when the system allows.

- Service ratelimiting

  Provides API for applications to conduct quota check and deduction, supports rate limit policies that are based on
  server level and port.

- AI Agent, MCP, and Skill registry

  Uses Provider/Consumer APIs for instance registration, discovery, and Service Contracts, stateless MCP metadata
  helpers, and `SkillAPI` for querying, streaming download, persistence, safe extraction, and local caching of
  published Skills. AI Agent management, AgentCard parsing, and optional Console service CRUD live in
  `go-agent-naming-polarismesh`; the Core SDK has no `ServiceAdminAPI`, `AgentAPI`, or `MCPAPI`.

## How to use

polaris-go can be referenced by go mod, user can add dependency to go.mod file

```go
go get -u github.com/polarismesh/polaris-go
```

API quick start guide，can reference：[QuickStart](examples/quickstart)

### AI Registry

Skill Registry uses an independent gRPC endpoint. Consumer discovery also returns service type and extended
metadata. Optional Console service management belongs to `go-agent-naming-polarismesh`, not Core configuration.
Service Contracts are reported with `ProviderAPI.ReportServiceContract` and queried with
`ConsumerAPI.GetServiceContract`; both are disabled unless `provider.reportServiceContractEnable` is true.

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

The same `SDKContext` should be reused by Provider, Consumer, and Skill APIs. `SkillAPI`
created by `New...ByContext` borrows the Context; `Close`/`Destroy` does not destroy it. APIs created from a
config own and close their Context exactly once. Consumer responses preserve the actual service type and a
detached extended-metadata snapshot. `ServiceRevision` contains only a matching service-resource projection;
it stays empty when the server provides no resource revision, and is distinct from instance-list `Revision`.

AI Agent Ensure/Mount/Unmount/Delete and Skill Repository assembly are provided by `go-agent-naming-polarismesh`.
Its default Agent service reader uses ConsumerAPI. Writes require an explicit adapter `agentapi.WithConsole`
option and return `ErrCodeServiceWriteUnsupported` without it. Clearing the final Skill still depends on
server-side HTTP protobuf support for repeated-field presence. Core registration and discovery do not require Console.

AI Agent instance metadata uses exactly three standard keys: `ai-agent-name`, `ai-agent-card`, and
`ai-a2a-version`. Core passes these values through as instance metadata and does not parse the AgentCard schema.
The complete 0.3/1.0 codec, same-IP validation, raw JSON/signature preservation, and atomic registration snapshot
are owned by `go-agent-naming-polarismesh`.

Skill installation rejects ZIP Slip, absolute paths, symlinks, individual files larger than 64 MiB, and packages
larger than 256 MiB. ZIP archives are limited to 10000 entries, and the actual extracted byte count is capped even if
archive headers are inconsistent. After full validation, content is published to an immutable cache generation, so
paths returned by earlier installations remain readable during forced or repair installations. The cache tree digest
covers file content and permission bits. The last successful Force selection is persisted atomically; a damaged current
generation is downloaded and repaired instead of silently falling back to an older cached package.

Successful Get/Download responses are also persisted under `get/`, `download/`, and `active/`. Only network errors can
fall back to this payload store; NotFound, authorization, protocol, and integrity errors remain visible. Skill lists are
remote-only. `GetSkillListResult` preserves the server pagination total, while the original `GetSkillList` method remains
source compatible. Persistence is asynchronous and bounded; overloaded tasks are dropped with a WARN log rather than
blocking registry reads.
Download fallback also verifies the persisted request Tag, and a tag-filtered download never replaces the unfiltered
active-version pointer. YAML cache envelopes and decoded content are bounded separately, so a 64 MiB Markdown payload
remains readable after persistence.

Core provides stateless helpers for the standard `mcp-server-name`, `mcp-protocol-version`, and
`mcp-endpoint-path` metadata. MCP Tool/Resource/Prompt features are represented by generic Service Contract
feature types `101/102/103`; framework-specific mapping belongs to `go-agent-naming-polarismesh`. `mcp-stdio`
is local-only and is never registered as a network instance. Instance registration remains the responsibility of
the naming plugins.

Service-call Prometheus and audit output include the bounded `callee_instance_type` label with values `microservice`,
`mcp`, or `a2a`; the zero value and unknown input normalize to `microservice`.

## Examples

A polaris-examples module is included in our project for you to get started with polaris-go quickly. It contains an example, and you can refer to the readme file in the example project for a quick walkthrough.

- [quick start](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [router example](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [circuit breaker example](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [rate limiter example](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [config center example](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)

