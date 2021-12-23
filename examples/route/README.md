# QuickStart

[中文文档](./README-zh.md)

## Polaris uses service routing function 

Polaris supports dynamic scheduling of online traffic based on request tags, instance tags, and tag matching rules, which can be applied to various scenarios such as geographical proximity, unitized isolation, and canary release. 

## How To Build

- linux/mac build command
```
go build -o route
```
- windows build command
```
go build -o route.exe
```

## How To Use

### Create Service

Create the corresponding service through the Polaris console in advance. If it is installed through a local one-click installation package, open the console directly in the browser through 127.0.0.1:8091

![create_service](./image/create_service.png)

### Create Route Rule

![create_service_rule](./image/create_service_rule.png)

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
./route --service="provider service name" --namespace="provider namespace name" --selfService="consumer service name" --selfNamespace="consumer namespace name"
```

- windows run command
```
./route.exe --service="provider service name" --namespace="provider namespace name" --selfService="consumer service name" --selfNamespace="consumer namespace name"
```

### Desired result

After running, it will finally print the service instance with the label `env=test` 

```
➜  route git:(feat_demo) ✗ ./route --service="polaris_go_provider"
2021/12/12 16:58:34 start to invoke GetInstancesRequest operation
2021/12/12 16:58:34 instance GetInstances 0 is 127.0.0.5:8080 metadata=>map[string]string{"env":"test", "protocol":"", "version":""}
2021/12/12 16:58:34 instance GetInstances 1 is 127.0.0.4:8080 metadata=>map[string]string{"env":"test", "protocol":"", "version":""}
```