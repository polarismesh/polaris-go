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

package tcp

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"net"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
	"github.com/polarismesh/polaris-go/plugin/healthcheck/utils"
)

//Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg                 *Config
	SendPackageBytes    []byte
	ReceivePackageBytes [][]byte
	timeout             time.Duration
}

//Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

//Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeHealthCheck
}

//Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return config.DefaultTCPHealthCheck
}

//Init 初始化插件
func (g *Detector) Init(ctx *plugin.InitContext) (err error) {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConsumer().GetHealthCheck().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	g.timeout = ctx.Config.GetConsumer().GetHealthCheck().GetTimeout()
	return nil
}

//DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance) (result healthcheck.DetectResult, err error) {
	start := time.Now()
	address := utils.GetAddressByInstance(ins)
	success := g.doTCPDetect(address)
	result = &healthcheck.DetectResultImp{
		Success:        success,
		DetectTime:     start,
		DetectInstance: ins,
	}
	return result, nil
}

// doTCPDetect 执行一次探测逻辑
func (g *Detector) doTCPDetect(address string) bool {
	// 建立连接
	conn, err := net.DialTimeout("tcp", address, g.timeout)
	if err != nil {
		log.GetDetectLogger().Errorf("[HealthCheck][tcp] fail to check %s, err is %v", address, err)
		return false
	}
	conn.Close()
	return true
}

// enable
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

//init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
