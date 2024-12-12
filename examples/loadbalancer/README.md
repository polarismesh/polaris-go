# Polaris Go

English | [中文](./README-zh.md)

## Using Load Balancing Feature

Experience the load balancing capability of Polaris quickly.

## How to use

### Build an executable

Build provider

```
# linux/mac
cd ./provider
go build -o provider

# windows
cd ./provider
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

### Accessing the Console

Create a corresponding service through the Arctic Star Console, if you are installed by a local one-click installation package, open the console directly on the browser through 127.0.0.1:8080

### Modifying Configuration

Specify the Polaris server address by editing the polaris.yaml file and filling in the server address.

Specify the load balancing strategy configuration:

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
consumer:
  loadbalancer:
    type: weightedRandom
```

### Execute program

Run the built **provider** executable

Run multiple providers on different nodes or specify different ports using the --port parameter. Run multiple providers on the same node.

```
# linux/mac
./provider

# windows
./provider.exe
```

Run the built **consumer** executable

```
# linux/mac
./consumer

# windows
./consumer.exe
```


### Verify

Requests will be load balanced to different providers.

```
curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.10:32451

curl http://127.0.0.1:18080/echo
Hello, I'm LoadBalanceEchoServer Provider, My host : 10.10.0.11:31102
```