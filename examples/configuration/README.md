# Polaris Go

[中文文档](./README-zh.md)

## Use service configuration function

Quickly experience the configuration management capabilities of Polaris

## How to use

### Build an executable

Build Configuration

```shell
# linux/mac
cd ./configuration
go build -o configuration

# windows
cd ./configuration
go build -o configuration.exe
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
config:
  configConnector:
    addresses:
      - 127.0.0.1:8093
```

### Execute program

Run the built **configuration** executable

```shell
# linux/mac
./configuration

# windows
./configuration.exe
```


### Sample code

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
	changeChan := make(chan model.ConfigFileChangeEvent)
	configFile.AddChangeListenerWithChannel(changeChan)

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