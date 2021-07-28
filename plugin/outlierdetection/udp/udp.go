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

package udp

import (
	"bytes"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"io"
	"net"
	"time"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/outlierdetection"
	"github.com/polarismesh/polaris-go/plugin/outlierdetection/utils"
)

//Detector UDP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg *Config
	// SendPackageBytes 实际发送的请求包
	SendPackageBytes []byte
	// CheckpackageBytes 实际校验的返回包
	CheckPackageBytes []byte
}

//Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeOutlierDetector
}

//Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return config.DefaultUDPOutlierDetect
}

//Init 初始化插件
func (g *Detector) Init(ctx *plugin.InitContext) (err error) {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConsumer().GetOutlierDetectionConfig().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	g.SendPackageBytes, g.CheckPackageBytes = utils.ConvertPackageConf(g.cfg.SendPackgeConf, g.cfg.CheckPackageConf)
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

// enable
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

//DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance) (result common.DetectResult, err error) {
	// 如果没有配置收发包，则不进行探测逻辑
	if len(g.SendPackageBytes) == 0 || len(g.CheckPackageBytes) == 0 {
		return nil, nil
	}
	start := time.Now()
	retCode := model.RetFail

	// 得到udp address
	address, e := utils.GetAddressByInstance(ins)
	if e != nil {
		return nil, model.NewSDKError(model.ErrCodeInstanceInfoError,
			fmt.Errorf("Face Error In DetectInstance By %s:%s", g.Name(), e.Error()), "")
	}

	for i := 0; i < g.cfg.RetryTimes+1; i++ {
		retCode = g.doUDPDetect(address)
		if retCode == model.RetSuccess {
			break
		}
	}
	result = &outlierdetection.DetectResultImp{
		DetectType:     g.Name(),
		DetectTime:     start,
		RetStatus:      retCode,
		DetectInstance: ins,
	}
	return result, nil
}

// doUDPDetect 执行一次健康探测逻辑
func (g *Detector) doUDPDetect(address string) model.RetStatus {
	retCode := model.RetFail
	udpAddr, e := net.ResolveUDPAddr("udp", address)
	if e != nil {
		return retCode
	}
	// 建立连接
	conn, e := net.DialUDP("udp", nil, udpAddr)
	if e != nil {
		return retCode
	}
	if conn == nil {
		return retCode
	}
	defer conn.Close()
	// 设置超时时间
	e = conn.SetDeadline(time.Now().Add(g.cfg.Timeout))
	if e != nil {
		return retCode
	}
	// 发包 0x0000abcd
	_, e = conn.Write(g.SendPackageBytes)
	if e != nil {
		return retCode
	}
	// 收包 0x0000dcba
	buf := make([]byte, 4)
	var n int
	n, _, e = conn.ReadFromUDP(buf[0:])
	// 收回包遇到错误，认为服务未恢复
	if e != nil && e != io.EOF {
		return retCode
	}
	if n > 0 {
		// check回包
		if bytes.Equal(buf, g.CheckPackageBytes) {
			retCode = model.RetSuccess
		}
	}
	return retCode
}

//init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
