/**
 * Tencent is pleased to support the open source community by making polaris-go available.
 *
 * Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
 *
 * Licensed under the BSD 3-Clause License (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 * https://opensource.org/licenses/BSD-3-Clause
 *
 * Unless required by applicable law or agreed to in writing, software distributed
 * under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
 * CONDITIONS OF ANY KIND, either express or implied. See the License for the
 * specific language governing permissions and limitations under the License.
 */

package mock

import (
	"context"
	"crypto/sha1"
	"errors"
	"fmt"
	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"io"
	log2 "log"
	"strings"
	"sync"
	"time"
)

const (
	IsolatedStatus = iota
	UnhealthyStatus
)

var (
	//请求与应答的类型转换
	namingTypeReqToResp = map[namingpb.DiscoverRequest_DiscoverRequestType]namingpb.DiscoverResponse_DiscoverResponseType{
		namingpb.DiscoverRequest_UNKNOWN:     namingpb.DiscoverResponse_UNKNOWN,
		namingpb.DiscoverRequest_ROUTING:     namingpb.DiscoverResponse_ROUTING,
		namingpb.DiscoverRequest_CLUSTER:     namingpb.DiscoverResponse_CLUSTER,
		namingpb.DiscoverRequest_INSTANCE:    namingpb.DiscoverResponse_INSTANCE,
		namingpb.DiscoverRequest_RATE_LIMIT:  namingpb.DiscoverResponse_RATE_LIMIT,
		namingpb.DiscoverRequest_MESH_CONFIG: namingpb.DiscoverResponse_MESH_CONFIG,
		namingpb.DiscoverRequest_MESH:        namingpb.DiscoverResponse_MESH,
		namingpb.DiscoverRequest_SERVICES:    namingpb.DiscoverResponse_SERVICES,
	}
)

//操作类型
type OperationType string

const (
	//服务发现实例接口操作
	OperationDiscoverInstance OperationType = "discoverInstance"
	//服务发现路由接口操作
	OperationDiscoverRouting OperationType = "discoverRouting"
	//服务注册接口
	OperationRegistry OperationType = "registry"
	//服务反注册接口
	OperationDeRegistry OperationType = "deregistry"
	//健康检查接口
	OperationHeartbeat OperationType = "heartbeat"
	//同步等待时间
	syncWaitTime = 3 * time.Second
)

//测试桩相关接口
type NamingServer interface {
	namingpb.PolarisGRPCServer
	//设置模拟某个方法进行超时
	MakeOperationTimeout(operation OperationType, enable bool)
	//设置强制模拟方法超时
	MakeForceOperationTimeout(operation OperationType, enable bool)
	//设置方法超时时间
	SetMethodInterval(interval time.Duration)
	//设置打印返回的服务列表信息
	SetPrintDiscoverReturn(v bool)
	//设置mockserver是否返回异常
	SetReturnException(e bool)
	//设置是否自动注册网格的辅助服务
	SetNotRegisterAssistant(e bool)
	//注册服务
	RegisterService(svc *namingpb.Service) string
	//反注册服务
	DeregisterService(namespace, service string) *namingpb.Service
	//注册限流规则
	RegisterRateLimitRule(svc *namingpb.Service, rateLimit *namingpb.RateLimit) error
	//注销限流规则
	DeRegisterRateLimitRule(svc *namingpb.Service)
	//注册网格规则
	RegisterMeshConfig(svc *namingpb.Service, mtype string, mc *namingpb.MeshConfig)
	RegisterMesh(svc *namingpb.Service, mtype string, mc *namingpb.Mesh)
	//注销网格规则
	DeRegisterMeshConfig(svc *namingpb.Service, meshID, mtype string)
	//注册路由规则
	RegisterRouteRule(svc *namingpb.Service, routing *namingpb.Routing) error
	//反注册路由规则
	DeregisterRouteRule(svc *namingpb.Service)
	//注册命名空间
	RegisterNamespace(namespace *namingpb.Namespace)
	//反注册命名空间
	DeregisterNamespace(name string)
	//构建系统服务的路由规则
	BuildRouteRule(namespace string, name string) *namingpb.Routing
	//注册服务实例
	RegisterServerInstance(host string, port int, name string, token string, health bool) *namingpb.Instance

	RegisterServerInstanceReturnId(host string, port int, name string, token string, health bool) string
	//批量注册服务实例
	RegisterServiceInstances(svc *namingpb.Service, instances []*namingpb.Instance)
	//直接获取服务实例
	GetServiceInstances(key *model.ServiceKey) []*namingpb.Instance
	//注册系统服务，返回服务token
	RegisterServerService(name string) string
	//注册所有系统服务以及对应的服务实例
	RegisterServerServices(host string, port int)
	//清空某个测试服务的实例
	ClearServiceInstances(svc *namingpb.Service)
	//设置服务的元数据信息
	SetServiceMetadata(token string, key string, value string)
	//为服务生成N个随机服务实例
	GenTestInstances(svc *namingpb.Service, num int) []*namingpb.Instance
	//删除测试实例
	DeleteServerInstance(namespace string, service string, id string)
	//修改系统服务实例权重
	UpdateServerInstanceWeight(namespace string, service string, id string, weight uint32)
	//修改系统服务实例健康状态
	UpdateServerInstanceHealthy(namespace string, service string, id string, healthy bool)
	//修改系统服务实例隔离状态
	UpdateServerInstanceIsolate(namespace string, service string, id string, isolate bool)
	//产生测试用实例，带上地址端口号，权重随机生成
	GenTestInstancesWithHostPort(svc *namingpb.Service, num int, host string, startPort int) []*namingpb.Instance

	GenTestInstancesWithHostPortAndMeta(
		svc *namingpb.Service, num int, host string, startPort int, metadata map[string]string) []*namingpb.Instance
	//产生测试用实例，带上元数据，权重随机生成
	GenTestInstancesWithMeta(svc *namingpb.Service, num int, metadata map[string]string) []*namingpb.Instance
	//产生测试用实例，带上状态信息，权重随机生成
	GenInstancesWithStatus(svc *namingpb.Service, num int, st int, startPort int) []*namingpb.Instance
	//设置地域信息
	SetLocation(region, zone, campus string)
	//设置某个服务的实例
	SetServiceInstances(key *model.ServiceKey, insts []*namingpb.Instance)
	//获取地域信息
	GetLocation() (region, zone, campus string)
	//获取服务请求
	GetServiceRequests(key *model.ServiceKey) int
	//清空服务请求
	ClearServiceRequests(key *model.ServiceKey)
	//获取服务token
	GetServiceToken(key *model.ServiceKey) string
	//设置服务版本号
	SetServiceRevision(token string, revision string, k model.ServiceEventKey)
	//插入一个路由信息
	InsertRouting(svcKey model.ServiceKey, routing *namingpb.Routing)
	//设置某个服务的某个实例的状态（健康、隔离、权重）
	SetInstanceStatus(svcKey model.ServiceKey, idx int, healthy bool, isolate bool, weight uint32) error
	//设置首次不返回某个请求
	SetFirstNoReturn(svcKey model.ServiceEventKey)
	//反设置首次不返回某个请求
	UnsetFirstNoReturn(svcKey model.ServiceEventKey)
}

//Polaris server模拟桩
type namingServer struct {
	rwMutex               sync.RWMutex
	printReturn           bool
	svcInstances          map[model.ServiceKey][]*namingpb.Instance
	namespaces            map[string]*namingpb.Namespace
	instances             map[string]*namingpb.Instance
	services              map[string]*namingpb.Service
	serviceTokens         map[model.ServiceKey]string
	serviceRoutes         map[model.ServiceKey]*namingpb.Routing
	serviceRateLimits     map[model.ServiceKey]*namingpb.RateLimit
	serviceRequests       map[model.ServiceKey]int
	timeoutOperation      map[OperationType]bool
	timeoutIndex          map[OperationType]int
	forceTimeoutOperation map[OperationType]bool
	returnException       bool
	region                string
	zone                  string
	campus                string
	methodInterval        time.Duration
	firstNoReturnMap      map[model.ServiceEventKey]bool
	mesh                  map[model.ServiceKey]*namingpb.MeshConfig
	meshs                 map[model.ServiceKey]*namingpb.Mesh
	notRegisterAssistant  bool
	scalableRand          *rand.ScalableRand
}

//创建NamingServer模拟桩
func NewNamingServer() NamingServer {
	//
	//
	//
	ns := &namingServer{
		svcInstances:      make(map[model.ServiceKey][]*namingpb.Instance, 0),
		namespaces:        make(map[string]*namingpb.Namespace, 0),
		services:          make(map[string]*namingpb.Service, 0),
		serviceTokens:     make(map[model.ServiceKey]string, 0),
		serviceRequests:   make(map[model.ServiceKey]int, 0),
		instances:         make(map[string]*namingpb.Instance, 0),
		serviceRoutes:     make(map[model.ServiceKey]*namingpb.Routing, 0),
		serviceRateLimits: make(map[model.ServiceKey]*namingpb.RateLimit, 0),
		timeoutOperation:  make(map[OperationType]bool, 0),
		timeoutIndex: map[OperationType]int{
			OperationDiscoverInstance: -1,
			OperationDiscoverRouting:  -1,
			OperationRegistry:         -1,
			OperationDeRegistry:       -1,
			OperationHeartbeat:        -1,
		},
		forceTimeoutOperation: make(map[OperationType]bool, 0),
		region:                "A",
		zone:                  "a",
		campus:                "0",
		firstNoReturnMap:      make(map[model.ServiceEventKey]bool),
		mesh:                  make(map[model.ServiceKey]*namingpb.MeshConfig),
		meshs:                 make(map[model.ServiceKey]*namingpb.Mesh),
	}
	return ns
}

//设置是否跳过第一次的请求回复
func (n *namingServer) SetFirstNoReturn(svcKey model.ServiceEventKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.firstNoReturnMap[svcKey] = true
}

//反设置是否跳过第一次的请求回复
func (n *namingServer) UnsetFirstNoReturn(svcKey model.ServiceEventKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	delete(n.firstNoReturnMap, svcKey)
}

//设置某个服务的某个实例的状态（健康、隔离、权重）
func (n *namingServer) SetInstanceStatus(svcKey model.ServiceKey, idx int, healthy bool, isolate bool, weight uint32) error {
	n.rwMutex.Lock()
	instances, ok := n.svcInstances[svcKey]
	if !ok {
		return fmt.Errorf("instances of %s not exists", svcKey)
	}
	if idx < 0 || len(instances) <= idx {
		return fmt.Errorf("idx %d, out of range of instances of %s", idx, svcKey)
	}
	instances[idx].Healthy = &wrappers.BoolValue{Value: healthy}
	instances[idx].Isolate = &wrappers.BoolValue{Value: isolate}
	instances[idx].Weight = &wrappers.UInt32Value{Value: weight}
	n.rwMutex.Unlock()
	return nil
}

//设置模拟某个方法进行超时
func (n *namingServer) MakeOperationTimeout(operation OperationType, enable bool) {
	//
	//
	//
	n.timeoutOperation[operation] = enable
}

//设置强制模拟方法超时
func (n *namingServer) MakeForceOperationTimeout(operation OperationType, enable bool) {
	n.forceTimeoutOperation[operation] = enable
}

//设置方法超时时间
func (n *namingServer) SetMethodInterval(interval time.Duration) {
	n.methodInterval = interval
}

//注册实例
func (n *namingServer) RegisterInstance(ctx context.Context,
	req *namingpb.Instance) (*namingpb.Response, error) {
	//
	//
	//
	fmt.Printf("%v, RegisterInstance in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationRegistry) {
		time.Sleep(syncWaitTime)
	}
	key := &model.ServiceKey{
		Service:   req.GetService().GetValue(),
		Namespace: req.GetNamespace().GetValue()}
	if token, ok := n.serviceTokens[*key]; ok {
		if token != req.ServiceToken.GetValue() {
			return &namingpb.Response{
				Code: &wrappers.UInt32Value{Value: namingpb.Unauthorized},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &namingpb.Response{
			Code: &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
			Info: &wrappers.StringValue{Value: "not found resource"},
		}, nil
	}
	instances := n.svcInstances[*key]
	for i := 0; i < len(instances); i++ {
		if req.GetHost().GetValue() == instances[i].GetHost().GetValue() &&
			req.GetPort().GetValue() == instances[i].GetPort().GetValue() {
			return &namingpb.Response{
				Code:      &wrappers.UInt32Value{Value: namingpb.ExistedResource},
				Info:      &wrappers.StringValue{Value: "existed resource"},
				Namespace: n.namespaces[key.Namespace],
				Service:   n.services[req.ServiceToken.GetValue()],
				Instance:  nil,
			}, nil
		}
	}
	if req.Id == nil {
		req.Id = &wrappers.StringValue{Value: uuid.New().String()}
	}
	n.svcInstances[*key] = append(instances, req)
	n.instances[req.Id.GetValue()] = req
	return &namingpb.Response{
		Code:      &wrappers.UInt32Value{Value: namingpb.ExecuteSuccess},
		Info:      &wrappers.StringValue{Value: "execute success"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  req,
	}, nil
}

//反注册实例
func (n *namingServer) DeregisterInstance(ctx context.Context,
	req *namingpb.Instance) (*namingpb.Response, error) {
	//
	//
	//
	fmt.Printf("%v, DeregisterInstance in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationDeRegistry) {
		time.Sleep(syncWaitTime)
	}
	inst, ok := n.instances[req.Id.GetValue()]
	if ok {
		req = inst
	}
	//
	key := &model.ServiceKey{
		Service:   req.GetService().GetValue(),
		Namespace: req.GetNamespace().GetValue()}
	if token, ok := n.serviceTokens[*key]; ok {
		if token != req.ServiceToken.GetValue() {
			return &namingpb.Response{
				Code: &wrappers.UInt32Value{Value: namingpb.Unauthorized},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &namingpb.Response{
			Code: &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
			Info: &wrappers.StringValue{Value: "service not found"},
		}, nil
	}
	//
	//
	//
	//
	instances := n.svcInstances[*key]
	instNum := len(instances) - 1
	for i := 0; i <= instNum; i++ {
		if req.GetHost().GetValue() == instances[i].GetHost().GetValue() &&
			req.GetPort().GetValue() == instances[i].GetPort().GetValue() {
			delInstance := instances[i]
			instances[i] = instances[instNum]
			n.svcInstances[*key] = instances[0:instNum]
			delete(n.instances, req.Id.GetValue())
			return &namingpb.Response{
				Code:      &wrappers.UInt32Value{Value: namingpb.ExecuteSuccess},
				Info:      &wrappers.StringValue{Value: "execute success"},
				Namespace: n.namespaces[key.Namespace],
				Service:   n.services[req.ServiceToken.GetValue()],
				Instance:  delInstance,
			}, nil
		}
	}
	return &namingpb.Response{
		Code:      &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
		Info:      &wrappers.StringValue{Value: "instance not found"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  nil,
	}, nil
}

//心跳上报
func (n *namingServer) Heartbeat(ctx context.Context, req *namingpb.Instance) (*namingpb.Response, error) {
	fmt.Printf("%v, Heartbeat in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationHeartbeat) {
		time.Sleep(syncWaitTime)
	}
	inst, ok := n.instances[req.Id.GetValue()]
	if ok {
		req = inst
	}
	//
	//
	//
	//
	//
	key := &model.ServiceKey{
		Service:   req.GetService().GetValue(),
		Namespace: req.GetNamespace().GetValue()}
	if token, ok := n.serviceTokens[*key]; ok {
		if token != req.ServiceToken.GetValue() {
			return &namingpb.Response{
				Code: &wrappers.UInt32Value{Value: namingpb.Unauthorized},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &namingpb.Response{
			Code: &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
			Info: &wrappers.StringValue{Value: "service not found"},
		}, nil
	}
	//
	//
	//
	//
	//
	instances := n.svcInstances[*key]
	instNum := len(instances) - 1
	for i := 0; i <= instNum; i++ {
		if req.GetHost().GetValue() == instances[i].GetHost().GetValue() &&
			req.GetPort().GetValue() == instances[i].GetPort().GetValue() &&
			req.GetId().GetValue() == instances[i].GetId().GetValue() {
			return &namingpb.Response{
				Code:      &wrappers.UInt32Value{Value: namingpb.ExecuteSuccess},
				Info:      &wrappers.StringValue{Value: "execute success"},
				Namespace: n.namespaces[key.Namespace],
				Service:   n.services[req.ServiceToken.GetValue()],
				Instance:  instances[i],
			}, nil
		}
	}
	return &namingpb.Response{
		Code:      &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
		Info:      &wrappers.StringValue{Value: "instance not found"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  nil,
	}, nil
}

//是否忽略当前请求
func (n *namingServer) skipRequest(operation OperationType) bool {
	//
	//
	//
	forceTimeout := n.forceTimeoutOperation[operation]
	if forceTimeout {
		log.GetBaseLogger().Debugf("ignore request for force timeout\n")
		return true
	}
	timeoutMock := n.timeoutOperation[operation]
	tid := n.timeoutIndex[operation]
	tid++
	n.timeoutIndex[operation] = tid
	if timeoutMock && tid == 0 {
		log.GetBaseLogger().Debugf("ignore request for timeoutIndex %v\n", n.timeoutIndex)
		return true
	}
	return false
}

var pbTypeToEvent = map[namingpb.DiscoverRequest_DiscoverRequestType]model.EventType{
	namingpb.DiscoverRequest_ROUTING:     model.EventRouting,
	namingpb.DiscoverRequest_INSTANCE:    model.EventInstances,
	namingpb.DiscoverRequest_RATE_LIMIT:  model.EventRateLimiting,
	namingpb.DiscoverRequest_SERVICES:    model.EventServices,
	namingpb.DiscoverRequest_MESH_CONFIG: model.EventMeshConfig,
	namingpb.DiscoverRequest_MESH:        model.EventMesh,
}

//检验是否首次不返回
func (n *namingServer) checkFirstNoReturn(svcEventKey model.ServiceEventKey) bool {
	res := false
	n.rwMutex.Lock()
	ok, noReturn := n.firstNoReturnMap[svcEventKey]
	if ok {
		if noReturn {
			res = true
		}
		n.firstNoReturnMap[svcEventKey] = false
	}
	n.rwMutex.Unlock()
	return res
}

func (n *namingServer) RegisterAssistant(req *namingpb.DiscoverRequest) {
	//如果service不存在那么注册一个辅助服务
	key := &model.ServiceKey{
		Namespace: req.Service.Namespace.GetValue(),
		Service:   req.Service.Name.GetValue(),
	}
	if _, ok := n.serviceTokens[*key]; !ok {
		serviceToken := uuid.New().String()
		testService := &namingpb.Service{
			Name:      &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Token:     &wrappers.StringValue{Value: serviceToken},
		}
		n.RegisterService(testService)
		fmt.Println("MESH_CONFIG RegisterService", *key)
	}
	if namingpb.DiscoverRequest_MESH_CONFIG == req.Type {
		//根据客户端命令更新server网格数据
		busis := strings.Split(req.MeshConfig.MeshName.GetValue(), ".")
		if len(busis) == 3 {

		}

	}

}

//服务实例发现
func (n *namingServer) Discover(server namingpb.PolarisGRPC_DiscoverServer) error {
	//
	//
	//
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Discover: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Discover: server recv error %v\n", err)
			return err
		}
		log.GetBaseLogger().Debugf("Discover: server recv request %v\n", req)
		if n.printReturn {
			log2.Printf("Discover: server recv request %v\n", req)
		}

		if !n.notRegisterAssistant && (namingpb.DiscoverRequest_MESH_CONFIG == req.Type ||
			namingpb.DiscoverRequest_SERVICES == req.Type) {
			n.RegisterAssistant(req)
		}

		if namingpb.DiscoverRequest_ROUTING == req.Type && n.skipRequest(OperationDiscoverRouting) {
			log.GetBaseLogger().Debugf("skip req %v for timeindex %v", req, n.timeoutIndex)
			continue
		}
		if namingpb.DiscoverRequest_INSTANCE == req.Type && n.skipRequest(OperationDiscoverInstance) {
			log.GetBaseLogger().Debugf("skip req %v for timeindex %v", req, n.timeoutIndex)
			continue
		}
		svcEventKey := model.ServiceEventKey{
			ServiceKey: model.ServiceKey{
				Namespace: req.GetService().GetNamespace().GetValue(),
				Service:   req.GetService().GetName().GetValue(),
			},
			Type: pbTypeToEvent[req.Type],
		}
		noReturn := n.checkFirstNoReturn(svcEventKey)
		if noReturn {
			log2.Printf("skip req %v for first time", req)
			log.GetBaseLogger().Debugf("skip req %v for first time", req)
			continue
		}
		if n.returnException {
			log.GetBaseLogger().Debugf("return exception")
			err = server.Send(&namingpb.DiscoverResponse{
				Code:    &wrappers.UInt32Value{Value: 500000},
				Info:    &wrappers.StringValue{Value: "mock server exception"},
				Type:    namingTypeReqToResp[req.Type],
				Service: req.Service,
			})
			if nil != err {
				return err
			}
		}
		key := &model.ServiceKey{
			Namespace: req.GetService().GetNamespace().GetValue(),
			Service:   req.GetService().GetName().GetValue(),
		}
		n.rwMutex.Lock()
		num, requested := n.serviceRequests[*key]
		if requested {
			n.serviceRequests[*key] = num + 1
		} else {
			n.serviceRequests[*key] = 1
		}
		n.rwMutex.Unlock()
		n.rwMutex.RLock()
		token, ok := n.serviceTokens[*key]
		n.rwMutex.RUnlock()
		if !ok && req.Type != namingpb.DiscoverRequest_MESH_CONFIG {
			if n.printReturn {
				log2.Printf("send resp for notFoundResource, type %v, %v", req.Type, req.Service)
			}
			//找不到服务，返回错误信息，继续收取后续的请求
			if err = server.Send(&namingpb.DiscoverResponse{
				Service: req.Service,
				Code:    &wrappers.UInt32Value{Value: namingpb.NotFoundResource},
				Type:    namingTypeReqToResp[req.Type],
				Info:    &wrappers.StringValue{Value: fmt.Sprintf("service not found for service %s", *key)},
			}); nil != err {
				log.GetBaseLogger().Debugf("Discover: server send error %v\n", err)
				return err
			}
			continue
		}
		//等待超时间隔
		if n.methodInterval > 0 {
			time.Sleep(n.methodInterval)
		}
		//构造应答并返回
		var instances []*namingpb.Instance
		var routing *namingpb.Routing
		var ratelimit *namingpb.RateLimit
		var code uint32 = namingpb.ExecuteSuccess
		var info = "execute success"
		var meshs *namingpb.MeshConfig
		var services []*namingpb.Service
		var mesh *namingpb.Mesh
		//根据不同类型返回不同的数据
		switch req.Type {
		case namingpb.DiscoverRequest_INSTANCE:
			n.rwMutex.RLock()
			instances = n.svcInstances[*key]
			n.rwMutex.RUnlock()
		case namingpb.DiscoverRequest_ROUTING:
			n.rwMutex.RLock()
			routing = n.serviceRoutes[*key]
			n.rwMutex.RUnlock()
		case namingpb.DiscoverRequest_RATE_LIMIT:
			n.rwMutex.RLock()
			ratelimit = n.serviceRateLimits[*key]
			n.rwMutex.RUnlock()
		case namingpb.DiscoverRequest_MESH_CONFIG:
			mr := req.GetMeshConfig()
			//mkey := mr.Namespace.GetValue() + "." + mr.Business.GetValue() + "." + mr.TypeUrl.GetValue()
			//log.GetBaseLogger().Debugf("DiscoverRequest_MESH_CONFIG mkey:", mkey, mr)
			keyMesh := model.ServiceKey{
				Namespace: req.Service.Namespace.GetValue(),
				//Service:   model.MeshPrefix + model.MeshKeySpliter + svc.Business.GetValue() + model.MeshKeySpliter + mtype,
				Service: model.MeshPrefix + mr.MeshId.GetValue() + mr.TypeUrl.GetValue(),
			}
			log2.Printf("keyMesh: %v", keyMesh)
			n.rwMutex.RLock()
			if n.mesh[keyMesh] != nil {
				meshs = n.mesh[keyMesh]
				//判断
				//for _, iter := range n.mesh[*key] {
				//	if len(iter.Resources) > 0 && iter.Resources[0].TypeUrl.GetValue() == mr.TypeUrl.GetValue() {
				//		meshs = iter
				//		break
				//	}
				//}
				//meshs = n.mesh[*key][0]
				//n.mesh[*key][0].Resources = append(n.mesh[*key][0].Resources, &namingpb.MeshResource{})
			} else {
				code = namingpb.NotFoundMeshConfig
			}
			n.rwMutex.RUnlock()
			log.GetBaseLogger().Debugf("DiscoverRequest_MESH_CONFIG", n.mesh, keyMesh)
		case namingpb.DiscoverRequest_SERVICES:
			busi := req.Service.Business.GetValue()
			n.rwMutex.RLock()
			for _, v := range n.services {
				if v.Business.GetValue() == busi {
					services = append(services, v)
				}
			}
			n.rwMutex.RUnlock()
			log.GetBaseLogger().Debugf("DiscoverRequest_SERVICES", *key, busi, len(services), req.Service)
		case namingpb.DiscoverRequest_MESH:
			key := model.ServiceKey{
				//Namespace: "mesh",
				Namespace: req.Service.Namespace.GetValue(),
				Service:   req.Mesh.Id.GetValue(),
			}
			n.rwMutex.RLock()
			if _, exist := n.meshs[key]; exist {
				mesh = n.meshs[key]
			}
			n.rwMutex.RUnlock()
		default:
			code = namingpb.InvalidParameter
			info = fmt.Sprintf("unsupported type %v", req.Type)
		}
		resp := &namingpb.DiscoverResponse{
			Type:       namingTypeReqToResp[req.Type],
			Code:       &wrappers.UInt32Value{Value: code},
			Info:       &wrappers.StringValue{Value: info},
			Service:    req.Service,
			Instances:  instances,
			Routing:    routing,
			RateLimit:  ratelimit,
			Meshconfig: meshs,
			Services:   services,
			Mesh:       mesh,
		}
		if req.Type != namingpb.DiscoverRequest_MESH_CONFIG && req.Type != namingpb.DiscoverRequest_MESH {
			n.rwMutex.Lock()
			rawSvc := n.services[token]
			metadata := make(map[string]string, 0)
			for k, v := range rawSvc.Metadata {
				metadata[k] = v
			}
			resp.Service = &namingpb.Service{
				Namespace: &wrappers.StringValue{Value: key.Namespace},
				Name:      &wrappers.StringValue{Value: key.Service},
				Metadata:  metadata,
			}
			n.rwMutex.Unlock()
			if rawSvc.Revision.GetValue() == "" {
				resp.Service.Revision = &wrappers.StringValue{Value: fmt.Sprintf("%X", model.HashMessage(resp))}
			} else {
				resp.Service.Revision = &wrappers.StringValue{Value: rawSvc.Revision.GetValue()}
			}
		}
		log.GetBaseLogger().Debugf("Discover: server send response for %s, type %v\n", *key, req.Type)
		switch req.Type {
		case namingpb.DiscoverRequest_INSTANCE:
			log.GetBaseLogger().Debugf("resp value of instance: %v", resp.Instances)
		case namingpb.DiscoverRequest_ROUTING:
			log.GetBaseLogger().Debugf("resp value of routing: %v", resp.Routing)
		case namingpb.DiscoverRequest_RATE_LIMIT:
			log.GetBaseLogger().Debugf("resp value of ratelimit: %v", resp.RateLimit)
		case namingpb.DiscoverRequest_MESH_CONFIG:
			log.GetBaseLogger().Debugf("resp value of meshconfig: %v", resp.Meshconfig)
		case namingpb.DiscoverRequest_MESH:
			log.GetBaseLogger().Debugf("resp value of mesh: %v", resp.Mesh)
		}
		if n.printReturn {
			log2.Printf("send resp, type %v, %v", req.Type, req.Service)
		}
		if err = server.Send(resp); nil != err {
			log2.Printf("send resp err: %v", err)
			return err
		}
	}
}

//注册服务信息
func (n *namingServer) RegisterService(svc *namingpb.Service) string {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	token := svc.Token.GetValue()
	n.serviceTokens[*key] = token
	n.services[token] = svc
	return token
}

//删除服务信息
func (n *namingServer) DeregisterService(namespace, service string) *namingpb.Service {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	var svc *namingpb.Service
	token, ok := n.serviceTokens[*key]
	if ok {
		svc = n.services[token]
		delete(n.services, token)
		delete(n.serviceTokens, *key)
	}
	return svc
}

//注册限流规则
func (n *namingServer) RegisterRateLimitRule(svc *namingpb.Service, rateLimit *namingpb.RateLimit) error {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}

	_, ok := n.serviceTokens[*key]
	if !ok {
		return errors.New("no service found")
	}

	n.serviceRateLimits[*key] = rateLimit
	log2.Printf("register RateLimit Rule: %v", rateLimit)
	return nil
}

//注销限流规则
func (n *namingServer) DeRegisterRateLimitRule(svc *namingpb.Service) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}

	delete(n.serviceRateLimits, *key)
	log2.Printf("deRegister RateLimit Rule: %v", svc)
}

// 注册服务路由
func (n *namingServer) RegisterRouteRule(svc *namingpb.Service, routing *namingpb.Routing) error {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()

	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	_, ok := n.serviceTokens[*key]
	if !ok {
		return errors.New("no service found")
	}

	n.serviceRoutes[*key] = routing
	return nil
}

// 反注册服务路由
func (n *namingServer) DeregisterRouteRule(svc *namingpb.Service) {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()

	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	_, ok := n.serviceTokens[*key]
	if ok {
		delete(n.serviceRoutes, *key)
	}
}

//注册命名空间
func (n *namingServer) RegisterNamespace(namespace *namingpb.Namespace) {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.namespaces[namespace.Name.GetValue()] = namespace
}

//删除命名空间
func (n *namingServer) DeregisterNamespace(name string) {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	_, ok := n.namespaces[name]
	if ok {
		delete(n.namespaces, name)
	}
}

//构建系统服务的路由规则
func (n *namingServer) BuildRouteRule(namespace string, name string) *namingpb.Routing {
	//构造GRPC实例匹配规则
	protocolMatchString := &namingpb.MatchString{
		Type:  namingpb.MatchString_EXACT,
		Value: &wrappers.StringValue{Value: "grpc"},
	}
	routing := &namingpb.Routing{}
	//设置基础属性
	routing.Namespace = &wrappers.StringValue{Value: namespace}
	routing.Service = &wrappers.StringValue{Value: name}
	protocolRoute := &namingpb.Route{}
	//构造源服务过滤规则
	protocolRoute.Sources = []*namingpb.Source{
		{
			Service:   &wrappers.StringValue{Value: "*"},
			Namespace: &wrappers.StringValue{Value: "*"},
			Metadata:  map[string]*namingpb.MatchString{"protocol": protocolMatchString},
		},
	}
	//构造目标服务过滤规则
	protocolRoute.Destinations = []*namingpb.Destination{
		{
			Service:   &wrappers.StringValue{Value: name},
			Namespace: &wrappers.StringValue{Value: namespace},
			Metadata:  map[string]*namingpb.MatchString{"protocol": protocolMatchString},
			Weight: &wrappers.UInt32Value{
				Value: 100,
			},
		},
	}
	//设置入规则
	routing.Inbounds = append(routing.Inbounds, protocolRoute)
	routing.Revision = &wrappers.StringValue{
		Value: fmt.Sprintf("%X", model.HashMessage(routing)),
	}
	return routing
}

//注册系统服务实例
func (n *namingServer) RegisterServerInstance(
	host string, port int, name string, token string, health bool) *namingpb.Instance {
	//
	//
	//
	//
	//
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", config.ServerNamespace, name, host, port)))
	instId := fmt.Sprintf("%x", h.Sum(nil))
	instanceDiscover := &namingpb.Instance{}
	instanceDiscover.Id = &wrappers.StringValue{Value: instId}
	instanceDiscover.Namespace = &wrappers.StringValue{Value: config.ServerNamespace}
	instanceDiscover.Service = &wrappers.StringValue{Value: name}
	instanceDiscover.Host = &wrappers.StringValue{Value: host}
	instanceDiscover.Port = &wrappers.UInt32Value{Value: uint32(port)}
	instanceDiscover.Weight = &wrappers.UInt32Value{Value: 100}
	instanceDiscover.Protocol = &wrappers.StringValue{Value: "grpc"}
	instanceDiscover.Metadata = map[string]string{"protocol": "grpc"}
	instanceDiscover.ServiceToken = &wrappers.StringValue{Value: token}
	instanceDiscover.Healthy = &wrappers.BoolValue{Value: health}
	log.GetBaseLogger().Debugf("register server instance %v\n", instanceDiscover)
	n.RegisterInstance(nil, instanceDiscover)
	return instanceDiscover
}

func (n *namingServer) RegisterServerInstanceReturnId(host string, port int, name string, token string,
	health bool) string {
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", config.ServerNamespace, name, host, port)))
	instId := fmt.Sprintf("%x", h.Sum(nil))
	instanceDiscover := &namingpb.Instance{}
	instanceDiscover.Id = &wrappers.StringValue{Value: instId}
	instanceDiscover.Namespace = &wrappers.StringValue{Value: config.ServerNamespace}
	instanceDiscover.Service = &wrappers.StringValue{Value: name}
	instanceDiscover.Host = &wrappers.StringValue{Value: host}
	instanceDiscover.Port = &wrappers.UInt32Value{Value: uint32(port)}
	instanceDiscover.Weight = &wrappers.UInt32Value{Value: 100}
	instanceDiscover.Protocol = &wrappers.StringValue{Value: "grpc"}
	instanceDiscover.Metadata = map[string]string{"protocol": "grpc"}
	instanceDiscover.ServiceToken = &wrappers.StringValue{Value: token}
	instanceDiscover.Healthy = &wrappers.BoolValue{Value: health}
	log.GetBaseLogger().Debugf("register server instance %v\n", instanceDiscover)
	n.RegisterInstance(nil, instanceDiscover)
	return instId
}

//删除系统服务实例
func (n *namingServer) DeleteServerInstance(namespace string, service string, id string) {
	k := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	nowList := n.svcInstances[k]
	n.svcInstances[k] = []*namingpb.Instance{}
	for _, v := range nowList {
		//fmt.Println(v.GetId().Value)
		if v.GetId().Value != id {
			t := v
			n.svcInstances[k] = append(n.svcInstances[k], t)
		}
	}
}

//修改系统服务实例权重
func (n *namingServer) UpdateServerInstanceWeight(namespace string, service string, id string, weight uint32) {
	k := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	for _, v := range n.svcInstances[k] {
		if v.GetId().Value == id {
			v.Weight = &wrappers.UInt32Value{Value: weight}
			v.Revision = &wrappers.StringValue{Value: "newReversion"}
			break
		}
	}
	//for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	//}
}

//修改系统服务实例健康状态
func (n *namingServer) UpdateServerInstanceHealthy(namespace string, service string, id string, healthy bool) {
	k := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	for _, v := range n.svcInstances[k] {
		if v.GetId().Value == id {
			v.Healthy = &wrappers.BoolValue{Value: healthy}
			v.Revision = &wrappers.StringValue{Value: "newReversion"}
			break
		}
	}
	//for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	//}
}

//修改系统服务实例隔离状态
func (n *namingServer) UpdateServerInstanceIsolate(namespace string, service string, id string, isolate bool) {
	k := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	for _, v := range n.svcInstances[k] {
		if v.GetId().Value == id {
			v.Isolate = &wrappers.BoolValue{Value: isolate}
			v.Revision = &wrappers.StringValue{Value: "newReversion"}
			break
		}
	}
	//for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	//}
}

//注册单个系统服务
func (n *namingServer) registerServerService(name string, token string) {
	n.rwMutex.Lock()
	if _, ok := n.namespaces[config.ServerNamespace]; !ok {
		ns := &namingpb.Namespace{
			Name: &wrappers.StringValue{
				Value: config.ServerNamespace,
			},
		}
		n.namespaces[config.ServerNamespace] = ns
	}
	n.rwMutex.Unlock()
	svcDiscover := &namingpb.Service{
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Name:      &wrappers.StringValue{Value: name},
		Token:     &wrappers.StringValue{Value: token},
	}
	n.RegisterService(svcDiscover)
}

//注册系统服务
func (n *namingServer) RegisterServerService(name string) string {
	//生成系统服务token
	tokenDiscover := uuid.New().String()
	//注册系统服务
	n.registerServerService(name, tokenDiscover)
	//注册系统服务路由规则则
	n.RegisterRouteRule(&namingpb.Service{
		Name:      &wrappers.StringValue{Value: name},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, name))
	return tokenDiscover
}

//注册系统服务相关的资源
func (n *namingServer) RegisterServerServices(host string, port int) {
	n.RegisterNamespace(&namingpb.Namespace{
		Name: &wrappers.StringValue{Value: config.ServerNamespace},
	})
	//生成系统服务token
	tokenDiscover := uuid.New().String()
	tokenHeartBeat := uuid.New().String()
	//tokenMonitor := uuid.New().String()
	//注册系统服务
	n.registerServerService(config.ServerDiscoverService, tokenDiscover)
	n.registerServerService(config.ServerHeartBeatService, tokenHeartBeat)
	//n.registerServerService(config.ServerMonitorService, tokenMonitor)
	//注册系统服务实例
	n.RegisterServerInstance(host, port, config.ServerDiscoverService, tokenDiscover, true)
	n.RegisterServerInstance(host, port, config.ServerHeartBeatService, tokenHeartBeat, true)
	//n.RegisterServerInstance(host, port, config.ServerMonitorService, tokenMonitor)
	//注册系统服务路由规则则
	n.RegisterRouteRule(&namingpb.Service{
		Name:      &wrappers.StringValue{Value: config.ServerDiscoverService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, config.ServerDiscoverService))
	//
	//
	//
	n.RegisterRouteRule(&namingpb.Service{
		Name:      &wrappers.StringValue{Value: config.ServerHeartBeatService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, config.ServerHeartBeatService))
	//
	//
	//
	//n.RegisterRouteRule(&namingpb.Service{
	//	Name:      &wrappers.StringValue{Value: config.ServerMonitorService},
	//	Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
	//	n.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))
}

//注册网格
func (n *namingServer) RegisterMesh(svc *namingpb.Service, mtype string, mc *namingpb.Mesh) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   mc.Id.GetValue(),
	}
	//if _, exist := n.mesh[key]; !exist {
	//	n.mesh[key] = []*namingpb.MeshConfig{}
	//}
	n.meshs[key] = mc

	log.GetBaseLogger().Infof("RegisterMesh", n.meshs[key])
}

//注册网格规则
func (n *namingServer) RegisterMeshConfig(svc *namingpb.Service, mtype string, mc *namingpb.MeshConfig) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   model.MeshPrefix + mc.MeshId.GetValue() + mtype,
	}
	//if _, exist := n.mesh[key]; !exist {
	//	n.mesh[key] = []*namingpb.MeshConfig{}
	//}
	n.mesh[key] = mc

	log2.Printf("RegisterMeshConfig: %v, meshID: %s", key, mc.MeshId.GetValue())
}

//注销网格规则
func (n *namingServer) DeRegisterMeshConfig(svc *namingpb.Service, meshId string, mtype string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   model.MeshPrefix + meshId + mtype,
	}
	if _, exist := n.mesh[key]; !exist {
		log2.Printf("delete mesh but not exist: %v", key)
		return
	}
	delete(n.mesh, key)
	log2.Printf("delte mesh: %v", key)
}

//产生测试用实例，权重随机生成，传入host和port
func (n *namingServer) GenTestInstancesWithHostPort(
	svc *namingpb.Service, num int, host string, startPort int) []*namingpb.Instance {
	//metadata := make(map[string]string)
	//metadata["logic_set"] = metaSet2
	return n.genTestInstancesByMeta(svc, num, host, startPort, nil)
}

func (n *namingServer) GenTestInstancesWithHostPortAndMeta(
	svc *namingpb.Service, num int, host string, startPort int, metadata map[string]string) []*namingpb.Instance {
	//metadata := make(map[string]string)
	//metadata["logic_set"] = metaSet2
	return n.genTestInstancesByMeta(svc, num, host, startPort, metadata)
}

//产生测试用实例，权重随机生成，传入host和port
func (n *namingServer) genTestInstancesByMeta(
	svc *namingpb.Service, num int, host string, startPort int, metadata map[string]string) []*namingpb.Instance {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	//
	//
	//
	//
	areaLst := [][]string{{"", "", ""},
		{"0", "a", "A"},
		{"1", "a", "A"},
		{"2", "b", "A"},
		{"3", "b", "A"},
		{"4", "c", "B"},
		{"5", "c", "B"},
		{"6", "d", "B"},
		{"7", "d", "B"},
	}
	var instances = make([]*namingpb.Instance, 0, num)
	h := sha1.New()
	keys := make([]string, 0, num)
	startCount := len(n.svcInstances[*key])
	for i := 0; i < num; i++ {
		pos := rand.Intn(len(areaLst))
		location := &namingpb.Location{
			Region: &wrappers.StringValue{Value: areaLst[pos][2]},
			Zone:   &wrappers.StringValue{Value: areaLst[pos][1]},
			Campus: &wrappers.StringValue{Value: areaLst[pos][0]},
		}
		port := uint32(i + startCount + startPort)
		h.Reset()
		idKey := fmt.Sprintf("%s-%s-%s-%d", key.Namespace, key.Service, host, port)
		keys = append(keys, idKey)
		h.Write([]byte(idKey))
		instId := fmt.Sprintf("%x", h.Sum(nil))
		instances = append(instances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: instId},
			Service:   &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Host:      &wrappers.StringValue{Value: host},
			Port:      &wrappers.UInt32Value{Value: port},
			Weight:    &wrappers.UInt32Value{Value: 100 + 10*uint32(rand.Intn(10))},
			//Weight:    &wrappers.UInt32Value{Value: 100},
			HealthCheck: &namingpb.HealthCheck{
				Type: namingpb.HealthCheck_HEARTBEAT,
				Heartbeat: &namingpb.HeartbeatHealthCheck{
					Ttl: &wrappers.UInt32Value{Value: 3},
				},
			},
			Healthy:  &wrappers.BoolValue{Value: true},
			Location: location,
			Metadata: metadata,
			Revision: &wrappers.StringValue{Value: "beginReversion"},
		})
	}
	fmt.Printf("id keys for %s:%s is %v\n", svc.GetNamespace().GetValue(), svc.GetName().GetValue(), keys)
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
	return instances
}

//产生测试用实例，权重随机生成
func (n *namingServer) GenTestInstances(svc *namingpb.Service, num int) []*namingpb.Instance {
	return n.GenTestInstancesWithHostPort(svc, num, "127.0.0.9", 1024)
}

//产生测试用实例，带上元数据，权重随机生成
func (n *namingServer) GenTestInstancesWithMeta(
	svc *namingpb.Service, num int, metadata map[string]string) []*namingpb.Instance {
	return n.genTestInstancesByMeta(svc, num, "127.0.0.9", 4096, metadata)
}

//生成带有不同状态的实例
func (n *namingServer) GenInstancesWithStatus(svc *namingpb.Service, num int, st int, startPort int) []*namingpb.Instance {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	isolated := false
	healthy := true
	if UnhealthyStatus == st {
		healthy = false
	}
	if IsolatedStatus == st {
		isolated = true
	}
	h := sha1.New()
	var instances = make([]*namingpb.Instance, 0, num)
	for i := 0; i < num; i++ {
		port := uint32(i + startPort)
		h.Reset()
		h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", key.Namespace, key.Service, "127.0.0.9", port)))
		instId := fmt.Sprintf("%x", h.Sum(nil))
		instances = append(instances, &namingpb.Instance{
			Id:        &wrappers.StringValue{Value: instId},
			Service:   &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.9"},
			Port:      &wrappers.UInt32Value{Value: port},
			Weight:    &wrappers.UInt32Value{Value: uint32(n.scalableRand.Intn(999) + 1)},
			Isolate:   &wrappers.BoolValue{Value: isolated},
			Healthy:   &wrappers.BoolValue{Value: healthy},
			HealthCheck: &namingpb.HealthCheck{
				Type: namingpb.HealthCheck_HEARTBEAT,
				Heartbeat: &namingpb.HeartbeatHealthCheck{
					Ttl: &wrappers.UInt32Value{Value: 3},
				},
			},
		})
	}
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
	return instances
}

//产生测试用实例，通过指定的服务实例初始化
func (n *namingServer) RegisterServiceInstances(svc *namingpb.Service, instances []*namingpb.Instance) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
}

//上报客户端情况
func (n *namingServer) ReportClient(ctx context.Context, req *namingpb.Client) (*namingpb.Response, error) {
	//fmt.Printf("receive report client req, time %v\n", time.Now())
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	if n.region == "" || n.zone == "" || n.campus == "" {
		fmt.Printf("return cmdb err\n")
		return &namingpb.Response{
			Code:   &wrappers.UInt32Value{Value: namingpb.CMDBNotFindHost},
			Info:   &wrappers.StringValue{Value: "not complete locaton info"},
			Client: nil,
		}, nil
	}
	svrLocation := &namingpb.Location{
		Region: &wrappers.StringValue{Value: n.region},
		Zone:   &wrappers.StringValue{Value: n.zone},
		Campus: &wrappers.StringValue{Value: n.campus},
	}
	respClient := &namingpb.Client{
		Host:     req.Host,
		Type:     req.Type,
		Version:  req.Version,
		Location: svrLocation,
	}
	return &namingpb.Response{
		Code:   &wrappers.UInt32Value{Value: namingpb.ExecuteSuccess},
		Info:   &wrappers.StringValue{Value: "execute success"},
		Client: respClient,
	}, nil
}

//设置地域信息
func (n *namingServer) SetLocation(region, zone, campus string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.region = region
	n.zone = zone
	n.campus = campus
}

//获取地域信息
func (n *namingServer) GetLocation() (region, zone, campus string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	region = n.region
	zone = n.zone
	campus = n.campus
	return
}

//清空某个测试服务的实例
func (n *namingServer) ClearServiceInstances(svc *namingpb.Service) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	n.svcInstances[*key] = nil
}

//设置服务的元数据信息
func (n *namingServer) SetServiceMetadata(token string, key string, value string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	svc, ok := n.services[token]
	if ok {
		if nil == svc.Metadata {
			svc.Metadata = make(map[string]string, 0)
		}
		svc.Metadata[key] = value
	}
}

//获取某个服务的实例
func (n *namingServer) GetServiceInstances(key *model.ServiceKey) []*namingpb.Instance {
	return n.svcInstances[*key]
}

//设置某个服务的实例
func (n *namingServer) SetServiceInstances(key *model.ServiceKey, insts []*namingpb.Instance) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.svcInstances[*key] = insts
}

//获取服务token
func (n *namingServer) GetServiceToken(key *model.ServiceKey) string {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	return n.serviceTokens[*key]
}

//这个server经历的关于某个服务信息的请求次数，包括路由和实例
func (n *namingServer) GetServiceRequests(key *model.ServiceKey) int {
	n.rwMutex.RLock()
	res, ok := n.serviceRequests[*key]
	if !ok {
		res = 0
	}
	n.rwMutex.RUnlock()
	return res
}

//删除某个服务的请求记录
func (n *namingServer) ClearServiceRequests(key *model.ServiceKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	delete(n.serviceRequests, *key)
}

//设置server在返回response的时候需不需要打印log
func (n *namingServer) SetPrintDiscoverReturn(v bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.printReturn = v
}

//设置服务的版本号
func (n *namingServer) SetServiceRevision(token string, revision string, k model.ServiceEventKey) {
	if k.Type == model.EventInstances {
		n.rwMutex.Lock()
		defer n.rwMutex.Unlock()
		svc, ok := n.services[token]
		if ok {
			svc.Revision = &wrappers.StringValue{Value: revision}
		}
	} else if k.Type == model.EventRouting {
		n.rwMutex.Lock()
		defer n.rwMutex.Unlock()
		rout, ok := n.serviceRoutes[k.ServiceKey]
		if ok {
			rout.Revision = &wrappers.StringValue{Value: revision}
		}
	}
}

//插入一个路由信息
func (n *namingServer) InsertRouting(svcKey model.ServiceKey, routing *namingpb.Routing) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.serviceRoutes[svcKey] = routing
}

//设置mockserver是否返回异常
func (n *namingServer) SetReturnException(e bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.returnException = e
}

//设置mockserver是否自动注册网格的辅助服务
func (n *namingServer) SetNotRegisterAssistant(e bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.notRegisterAssistant = e
}
