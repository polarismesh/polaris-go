# Examples Demo 开发规约

本规约用于约束 `examples/` 目录下所有 Demo 程序的开发，**以下是每次开发都必须固定带上的内容**，不得省略。

## 1. 必须依赖的包

Demo 中需要使用 Polaris SDK 的日志级别开关，必须导入：

```go
import (
    "github.com/polarismesh/polaris-go/api"
)
```

## 2. 必须新增的命令行参数

在 Demo 的命令行参数变量声明区，必须增加 `debug` 字段：

```go
var (
    // ... 业务参数 ...
    debug     bool
)
```

并在 `initArgs()`（或等价的参数初始化函数）中注册该参数：

```go
flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
```

## 3. main 函数必须固定包含的内容

每个 Demo 的 `main()` 函数开头必须包含以下两段固定逻辑：

### 3.1 设置标准库日志格式（带文件名与行号，便于排查）

```go
log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
```

### 3.2 根据 `-debug` 参数动态调整 Polaris SDK 日志级别

在 `flag.Parse()` 之后、业务初始化之前，必须加入以下代码块：

```go
if debug {
    if err := api.SetLoggersLevel(api.DebugLog); err != nil {
        log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
    } else {
        log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
    }
}
```

## 4. 完整骨架示例

```go
package main

import (
    "flag"
    "log"

    "github.com/polarismesh/polaris-go/api"
)

var (
    namespace string
    service   string
    token     string
    port      int64
    debug     bool
)

func initArgs() {
    flag.StringVar(&namespace, "namespace", "default", "namespace")
    flag.StringVar(&service, "service", "DemoService", "service")
    flag.StringVar(&token, "token", "", "token")
    flag.Int64Var(&port, "port", 0, "port")
    flag.BoolVar(&debug, "debug", false, "是否开启 Polaris SDK debug 日志")
}

func main() {
    log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)
    initArgs()
    flag.Parse()

    if debug {
        if err := api.SetLoggersLevel(api.DebugLog); err != nil {
            log.Printf("[WARN] 设置日志级别为 DEBUG 失败: %v", err)
        } else {
            log.Printf("[INFO] 已设置 Polaris SDK 日志级别为 DEBUG")
        }
    }

    // ... 业务逻辑 ...
}
```

## 5. 目录结构模板

新增一个示例时按以下结构组织（以 `lossless` 功能的 `consumer` 场景为例）：

```
examples/$FEATURE_NAME/
├── $SCENARIO/
│   ├── Makefile
│   ├── go.mod          # 独立 module，使用 replace 指向本地
│   ├── go.sum
│   ├── main.go
│   └── polaris.yaml    # 示例配置
├── cleanup.sh          # 清理脚本（可选）
├── verify_xxx.sh       # 验证脚本（可选）
└── README.md           # 示例说明
```

**编写 main.go 时的固定要求**：
- 使用 `flag` 解析命令行参数
- 定义 `PolarisClient` 结构体封装 API
- 包含 `initArgs()`、`main()` 函数
- 包含优雅退出逻辑（signal 监听）

## 6. HTTP 请求/响应日志（必须）

每个 HTTP handler 必须记录收到的请求和返回的响应。所有日志统一带 `[reqID]` 前缀，确保一条请求从接收到回复、中间所有处理步骤的日志都能通过同一个 ID 串联。参考 `examples/ratelimit/provider-concurrency/main.go` 中的实现模式。

### 6.1 请求 ID 贯穿全程（核心要求）

每个 handler 入口第一行就生成 `reqID`，之后 handler 内及所调用的子函数（如限流、路由）中**所有 `log.Printf` 都携带该 `[%s]` 前缀**。这样 `grep` 同一个 `reqID` 就能看到一条请求的完整处理链条：

```
[abc12345] <<< recv from 1.2.3.4:54321 | path=/echo method=GET ...
[abc12345] limiter resp | cost=123μs code=200 info=ok ...
[abc12345] >>> reply to client 1.2.3.4:54321: status=200 body=Hello, ...
```

**工具函数**（需要复制到示例中）：

- `newReqID()`：为每次请求生成唯一 8 字符 ID，作为整条请求处理链的追踪标识
- `formatHeaders(h http.Header) string`：将 headers 压缩为单行字符串
- `formatQuery(q map[string][]string) string`：将 query 参数压缩为单行字符串
- `sanitizeHeaderValue(v string) string`：过滤 header 注入风险字符

**newReqID**：

```go
func newReqID() string {
    var b [4]byte
    if _, err := rand.Read(b[:]); err != nil {
        return fmt.Sprintf("%08x", time.Now().UnixNano()&0xffffffff)
    }
    return hex.EncodeToString(b[:])
}
```

**logIncomingRequest**：单行打印收到的请求，包含 reqID、method、URL、query、headers、body：

```go
func logIncomingRequest(reqID string, r *http.Request, method string) {
    var bodyStr string
    if r.Body != nil {
        bodyBytes, err := ioutil.ReadAll(r.Body)
        if err == nil {
            bodyStr = string(bodyBytes)
        }
        _ = r.Body.Close()
    }
    log.Printf("[%s] <<< recv from %s | path=%s method=%s url=%s query=%s headers=%s body=%s",
        reqID, r.RemoteAddr, method, r.Method, r.URL.String(),
        formatQuery(r.URL.Query()), formatHeaders(r.Header), bodyStr)
}
```

**logReply**：单行打印返回给客户端的响应，包含 reqID、remoteAddr、status、body：

```go
func logReply(reqID, remoteAddr string, status int, body string) {
    log.Printf("[%s] >>> reply to client %s: status=%d body=%s", reqID, remoteAddr, status, body)
}
```

**中间处理日志也必须带 reqID**，例如限流调用返回后：

```go
log.Printf("[%s] limiter resp | cost=%s code=%d info=%s waitMs=%d "+
    "rule_name=%q rule_id=%q resource_type=%q custom_body_len=%d | "+
    "quota_req: ns=%s svc=%s method=%v labels=%v",
    reqID, time.Since(start).String(), resp.Code, resp.Info, resp.WaitMs,
    ruleName, ruleID, resourceType, len(customBody),
    quotaReq.GetNamespace(), quotaReq.GetService(), quotaReq.GetMethod(), quotaReq.GetLabels())
```

**每个 HTTP handler 的调用模式**（注意 reqID 传递给子函数）：

```go
http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
    reqID := newReqID()
    logIncomingRequest(reqID, r, "/echo")

    // 子函数也需要接收 reqID，内部日志必须带 [reqID]
    future := svr.applyLimit(reqID, rw, r, "/echo")
    if future == nil {
        return
    }
    defer future.Release()

    body := "response body"
    rw.WriteHeader(http.StatusOK)
    _, _ = rw.Write([]byte(body))
    logReply(reqID, r.RemoteAddr, http.StatusOK, body)
})
```

### 6.2 多跳链路 reqID 透传（调用链串联）

当示例涉及多级调用（如 gateway → consumer → provider）时，reqID 必须贯穿每一跳，确保全链路日志通过同一个 ID 串联。

**（1）定义 reqID header 常量**（与 `trafficStainHeader` 放在同一 const 块）：

```go
const (
    // reqIDHeader 全链路追踪请求 ID，贯穿所有中间跳。
    reqIDHeader = "X-Request-ID"
)
```

**（2）handler 入口优先从请求头读取 reqID**，仅在没有时才生成：

```go
// 调用链入口（gateway）：
func (gw *LaneGateway) handleProxy(rw http.ResponseWriter, r *http.Request) {
    reqID := r.Header.Get(reqIDHeader)
    if reqID == "" {
        reqID = newReqID()
    }
    logIncomingRequest(reqID, r, r.URL.Path)
    // ...
}

// 调用链中间节点（consumer）：
func (svr *LaneConsumer) handleEcho(rw http.ResponseWriter, r *http.Request) {
    reqID := r.Header.Get(reqIDHeader)
    if reqID == "" {
        reqID = newReqID()
    }
    // ...
}

// 调用链终点（provider）：
http.HandleFunc("/echo", func(rw http.ResponseWriter, r *http.Request) {
    reqID := r.Header.Get("X-Request-ID")
    if reqID == "" {
        reqID = newReqID()
    }
    // ...
})
```

**（3）向下游发请求时显式注入 reqID**，确保下一跳能读取到：

```go
upstreamReq, _ := http.NewRequestWithContext(r.Context(), http.MethodGet,
    fmt.Sprintf("http://%s:%d/echo", instance.GetHost(), instance.GetPort()), nil)
// 将 reqID 注入下游请求头，便于全链路日志串联
upstreamReq.Header.Set(reqIDHeader, reqID)
```

**（4）全链路效果**：客户端发送请求时可带 `X-Request-ID: abc123`，整个调用链所有跳的日志共享同一个 ID：

```
gateway:     [abc123] <<< recv from ...
gateway:     [abc123] selected instance: ...
consumer:    [abc123] <<< recv from ...
consumer:    [abc123] selected instance: ...
provider:    [abc123] <<< recv from ...
```

若客户端不携带该 header，入口节点自动生成并透传，同样贯穿全链路。

## 7. go.mod 模板

```go
module github.com/polarismesh/polaris-go/examples/$FEATURE_NAME/$SCENARIO

go 1.21

require github.com/polarismesh/polaris-go v1.7.1-snapshot

replace github.com/polarismesh/polaris-go => ../../../
```

## 8. 验证步骤

```bash
# 编译示例
cd examples/$FEATURE_NAME/$SCENARIO
go build -o example .

# 确认可运行（可能需要 Polaris 服务端）
./example --help
```

## 9. 检查清单（每次开发必须自检）

### 9.1 基础规约

- [ ] 已导入 `github.com/polarismesh/polaris-go/api`
- [ ] 变量声明中包含 `debug bool`
- [ ] `initArgs()` 中注册了 `-debug` 参数
- [ ] `main()` 第一行设置了 `log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds | log.Lshortfile)`
- [ ] `flag.Parse()` 之后存在根据 `debug` 切换 SDK 日志级别的代码块

### 9.2 目录与依赖

- [ ] 目录结构完整（`examples/$FEATURE_NAME/$SCENARIO/{Makefile,go.mod,go.sum,main.go,polaris.yaml}`）
- [ ] go.mod 使用 `replace` 指向本地 `../../../../`
- [ ] `go build` 可成功编译
- [ ] `./example --help` 可正常打印

### 9.3 HTTP 日志（仅对带 HTTP handler 的 demo）

- [ ] 每个 handler 入口第一行生成 reqID
- [ ] handler 内及子函数所有 `log.Printf` 都带 `[reqID]` 前缀
- [ ] 多跳调用链中 reqID 通过 `X-Request-ID` header 透传
- [ ] handler 出口调用 `logReply` 打印响应

### 9.4 代码质量

- [ ] main.go 可编译运行
- [ ] 包含必要的配置文件
- [ ] 代码包含 `//` 许可证头（`Copyright (C) 2021 Tencent. All rights reserved.`）
