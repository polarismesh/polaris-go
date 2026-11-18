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

Core SDK 支持 AI Agent、MCP 与 Skill 的注册和发现，Skill 下载、持久化与安装，以及
Service Contract 读写。AI Agent 管理、AgentCard 解析和可选 Console CRUD 由
`go-agent-naming-polarismesh` 提供。

相关 API 当前仍在 `main` 分支的 Unreleased 阶段。配置、API 用法、行为边界和真实服务端 E2E
验证见 [AI Registry 示例](examples/ai-registry/README.md)。

## 使用示例

`examples` 目录包含多个独立 Go module，用于快速体验 polaris-go 的各类能力。当前示例模块要求 Go 1.24，
并声明依赖 polaris-go v1.7.2；大部分示例通过 `replace` 使用当前工作区的 SDK 源码。每个示例目录下的
README 提供了对应的运行说明。

- [快速开始](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [路由示例](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [熔断示例](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [限流示例](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [配置中心示例](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)
- [AI Registry 示例](https://github.com/polarismesh/polaris-go/tree/main/examples/ai-registry)

