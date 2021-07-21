/**
 * Tencent is pleased to support the open source community by making CL5 available.
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

package http

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"net/http"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/outlierdetection"
	"github.com/polarismesh/polaris-go/plugin/outlierdetection/utils"
)

//Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg *Config
}

//Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeOutlierDetector
}

//Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return "http"
}

//Init 初始化插件
func (g *Detector) Init(ctx *plugin.InitContext) (err error) {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConsumer().GetOutlierDetectionConfig().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

//DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance) (result common.DetectResult, err error) {
	if g.cfg.HTTPPattern == "" {
		return nil, nil
	}
	start := time.Now()
	retCode := model.RetFail

	// 得到Http address
	address, e := utils.GetAddressByInstance(ins)
	if e != nil {
		return nil, model.NewSDKError(model.ErrCodeInstanceInfoError,
			fmt.Errorf("Face Error In DetectInstance By %s:%s", g.Name(), e.Error()), "")
	}

	retCode = g.doHttpDetect(address)
	result = &outlierdetection.DetectResultImp{
		DetectType:     g.Name(),
		DetectTime:     start,
		RetStatus:      retCode,
		DetectInstance: ins,
	}
	return result, nil
}

// enable
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

// doHttpDetect 执行一次健康探测逻辑
func (g *Detector) doHttpDetect(address string) model.RetStatus {
	c := &http.Client{
		Timeout: g.cfg.Timeout,
	}
	resp, e := c.Get(fmt.Sprintf("http://%s%s", address, g.cfg.HTTPPattern))
	if e != nil {
		return model.RetFail
	}
	if resp.StatusCode >= 200 && resp.StatusCode < 400 {
		return model.RetSuccess
	}
	return model.RetFail
}

//init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
