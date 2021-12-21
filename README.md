polaris-go
========================================
Polaris is an operation centre that supports multiple programming languages, with high compatibility to different
application framework. Polaris-go is golang SDK for Polaris.

## Overview

Polaris-go provide features listed as below:

* ** Service instance registration, and health check

  Provides API on/offline registration instance information, with regular report to inform caller server's healthy
  status.

* ** Service discovery

  Provides multiple API, for users to get a full list of server instance, or get one server instance after route rule
  filtering and loadbalancing, which can be applied to srevice invocation soon.

* ** Service circuitbreaking

  Provide API to report the invocation result, and conduct circuit breaker instance/group insolation based on collected
  data, eventually recover when the system allows.

* ** Service ratelimiting

  Provides API for applications to conduct quota check and deduction, supports rate limit policies that are based on
  server level and port.

## Quick Guide

### Dependencies

polaris-go can be referenced by go mod, user can add dependency to go.mod file

```
github.com/polarismesh/polaris-go v1.0.0
```

### Using API

API quick start guide，can reference：[QuickStart](sample/quickstart)

## License

The polaris-go is licensed under the BSD 3-Clause License. Copyright and license information can be found in the
file [LICENSE](LICENSE)