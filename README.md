# Polaris Go

## QuickStart

#### 什么是polaris-go

polaris-go是名字服务北极星(Polaris)的GO语言数据面组件，主要提供服务发现、动态路由、负载均衡、故障熔断、服务限流等功能。

#### 获取polaris-go

polaris-go属于GO语言组件，可以通过go modules以及go path的方式进行获取，建议使用go modules进行获取。

- go modules：
  - 导入"github.com/polarismesh/polaris-go/api"的包，执行go mod tidy

#### 使用polaris-go

##### 1.使用北极星上下文

北极星的所有的接口方法都是通过API对象来进行提供。由于北极星的所有接口方法都是协程安全的。
因此，业务程序只需要将API对象声明为一个单例来使用即可，无需每次调用接口方法前都创建一个API对象。
API对象分为客户端API对象（ConsumerAPI），以及服务端API对象（ProviderAPI）两类，分别对应不同的使用场景。

- ConsumerAPI: 客户端API对象，提供服务发现，动态路由，负载均衡，服务熔断的能力。
- ProviderAPI: 服务端API对象，提供服务注册反注册、心跳上报，服务限流的能力
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

##### 2.配置北极星api对象

一般直接使用api.NewProviderAPI()和api.NewConsumerAPI()两个方法创建的使用默认配置的api对象就可以满足大多数需求，但是对于需要定制化配置的用户，polaris-go也提供了相应的接口。

ConsumerAPI和ProviderAPI本质上都是对于一个SDKContext对象的封装，使用同一个底层，由于这样的设计，它们的配置也是一样的，用户可以通过创建一个配置对象，修改配置对象，再使用这个配置对象创建ConsumerAPI和ProviderAPI，从而实现定制两种api对象的行为。

```go
import (
  "github.com/polarismesh/polaris-go/api"
)

func useSDK() {
  //创建默认的配置对象
  configuration := api.NewConfiguration()
  //设置北极星server的地址
  configuration.GetGlobal().GetServerConnector().SetAddresses([]string{"127.0.0.1:8090"})
  //设置连接北极星server的超时时间
  configuration.GetGlobal().GetServerConnector().SetConnectTimeout(2*time.Second)
  // 利用修改了的配置对象，创建ConsumerAPI
  consumer, err := api.NewConsumerAPIByConfig(configuration)
  // 销毁consumerAPI对象
  // 切记，consumerAPI对象是可以作为全局变量使用的，因此建议销毁操作只有当进程退出才进行销毁
  consumer.Destroy()

  // 利用修改了的配置对象，创建ProviderAPI
  provider, err := api.NewProviderAPIByConfig(configuration)
  //销毁providerAPI对象
  provider.Destroy()

  // 同时作为主调和被调使用
  // 由于北极星的API对象都是通过Context来进行资源（包括协程、缓存）管理的，每个Context对象所管理的资源都是独立的
  // 因此为节省资源的损耗，对于同时作为主调端和被调端的场景，可以使用同一个Context来创建API
  // 1.创建consumerAPI
  consumer, err := api.NewConsumerAPIByConfig(configuration)
  // 2. 使用consumerAPI的Context创建providerAPI
  provider := api.NewProviderAPIByContext(consumer.SDKContext())
  // 由于consumer和provider都使用了同一个context，因此，销毁的时候，只需要销毁其中一个就行
  consumer.Destroy()
  // 使用相同context的情况下，consumer销毁后，provider也紧接着会被销毁
}
```

上面的示例里面，展示了修改api对象的北极星server地址和连接server超时时间的方式。实际上，api对象的配置还有其他类型，完整的配置结构参考

- [配置模型](https://iwiki.woa.com/pages/viewpage.action?pageId=459081537)

基本上每种配置项都可以通过和上面类似的方法进行修改，即通过Get方法获取总体配置对象里面的具体配置项，再调用相应的Set方法设置配置项的值。

### 3.随机获取单个服务实例

北极星默认提供随机负载均衡策略，可以通过随机的方式从可用的服务实例中选择一个实例进行返回
（走动态路由，以及随机负载均衡策略）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstance/syncrandom/main.go
使用方式：

````
go build -mod=vendor
# 命令行格式：./syncrandom <命名空间> <服务名>
./syncrandom --namespace=Test --service=nearby-svc
````

### 4.一致性hash（基于Hash环算法）获取单个服务实例

北极星默认提供基于割环法的一致性hash负载均衡策略，可以通过一致性hash的方式从可用的服务实例中选择一个实例进行返回
（走动态路由，以及一致性hash负载均衡策略）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstance/synchashring/main.go
使用方式：

````
go build -mod=vendor
# 命令行格式：./synchashring <命名空间> <服务名> 0 <执行时间>
./synchashring Test nearby-svc 0 1
````

### 5.一致性hash（基于Hash环算法）获取单个服务实例以及副本实例

北极星基于Hash环算法的一致性hash负载均衡策略，除了返回主节点外，还可以返回备份节点（当主节点不可用后，自动切换的下一个节点）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstance/synchashring/main.go
使用方式：

````
go build -mod=vendor
# 命令行格式：./synchashring <命名空间> <服务名> <备份节点数> <执行时间>
./synchashring Test nearby-svc 1 1
````

### 6.一致性hash（基于Maglev算法）获取单个服务实例

北极星默认提供基于谷歌Maglev算法的负载均衡策略（算法论文可参考https://static.googleusercontent.com/media/research.google.com/en//pubs/archive/44824.pdf），
可以通过随机的方式从可用的服务实例中选择一个实例进行返回
（走动态路由，以及maglev负载均衡策略）
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/getinstance/syncmaglev/main.go
使用方式：

````
go build -mod=vendor
# 命令行格式：./syncmaglev <命名空间> <服务名> <执行时间>
./syncmaglev Test nearby-svc 1
````

### 10. 获取服务token

以下接口为服务生命周期管理接口，调用接口时，需要获取服务唯一token，获取token方式请参考链接：https://iwiki.oa.tencent.com/pages/viewpage.action?pageId=40342869（在打开的页面中搜索token关键字）

##### 11.注册服务实例

获得了ProviderAPI之后，用户可以利用其方法向polaris server注册某个服务的实例。
示例代码见：https://github.com/polarismesh/polaris-go/tree/master/sample/lifecycle/register/main.go

````
go build -mod=vendor
# 命令行格式：./register <命名空间> <服务名> <token> <ip> <port>
./register Test nearby-svc xxx-token 127.0.0.1 1010
````

##### 12.反注册服务实例

与注册实例相对应的，ProviderAPI也可以反注册某个服务的实例。
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/dereigster/main.go

````
go build -mod=vendor
# 命令行格式：./dereigster <命名空间> <服务名> <token> <ip> <port>
./dereigster Test nearby-svc xxx-token 127.0.0.1 1010
````

##### 13.心跳上报

ProviderAPI还提供了向polaris server发送心跳以说明自身健康状态的功能
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/heartbeat/main.go

````
go build -mod=vendor
# 命令行格式：./heartbeat <命名空间> <服务名> <token> <ip> <port> <上报次数>
./heartbeat Test nearby-svc xxx-token 127.0.0.1 1010 5
````

### 14.同时作为主调端和被调端

对于大部分业务场景，会存在同时作为被调端和主调端来使用，这时候，比较好的实践是ProviderAPI和ConsumerAPI使用同一个Context来进行创建
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/lifecycle/full/main.go

````
go build -mod=vendor
# 命令行格式：./heartbeat <命名空间> <服务名> <token> <ip> <port> <上报次数>
./full Test nearby-svc xxx-token 127.0.0.1 1010 5
````

### 15. 单线程进行服务限流

北极星提供了服务限流的能力，被调段通过申请配额接口可以进行申请分布式配额，以判断本次请求是否需要处理

简单的单线程限流示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/ratelimit/singlethread/main.go
使用方式：

````
go build -mod=vendor
# 命令行格式：./singlethread <命名空间> <服务名> <标签> <执行次数>
./singlethread Test nearby-svc version:v1,tag:xxx 1
````

### 16.多线程进行服务限流

北极星也提供了模拟多个线程进行限流请求的实例代码：https://github.com/polarismesh/polaris-go/blob/master/sample/ratelimit/multithread/main.go

使用方式：

```
go build -mod=vendor
# 命令行格式：./multithread <并发线程数量> <命名空间> <服务名> <限流统计周期> <统计周期数> <标签>
./multithread 10 Test nearby-svc 60s 5 version:v1,tag:xxx
```

### 17. 服务实例change消息订阅
SDK提供了获取服务实例修改消息的接口：
ConsumerAPI WatchService  
该接口返回一个buffer channel, 用户从channel中读取消息即可

####1.原理
GO-SDK内部定时和北极星服务同步服务实例等，当发现服务实例对比SDK本地缓存有变化时，经过对比产生add、delete、update子事件类型（参考pkg\model\subscribe_event.go），
让后写进WatchService接口产生的消息订阅channel

####2.使用demo
sample\subscribe\main.go
```
go build -mod=vendor
# 命令行格式：./subscribe  命名空间> <服务名>
如： ./subscribe Test nearby-svc
```

#### sdk错误码

在使用sdk提供的各种功能时，有时会因为用户自身或者polaris server的原因发生一些错误，sdk归纳了不同类型的错误。

- [错误码列表](https://iwiki.woa.com/pages/viewpage.action?pageId=219385779)

用户在初始化SDKContext时，最可能返回的错误码为INVALID_CONFIG、PLUGIN_NOT_FOUND、PLUGIN_INIT_ERROR，这主要是用户的配置出问题，导致SDKContext的初始插件等方面出错。

在使用ConsumerAPI和ProviderAPI的过程中，最可能返回的错误码为API_INVALID_ARGUMENT、API_TIMEOUT、SERVER_ERROR_RESPONSE、NETWORK_ERROR、INSTANCE_NOT_FOUND，可能由用户传的参数不对或者polaris server连接问题。

