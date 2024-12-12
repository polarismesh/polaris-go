# Polaris Go

[English](./README.md) | 中文

## 使用负载均衡功能

快速体验北极星的负载均衡能力

## 如何使用

### 构建可执行文件

构建 provider

```
# linux/mac
cd ./provider
go build -o provider

# windows
cd ./provider
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

### 进入控制台

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8080打开控制台

### 修改配置

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

指定负载均衡策略配置

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
consumer:
  loadbalancer:
    type: weightedRandom
```

### 执行程序

运行构建出的**provider**可执行文件

在不同的节点运行多个provider，或通过--port参数指定不同端口, 在同一个节点运行多个provider

```
# linux/mac运行命令
./provider

# windows运行命令
./provider.exe
```

运行构建出的**consumer**可执行文件

```
# linux/mac运行命令
./consumer

# windows运行命令
./consumer.exe
```


### 验证
请求被负载均衡到各个provider

```
curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102
```
