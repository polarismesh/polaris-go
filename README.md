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

Core SDK supports AI Agent, MCP, and Skill registration and discovery, Skill download, persistence, and installation,
and Service Contract read/write. Agent management, AgentCard parsing, and optional Console CRUD are provided
by `go-agent-naming-polarismesh`.

These APIs are currently unreleased on `main`. See the
[AI Registry example (Chinese)](examples/ai-registry/README.md) for configuration, API usage, behavior boundaries,
and real-server E2E verification.

## Examples

The `examples` directory contains independent Go modules to help you get started with polaris-go quickly. The
current example modules require Go 1.24 and declare polaris-go v1.7.2; most examples use a local `replace` to
exercise the checked-out SDK source. Each example README describes how to run it.

- [quick start](https://github.com/polarismesh/polaris-go/tree/main/examples/quickstart)
- [router example](https://github.com/polarismesh/polaris-go/tree/main/examples/route)
- [circuit breaker example](https://github.com/polarismesh/polaris-go/tree/main/examples/circuitbreaker)
- [rate limiter example](https://github.com/polarismesh/polaris-go/tree/main/examples/ratelimit)
- [config center example](https://github.com/polarismesh/polaris-go/tree/main/examples/configuration)
- [AI Registry example](https://github.com/polarismesh/polaris-go/tree/main/examples/ai-registry)

