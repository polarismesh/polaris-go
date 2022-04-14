# Polaris Go

[中文文档](./README-zh.md)

## Examples of Polaris Active Detection Function

Polaris supports the active detection capability triggered by the main tuning terminal, and supports two detection modes

- Continuous detection: Perform continuous detection on all service instances cached by the client. If the detection fails, it will be fused, and if the detection succeeds, it will be half-open.

- Detection during failure: When the service instance is blown, it will be detected, and the detection will be half-open if it succeeds.

Support the following two detection protocols, which can be used at the same time, or only one of them can be used

- HTTP protocol: Supports detection of the HTTP interface of the called end, and the user can specify http path, header and other information

- TCP protocol: support port detection in tcp connect mode 

## How To Build

- linux/mac build command
```
go build -o activehealthcheck
```
- windows build command
```
go build -o activehealthcheck.exe
```

## How To Use

### Create Service

Create the corresponding service through the Polaris console in advance. If it is installed through a local one-click installation package, open the console directly in the browser through 127.0.0.1:8080

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
./activehealthcheck --service="your service name" --namespace="your namespace name"
```

- windows run command
>>>>>>> upstream/main
```
./activehealthcheck.exe --service="your service name" --namespace="your namespace name"
```

### Desired result

After running, it will finally print out the fusing status of each instance. The status is open, which means it is fused, and the status is close, which means it can continue to provide services.
After the health check, only one instance is closed, the others are open 

```
2021/09/18 16:58:02 instance after activehealthcheck 0 is 127.0.0.1:2003, status is open
2021/09/18 16:58:02 instance after activehealthcheck 1 is 127.0.0.1:2005, status is open
2021/09/18 16:58:02 instance after activehealthcheck 2 is 127.0.0.1:2002, status is open
2021/09/18 16:58:02 instance after activehealthcheck 3 is 127.0.0.1:2004, status is open
2021/09/18 16:58:02 instance after activehealthcheck 4 is 127.0.0.1:2001, status is close
```