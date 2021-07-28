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

package serverconnector

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
)

//proxy of ServerConnector
type Proxy struct {
	ServerConnector
	engine model.Engine
}

//设置
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.ServerConnector = plug.(ServerConnector)
	p.engine = engine
}

//proxy ServerConnector RegisterServiceHandler
func (p *Proxy) RegisterServiceHandler(handler *ServiceEventHandler) error {
	err := p.ServerConnector.RegisterServiceHandler(handler)
	return err
}

//proxy ServerConnector DeRegisterServiceHandler
func (p *Proxy) DeRegisterServiceHandler(key *model.ServiceEventKey) error {
	err := p.ServerConnector.DeRegisterServiceHandler(key)
	return err
}

//proxy ServerConnector RegisterInstance
func (p *Proxy) RegisterInstance(req *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	result, err := p.ServerConnector.RegisterInstance(req)
	return result, err
}

//proxy ServerConnector Heartbeat
func (p *Proxy) Heartbeat(instance *model.InstanceHeartbeatRequest) error {
	err := p.ServerConnector.Heartbeat(instance)
	return err
}

//proxy ServerConnector ReportClient
func (p *Proxy) ReportClient(req *model.ReportClientRequest) (*model.ReportClientResponse, error) {
	result, err := p.ServerConnector.ReportClient(req)
	return result, err
}

//proxy ServerConnector UpdateServers
func (p *Proxy) UpdateServers(key *model.ServiceEventKey) error {
	err := p.ServerConnector.UpdateServers(key)
	return err
}

//注册proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeServerConnector, &Proxy{})
}
