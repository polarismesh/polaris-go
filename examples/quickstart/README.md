# QuickStart

[中文文档](./README-zh.md)

## Catalog description

> provider

The Polaris adjusted terminal sample contains the simplest basic operation of the adjusted terminal

> consumer

Polaris main tuning terminal sample contains the simplest basic client operation

## How To Build

> provider

- linux/mac build command
```
cd ./provider
go build -o provider
```
- windows build command
```
cd ./consumer
go build -o provider.exe
```

> consumer

- linux/mac build command
```
cd ./consumer
go build -o consumer
```
- windows build command
```
cd ./consumer
go build -o consumer.exe
```

## How To Use 

### Create Service

Create the corresponding service through the Polaris console in advance. If it is installed through a local one-click installation package, open the console directly in the browser through 127.0.0.1:8091

### Change setting

To specify the Polaris server address, you need to edit the polaris.yaml file and fill in the server address

```
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
```

### Execute Program

Directly execute the generated executable program

> provider

- linux/mac run command
```
./provider
```

- windows run command
```
./provider.exe
```

> consumer


- linux/mac run command
```
./consumer
```

- windows run command
```
./consumer.exe
```

### Verify

```
curl http://127.0.0.1:18080/echo

Hello, I'm EchoServerGolang Provider
```