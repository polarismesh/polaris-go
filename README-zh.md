# polaris-go

========================================
北极星polaris是一个支持多种开发语言、兼容主流开发框架的服务治理中心。polaris-go是北极星的Go语言嵌入式服务治理SDK

## 概述

polaris-go提供以下功能特性：

- 服务实例注册，心跳上报

  提供API接口供应用上下线时注册/反注册自身实例信息，并且可通过定时上报心跳来通知主调方自身健康状态。

- 服务发现

  提供多种API接口，通过API接口，用户可以获取服务下的全量服务实例，或者获取通过服务治理规则过滤后的一个服务实例，可供业务获取实例后马上发起调用。

- 故障熔断

  提供API接口供应用上报接口调用结果数据，并根据汇总数据快速对故障实例/分组进行隔离，以及在合适的时机进行探测恢复。

- 服务限流

  提供API接口供应用进行配额的检查及划扣，支持按服务级，以及接口级的限流策略。

## 快速入门

### 依赖引入

polaris-go通过go mod进行管理，用户可以在go.mod文件中引入polaris-go的依赖

```go
github.com/polarismesh/polaris-go v1.0.0
```

### 使用API

API的快速使用指南，可以参考：[QuickStart](examples/quickstart)

## License

The polaris-go is licensed under the BSD 3-Clause License. Copyright and license information can be found in the
file [LICENSE](LICENSE)

