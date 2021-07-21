# Polaris Go

## 服务限流

### 1. 进行服务限流

北极星提供了服务限流的能力，被调段通过申请配额接口可以进行申请分布式配额，以判断本次请求是否需要处理
示例代码见：https://github.com/polarismesh/polaris-go/blob/master/sample/ratelimit/main.go
使用方式：
````
go build -mod=vendor
# 命令行格式：./syncrandom <命名空间> <服务名> <标签> <执行次数>
./ratelimit Test nearby-svc version:v1,tag:xxx 1
````

## sdk错误码

在使用sdk提供的各种功能时，有时会因为用户自身或者polaris server的原因发生一些错误，sdk归纳了不同类型的错误。

- [错误码列表](https://git.code.oa.com/polaris/polaris/wikis/%E8%AF%A6%E7%BB%86%E8%AE%BE%E8%AE%A1/%E5%8F%AF%E6%9C%8D%E5%8A%A1%E6%80%A7/sdk%E9%94%99%E8%AF%AF%E7%A0%81)

用户在初始化SDKContext时，最可能返回的错误码为INVALID_CONFIG、PLUGIN_NOT_FOUND、PLUGIN_INIT_ERROR，这主要是用户的配置出问题，导致SDKContext的初始插件等方面出错。

在使用ConsumerAPI和ProviderAPI的过程中，最可能返回的错误码为API_INVALID_ARGUMENT、API_TIMEOUT、SERVER_ERROR_RESPONSE、NETWORK_ERROR、INSTANCE_NOT_FOUND，可能由用户传的参数不对或者polaris server连接问题。

