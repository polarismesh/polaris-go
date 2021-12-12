# Polaris Go

## 北极星使用服务熔断功能

北极星支持及时熔断异常的服务、接口、实例或者实例分组，降低请求失败率。

## 如何构建

直接依赖go mod进行构建

- linux/mac构建命令
```
go build -o circuitbreaker
```
- windows构建命令
```
go build -o circuitbreaker.exe
```

## 如何使用

### 创建服务

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8091打开控制台

![create_service](./image/create_service.png)

### 创建服务实例

![create_service_instances](./image/create_service_instances.png)

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
./circuitbreaker --service="your service name" --namespace="your namespace name"
```

- windows运行命令
```
./circuitbreaker.exe --service="your service name" --namespace="your namespace name"name"
```

### 期望结果

运行后，最终只会打印出没有被熔断的实例

```
➜  circuitbreaker git:(feat_demo) ✗ ./circuitbreaker --service=polaris_go_provider
2021/12/12 17:12:19 start to invoke GetInstancesRequest operation
2021/12/12 17:12:19 choose instances 127.0.0.2:8080 to circuirbreaker
2021/12/12 17:12:24 instance GetInstances 0 is 127.0.0.1:8080
2021/12/12 17:12:24 instance GetInstances 1 is 127.0.0.5:8080
2021/12/12 17:12:24 instance GetInstances 2 is 127.0.0.4:8080
2021/12/12 17:12:24 instance GetInstances 3 is 127.0.0.3:8080
```