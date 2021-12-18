# Polaris Go

[English Document](./README.md)

## 北极星主动探测功能实例

北极星支持主调端触发的主动探测能力，支持两种探测模式

- 持续探测：对客户端缓存的所有的服务实例，进行持续探测，探测失败则被熔断，探测成功则半开

- 故障时探测：当服务实例被熔断后，进行探测，探测成功则半开

支持以下2种探测协议，可同时使用，也可只使用其中一种

- HTTP协议：支持针对被调端的HTTP接口进行探测，用户可以指定http path, header等信息

- TCP协议：支持tcp connect模式的端口探测

## 如何构建

直接依赖go mod进行构建

- linux/mac构建命令
```
go build -o activehealthcheck
```
- windows构建命令
```
go build -o activehealthcheck.exe
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
./activehealthcheck --service="your service name" --namespace="your namespace name"
```

- windows运行命令
```
./activehealthcheck.exe --service="your service name" --namespace="your namespace name"
```

### 期望结果

运行后，最终会打印出每个实例的熔断状态，状态为open，代表的是被熔断，状态为close，代表是可继续提供服务。

经过健康检查后，只有一个实例的状态为close，其他都是open

```
2021/09/18 16:58:02 instance after activehealthcheck 0 is 127.0.0.1:2003, status is open
2021/09/18 16:58:02 instance after activehealthcheck 1 is 127.0.0.1:2005, status is open
2021/09/18 16:58:02 instance after activehealthcheck 2 is 127.0.0.1:2002, status is open
2021/09/18 16:58:02 instance after activehealthcheck 3 is 127.0.0.1:2004, status is open
2021/09/18 16:58:02 instance after activehealthcheck 4 is 127.0.0.1:2001, status is close
```