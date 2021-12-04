# Polaris Go

## 北极星被调端样例

北极星被调端样例包含了最简单的被调端基本操作，包含以下3个操作场景：

- 服务实例注册（Register）

- 服务实例心跳上报（Heartbeat）

- 服务实例反注册（Deregister）

## 如何构建

直接依赖go mod进行构建

- linux/mac构建命令

```
go build -o provider
```

- windows构建命令

```
go build -o provider.exe
```

## 如何使用

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8091打开控制台

### 修改配置

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### 执行程序

直接执行生成的可执行程序

- linux/mac运行命令

```
./provider --service="your service name" --namespace="your namespace name" --host="your ip address" --port=your_port
```

- windows运行命令

```
./provider.exe --service="your service name" --namespace="your namespace name" --host="your ip address" --port=your_port
```
