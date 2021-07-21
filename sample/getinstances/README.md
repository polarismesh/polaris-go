# Polaris Go

## 获取服务列表

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

### 2.获取有效服务实例

通过接口，北极星会返回全量的有效服务实例（除去无效服务实例：被手动隔离以及权重为0的实例）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstances/main.go
使用方式
````
go build -mod=vendor
# 命令行格式：./getinstances <命名空间> <服务名> <是否返回全量:true/false> <执行时间>
./getinstances Test nearby-svc true 1
````

### 3.获取部分服务实例

通过接口，北极星会返回部分可用的服务实例
（走动态路由，根据用户的路由规则、就近规则以及服务健康状态等信息来过滤服务实例）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstances/main.go
使用方式
````
go build -mod=vendor
# 命令行格式：./getinstances <命名空间> <服务名> <是否返回全量:true/false> <执行时间>
./getinstances Test nearby-svc false 1
````

## sdk错误码

在使用sdk提供的各种功能时，有时会因为用户自身或者polaris server的原因发生一些错误，sdk归纳了不同类型的错误。

- [错误码列表](https://git.code.oa.com/polaris/polaris/wikis/%E8%AF%A6%E7%BB%86%E8%AE%BE%E8%AE%A1/%E5%8F%AF%E6%9C%8D%E5%8A%A1%E6%80%A7/sdk%E9%94%99%E8%AF%AF%E7%A0%81)

用户在初始化SDKContext时，最可能返回的错误码为INVALID_CONFIG、PLUGIN_NOT_FOUND、PLUGIN_INIT_ERROR，这主要是用户的配置出问题，导致SDKContext的初始插件等方面出错。

在使用ConsumerAPI和ProviderAPI的过程中，最可能返回的错误码为API_INVALID_ARGUMENT、API_TIMEOUT、SERVER_ERROR_RESPONSE、NETWORK_ERROR、INSTANCE_NOT_FOUND，可能由用户传的参数不对或者polaris server连接问题。

