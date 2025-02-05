# Polaris Go

English | [中文](./README-zh.md)

## Use troubleshooting

Polaris supports timely fuse from abnormal services, interfaces, examples, or instance packets, and reduce the request failure rate.
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

### Set CircuitBreaker Rule

![create_circuitbreaker](./image/create_circuitbreaker.png)

### Change setting

Specify the Arctic Star server address, you need to edit the Polaris.yaml file, fill in the server address.

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### Execute program

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

Hello, My host : 127.0.0.1:8888
Hello, My host : 127.0.0.1:9999
...
Hello, My host : 127.0.0.1:9999

-- Close any provider，request again

"Hello, My host : %s:%d", svr.host, svr.port
dial tcp 127.0.0.1:37907: connect: connection refused

<trigger the cirtcuitbreaker>

it's fallback
...
