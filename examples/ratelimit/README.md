# QuickStart

[中文文档](./README-zh.md)

## Polaris uses the service current limit function 

Polaris supports access current limiting for different request sources and system resources to prevent services from being overwhelmed. 

## How To Build

- linux/mac build command
```
go build -o ratelimit
```
- windows build command
```
go build -o ratelimit.exe
```

## How To Use

### Create Service

Create the corresponding service through the Polaris console in advance. If it is installed through a local one-click installation package, open the console directly in the browser through 127.0.0.1:8091

![create_service](./image/create_service.png)

### Create RateLimit Rule

![create_service_ratelimit](./image/create_service_ratelimit.png)

### Create Service Instance

![create_service_instances](./image/create_service_instances.png)

### Change setting

To specify the Polaris server address, you need to edit the polaris.yaml file and fill in the server address

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### Execute Program

- linux/mac run command
```
./ratelimit --service={your service name} --namespace={your namespace name} --labels={your labels, ex: k1:v1,k2:v2} --concurrency={your request concurrency}
```

- windows run command
```
./ratelimit.exe --service={your service name} --namespace={your namespace name} --labels={your labels, ex: k1:v1,k2:v2} --concurrency={your request concurrency}
```

### Desired result

After running, the three coroutines will finally print `quota-ret: 0`, indicating that they have obtained the requested quota; the two coroutines will print `quota-ret: -1`, indicating that the current flow is restricted 

```
➜  ratelimit git:(feat_demo) ✗ ./ratelimit --service=polaris_go_provider --labels="env:test,method:GetUser" --concurrency=5 
2021/12/12 18:10:45 labels: map[env:test method:GetUser]
2021/12/12 18:10:45 thread 2 starts, sleep 23ms
2021/12/12 18:10:45 thread 3 starts, sleep 21ms
2021/12/12 18:10:45 thread 1 starts, sleep 21ms
2021/12/12 18:10:45 thread 4 starts, sleep 21ms
2021/12/12 18:10:45 thread 0 starts, sleep 22ms
2021/12/12 18:10:45 receive quit signal urgent I/O condition
2021/12/12 18:10:45 stop closed
2021/12/12 18:10:45 thread 0 request quota-ret : 0
2021/12/12 18:10:45 thread 3 request quota-ret : 0
2021/12/12 18:10:45 thread 1 request quota-ret : 0
2021/12/12 18:10:45 thread 2 request quota-ret : -1
2021/12/12 18:10:45 thread 4 request quota-ret : -1
2021/12/12 18:10:45 thread 0 stops
2021/12/12 18:10:45 thread 2 stops
2021/12/12 18:10:45 thread 3 stops
2021/12/12 18:10:45 thread 1 stops
2021/12/12 18:10:45 thread 4 stops
2021/12/12 18:10:45 total Pass 3, all 5
```