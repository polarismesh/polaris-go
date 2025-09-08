# Polaris Go

[English](./README.md) | 中文

## 使用元数据路由功能

使用场景: 用户希望服务调用时候，能控制请求只调度到具备某些标签的节点，比如：过滤env:test的节点

## 如何使用

### 构建可执行文件

构建 provider

```
cd ./provider
make build
```

构建 consumer

```
cd ./consumer
make build
```

### 修改配置

- 方式一:指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址
```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

- 方式二: 设置环境变量POLARIS_SERVER,值为服务端地址
```shell
export POLARIS_SERVER=127.0.0.1
```

### 执行程序

- 运行构建出的**provider-a**可执行文件
```shell
cd provider-a
./bin --metadata "env=dev"
```

- 运行构建出的**provider-b**可执行文件
```shell
cd provider-b
./bin --metadata "env=prod"
```

- 运行构建出的**consumer**可执行文件

```shell
cd consumer
./bin --metadata "env=dev"
```

### 验证
- 由于consumer带有标签"env=dev", 因此只会请求到带有"env=dev"的provider实例
```
❯ for i in {1..20};do curl http://127.0.0.1:18090/echo -w '\n';echo $(date);sleep 0.1s;done

RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分03秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分04秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分05秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分05秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分05秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分05秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分05秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分06秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分07秒 CST
RouteNearbyEchoServer Consumer, MyLocInfo's : {"Region":"","Zone":"","Campus":""}, host : 10.64.44.79:18090 => Hello, I'm RouteEchoServer Provider, My metadata's : "env=dev", host : 10.64.44.79:51405

2025年 9月28日 星期日 16时04分07秒 CST
```