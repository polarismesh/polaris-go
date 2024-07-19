# Polaris Go

[English](./README.md) | 中文

## 使用就近路由功能

北极星支持按照实例的地域信息实现按地域就近路由。

## 如何使用

### 构建可执行文件

构建 provider

```
# linux/mac构建命令
cd ./provider
go build -o provider

# windows构建命令
cd ./consumer
go build -o provider.exe
```

构建 consumer

```
# linux/mac构建命令
cd ./consumer
go build -o consumer

# windows构建命令
cd ./consumer
go build -o consumer.exe
```

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8080打开控制台

### 打开服务的就近路由

![open_service_nearby](./image/open_service_nearby.png)

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
for ((i=1; i<=3; i ++))
do
    # 设置地域信息
    export REGION=china
    export ZONE=ap-guangzhou
    export CAMPUS=ap-guangzhou-${i}
    
    # linux/mac运行命令
    ./provider > provider-20000.log 2>&1 &
then
```

运行构建出的**consumer**可执行文件

```
# 设置地域信息
export REGION=china
export ZONE=ap-guangzhou
export CAMPUS=ap-guangzhou-1

# linux/mac运行命令
./consumer --selfNamespace={selfName} --selfService=EchoConsumer

# windows运行命令
./consumer.exe --selfNamespace={selfName} --selfService=EchoConsumer
```

### 验证

实现根据实例的地域信息进行就近路由

```
curl -H 'env: pre' http://127.0.0.1:18080/echo

RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"china","Zone":"ap-guangzhou","Campus":"ap-guangzhou-5"}, host : 127.0.0.1:18080 => Hello, I'm RouteNearbyEchoServer Provider, MyLocInfo's : {"Region":"china","Zone":"ap-guangzhou","Campus":"ap-guangzhou-5"}, host : 127.0.0.1:37341
```