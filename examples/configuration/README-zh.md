# Polaris Go

[English](./README.md) | 中文

## 使用配置管理功能

快速体验北极星的配置管理能力

## 如何使用

### 构建可执行文件

构建 Configuration

```shell
# linux/mac
cd ./configuration
go build -o configuration

# windows
cd ./configuration
go build -o configuration.exe
```

### 进入控制台

预先通过北极星控制台创建对应的服务，如果是通过本地一键安装包的方式安装，直接在浏览器通过127.0.0.1:8080打开控制台

### 修改配置

指定北极星服务端地址，需编辑polaris.yaml文件，填入服务端地址

```
global:
  serverConnector:
    addresses:
      - 127.0.0.1:8091
config:
  configConnector:
    addresses:
      - 127.0.0.1:8093
```

### 执行程序

运行构建出的**configuration**可执行文件

```shell
# linux/mac运行命令
./configuration

# windows运行命令
./configuration.exe
```


### 示例代码

```go
package main

import (
	"fmt"

	"github.com/polarismesh/polaris-go"
	"github.com/polarismesh/polaris-go/pkg/model"
)

func main() {
	configAPI, err := polaris.NewConfigAPI()

	if err != nil {
		fmt.Println("fail to start example.", err)
		return
	}

	// 获取远程的配置文件
	namespace := "default"
	fileGroup := "polaris-config-example"
	fileName := "example.yaml"

	configFile, err := configAPI.GetConfigFile(namespace, fileGroup, fileName)
	if err != nil {
		fmt.Println("fail to get config.", err)
		return
	}

	// 打印配置文件内容
	fmt.Println(configFile.GetContent())

	// 方式一：添加监听器
	configFile.AddChangeListener(changeListener)

	// 方式二：添加监听器
	changeChan := configFile.AddChangeListenerWithChannel()

	for {
		select {
		case event := <-changeChan:
			fmt.Println(fmt.Sprintf("received change event by channel. %+v", event))
		}
	}
}

func changeListener(event model.ConfigFileChangeEvent) {
	fmt.Println(fmt.Sprintf("received change event. %+v", event))
}


```