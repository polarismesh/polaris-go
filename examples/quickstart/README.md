# Polaris Go

[中文文档](./README-zh.md)

## Use service registration discovery function

Quickly experience the service registration and service discovery capabilities of Arctic Stars

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

### Enter the console

Create a corresponding service through the Arctic Star Console, if you are installed by a local one-click installation package, open the console directly on the browser through 127.0.0.1:8080

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

Run the built **consumer** executable

```
# linux/mac
./provider

# windows
./provider.exe
```


### Verify

```
curl http://127.0.0.1:18080/echo

Hello, I'm DiscoverEchoServer Provider, My host : %s:%d
```