# Polaris Go

[![codecov](https://codecov.io/gh/polarismesh/polaris-go/branch/main/graph/badge.svg?token=EK9174H91T)](https://codecov.io/gh/polarismesh/polaris-go)
[![Go](https://github.com/polarismesh/polaris-go/workflows/Go/badge.svg?branch=main)](https://github.com/polarismesh/polaris-go/actions)
[![goproxy](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/polarismesh/polaris-go.svg)](https://pkg.go.dev/github.com/polarismesh/polaris-go)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/polarismesh/polaris-go?style=flat-square)](https://github.com/polarismesh/polaris-go)

[English](./README.md) | 简体中文

---

README：

- [介绍](#介绍)
- [如何使用](#如何使用)
- [使用示例](#使用示例)
- [开发框架](#开发框架)

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

## 如何使用

polaris-go通过go mod进行管理，用户可以在go.mod文件中引入polaris-go的依赖

```go
go get -u github.com/polarismesh/polaris-go
```

API的快速使用指南，可以参考：[QuickStart](examples/quickstart)

## 使用示例

为了演示功能如何使用，polaris-go 项目包含了一个子模块polaris-examples。此模块中提供了演示用的 example ，您可以阅读对应的 example 工程下的 README-zh 文档，根据里面的步骤来体验。

- [快速开始](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [路由示例](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [熔断示例](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [限流示例](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [配置中心示例](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)

## 开发框架

通常，开发者使用开源或者自研的 HTTP 或者 RPC 框架开发分布式服务。北极星提供多个框架和北极星 SDK 的集成方案和实现。如果你在使用这些框架，不需要直接调用北极星 SDK，就可以使用北极星的服务治理功能。

- Dubbo Go
  - [registry, discovery and routing](https://github.com/apache/dubbo-go/tree/main/registry)
  - [circuit breaker and rate limiter](https://github.com/apache/dubbo-go/tree/main/filter)
- grpc-go
  - [grpc-go-polaris](https://github.com/polarismesh/grpc-go-polaris)
