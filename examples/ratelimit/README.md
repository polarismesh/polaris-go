# Polaris Go

[中文文档](./README-zh.md)

## Use service limited stream function

Polaris supports access to different request sources and system resources, avoiding service being crushed.

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

### Enter the console

Create a corresponding service through the Arctic Star Console, if you are installed by a local one-click installation package, open the console directly on the browser through 127.0.0.1:8080

### Create a streaming rule

![create_service_ratelimit](./image/create_service_ratelimit.png)

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
./provider

# windows
./provider.exe
```

Run the built-in **consumer** executable

```
# linux/mac
./provider

# windows
./provider.exe
```

### Verify

Quick initiatures multiple times **curl** request command

```
-- First initiative
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo

Hello, I'm RateLimitEchoServer Provider, My host : %s:%d

-- Travel in the middle of the CURL request

...

-- Trigger a current limit CURL request
curl -H 'user-id: polaris' http://127.0.0.1:18080/echo

Too Many Requests
```