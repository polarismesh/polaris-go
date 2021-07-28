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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	//加载插件注册函数
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
)

//providerAPI 被调者对外接口实现
type providerAPI struct {
	context SDKContext
}

//Register 同步注册服务，服务注册成功后会填充instance中的InstanceId字段
// 用户可保持该instance对象用于反注册和心跳上报
func (c *providerAPI) Register(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	if err := checkAvailable(c); nil != err {
		return nil, err
	}
	if err := instance.Validate(); nil != err {
		return nil, err
	}
	return c.context.GetEngine().SyncRegister(&instance.InstanceRegisterRequest)
}

//Deregister 同步反注册服务
func (c *providerAPI) Deregister(instance *InstanceDeRegisterRequest) error {
	if err := checkAvailable(c); nil != err {
		return err
	}
	if err := instance.Validate(); nil != err {
		return err
	}
	return c.context.GetEngine().SyncDeregister(&instance.InstanceDeRegisterRequest)
}

//Heartbeat 心跳上报
func (c *providerAPI) Heartbeat(instance *InstanceHeartbeatRequest) error {
	if err := checkAvailable(c); nil != err {
		return err
	}
	if err := instance.Validate(); nil != err {
		return err
	}
	return c.context.GetEngine().SyncHeartbeat(&instance.InstanceHeartbeatRequest)
}

//获取SDK上下文
func (c *providerAPI) SDKContext() SDKContext {
	return c.context
}

//销毁API
func (c *providerAPI) Destroy() {
	if nil != c.context {
		c.context.Destroy()
	}
}

//通过以默认域名为埋点server的默认配置创建ProviderAPI
func newProviderAPI() (ProviderAPI, error) {
	return newProviderAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

//NewProviderAPIByFile 通过配置文件创建SDK ProviderAPI对象
func newProviderAPIByFile(path string) (ProviderAPI, error) {
	context, err := InitContextByFile(path)
	if nil != err {
		return nil, err
	}
	return &providerAPI{context}, nil
}

//NewProviderAPIByConfig 通过配置对象创建SDK ProviderAPI对象
func newProviderAPIByConfig(cfg config.Configuration) (ProviderAPI, error) {
	context, err := InitContextByConfig(cfg)
	if nil != err {
		return nil, err
	}
	return &providerAPI{context}, nil
}

//NewProviderAPIByContext 通过上下文创建SDK ProviderAPI对象
func newProviderAPIByContext(context SDKContext) ProviderAPI {
	return &providerAPI{context}
}

//通过系统默认配置文件创建ProviderAPI
func newProviderAPIByDefaultConfigFile() (ProviderAPI, error) {
	path := model.ReplaceHomeVar(config.DefaultConfigFile)
	return newProviderAPIByFile(path)
}
