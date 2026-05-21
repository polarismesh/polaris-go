# Polaris Go

[English](./README.md) | 中文

## 使用服务限流功能

北极星支持针对不同的请求来源和系统资源进行访问限流，避免服务被压垮。本目录包含两类限流的端到端示例：

| 类型 | 资源 (`Rule.Resource`) + 策略 | 阈值字段 | 关键 API | 示例 provider |
| --- | --- | --- | --- | --- |
| **请求数限流（QPS）- reject** | `QPS` + `action=REJECT` | `Rule.Amounts[*].MaxAmount + ValidDuration` | `LimitAPI.GetQuota` | `provider-qps` |
| **请求数限流（QPS）- unirate** | `QPS` + `action=UNIRATE` | `Rule.Amounts[*]` + `max_queue_delay` | `LimitAPI.GetQuota`（SDK 内部排队等待） | `provider-qps`（同二进制，--service 切换） |
| **并发数限流（CONCURRENCY）** | `CONCURRENCY` | `Rule.ConcurrencyAmount.MaxAmount` | `LimitAPI.GetQuota` + **必须 `defer future.Release()`** | `provider-concurrency` |
| **多维匹配（AND）** | 任一资源类型 + `Rule.Arguments[*]` | HEADER / QUERY / METHOD / CALLER_SERVICE / CALLER_IP / CALLER_METADATA | `quotaReq.AddArgument(model.BuildXxxArgument(...))` | `provider-qps` + consumer `--caller-*` 注入 |

> **reject vs unirate**：reject 超出阈值立即返回 429；unirate 超出速率的请求会被 SDK 排队等待（仍返回 200，但耗时被拉长），仅当排队超过 `max_queue_delay` 才拒绝。
>
> **多维匹配**：规则的 `arguments` 之间是 AND 关系，6 类维度全命中才生效；演示见 `verify_ratelimit.sh` 用例 4.x。
>
> 并发数限流由 `concurrency` 插件实现（`plugin/ratelimiter/reject_concurrency`），是**纯本地模式**，
> 不依赖远程限流服务器；即便规则下发为 `Type=GLOBAL` 也会被框架强制按本地处理。

## 目录结构

```
examples/ratelimit/
├── consumer/                 调用方示例（任意限流共用）
├── provider-qps/             QPS 限流 provider 示例（Release 为 no-op，但已 defer 兼容）
├── provider-concurrency/     并发数限流 provider 示例（必须 defer Release）
├── verify_ratelimit.sh       端到端验证脚本（QPS + 并发数）
├── cleanup.sh                清理 provider/consumer 进程及 .build/.logs
├── test.md                   端到端用例编号与判定标准
└── README-zh.md / README.md  本说明
```

> 如需演示"同服务多实例"，使用同一个 provider-qps 二进制启动两次并指定不同 `--port` 即可，
> 例如 `./provider-qps/bin --port 18080` 和 `./provider-qps/bin --port 18081`。

## 如何使用

### 构建可执行文件

QPS 示例：

```bash
# provider-qps
cd provider-qps && go build -o bin && cd ..

# consumer
cd consumer && go build -o bin && cd ..
```

并发数示例：

```bash
cd provider-concurrency && go build -o bin && cd ..
```

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过 `127.0.0.1:8080` 打开控制台。

QPS 示例 默认服务名 `QpsRatelimitEchoServer`，并发数示例 默认服务名 `ConcurrencyEchoServer`，命名空间均为 `default`。

### 创建限流规则

可以通过控制台创建（参考 `image/create_service_ratelimit.png`），也可以直接调用 HTTP API：

```bash
# QPS 限流：限制 /echo 每秒最多 2 次
curl -X POST http://127.0.0.1:8090/naming/v1/ratelimits \
  -H "X-Polaris-Token:${POLARIS_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '[{
    "name": "qps-rule",
    "service": "QpsRatelimitEchoServer",
    "namespace": "default",
    "resource": "QPS",
    "type": "LOCAL",
    "method": {"type": "EXACT", "value": "/echo"},
    "amounts": [{"maxAmount": 2, "validDuration": "1s"}],
    "action": "REJECT"
  }]'

# 并发数限流：限制 /slow 同时只能有 2 个请求在处理
curl -X POST http://127.0.0.1:8090/naming/v1/ratelimits \
  -H "X-Polaris-Token:${POLARIS_TOKEN}" \
  -H 'Content-Type: application/json' \
  -d '[{
    "name": "concurrency-rule",
    "service": "ConcurrencyEchoServer",
    "namespace": "default",
    "resource": "CONCURRENCY",
    "type": "LOCAL",
    "method": {"type": "EXACT", "value": "/slow"},
    "concurrencyAmount": {"maxAmount": 2}
  }]'
```

> ⚠️ 并发数限流的 `Resource` 必须设置为 `CONCURRENCY`，且阈值在 `concurrencyAmount.maxAmount` 字段，
> 而不是 `amounts`。SDK 框架会根据 `Resource` 自动选用 `concurrency` 插件，无视 `Rule.Action`。

### 修改配置

指定北极星服务端地址，编辑各 provider 子目录下的 `polaris.yaml` 文件：

```yaml
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### 执行程序

```bash
# QPS 示例
./provider-qps/bin --service QpsRatelimitEchoServer --port 18080 &
./consumer/bin --service QpsRatelimitEchoServer --port 18090 &

# 并发数示例
./provider-concurrency/bin --service ConcurrencyEchoServer --port 18181 &
```

### 验证

#### QPS 限流

快速发起多次 `curl` 请求：

```bash
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo
# 第 1、2 次：Hello, I'm QpsRatelimitEchoServer Provider, ...
# 第 3 次起：Too Many Requests
```

#### 并发数限流

发起两个 1 秒以上的并发请求测试：

```bash
# 在两个终端各发一个 /slow，第三个会被限流
curl http://127.0.0.1:18181/slow?ms=2000 &
curl http://127.0.0.1:18181/slow?ms=2000 &
sleep 0.2
curl http://127.0.0.1:18181/slow?ms=2000   # 立刻收到 Too Many Requests
wait
# 等到前两个请求完成，配额自动归还，新的请求又能通过
```

`provider-concurrency/main.go` 关键模式：

```go
future, err := svr.limiter.GetQuota(quotaReq)
if future.Get().Code != model.QuotaResultOk {
    rw.WriteHeader(http.StatusTooManyRequests)
    return
}
// 对于并发数限流：必须在请求完成前 Release 归还配额，否则计数会泄漏
defer future.Release()

// 业务逻辑
doBusinessLogic()
```

## 端到端验证脚本

`verify_ratelimit.sh` 模拟**完整链路 `curl → consumer → provider`**：consumer 通过 polaris 服务发现选 provider 并 HTTP 转发，provider 内 `LimitAPI.GetQuota` 命中限流规则，返回的 429 由 consumer 透传给 curl。这与生产环境调用链一致——和上面 README 里"直接 curl provider"的快速演示是两种使用方式。

仓库根目录运行：

```bash
cd examples/ratelimit
./verify_ratelimit.sh                          # 默认本地 polaris (127.0.0.1)
./verify_ratelimit.sh --polaris-server 1.2.3.4 # 指定 polaris 地址
./verify_ratelimit.sh --skip qps,unirate       # 仅跑并发数用例
./verify_ratelimit.sh --skip concurrency       # 仅跑 QPS 用例
./verify_ratelimit.sh --keep                   # 保留 provider 进程与日志便于排查（限流规则始终保留）
```

详细用例编号与判定标准见 [`test.md`](./test.md)。

清理：

```bash
./cleanup.sh -f          # 强制清理 provider/consumer 进程及 .build/.logs
./cleanup.sh --dry-run   # 仅预览不实际清理
```
