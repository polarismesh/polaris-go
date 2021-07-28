polaris-go
========================================
Polaris is an operation centre that supports multiple programming languages, with high compatibility to different application framework. Polaris-go is golang SDK for Polaris.

## Overview

Polaris-go provide features listed as below:

* ** Service instance registration, and health check
   
   Provides API on/offline registration instance information,  with regular report to inform caller server's healthy status. 

* ** Service discovery
 
   Provides multiple API, for users to get a full list of server instance, or get one server instance after route rule filtering and loadbalancing, which can be applied to srevice invocation soon.

* ** Service circuitbreaking
   
   Provide API to report the invocation result, and conduct circuit breaker instance/group insolation based on collected data, eventually recover when the system allows. 

* ** Service ratelimiting

   Provides API for applications to conduct quota check and deduction, supports rate limit  policies that are based on server level and port.

## Quick Guide

### Preconditions

 - Launch Polaris server locally.
 - Create services from polaris console 

### Service Instance Registration

For the entire code instance, please refer to component provider/register's example, the key logics are listed below：
````
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

### Service Instance Deregistration

For the entire code instance, please refer to component provider/deregister's example, the key logics are listed below:
````
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

### Service Instance HeartBeating 

For the entire code instance, please refer to component provider/deregister's example, the key logics are listed below:
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

### Service Discovery

#### Get individual service instance（through service routing and loadbalancing）

For the entire code instance, please refer to component consumer/getoneinstance's example, the key logics are listed below:
````
var getInstancesReq *api.GetOneInstanceRequest
getInstancesReq = &api.GetOneInstanceRequest{}
getInstancesReq.Namespace = namespace
getInstancesReq.Service = service
getInstancesReq.SourceService = svcInfo
getInstancesReq.LbPolicy = lbPolicy
getInstancesReq.HashKey = []byte(hashKey)
startTime := time.Now()
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

#### Get whole list of service instances

For the entire code instance, please refer to component consumer/getallinstances's example, the key logics are listed below:
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

### Service CircuitBreaking

For the entire code instance, please refer to component consumer/circuitbreak's example, the key logics are listed below: 
````
//service discovery，pull instance, report invoke result 
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
svcCallResult := &api.ServiceCallResult{}
svcCallResult.SetCalledInstance(targetInstance)
svcCallResult.SetRetStatus(retStatus)
svcCallResult.SetRetCode(retCode)
svcCallResult.SetDelay(30 * time.Millisecond)
err = consumer.UpdateServiceCallResult(svcCallResult)
if nil != err {
    log.Fatal(err)
}
````

### Service Ratelimiting

For the entire code instance, please refer to component provider/ratelimit's example, the key logics are listed below: 
````
quotaReq := api.NewQuotaRequest()
quotaReq.SetNamespace(namespace)
quotaReq.SetService(service)
quotaReq.SetLabels(labels)
future, err := limitAPI.GetQuota(quotaReq)
if nil != err {
    log.Fatalf("fail to getQuota, err %v", err)
}
resp := future.Get()
if api.QuotaResultOk == resp.Code {
    log.Printf("quota result ok")
} else {
    log.Printf("quota result fail, info is %s", resp.Info)
}
````

## License

The polaris-go is licensed under the BSD 3-Clause License. Copyright and license information can be found in the file [LICENSE](LICENSE)