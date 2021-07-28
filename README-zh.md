polaris-go
========================================
北极星polaris是一个支持多种开发语言、兼容主流开发框架的服务治理中心。polaris-go是北极星的Go语言嵌入式服务治理SDK

## 概述

polaris-go提供以下功能特性：

* ** 服务实例注册，心跳上报
   
   提供API接口供应用上下线时注册/反注册自身实例信息，并且可通过定时上报心跳来通知主调方自身健康状态。

* ** 服务发现

   提供多种API接口，通过API接口，用户可以获取服务下的全量服务实例，或者获取通过服务治理规则过滤后的一个服务实例，可供业务获取实例后马上发起调用。

* ** 故障熔断
   
   提供API接口供应用上报接口调用结果数据，并根据汇总数据快速对故障实例/分组进行隔离，以及在合适的时机进行探测恢复。

* ** 服务限流

   提供API接口供应用进行配额的检查及划扣，支持按服务级，以及接口级的限流策略。

## 快速入门

### 前置准备

 - 在本地启动Polaris服务端。
 - 通过Polaris控制台创建好服务。

### 服务实例注册

完整的实例代码可参考组件examples下的provider/register的实现，以下为核心逻辑：
````
//注册服务
request := &api.InstanceRegisterRequest{}
request.Namespace = namespace
request.Service = service
request.ServiceToken = token
request.Host = ip
request.Port = port
request.SetTTL(2)
resp, err := provider.Register(request)
if nil != err {
    log.Fatalf("fail to register instance, err %v", err)
}
log.Printf("success to register instance, id is %s", resp.InstanceID)
````

### 服务实例反注册

完整的实例代码可参考组件examples下的provider/deregister的实现，以下为核心逻辑：
````
//反注册服务
request := &api.InstanceDeRegisterRequest{}
request.Namespace = namespace
request.Service = service
request.ServiceToken = token
request.Host = ip
request.Port = port
err = provider.Deregister(request)
if nil != err {
    log.Fatalf("fail to deregister instance, err %v", err)
}
log.Printf("success to deregister instance")
````

### 服务实例心跳上报

完整的实例代码可参考组件examples下的provider/deregister的实现，以下为核心逻辑：
````
hbRequest := &api.InstanceHeartbeatRequest{}
hbRequest.Namespace = namespace
hbRequest.Service = service
hbRequest.Host = ip
hbRequest.Port = port
hbRequest.ServiceToken = token
if err = provider.Heartbeat(hbRequest); nil != err {
    log.Printf("fail to heartbeat, error is %v", err)
} else {
    log.Printf("success to call heartbeat for index %d", i)
}
````

### 服务发现

#### 获取单个服务实例（走路由和负载均衡）

完整的实例代码可参考组件examples下的consumer/getoneinstance的实现，以下为核心逻辑：
````
var getInstancesReq *api.GetOneInstanceRequest
getInstancesReq = &api.GetOneInstanceRequest{}
getInstancesReq.Namespace = namespace
getInstancesReq.Service = service
getInstancesReq.SourceService = svcInfo
getInstancesReq.LbPolicy = lbPolicy
getInstancesReq.HashKey = []byte(hashKey)
startTime := time.Now()
//进行服务发现，获取单一服务实例
getInstResp, err := consumer.GetOneInstance(getInstancesReq)
if nil != err {
    log.Fatalf("fail to sync GetOneInstance, err is %v", err)
}
consumeDuration := time.Since(startTime)
log.Printf("success to sync GetOneInstance, count is %d, consume is %v\n",
    len(getInstResp.Instances), consumeDuration)
targetInstance := getInstResp.Instances[0]
log.Printf("sync instance is id=%s, address=%s:%d\n",
    targetInstance.GetId(), targetInstance.GetHost(), targetInstance.GetPort())
````

#### 获取全量服务实例

完整的实例代码可参考组件examples下的consumer/getallinstances的实现，以下为核心逻辑：
````
var getAllInstancesReq *api.GetAllInstancesRequest
getAllInstancesReq = &api.GetAllInstancesRequest{}
getAllInstancesReq.Namespace = namespace
getAllInstancesReq.Service = service

getAllInstResp, err := consumer.GetAllInstances(getAllInstancesReq)
if nil != err {
    log.Fatalf("fail to sync GetAllInstances, err is %v", err)
}
log.Printf("success to sync GetAllInstances, count is %d, revision is %s\n", len(getAllInstResp.Instances),
    getAllInstResp.Revision)
````

### 故障熔断

完整的实例代码可参考组件examples下的consumer/circuitbreak的实现，以下为核心逻辑：
````
//执行服务发现，拉取服务实例，以及上报调用结果
var getInstancesReq *api.GetOneInstanceRequest
getInstancesReq = &api.GetOneInstanceRequest{}
getInstancesReq.Namespace = namespace
getInstancesReq.Service = service
resp, err := consumer.GetOneInstance(getInstancesReq)
if nil != err {
    log.Fatal(err)
}
targetInstance := resp.GetInstances()[0]
addr := fmt.Sprintf("%s:%d", targetInstance.GetHost(), targetInstance.GetPort())
fmt.Printf("select address is %s\n", addr)
var retStatus model.RetStatus
var retCode int32
if target == addr {
    retStatus = api.RetFail
    retCode = 500
} else {
    retStatus = api.RetSuccess
    retCode = 200
}
//构造请求，进行服务调用结果上报
svcCallResult := &api.ServiceCallResult{}
//设置被调的实例信息
svcCallResult.SetCalledInstance(targetInstance)
//设置服务调用结果，枚举，成功或者失败
svcCallResult.SetRetStatus(retStatus)
//设置服务调用返回码
svcCallResult.SetRetCode(retCode)
//设置服务调用时延信息
svcCallResult.SetDelay(30 * time.Millisecond)
err = consumer.UpdateServiceCallResult(svcCallResult)
if nil != err {
    log.Fatal(err)
}
````

### 服务限流

完整的实例代码可参考组件examples下的provider/ratelimit的实现，以下为核心逻辑：
````
//创建访问限流请求
quotaReq := api.NewQuotaRequest()
//设置命名空间
quotaReq.SetNamespace(namespace)
//设置服务名
quotaReq.SetService(service)
//设置标签值
quotaReq.SetLabels(labels)
//调用配额获取接口
future, err := limitAPI.GetQuota(quotaReq)
if nil != err {
    log.Fatalf("fail to getQuota, err %v", err)
}
resp := future.Get()
if api.QuotaResultOk == resp.Code {
    //本次调用不限流，放通，接下来可以处理业务逻辑
    log.Printf("quota result ok")
} else {
    //本次调用限流判定不通过，调用受限，需返回错误给主调端
    log.Printf("quota result fail, info is %s", resp.Info)
}
````

## License

The polaris-go is licensed under the BSD 3-Clause License. Copyright and license information can be found in the file [LICENSE](LICENSE)

