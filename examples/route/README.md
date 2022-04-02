#Polaris Go

[中文文档](./README-zh.md)

## Polaris uses the service routing function

Polaris supports dynamic scheduling of online traffic based on request tags, instance tags, and tag matching rules, which can be applied to various scenarios such as proximity by region, unit isolation, and canary release.

## How to build

> provider

Build directly on go mod

- linux/mac build command
````
cd ./provider
go build -o provider
````
- windows build command
````
cd ./consumer
go build -o provider.exe
````

> consumer

- linux/mac build command
````
cd ./consumer
go build -o consumer
````
- windows build command
````
cd ./consumer
go build -o consumer.exe
````


## how to use

### Create service

Create the corresponding service through the Polaris console in advance. If it is installed through the local one-click installation package, open the console directly in the browser through 127.0.0.1:8080

![create_service](./image/create_service.png)

### Create routing rules

![create_service_rule](./image/create_service_rule.png)

### Change setting

To specify the address of the Polaris server, you need to edit the polaris.yaml file and fill in the server address

````
global:
  serverConnector:
    addresses:
    - 127.0.0.1:8091
````
### execute program

Directly execute the generated executable program, for the provider process

> provider

- linux/mac run command
````
./provider --port=20000 --metadata="env=dev" > provider-20000.log 2>&1 &
./provider --port=20001 --metadata="env=test" > provider-20001.log 2>&1 &
./provider --port=20002 --metadata="env=pre" > provider-20002.log 2>&1 &
./provider --port=20003 --metadata="env=prod" > provider-20003.log 2>&1 &
````

- windows run command
````
./provider.exe --port=20000 --metadata="env=dev" > provider-20000.log 2>&1 &
./provider.exe --port=20001 --metadata="env=test" > provider-20001.log 2>&1 &
./provider.exe --port=20002 --metadata="env=pre" > provider-20002.log 2>&1 &
./provider.exe --port=20003 --metadata="env=prod" > provider-20003.log 2>&1 &
````

> consumer


- linux/mac run command
````
./consumer --selfNamespace={selfName} --selfService=EchoConsumer
````

- windows run command
````
./consumer.exe --selfNamespace={selfName} --selfService=EchoConsumer
````

### verify

Route to different service instances by setting the value of the request header parameter ***env***

````
curl -H 'env: pre' http://127.0.0.1:18080/echo

Hello, I'm EchoServerGolang Provider env=pre
```` 