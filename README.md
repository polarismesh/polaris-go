# Polaris Go

[![codecov](https://codecov.io/gh/polarismesh/polaris-go/branch/main/graph/badge.svg?token=EK9174H91T)](https://codecov.io/gh/polarismesh/polaris-go)
[![Go](https://github.com/polarismesh/polaris-go/workflows/Go/badge.svg?branch=main)](https://github.com/polarismesh/polaris-go/actions)
[![goproxy](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)](https://goproxy.cn/stats/github.com/polarismesh/polaris-go/badges/download-count.svg)
[![Go Reference](https://pkg.go.dev/badge/github.com/polarismesh/polaris-go.svg)](https://pkg.go.dev/github.com/polarismesh/polaris-go)
[![GitHub release (latest by date)](https://img.shields.io/github/v/release/polarismesh/polaris-go?style=flat-square)](https://github.com/polarismesh/polaris-go)

English | [简体中文](./README-zh.md) 

README：

- [Introduction](#introduction)
- [How to use](#how-to-use)
- [Examples](#examples)
- [Frameworks](#frameworks)

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

## How to use

polaris-go can be referenced by go mod, user can add dependency to go.mod file

```go
go get -u github.com/polarismesh/polaris-go
```

API quick start guide，can reference：[QuickStart](examples/quickstart)

## Examples

A polaris-examples module is included in our project for you to get started with polaris-go quickly. It contains an example, and you can refer to the readme file in the example project for a quick walkthrough.

- [quick start](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [router example](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [circuit breaker example](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [rate limiter example](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [config center example](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)

## Frameworks

Developers usually use HTTP or RPC frameworks to develop distributed service. Polaris SDK is already integrated into some development frameworks. If using these frameworks, you can enable Polaris Service Governance functions without using Polaris SDK directly.

- dubbo-go
  - [registry, discovery and routing](https://github.com/apache/dubbo-go/tree/main/registry)
  - [circuit breaker and rate limiter](https://github.com/apache/dubbo-go/tree/main/filter)
  - [examples](https://github.com/apache/dubbo-go-samples/tree/master/polaris)
- [grpc-go](https://github.com/polarismesh/grpc-go-polaris)
