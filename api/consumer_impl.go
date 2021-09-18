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

package api

import (
	"fmt"
	"github.com/hashicorp/go-multierror"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/model"

	//加载插件注册函数
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
)

//consumerAPI 调用者对外函数实现
type consumerAPI struct {
	context SDKContext
}

//GetOneInstance sync get one instance after load balance
func (c *consumerAPI) GetOneInstance(req *GetOneInstanceRequest) (*model.OneInstanceResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := req.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetOneInstance(&req.GetOneInstanceRequest)
}

//GetInstances sync get one instances after route
func (c *consumerAPI) GetInstances(req *GetInstancesRequest) (*model.InstancesResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := req.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetInstances(&req.GetInstancesRequest)
}

//获取完整的服务列表
func (c *consumerAPI) GetAllInstances(req *GetAllInstancesRequest) (*model.InstancesResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := req.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetAllInstances(&req.GetAllInstancesRequest)
}

//UpdateServiceCallResult update the service call error code and delay
func (c *consumerAPI) UpdateServiceCallResult(req *ServiceCallResult) error {
	if err := checkAvailable(c); nil != err {
		return err
	}
	if err := req.Validate(); nil != err {
		return err
	}
	return c.context.GetEngine().SyncUpdateServiceCallResult(&req.ServiceCallResult)
}

// 同步获取服务路由规则
func (c *consumerAPI) GetRouteRule(req *GetServiceRuleRequest) (*model.ServiceRuleResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := req.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetServiceRule(model.EventRouting, &req.GetServiceRuleRequest)
}

//同步获取mesh配置
func (c *consumerAPI) GetMeshConfig(req *GetMeshConfigRequest) (*model.MeshConfigResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetMeshConfig(model.EventMeshConfig, &req.GetMeshConfigRequest)
}

// 同步获取网格
func (c *consumerAPI) GetMesh(req *GetMeshRequest) (*model.MeshResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetMesh(model.EventMesh, &req.GetMeshRequest)
}

//同步获取批量服务
func (c *consumerAPI) GetServicesByBusiness(req *GetServicesRequest) (*model.ServicesResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := req.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncGetServices(model.EventServices, &req.GetServicesRequest)
}

//初始化服务运行中需要的被调服务
func (c *consumerAPI) InitCalleeService(req *InitCalleeServiceRequest) error {
	if err := checkAvailable(c); nil != err {
		return err
	}
	if err := req.Validate(); nil != err {
		return err
	}
	return c.context.GetEngine().InitCalleeService(&req.InitCalleeServiceRequest)
}

//获取SDK上下文
func (c *consumerAPI) SDKContext() SDKContext {
	return c.context
}

//销毁API，销毁后无法再进行调用
func (c *consumerAPI) Destroy() {
	if nil != c.context {
		c.context.Destroy()
	}
}

//订阅服务消息
func (c *consumerAPI) WatchService(req *WatchServiceRequest) (*model.WatchServiceResponse, error) {
	if err := checkAvailable(c); err != nil {
		return nil, err
	}
	if err := req.Validate(); err != nil {
		return nil, err
	}
	return c.context.GetEngine().WatchService(&req.WatchServiceRequest)
}

//通过以默认域名为埋点server的默认配置创建ConsumerAPI
func newConsumerAPI() (ConsumerAPI, error) {
	return NewConsumerAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

//NewConsumerAPIByFile 通过配置文件创建SDK ConsumerAPI对象
func newConsumerAPIByFile(path string) (ConsumerAPI, error) {
	context, err := InitContextByFile(path)
	if nil != err {
		return nil, err
	}
	return &consumerAPI{context}, nil
}

//NewConsumerAPIByFile 通过配置对象创建SDK ConsumerAPI对象
func newConsumerAPIByConfig(cfg config.Configuration) (ConsumerAPI, error) {
	context, err := InitContextByConfig(cfg)
	if nil != err {
		return nil, err
	}
	return &consumerAPI{context}, nil
}

//NewConsumerAPIByContext 通过上下文创建SDK ConsumerAPI对象
func newConsumerAPIByContext(context SDKContext) ConsumerAPI {
	return &consumerAPI{context}
}

//从系统默认配置文件中创建ConsumerAPI
func newConsumerAPIByDefaultConfigFile() (ConsumerAPI, error) {
	return NewConsumerAPIByFile(config.DefaultConfigFile)
}

//实例请求
type InstanceRequest struct {
	//服务标识
	model.ServiceKey
	//实例ID
	InstanceId string

	IP   string
	Port uint16
}

//校验实例请求对象
func (g InstanceRequest) Validate() error {
	var errs error
	if len(g.ServiceKey.Namespace) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRequest ServiceKey Namespace is empty"))
	}
	if len(g.ServiceKey.Service) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRequest ServiceKey Service is empty"))
	}
	if len(g.InstanceId) == 0 {
		errs = multierror.Append(errs, fmt.Errorf("InstanceRequest InstanceId is empty"))
	}
	if errs != nil {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, errs, "fail to validate InstanceRequest")
	} else {
		return nil
	}
}

//创建上报结果对象
func newServiceCallResult(ctx SDKContext, request InstanceRequest) (*ServiceCallResult, error) {
	if ctx.IsDestroyed() {
		return nil, model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"api SDKContext has been destroyed")
	}
	if err := request.Validate(); err != nil {
		return nil, err
	}

	serviceKey := model.ServiceKey{
		Namespace: request.Namespace,
		Service:   request.Service,
	}
	registry, err := data.GetRegistry(ctx.GetConfig(), ctx.GetPlugins())
	if err != nil {
		return nil, err
	}
	instances := registry.GetInstances(&serviceKey, true, false)
	if instances.IsInitialized() == false {
		return nil, model.NewSDKError(model.ErrCodeServiceNotFound, nil,
			fmt.Sprintf("not found instances in Registry service_key:%s", serviceKey))
	}
	ins := instances.GetInstance(request.InstanceId)
	if reflect2.IsNil(ins) {
		return nil, model.NewSDKError(model.ErrCodeAPIInstanceNotFound, nil,
			fmt.Sprintf("not found instance in Registry service_key:%s instanceId:%s ip:%s port:%d",
				serviceKey, request.InstanceId, request.IP, request.Port))
	}
	serviceCallResult := ServiceCallResult{}
	serviceCallResult.SetCalledInstance(ins)
	return &serviceCallResult, nil
}
