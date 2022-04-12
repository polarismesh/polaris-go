# Polaris Go

[中文文档](./README-zh.md)

## Illustrate

The quick experience DEMO is divided into two parts

- consumer : exposes a `GET:/echo` method for users through `http` agreement
- provier : exposed a `GET:/echo` by `http` agreement for home adjustment service

Call relationship diagram is as follows

![](./quickstart-demo.png)

## How to use

### Build command

- build provider executable file

```
# linux/mac 
cd ./provider
go build -o provider

# windows
cd ./consumer
go build -o provider.exe
```

- build consumer executable file

```
# linux/mac
cd ./consumer
go build -o consumer

# windows
cd ./consumer
go build -o consumer.exe
```
### Change setting

Specify the Arctic Star server address, you need to edit the Polaris.yaml file, fill in the server address.

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### Execute program

- run the executable of the provider

```
# linux/mac
./provider

# windows
./provider.exe
```

- run the executable of the consumer


```
# linux/mac
./consumer

# windows
./consumer.exe
```

### Verify

```
curl http://127.0.0.1:18080/echo

Hello, I'm EchoServerGolang Provider
```