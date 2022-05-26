# Polaris Go

English | [中文](./README-zh.md)

## Using Service Routing Function

Polaris support according to the request label, instance tag, and label matching rules, the line traffic is dynamically scheduled, which can be applied to a variety of scenes such as the region, unitized isolation and Canary release.

## How to use

### Build an executable

Build provider

```
# linux/mac
cd ./provider
go build -o provider

# windows
cd ./consumer
go build -o provider.exe
```

Build consumer

```
# linux/mac
cd ./consumer
go build -o consumer

# windows
cd ./consumer
go build -o consumer.exe
```

### Enter console

Create a corresponding service through the Arctic Star Console, if you are installed by a local one-click installation package, open the console directly on the browser through 127.0.0.1:8080

### Create routing rules

![create_service_rule](./image/create_service_rule.png)

### Change setting

Specify the Arctic Star server address, you need to edit the Polaris.yaml file, fill in the server address.

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```
### execute program

Run the built **provider** executable

```
# linux/mac
./provider --metadata="env=dev" > provider-20000.log 2>&1 &
./provider --metadata="env=test" > provider-20001.log 2>&1 &
./provider --metadata="env=pre" > provider-20002.log 2>&1 &
./provider --metadata="env=prod" > provider-20003.log 2>&1 &

# windows
./provider.exe --metadata="env=dev" > provider-20000.log
./provider.exe --metadata="env=test" > provider-20001.log
./provider.exe --metadata="env=pre" > provider-20002.log
./provider.exe --metadata="env=prod" > provider-20003.log
```

Run the built **consumer** executable

> consumer

```
# linux/mac
./consumer --selfNamespace={selfName} --selfService=EchoConsumer

# windows
./consumer.exe --selfNamespace={selfName} --selfService=EchoConsumer
```

### Verify

Realize the route to different service instances by setting the value of the request header **env**

```
curl -H 'env: pre' http://127.0.0.1:18080/echo

Hello, I'm RouteEchoServer Provider, My metadata's : env=pre, host : x.x.x.x:x
```