# Polaris Go

## 北极星主调端样例

北极星主调端样例包含了最简单的客户端基本操作，包含以下2个操作场景：

- 根据服务名获取全量服务实例（GetAllInstances）

- 根据服务名获取单个服务实例（GetOneInstance）

## 如何构建

直接依赖go mod进行构建

- linux/mac构建命令
```
go build -o consumer
```
- windows构建命令
```
go build -o consumer.exe
```

## 如何使用

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

直接执行生成的可执行程序

- linux/mac运行命令
```
./consumer --service="your service name" --namespace="your namespace name"
```

- windows运行命令
```
./consumer.exe --service="your service name" --namespace="your namespace name"
```
