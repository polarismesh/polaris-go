# Polaris Go

[English](./README.md) | 中文

## 使用服务限流功能

北极星支持针对不同的请求来源和系统资源进行访问限流，避免服务被压垮。

## 如何使用
### 构建可执行文件

构建 provider

```
# linux/mac
cd ./provider
go build -o provider

# windows
cd ./consumer
go build -o provider.exe
```

构建 consumer

```
# linux/mac
cd ./consumer
go build -o consumer

# windows
cd ./consumer
go build -o consumer.exe
```

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8080打开控制台

### 创建限流规则

![create_service_ratelimit](./image/create_service_ratelimit.png)

### 修改配置

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### 执行程序

运行构建出的**provider**可执行文件

```
# linux/mac运行命令
./provider

# windows运行命令
./provider.exe
```

运行构建出的**consumer**可执行文件

```
# linux/mac运行命令
./provider

# windows运行命令
./provider.exe
```

### 验证

快速的发起多次**curl**请求命令

```
-- 第一次发起请求
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo

Hello, I'm RateLimitEchoServer Provider, My host : %s:%d

-- 中途频繁发起 curl 请求

...

-- 触发限流的 curl 请求
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo

Too Many Requests
```