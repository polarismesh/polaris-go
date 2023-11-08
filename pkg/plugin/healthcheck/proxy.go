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

package healthcheck

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"
)

// Proxy .proxy of HealthChecker
type Proxy struct {
	HealthChecker
	engine model.Engine
}

// SetRealPlugin 设置
func (p *Proxy) SetRealPlugin(plug plugin.Plugin, engine model.Engine) {
	p.HealthChecker = plug.(HealthChecker)
	p.engine = engine
}

// DetectInstance proxy HealthChecker DetectInstance
func (p *Proxy) DetectInstance(inst model.Instance) (DetectResult, error) {
	result, err := p.HealthChecker.DetectInstance(inst)
	return result, err
}

// Protocol .
func (p *Proxy) Protocol() fault_tolerance.FaultDetectRule_Protocol {
	return p.HealthChecker.Protocol()
}

// init 注册proxy
func init() {
	plugin.RegisterPluginProxy(common.TypeHealthCheck, &Proxy{})
}
