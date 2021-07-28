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

package localregistry

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/golang/protobuf/proto"
)

//proxy of LocalRegistry
type Proxy struct {
	LocalRegistry
	engine model.Engine
}

//设置
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.LocalRegistry = plug.(LocalRegistry)
	p.engine = engine
}

//proxy LocalRegistry LoadInstances
func (p *Proxy) LoadInstances(svcKey *model.ServiceKey) (*common.Notifier, error) {
	result, err := p.LocalRegistry.LoadInstances(svcKey)
	return result, err
}

//proxy LocalRegistry UpdateInstances
func (p *Proxy) UpdateInstances(req *ServiceUpdateRequest) error {
	err := p.LocalRegistry.UpdateInstances(req)
	return err
}

//proxy LocalRegistry PersistMessage
func (p *Proxy) PersistMessage(file string, msg proto.Message) error {
	err := p.LocalRegistry.PersistMessage(file, msg)
	return err
}

//func (p *Proxy) LoadPersistedMessage(file string, msg proto.Message) error {
//	err := p.LocalRegistry.LoadPersistedMessage(file, msg)
//	stat.ReportPluginStat(p, p.engine, stat.MethodLoadPersistedMessage, err)
//	return err
//}

//proxy LocalRegistry LoadServiceRouteRule
func (p *Proxy) LoadServiceRouteRule(key *model.ServiceKey) (*common.Notifier, error) {
	result, err := p.LocalRegistry.LoadServiceRouteRule(key)
	return result, err
}

//加载网格规则
func (p *Proxy) LoadMeshConfig(key *model.ServiceKey) (*common.Notifier, error) {
	result, err := p.LocalRegistry.LoadMeshConfig(key)
	return result, err
}

//proxy LocalRegistry LoadServiceRateLimitRule
func (p *Proxy) LoadServiceRateLimitRule(key *model.ServiceKey) (*common.Notifier, error) {
	result, err := p.LocalRegistry.LoadServiceRateLimitRule(key)
	return result, err
}

//注册proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeLocalRegistry, &Proxy{})
}
