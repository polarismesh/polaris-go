# Polaris Go

## 服务生命周期管理

### 1.使用北极星上下文

北极星的所有的接口方法都是通过API对象来进行提供。由于北极星的所有接口方法都是协程安全的。
因此，业务程序只需要将API对象声明为一个单例来使用即可，无需每次调用接口方法前都创建一个API对象。
API对象分为客户端API对象（ConsumerAPI）、服务端API对象（ProviderAPI）、限流API（LimitAPI），分别对应不同的使用场景。
- ConsumerAPI: 客户端API对象，提供服务发现，动态路由，负载均衡，服务熔断的能力。
- ProviderAPI: 服务端API对象，提供服务注册反注册、心跳上报，服务限流的能力
- LimitAPI: 限流API对象，提供服务服务限流频控的能力
- 使用方式：
```go
import (
  "github.com/polarismesh/polaris-go/api"
)

func useSDK() {
  // 作为主调端使用，直接创建ConsumerAPI
  consumer, err := api.NewConsumerAPI()
  // 销毁consumerAPI对象
  // 切记，consumerAPI对象是可以作为全局变量使用的，因此建议销毁操作只有当进程退出才进行销毁
  consumer.Destroy()

  // 作为被调端使用，直接创建ProviderAPI
  provider, err := api.NewProviderAPI()
  //销毁providerAPI对象
  provider.Destroy()

  // 同时作为主调和被调使用
  // 由于北极星的API对象都是通过Context来进行资源（包括协程、缓存）管理的，每个Context对象所管理的资源都是独立的
  // 因此为节省资源的损耗，对于同时作为主调端和被调端的场景，可以使用同一个Context来创建API
  // 1.创建consumerAPI
  consumer, err := api.NewConsumerAPI()
  // 2. 使用consumerAPI的Context创建providerAPI
  provider := api.NewProviderAPIByContext(consumer.SDKContext())
  // 由于consumer和provider都使用了同一个context，因此，销毁的时候，只需要销毁其中一个就行
  consumer.Destroy()
  // 使用相同context的情况下，consumer销毁后，provider也紧接着会被销毁
}
```

### 2. 获取服务token

使用服务生命周期管理接口时，需要获取服务唯一token，获取token方式请参考链接：https://iwiki.oa.tencent.com/pages/viewpage.action?pageId=40342869

### 3.注册服务实例

获得了ProviderAPI之后，用户可以利用其方法向polaris server注册某个服务的实例。
示例代码见：https://github.com/polarismesh/polaris-go/tree/master/sample/lifecycle/register/main.go
````
go build -mod=vendor
# 命令行格式：./register <命名空间> <服务名> <token> <ip> <port>
./register Test nearby-svc xxx-token 127.0.0.1 1010
````

### 4.反注册服务实例

与注册实例相对应的，ProviderAPI也可以反注册某个服务的实例。
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/dereigster/main.go
````
go build -mod=vendor
# 命令行格式：./dereigster <命名空间> <服务名> <token> <ip> <port>
./dereigster Test nearby-svc xxx-token 127.0.0.1 1010
````

### 5.心跳上报

ProviderAPI还提供了向polaris server发送心跳以说明自身健康状态的功能
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/heartbeat/main.go
````
go build -mod=vendor
# 命令行格式：./heartbeat <命名空间> <服务名> <token> <ip> <port> <上报次数>
./heartbeat Test nearby-svc xxx-token 127.0.0.1 1010 5
````

### 6.同时作为主调端和被调端

对于大部分业务场景，会存在同时作为被调端和主调端来使用，这时候，比较好的实践是ProviderAPI和ConsumerAPI使用同一个Context来进行创建
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/full/main.go
````
go build -mod=vendor
# 命令行格式：./heartbeat <命名空间> <服务名> <token> <ip> <port> <上报次数>
./full Test nearby-svc xxx-token 127.0.0.1 1010 5
````

## sdk错误码

在使用sdk提供的各种功能时，有时会因为用户自身或者polaris server的原因发生一些错误，sdk归纳了不同类型的错误。

- [错误码列表](https://git.code.oa.com/polaris/polaris/wikis/%E8%AF%A6%E7%BB%86%E8%AE%BE%E8%AE%A1/%E5%8F%AF%E6%9C%8D%E5%8A%A1%E6%80%A7/sdk%E9%94%99%E8%AF%AF%E7%A0%81)

用户在初始化SDKContext时，最可能返回的错误码为INVALID_CONFIG、PLUGIN_NOT_FOUND、PLUGIN_INIT_ERROR，这主要是用户的配置出问题，导致SDKContext的初始插件等方面出错。

在使用ConsumerAPI和ProviderAPI的过程中，最可能返回的错误码为API_INVALID_ARGUMENT、API_TIMEOUT、SERVER_ERROR_RESPONSE、NETWORK_ERROR、INSTANCE_NOT_FOUND，可能由用户传的参数不对或者polaris server连接问题。

