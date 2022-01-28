# Polaris Go

[English Document](./README.md)

## 北极星使用服务路由功能

北极星支持根据请求标签、实例标签和标签匹配规则，对线上流量进行动态调度，可以应用于按地域就近、单元化隔离和金丝雀发布等多种场景。

## 如何构建

> provider

直接依赖go mod进行构建

- linux/mac构建命令
```
cd ./provider
go build -o provider
```
- windows构建命令
```
cd ./consumer
go build -o provider.exe
```

> consumer

- linux/mac构建命令
```
cd ./consumer
go build -o consumer
```
- windows构建命令
```
cd ./consumer
go build -o consumer.exe
```


## 如何使用

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8091打开控制台

![create_service](./image/create_service.png)

### 创建路由规则

![create_service_rule](./image/create_service_rule.png)

### 修改配置

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```
### 执行程序

直接执行生成的可执行程序, 对于provider进程

> provider

- linux/mac运行命令
```
./provider --port=20000 --metadata="env=dev" > provider-20000.log 2>&1 &
./provider --port=20001 --metadata="env=test" > provider-20001.log 2>&1 &
./provider --port=20002 --metadata="env=pre" > provider-20002.log 2>&1 &
./provider --port=20003 --metadata="env=prod" > provider-20003.log 2>&1 &
```

- windows运行命令
```
./provider.exe --port=20000 --metadata="env=dev" > provider-20000.log 2>&1 &
./provider.exe --port=20001 --metadata="env=test" > provider-20001.log 2>&1 &
./provider.exe --port=20002 --metadata="env=pre" > provider-20002.log 2>&1 &
./provider.exe --port=20003 --metadata="env=prod" > provider-20003.log 2>&1 &
```

> consumer


- linux/mac运行命令
```
./consumer --selfNamespace={selfName} --selfService=EchoConsumer
```

- windows运行命令
```
./consumer.exe --selfNamespace={selfName} --selfService=EchoConsumer
```

### 验证

通过设置请求头参数***env***的值，实现路由到不同的服务实例

```
curl -H 'env: pre' http://127.0.0.1:18080/echo

Hello, I'm EchoServerGolang Provider env=pre
```