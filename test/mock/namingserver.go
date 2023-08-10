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
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	log2 "log"
	"sort"
	"sync"
	"time"

	"github.com/golang/protobuf/ptypes/wrappers"
	"github.com/google/uuid"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	"github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	"google.golang.org/protobuf/types/known/wrapperspb"

	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
)

const (
	IsolatedStatus = iota
	UnhealthyStatus
)

var (
	// 请求与应答的类型转换
	namingTypeReqToResp = map[service_manage.DiscoverRequest_DiscoverRequestType]service_manage.DiscoverResponse_DiscoverResponseType{
		service_manage.DiscoverRequest_UNKNOWN:    service_manage.DiscoverResponse_UNKNOWN,
		service_manage.DiscoverRequest_ROUTING:    service_manage.DiscoverResponse_ROUTING,
		service_manage.DiscoverRequest_CLUSTER:    service_manage.DiscoverResponse_CLUSTER,
		service_manage.DiscoverRequest_INSTANCE:   service_manage.DiscoverResponse_INSTANCE,
		service_manage.DiscoverRequest_RATE_LIMIT: service_manage.DiscoverResponse_RATE_LIMIT,
		service_manage.DiscoverRequest_SERVICES:   service_manage.DiscoverResponse_SERVICES,
	}
)

// 操作类型
type OperationType string

const (
	// OperationDiscoverInstance 服务发现实例接口操作
	OperationDiscoverInstance OperationType = "discoverInstance"
	// OperationDiscoverRouting 服务发现路由接口操作
	OperationDiscoverRouting OperationType = "discoverRouting"
	// OperationRegistry 服务注册接口
	OperationRegistry OperationType = "registry"
	// OperationDeRegistry 服务反注册接口
	OperationDeRegistry OperationType = "deregistry"
	// OperationHeartbeat 健康检查接口
	OperationHeartbeat OperationType = "heartbeat"
	// syncWaitTime 同步等待时间
	syncWaitTime = 3 * time.Second
)

// NamingServer 测试桩相关接口
type NamingServer interface {
	service_manage.PolarisGRPCServer
	// MakeOperationTimeout 设置模拟某个方法进行超时
	MakeOperationTimeout(operation OperationType, enable bool)
	// MakeForceOperationTimeout 设置强制模拟方法超时
	MakeForceOperationTimeout(operation OperationType, enable bool)
	// SetMethodInterval 设置方法超时时间
	SetMethodInterval(interval time.Duration)
	// SetPrintDiscoverReturn 设置打印返回的服务列表信息
	SetPrintDiscoverReturn(v bool)
	// SetReturnException 设置mockserver是否返回异常
	SetReturnException(e bool)
	// SetNotRegisterAssistant 设置是否自动注册网格的辅助服务
	SetNotRegisterAssistant(e bool)
	// RegisterService 注册服务
	RegisterService(svc *service_manage.Service) string
	// DeregisterService 反注册服务
	DeregisterService(namespace, service string) *service_manage.Service
	// RegisterRateLimitRule 注册限流规则
	RegisterRateLimitRule(svc *service_manage.Service, rateLimit *traffic_manage.RateLimit) error
	// DeRegisterRateLimitRule 注销限流规则
	DeRegisterRateLimitRule(svc *service_manage.Service)
	// RegisterRouteRule 注册路由规则
	RegisterRouteRule(svc *service_manage.Service, routing *traffic_manage.Routing) error
	// DeregisterRouteRule 反注册路由规则
	DeregisterRouteRule(svc *service_manage.Service)
	// RegisterNamespace 注册命名空间
	RegisterNamespace(namespace *apimodel.Namespace)
	// DeregisterNamespace 反注册命名空间
	DeregisterNamespace(name string)
	// BuildRouteRule 构建系统服务的路由规则
	BuildRouteRule(namespace string, name string) *traffic_manage.Routing
	// RegisterServerInstance 注册服务实例
	RegisterServerInstance(host string, port int, name string, token string, health bool) *service_manage.Instance
	// RegisterServerInstanceReturnId .
	RegisterServerInstanceReturnId(host string, port int, name string, token string, health bool) string
	// RegisterServiceInstances 批量注册服务实例
	RegisterServiceInstances(svc *service_manage.Service, instances []*service_manage.Instance)
	// GetServiceInstances 直接获取服务实例
	GetServiceInstances(key *model.ServiceKey) []*service_manage.Instance
	// RegisterServerService 注册系统服务，返回服务token
	RegisterServerService(name string) string
	// RegisterServerServices 注册所有系统服务以及对应的服务实例
	RegisterServerServices(host string, port int)
	// ClearServiceInstances 清空某个测试服务的实例
	ClearServiceInstances(svc *service_manage.Service)
	// SetServiceMetadata 设置服务的元数据信息
	SetServiceMetadata(token string, key string, value string)
	// GenTestInstances 为服务生成N个随机服务实例
	GenTestInstances(svc *service_manage.Service, num int) []*service_manage.Instance
	// DeleteServerInstance 删除测试实例
	DeleteServerInstance(namespace string, service string, id string)
	// UpdateServerInstanceWeight 修改系统服务实例权重
	UpdateServerInstanceWeight(namespace string, service string, id string, weight uint32)
	// UpdateServerInstanceHealthy 修改系统服务实例健康状态
	UpdateServerInstanceHealthy(namespace string, service string, id string, healthy bool)
	// UpdateServerInstanceIsolate 修改系统服务实例隔离状态
	UpdateServerInstanceIsolate(namespace string, service string, id string, isolate bool)
	// GenTestInstancesWithHostPort 产生测试用实例，带上地址端口号，权重随机生成
	GenTestInstancesWithHostPort(svc *service_manage.Service, num int, host string, startPort int) []*service_manage.Instance
	// GenTestInstancesWithHostPortAndMeta .
	GenTestInstancesWithHostPortAndMeta(
		svc *service_manage.Service, num int, host string, startPort int, metadata map[string]string) []*service_manage.Instance
	// GenTestInstancesWithMeta 产生测试用实例，带上元数据，权重随机生成
	GenTestInstancesWithMeta(svc *service_manage.Service, num int, metadata map[string]string) []*service_manage.Instance
	// GenInstancesWithStatus 产生测试用实例，带上状态信息，权重随机生成
	GenInstancesWithStatus(svc *service_manage.Service, num int, st int, startPort int) []*service_manage.Instance
	// SetLocation 设置地域信息
	SetLocation(region, zone, campus string)
	// SetServiceInstances 设置某个服务的实例
	SetServiceInstances(key *model.ServiceKey, insts []*service_manage.Instance)
	// GetLocation 获取地域信息
	GetLocation() (region, zone, campus string)
	// GetServiceRequests 获取服务请求
	GetServiceRequests(key *model.ServiceKey) int
	// ClearServiceRequests 清空服务请求
	ClearServiceRequests(key *model.ServiceKey)
	// GetServiceToken 获取服务token
	GetServiceToken(key *model.ServiceKey) string
	// SetServiceRevision 设置服务版本号
	SetServiceRevision(token string, revision string, k model.ServiceEventKey)
	// InsertRouting 插入一个路由信息
	InsertRouting(svcKey model.ServiceKey, routing *traffic_manage.Routing)
	// SetInstanceStatus 设置某个服务的某个实例的状态（健康、隔离、权重）
	SetInstanceStatus(svcKey model.ServiceKey, idx int, healthy bool, isolate bool, weight uint32) error
	// SetFirstNoReturn 设置首次不返回某个请求
	SetFirstNoReturn(svcKey model.ServiceEventKey)
	// UnsetFirstNoReturn 反设置首次不返回某个请求
	UnsetFirstNoReturn(svcKey model.ServiceEventKey)
}

// Polaris server模拟桩
type namingServer struct {
	rwMutex               sync.RWMutex
	printReturn           bool
	svcInstances          map[model.ServiceKey][]*service_manage.Instance
	namespaces            map[string]*apimodel.Namespace
	instances             map[string]*service_manage.Instance
	services              map[string]*service_manage.Service
	serviceTokens         map[model.ServiceKey]string
	serviceRoutes         map[model.ServiceKey]*traffic_manage.Routing
	serviceRateLimits     map[model.ServiceKey]*traffic_manage.RateLimit
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
	notRegisterAssistant  bool
	scalableRand          *rand.ScalableRand
}

// NewNamingServer 创建NamingServer模拟桩
func NewNamingServer() NamingServer {
	//
	//
	//
	ns := &namingServer{
		scalableRand:      rand.NewScalableRand(),
		svcInstances:      make(map[model.ServiceKey][]*service_manage.Instance, 0),
		namespaces:        make(map[string]*apimodel.Namespace, 0),
		services:          make(map[string]*service_manage.Service, 0),
		serviceTokens:     make(map[model.ServiceKey]string, 0),
		serviceRequests:   make(map[model.ServiceKey]int, 0),
		instances:         make(map[string]*service_manage.Instance, 0),
		serviceRoutes:     make(map[model.ServiceKey]*traffic_manage.Routing, 0),
		serviceRateLimits: make(map[model.ServiceKey]*traffic_manage.RateLimit, 0),
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
	}
	return ns
}

// SetFirstNoReturn 设置是否跳过第一次的请求回复
func (n *namingServer) SetFirstNoReturn(svcKey model.ServiceEventKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.firstNoReturnMap[svcKey] = true
}

// UnsetFirstNoReturn 反设置是否跳过第一次的请求回复
func (n *namingServer) UnsetFirstNoReturn(svcKey model.ServiceEventKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	delete(n.firstNoReturnMap, svcKey)
}

// SetInstanceStatus 设置某个服务的某个实例的状态（健康、隔离、权重）
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

// MakeOperationTimeout 设置模拟某个方法进行超时
func (n *namingServer) MakeOperationTimeout(operation OperationType, enable bool) {
	//
	//
	//
	n.timeoutOperation[operation] = enable
}

// MakeForceOperationTimeout 设置强制模拟方法超时
func (n *namingServer) MakeForceOperationTimeout(operation OperationType, enable bool) {
	n.forceTimeoutOperation[operation] = enable
}

// SetMethodInterval 设置方法超时时间
func (n *namingServer) SetMethodInterval(interval time.Duration) {
	n.methodInterval = interval
}

// RegisterInstance 注册实例
func (n *namingServer) RegisterInstance(ctx context.Context,
	req *service_manage.Instance) (*service_manage.Response, error) {
	fmt.Printf("%v, RegisterInstance in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationRegistry) {
		time.Sleep(syncWaitTime)
	}
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Service:   req.GetService().GetValue(),
		Namespace: req.GetNamespace().GetValue()}
	if token, ok := n.serviceTokens[*key]; ok {
		if token != req.ServiceToken.GetValue() {
			return &service_manage.Response{
				Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_Unauthorized)},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &service_manage.Response{
			Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
			Info: &wrappers.StringValue{Value: "not found resource"},
		}, nil
	}
	instances := n.svcInstances[*key]
	for i := 0; i < len(instances); i++ {
		if req.GetHost().GetValue() == instances[i].GetHost().GetValue() &&
			req.GetPort().GetValue() == instances[i].GetPort().GetValue() {
			return &service_manage.Response{
				Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_ExistedResource)},
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
	return &service_manage.Response{
		Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_ExecuteSuccess)},
		Info:      &wrappers.StringValue{Value: "execute success"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  req,
	}, nil
}

// DeregisterInstance 反注册实例
func (n *namingServer) DeregisterInstance(ctx context.Context,
	req *service_manage.Instance) (*service_manage.Response, error) {
	fmt.Printf("%v, DeregisterInstance in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationDeRegistry) {
		time.Sleep(syncWaitTime)
	}
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
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
			return &service_manage.Response{
				Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_Unauthorized)},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &service_manage.Response{
			Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
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
			return &service_manage.Response{
				Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_ExecuteSuccess)},
				Info:      &wrappers.StringValue{Value: "execute success"},
				Namespace: n.namespaces[key.Namespace],
				Service:   n.services[req.ServiceToken.GetValue()],
				Instance:  delInstance,
			}, nil
		}
	}
	return &service_manage.Response{
		Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
		Info:      &wrappers.StringValue{Value: "instance not found"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  nil,
	}, nil
}

// Heartbeat 心跳上报
func (n *namingServer) Heartbeat(ctx context.Context, req *service_manage.Instance) (*service_manage.Response, error) {
	fmt.Printf("%v, Heartbeat in server, %v\n", time.Now(), req)
	if n.skipRequest(OperationHeartbeat) {
		time.Sleep(syncWaitTime)
	}
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
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
			return &service_manage.Response{
				Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_Unauthorized)},
				Info: &wrappers.StringValue{Value: "unauthorized"},
			}, nil
		}
	} else {
		return &service_manage.Response{
			Code: &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
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
			return &service_manage.Response{
				Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_ExecuteSuccess)},
				Info:      &wrappers.StringValue{Value: "execute success"},
				Namespace: n.namespaces[key.Namespace],
				Service:   n.services[req.ServiceToken.GetValue()],
				Instance:  instances[i],
			}, nil
		}
	}
	return &service_manage.Response{
		Code:      &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
		Info:      &wrappers.StringValue{Value: "instance not found"},
		Namespace: n.namespaces[key.Namespace],
		Service:   n.services[req.ServiceToken.GetValue()],
		Instance:  nil,
	}, nil
}

func (n *namingServer) BatchHeartbeat(server service_manage.PolarisHeartbeatGRPC_BatchHeartbeatClient) error {
	// TODO
	return nil
}

func (n *namingServer) BatchGetHeartbeat(ctx context.Context, req *service_manage.GetHeartbeatsRequest) (*service_manage.GetHeartbeatsResponse, error) {
	// TODO
	return &service_manage.GetHeartbeatsResponse{}, nil
}

func (n *namingServer) BatchDelHeartbeat(ctx context.Context, req *service_manage.DelHeartbeatsRequest) (*service_manage.DelHeartbeatsResponse, error) {
	// TODO
	return &service_manage.DelHeartbeatsResponse{}, nil
}

// 是否忽略当前请求
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

var pbTypeToEvent = map[service_manage.DiscoverRequest_DiscoverRequestType]model.EventType{
	service_manage.DiscoverRequest_ROUTING:    model.EventRouting,
	service_manage.DiscoverRequest_INSTANCE:   model.EventInstances,
	service_manage.DiscoverRequest_RATE_LIMIT: model.EventRateLimiting,
	service_manage.DiscoverRequest_SERVICES:   model.EventServices,
}

// 检验是否首次不返回
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

func (n *namingServer) RegisterAssistant(req *service_manage.DiscoverRequest) {
	// 如果service不存在那么注册一个辅助服务
	key := &model.ServiceKey{
		Namespace: req.Service.Namespace.GetValue(),
		Service:   req.Service.Name.GetValue(),
	}
	if _, ok := n.serviceTokens[*key]; !ok {
		serviceToken := uuid.New().String()
		testService := &service_manage.Service{
			Name:      &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Token:     &wrappers.StringValue{Value: serviceToken},
		}
		n.RegisterService(testService)
		fmt.Println("MESH_CONFIG RegisterService", *key)
	}
}

// Discover 服务实例发现
func (n *namingServer) Discover(server service_manage.PolarisGRPC_DiscoverServer) error {
	//
	//
	//
	for {
		req, err := server.Recv()
		if err != nil {
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

		if !n.notRegisterAssistant && (service_manage.DiscoverRequest_SERVICES == req.Type) {
			n.RegisterAssistant(req)
		}

		if service_manage.DiscoverRequest_ROUTING == req.Type && n.skipRequest(OperationDiscoverRouting) {
			log.GetBaseLogger().Debugf("skip req %v for timeindex %v", req, n.timeoutIndex)
			continue
		}
		if service_manage.DiscoverRequest_INSTANCE == req.Type && n.skipRequest(OperationDiscoverInstance) {
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
			err = server.Send(&service_manage.DiscoverResponse{
				Code:    &wrappers.UInt32Value{Value: 500000},
				Info:    &wrappers.StringValue{Value: "mock server exception"},
				Type:    namingTypeReqToResp[req.Type],
				Service: req.Service,
			})
			if err != nil {
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
		_, ok := n.serviceTokens[*key]
		n.rwMutex.RUnlock()
		if !ok {
			if n.printReturn {
				log2.Printf("send resp for notFoundResource, type %v, %v", req.Type, req.Service)
			}
			// 找不到服务，返回错误信息，继续收取后续的请求
			if err = server.Send(&service_manage.DiscoverResponse{
				Service: req.Service,
				Code:    &wrappers.UInt32Value{Value: uint32(apimodel.Code_NotFoundResource)},
				Type:    namingTypeReqToResp[req.Type],
				Info:    &wrappers.StringValue{Value: fmt.Sprintf("service not found for service %s", *key)},
			}); err != nil {
				log.GetBaseLogger().Debugf("Discover: server send error %v\n", err)
				return err
			}
			continue
		}
		// 等待超时间隔
		if n.methodInterval > 0 {
			time.Sleep(n.methodInterval)
		}
		// 构造应答并返回
		var instances []*service_manage.Instance
		var routing *traffic_manage.Routing
		var ratelimit *traffic_manage.RateLimit
		var code uint32 = uint32(apimodel.Code_ExecuteSuccess)
		var info = "execute success"
		var services []*service_manage.Service
		var revision = ""
		// 根据不同类型返回不同的数据
		switch req.Type {
		case service_manage.DiscoverRequest_INSTANCE:
			n.rwMutex.RLock()
			instances = n.svcInstances[*key]
			revisions := make([]string, 0, len(instances))
			for i := range instances {
				revisions = append(revisions, instances[i].GetRevision().GetValue())
			}
			revisions = append(revisions)
			revision, _ = CompositeComputeRevision(revisions)
			n.rwMutex.RUnlock()
		case service_manage.DiscoverRequest_ROUTING:
			n.rwMutex.RLock()
			routing = n.serviceRoutes[*key]
			n.rwMutex.RUnlock()
		case service_manage.DiscoverRequest_RATE_LIMIT:
			n.rwMutex.RLock()
			ratelimit = n.serviceRateLimits[*key]
			n.rwMutex.RUnlock()
		case service_manage.DiscoverRequest_SERVICES:
			busi := req.Service.Business.GetValue()
			n.rwMutex.RLock()
			for _, v := range n.services {
				if v.Business.GetValue() == busi {
					services = append(services, v)
				}
			}
			n.rwMutex.RUnlock()
			log.GetBaseLogger().Debugf("DiscoverRequest_SERVICES", *key, busi, len(services), req.Service)
		default:
			code = uint32(apimodel.Code_InvalidParameter)
			info = fmt.Sprintf("unsupported type %v", req.Type)
		}

		var svc = req.Service
		for i := range n.services {
			item := n.services[i]
			if item.GetNamespace().GetValue() == req.Service.GetNamespace().GetValue() && item.GetName().GetValue() == req.Service.GetName().GetValue() {
				svc = item
				break
			}
		}
		revision, _ = CompositeComputeRevision([]string{revision, svc.GetRevision().GetValue()})
		svc.Revision = wrapperspb.String(revision)

		resp := &service_manage.DiscoverResponse{
			Type:      namingTypeReqToResp[req.Type],
			Code:      &wrappers.UInt32Value{Value: code},
			Info:      &wrappers.StringValue{Value: info},
			Service:   svc,
			Instances: instances,
			Routing:   routing,
			RateLimit: ratelimit,
			Services:  services,
		}
		log.GetBaseLogger().Debugf("Discover: server send response for %s, type %v\n", *key, req.Type)
		switch req.Type {
		case service_manage.DiscoverRequest_INSTANCE:
			log.GetBaseLogger().Debugf("resp value of instance: %v", resp.Instances)
		case service_manage.DiscoverRequest_ROUTING:
			log.GetBaseLogger().Debugf("resp value of routing: %v", resp.Routing)
		case service_manage.DiscoverRequest_RATE_LIMIT:
			log.GetBaseLogger().Debugf("resp value of ratelimit: %v", resp.RateLimit)
		}
		if n.printReturn {
			log2.Printf("send resp, type %v, %v", req.Type, req.Service)
		}
		if err = server.Send(resp); err != nil {
			log2.Printf("send resp err: %v", err)
			return err
		}
	}
}

// CompositeComputeRevision 将多个 revision 合并计算为一个
func CompositeComputeRevision(revisions []string) (string, error) {
	h := sha1.New()

	sort.Strings(revisions)

	for i := range revisions {
		if _, err := h.Write([]byte(revisions[i])); err != nil {
			return "", err
		}
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

// RegisterService 注册服务信息
func (n *namingServer) RegisterService(svc *service_manage.Service) string {
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

// DeregisterService 删除服务信息
func (n *namingServer) DeregisterService(namespace, service string) *service_manage.Service {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	var svc *service_manage.Service
	token, ok := n.serviceTokens[*key]
	if ok {
		svc = n.services[token]
		delete(n.services, token)
		delete(n.serviceTokens, *key)
	}
	return svc
}

// RegisterRateLimitRule 注册限流规则
func (n *namingServer) RegisterRateLimitRule(svc *service_manage.Service, rateLimit *traffic_manage.RateLimit) error {
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

// DeRegisterRateLimitRule 注销限流规则
func (n *namingServer) DeRegisterRateLimitRule(svc *service_manage.Service) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}

	delete(n.serviceRateLimits, *key)
	log2.Printf("deRegister RateLimit Rule: %v", svc)
}

// RegisterRouteRule 注册服务路由
func (n *namingServer) RegisterRouteRule(svc *service_manage.Service, routing *traffic_manage.Routing) error {
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

// DeregisterRouteRule 反注册服务路由
func (n *namingServer) DeregisterRouteRule(svc *service_manage.Service) {
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

// RegisterNamespace 注册命名空间
func (n *namingServer) RegisterNamespace(namespace *apimodel.Namespace) {
	//
	//
	//
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.namespaces[namespace.Name.GetValue()] = namespace
}

// DeregisterNamespace 删除命名空间
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

// BuildRouteRule 构建系统服务的路由规则
func (n *namingServer) BuildRouteRule(namespace string, name string) *traffic_manage.Routing {
	// 构造GRPC实例匹配规则
	protocolMatchString := &apimodel.MatchString{
		Type:  apimodel.MatchString_EXACT,
		Value: &wrappers.StringValue{Value: "grpc"},
	}
	routing := &traffic_manage.Routing{}
	// 设置基础属性
	routing.Namespace = &wrappers.StringValue{Value: namespace}
	routing.Service = &wrappers.StringValue{Value: name}
	protocolRoute := &traffic_manage.Route{}
	// 构造源服务过滤规则
	protocolRoute.Sources = []*traffic_manage.Source{
		{
			Service:   &wrappers.StringValue{Value: "*"},
			Namespace: &wrappers.StringValue{Value: "*"},
			Metadata:  map[string]*apimodel.MatchString{"protocol": protocolMatchString},
		},
	}
	// 构造目标服务过滤规则
	protocolRoute.Destinations = []*traffic_manage.Destination{
		{
			Service:   &wrappers.StringValue{Value: name},
			Namespace: &wrappers.StringValue{Value: namespace},
			Metadata:  map[string]*apimodel.MatchString{"protocol": protocolMatchString},
			Weight: &wrappers.UInt32Value{
				Value: 100,
			},
		},
	}
	// 设置入规则
	routing.Inbounds = append(routing.Inbounds, protocolRoute)
	routing.Revision = &wrappers.StringValue{
		Value: fmt.Sprintf("%X", model.HashMessage(routing)),
	}
	return routing
}

// RegisterServerInstance 注册系统服务实例
func (n *namingServer) RegisterServerInstance(
	host string, port int, name string, token string, health bool) *service_manage.Instance {
	//
	//
	//
	//
	//
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", config.ServerNamespace, name, host, port)))
	instId := fmt.Sprintf("%x", h.Sum(nil))
	instanceDiscover := &service_manage.Instance{}
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
	n.RegisterInstance(context.TODO(), instanceDiscover)
	return instanceDiscover
}

// RegisterServerInstanceReturnId 注册服务返回标识
func (n *namingServer) RegisterServerInstanceReturnId(host string, port int, name string, token string,
	health bool) string {
	h := sha1.New()
	h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", config.ServerNamespace, name, host, port)))
	instId := fmt.Sprintf("%x", h.Sum(nil))
	instanceDiscover := &service_manage.Instance{}
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
	n.RegisterInstance(context.TODO(), instanceDiscover)
	return instId
}

// DeleteServerInstance 删除系统服务实例
func (n *namingServer) DeleteServerInstance(namespace string, service string, id string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	k := model.ServiceKey{
		Namespace: namespace,
		Service:   service,
	}
	nowList := n.svcInstances[k]
	n.svcInstances[k] = []*service_manage.Instance{}
	for _, v := range nowList {
		// fmt.Println(v.GetId().Value)
		if v.GetId().Value != id {
			t := v
			n.svcInstances[k] = append(n.svcInstances[k], t)
		}
	}
}

// UpdateServerInstanceWeight 修改系统服务实例权重
func (n *namingServer) UpdateServerInstanceWeight(namespace string, service string, id string, weight uint32) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
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
	// for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	// }
}

// UpdateServerInstanceHealthy 修改系统服务实例健康状态
func (n *namingServer) UpdateServerInstanceHealthy(namespace string, service string, id string, healthy bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
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
	// for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	// }
}

// UpdateServerInstanceIsolate 修改系统服务实例隔离状态
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
	// for _, v := range n.svcInstances[k] {
	//	//fmt.Println(v.GetId().Value)
	//	fmt.Println("--------------------weight: ", v.GetWeight().Value)
	// }
}

// 注册单个系统服务
func (n *namingServer) registerServerService(name string, token string) {
	n.rwMutex.Lock()
	if _, ok := n.namespaces[config.ServerNamespace]; !ok {
		ns := &apimodel.Namespace{
			Name: &wrappers.StringValue{
				Value: config.ServerNamespace,
			},
		}
		n.namespaces[config.ServerNamespace] = ns
	}
	n.rwMutex.Unlock()
	svcDiscover := &service_manage.Service{
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace},
		Name:      &wrappers.StringValue{Value: name},
		Token:     &wrappers.StringValue{Value: token},
	}
	n.RegisterService(svcDiscover)
}

// RegisterServerService 注册系统服务
func (n *namingServer) RegisterServerService(name string) string {
	// 生成系统服务token
	tokenDiscover := uuid.New().String()
	// 注册系统服务
	n.registerServerService(name, tokenDiscover)
	// 注册系统服务路由规则则
	n.RegisterRouteRule(&service_manage.Service{
		Name:      &wrappers.StringValue{Value: name},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, name))
	return tokenDiscover
}

// RegisterServerServices 注册系统服务相关的资源
func (n *namingServer) RegisterServerServices(host string, port int) {
	n.RegisterNamespace(&apimodel.Namespace{
		Name: &wrappers.StringValue{Value: config.ServerNamespace},
	})
	// 生成系统服务token
	tokenDiscover := uuid.New().String()
	tokenHeartBeat := uuid.New().String()
	// tokenMonitor := uuid.New().String()
	// 注册系统服务
	n.registerServerService(config.ServerDiscoverService, tokenDiscover)
	n.registerServerService(config.ServerHeartBeatService, tokenHeartBeat)
	// n.registerServerService(config.ServerMonitorService, tokenMonitor)
	// 注册系统服务实例
	n.RegisterServerInstance(host, port, config.ServerDiscoverService, tokenDiscover, true)
	n.RegisterServerInstance(host, port, config.ServerHeartBeatService, tokenHeartBeat, true)
	// n.RegisterServerInstance(host, port, config.ServerMonitorService, tokenMonitor)
	// 注册系统服务路由规则则
	n.RegisterRouteRule(&service_manage.Service{
		Name:      &wrappers.StringValue{Value: config.ServerDiscoverService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, config.ServerDiscoverService))
	//
	//
	//
	n.RegisterRouteRule(&service_manage.Service{
		Name:      &wrappers.StringValue{Value: config.ServerHeartBeatService},
		Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
		n.BuildRouteRule(config.ServerNamespace, config.ServerHeartBeatService))
	//
	//
	//
	// n.RegisterRouteRule(&service_manage.Service{
	//	Name:      &wrappers.StringValue{Value: config.ServerMonitorService},
	//	Namespace: &wrappers.StringValue{Value: config.ServerNamespace}},
	//	n.BuildRouteRule(config.ServerNamespace, config.ServerMonitorService))
}

// GenTestInstancesWithHostPort 产生测试用实例，权重随机生成，传入host和port
func (n *namingServer) GenTestInstancesWithHostPort(
	svc *service_manage.Service, num int, host string, startPort int) []*service_manage.Instance {
	// metadata := make(map[string]string)
	// metadata["logic_set"] = metaSet2
	return n.genTestInstancesByMeta(svc, num, host, startPort, nil)
}

// GenTestInstancesWithHostPortAndMeta 产生测试用实例，权重随机生成，传入host和port，meta
func (n *namingServer) GenTestInstancesWithHostPortAndMeta(
	svc *service_manage.Service, num int, host string, startPort int, metadata map[string]string) []*service_manage.Instance {
	// metadata := make(map[string]string)
	// metadata["logic_set"] = metaSet2
	return n.genTestInstancesByMeta(svc, num, host, startPort, metadata)
}

// 产生测试用实例，权重随机生成，传入host和port
func (n *namingServer) genTestInstancesByMeta(
	svc *service_manage.Service, num int, host string, startPort int, metadata map[string]string) []*service_manage.Instance {
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
	var instances = make([]*service_manage.Instance, 0, num)
	h := sha1.New()
	keys := make([]string, 0, num)
	startCount := len(n.svcInstances[*key])
	for i := 0; i < num; i++ {
		pos := rand.Intn(len(areaLst))
		location := &apimodel.Location{
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
		instances = append(instances, &service_manage.Instance{
			Id:        &wrappers.StringValue{Value: instId},
			Service:   &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Host:      &wrappers.StringValue{Value: host},
			Port:      &wrappers.UInt32Value{Value: port},
			Weight:    &wrappers.UInt32Value{Value: 100 + 10*uint32(rand.Intn(10))},
			// Weight:    &wrappers.UInt32Value{Value: 100},
			HealthCheck: &service_manage.HealthCheck{
				Type: service_manage.HealthCheck_HEARTBEAT,
				Heartbeat: &service_manage.HeartbeatHealthCheck{
					Ttl: &wrappers.UInt32Value{Value: 3},
				},
			},
			Healthy:  &wrappers.BoolValue{Value: true},
			Location: location,
			Metadata: metadata,
			Revision: &wrappers.StringValue{Value: "beginReversion"},
		})
	}
	// fmt.Printf("id keys for %s:%s is %v\n", svc.GetNamespace().GetValue(), svc.GetName().GetValue(), keys)
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
	return instances
}

// GenTestInstances 产生测试用实例，权重随机生成
func (n *namingServer) GenTestInstances(svc *service_manage.Service, num int) []*service_manage.Instance {
	return n.GenTestInstancesWithHostPort(svc, num, "127.0.0.9", 1024)
}

// GenTestInstancesWithMeta 产生测试用实例，带上元数据，权重随机生成
func (n *namingServer) GenTestInstancesWithMeta(
	svc *service_manage.Service, num int, metadata map[string]string) []*service_manage.Instance {
	return n.genTestInstancesByMeta(svc, num, "127.0.0.9", 4096, metadata)
}

// GenInstancesWithStatus 生成带有不同状态的实例
func (n *namingServer) GenInstancesWithStatus(svc *service_manage.Service, num int, st int, startPort int) []*service_manage.Instance {
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
	var instances = make([]*service_manage.Instance, 0, num)
	for i := 0; i < num; i++ {
		port := uint32(i + startPort)
		h.Reset()
		h.Write([]byte(fmt.Sprintf("%s-%s-%s-%d", key.Namespace, key.Service, "127.0.0.9", port)))
		instId := fmt.Sprintf("%x", h.Sum(nil))
		instances = append(instances, &service_manage.Instance{
			Id:        &wrappers.StringValue{Value: instId},
			Service:   &wrappers.StringValue{Value: key.Service},
			Namespace: &wrappers.StringValue{Value: key.Namespace},
			Host:      &wrappers.StringValue{Value: "127.0.0.9"},
			Port:      &wrappers.UInt32Value{Value: port},
			Weight:    &wrappers.UInt32Value{Value: uint32(n.scalableRand.Intn(999) + 1)},
			Isolate:   &wrappers.BoolValue{Value: isolated},
			Healthy:   &wrappers.BoolValue{Value: healthy},
			HealthCheck: &service_manage.HealthCheck{
				Type: service_manage.HealthCheck_HEARTBEAT,
				Heartbeat: &service_manage.HeartbeatHealthCheck{
					Ttl: &wrappers.UInt32Value{Value: 3},
				},
			},
		})
	}
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
	return instances
}

// RegisterServiceInstances 产生测试用实例，通过指定的服务实例初始化
func (n *namingServer) RegisterServiceInstances(svc *service_manage.Service, instances []*service_manage.Instance) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	n.svcInstances[*key] = append(n.svcInstances[*key], instances...)
}

// ReportClient 上报客户端情况
func (n *namingServer) ReportClient(ctx context.Context, req *service_manage.Client) (*service_manage.Response, error) {
	// fmt.Printf("receive report client req, time %v\n", time.Now())
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	if n.region == "" || n.zone == "" || n.campus == "" {
		fmt.Printf("return cmdb err\n")
		return &service_manage.Response{
			Code:   &wrappers.UInt32Value{Value: uint32(apimodel.Code_CMDBNotFindHost)},
			Info:   &wrappers.StringValue{Value: "not complete locaton info"},
			Client: nil,
		}, nil
	}
	svrLocation := &apimodel.Location{
		Region: &wrappers.StringValue{Value: n.region},
		Zone:   &wrappers.StringValue{Value: n.zone},
		Campus: &wrappers.StringValue{Value: n.campus},
	}
	respClient := &service_manage.Client{
		Host:     req.Host,
		Type:     req.Type,
		Version:  req.Version,
		Location: svrLocation,
	}
	return &service_manage.Response{
		Code:   &wrappers.UInt32Value{Value: uint32(apimodel.Code_ExecuteSuccess)},
		Info:   &wrappers.StringValue{Value: "execute success"},
		Client: respClient,
	}, nil
}

// SetLocation 设置地域信息
func (n *namingServer) SetLocation(region, zone, campus string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.region = region
	n.zone = zone
	n.campus = campus
}

// GetLocation 获取地域信息
func (n *namingServer) GetLocation() (region, zone, campus string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	region = n.region
	zone = n.zone
	campus = n.campus
	return
}

// ClearServiceInstances 清空某个测试服务的实例
func (n *namingServer) ClearServiceInstances(svc *service_manage.Service) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	key := &model.ServiceKey{
		Namespace: svc.Namespace.GetValue(),
		Service:   svc.Name.GetValue(),
	}
	n.svcInstances[*key] = nil
}

// SetServiceMetadata 设置服务的元数据信息
func (n *namingServer) SetServiceMetadata(token string, key string, value string) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	svc, ok := n.services[token]
	if ok {
		if nil == svc.Metadata {
			svc.Metadata = make(map[string]string, 0)
		}
		svc.Metadata[key] = value
		svc.Revision = wrapperspb.String(uuid.NewString())
	}
}

// GetServiceInstances 获取某个服务的实例
func (n *namingServer) GetServiceInstances(key *model.ServiceKey) []*service_manage.Instance {
	return n.svcInstances[*key]
}

// SetServiceInstances 设置某个服务的实例
func (n *namingServer) SetServiceInstances(key *model.ServiceKey, insts []*service_manage.Instance) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.svcInstances[*key] = insts
}

// GetServiceToken 获取服务token
func (n *namingServer) GetServiceToken(key *model.ServiceKey) string {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	return n.serviceTokens[*key]
}

// GetServiceRequests 这个server经历的关于某个服务信息的请求次数，包括路由和实例
func (n *namingServer) GetServiceRequests(key *model.ServiceKey) int {
	n.rwMutex.RLock()
	res, ok := n.serviceRequests[*key]
	if !ok {
		res = 0
	}
	n.rwMutex.RUnlock()
	return res
}

// ClearServiceRequests 删除某个服务的请求记录
func (n *namingServer) ClearServiceRequests(key *model.ServiceKey) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	delete(n.serviceRequests, *key)
}

// SetPrintDiscoverReturn 设置server在返回response的时候需不需要打印log
func (n *namingServer) SetPrintDiscoverReturn(v bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.printReturn = v
}

// SetServiceRevision 设置服务的版本号
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

// InsertRouting 插入一个路由信息
func (n *namingServer) InsertRouting(svcKey model.ServiceKey, routing *traffic_manage.Routing) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.serviceRoutes[svcKey] = routing
}

// SetReturnException 设置mockserver是否返回异常
func (n *namingServer) SetReturnException(e bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.returnException = e
}

// SetNotRegisterAssistant 设置mockserver是否自动注册网格的辅助服务
func (n *namingServer) SetNotRegisterAssistant(e bool) {
	n.rwMutex.Lock()
	defer n.rwMutex.Unlock()
	n.notRegisterAssistant = e
}
