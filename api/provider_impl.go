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
	"sync"
	"time"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
)

// providerAPI 被调者对外接口实现
type providerAPI struct {
	context          SDKContext
	mutex            *sync.Mutex
	registerStateMap map[string]*registerState
}

type registerState struct {
	instance         *InstanceRegisterRequest
	lastRegisterTime time.Time
	stoppedchan      chan struct{}
}

func newRegisterState(instance *InstanceRegisterRequest) *registerState {
	return &registerState{
		instance:         instance,
		lastRegisterTime: time.Now(),
		stoppedchan:      make(chan struct{}),
	}
}

func (c *providerAPI) putRegisterState(instance *InstanceRegisterRequest) (*registerState, bool) {
	key := buildRegisterKey(instance.Namespace, instance.Service, instance.Host, instance.Port)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	_, ok := c.registerStateMap[key]
	if !ok {
		state := newRegisterState(instance)
		c.registerStateMap[key] = state
		return state, true
	}
	return nil, false
}

func (c *providerAPI) removeRegisterState(instance *InstanceDeRegisterRequest) (*registerState, bool) {
	key := buildRegisterKey(instance.Namespace, instance.Service, instance.Host, instance.Port)
	c.mutex.Lock()
	defer c.mutex.Unlock()
	state, ok := c.registerStateMap[key]
	if ok {
		delete(c.registerStateMap, key)
		return state, true
	}
	return nil, false
}

func buildRegisterKey(namespace string, service string, host string, port int) string {
	return fmt.Sprintf("%s##%s##%s##%d", namespace, service, host, port)
}

func (c *providerAPI) AsyncRegister(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	if err := checkAvailable(c); err != nil {
		return nil, err
	}
	instance.SetDefaultAsyncRegister()
	if err := instance.ValidateAsyncRegister(); err != nil {
		return nil, err
	}

	resp, err := c.context.GetEngine().SyncRegister(&instance.InstanceRegisterRequest)
	if err != nil {
		return nil, err
	}

	state, ok := c.putRegisterState(instance)
	if ok {
		go c.runAsyncRegisterTask(state)
	}
	return resp, nil
}

func (c *providerAPI) runAsyncRegisterTask(state *registerState) {
	GetBaseLogger().Infof("async register task started %s", state.instance.String())
	ticker := time.NewTicker(time.Duration(*state.instance.TTL) * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-state.stoppedchan:
			GetBaseLogger().Infof("async register task stopped %s", state.instance.String())
			return
		case <-ticker.C:
			if err := checkAvailable(c); err != nil {
				GetBaseLogger().Errorf("async register task abnormal %s", state.instance.String(), err)
				break
			}
			err := c.doHeartbeat(state.instance)
			if err == nil {
				break
			}
			sdkErr, ok := err.(model.SDKError)
			if !ok {
				break
			}
			ec := sdkErr.ServerCode()
			// HeartbeatOnDisabledIns
			if ec != 400141 {
				break
			}
			minInterval := c.context.GetConfig().GetProvider().GetMinRegisterInterval()
			now := time.Now()
			if now.Before(state.lastRegisterTime.Add(minInterval)) {
				break
			}

			state.lastRegisterTime = now
			_, err = c.context.GetEngine().SyncRegister(&state.instance.InstanceRegisterRequest)
			if err == nil {
				GetBaseLogger().Infof("re-register instatnce success %s", state.instance.String())
			} else {
				GetBaseLogger().Warnf("re-register instatnce failed %s", state.instance.String(), err)
			}
		}
	}
}

func (c *providerAPI) doHeartbeat(instance *InstanceRegisterRequest) error {
	hbRequest := &InstanceHeartbeatRequest{}
	hbRequest.Namespace = instance.Namespace
	hbRequest.Service = instance.Service
	hbRequest.Host = instance.Host
	hbRequest.Port = instance.Port
	hbRequest.ServiceToken = instance.ServiceToken
	err := c.context.GetEngine().SyncHeartbeat(&hbRequest.InstanceHeartbeatRequest)
	if err != nil {
		GetBaseLogger().Warnf("heartbeat failed %s", instance.String(), err)
	} else {
		GetBaseLogger().Debugf("heartbeat success %s", instance.String())
	}
	return err
}

// Register 同步注册服务，服务注册成功后会填充instance中的InstanceId字段
// 用户可保持该instance对象用于反注册和心跳上报
func (c *providerAPI) Register(instance *InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	if err := checkAvailable(c); err != nil {
		return nil, err
	}
	if err := instance.Validate(); err != nil {
		return nil, err
	}
	return c.context.GetEngine().SyncRegister(&instance.InstanceRegisterRequest)
}

// Deregister 同步反注册服务
func (c *providerAPI) Deregister(instance *InstanceDeRegisterRequest) error {
	if err := checkAvailable(c); err != nil {
		return err
	}
	if err := instance.Validate(); err != nil {
		return err
	}

	state, ok := c.removeRegisterState(instance)
	if ok {
		close(state.stoppedchan)
	}

	return c.context.GetEngine().SyncDeregister(&instance.InstanceDeRegisterRequest)
}

// Heartbeat 心跳上报
func (c *providerAPI) Heartbeat(instance *InstanceHeartbeatRequest) error {
	if err := checkAvailable(c); err != nil {
		return err
	}
	if err := instance.Validate(); err != nil {
		return err
	}
	return c.context.GetEngine().SyncHeartbeat(&instance.InstanceHeartbeatRequest)
}

// SDKContext 获取SDK上下文
func (c *providerAPI) SDKContext() SDKContext {
	return c.context
}

// Destroy 销毁API
func (c *providerAPI) Destroy() {
	if nil != c.context {
		c.context.Destroy()
	}
}

// newProviderAPI 通过以默认域名为埋点server的默认配置创建ProviderAPI
func newProviderAPI() (ProviderAPI, error) {
	return newProviderAPIByConfig(config.NewDefaultConfigurationWithDomain())
}

// NewProviderAPIByFile 通过配置文件创建SDK ProviderAPI对象
func newProviderAPIByFile(path string) (ProviderAPI, error) {
	context, err := InitContextByFile(path)
	if err != nil {
		return nil, err
	}
	return newProviderAPIByContext(context), nil
}

// NewProviderAPIByConfig 通过配置对象创建SDK ProviderAPI对象
func newProviderAPIByConfig(cfg config.Configuration) (ProviderAPI, error) {
	context, err := InitContextByConfig(cfg)
	if err != nil {
		return nil, err
	}
	return newProviderAPIByContext(context), nil
}

// NewProviderAPIByContext 通过上下文创建SDK ProviderAPI对象
func newProviderAPIByContext(context SDKContext) ProviderAPI {
	return &providerAPI{context, &sync.Mutex{}, map[string]*registerState{}}
}

// newProviderAPIByDefaultConfigFile 通过系统默认配置文件创建ProviderAPI
func newProviderAPIByDefaultConfigFile() (ProviderAPI, error) {
	path := model.ReplaceHomeVar(config.DefaultConfigFile)
	return newProviderAPIByFile(path)
}

// newProviderAPIByAddress 通过address创建ProviderAPI
func newProviderAPIByAddress(address ...string) (ProviderAPI, error) {
	conf := config.NewDefaultConfiguration(address)
	return newProviderAPIByConfig(conf)
}
