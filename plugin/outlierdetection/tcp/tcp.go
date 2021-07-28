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

//Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg               *Config
	SendPackageBytes  []byte
	CheckPackageBytes []byte
}

//Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

//Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeOutlierDetector
}

//Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return config.DefaultTCPOutlierDetect
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

//DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance) (result common.DetectResult, err error) {
	start := time.Now()
	retCode := model.RetFail
	defer func() {
		result = &outlierdetection.DetectResultImp{
			DetectType:     g.Name(),
			RetStatus:      retCode,
			DetectTime:     start,
			DetectInstance: ins,
		}
	}()
	// 得到tcp address
	address, e := utils.GetAddressByInstance(ins)
	if e != nil {
		return nil, model.NewSDKError(model.ErrCodeInstanceInfoError,
			fmt.Errorf("Face Error In DetectInstance By %s:%s", g.Name(), e.Error()), "")
	}
	for i := 0; i < g.cfg.RetryTimes+1; i++ {
		retCode = g.doTCPDetect(address)
		if retCode == model.RetSuccess {
			break
		}
	}
	return result, nil
}

// doTCPDetect 执行一次探测逻辑
func (g *Detector) doTCPDetect(address string) model.RetStatus {
	// 建立连接
	retCode := model.RetFail
	conn, e := net.DialTimeout("tcp", address, g.cfg.Timeout)
	if e != nil {
		return retCode
	}
	if conn == nil {
		return retCode
	}
	defer conn.Close()
	// 默认配置无探测请求和应答包格式，表示只进行连接探测，连接成功即表示恢复
	if len(g.SendPackageBytes) == 0 || len(g.CheckPackageBytes) == 0 {
		retCode = model.RetSuccess
		return retCode
	}
	// 当用户主动配置的探测请求包和应答包格式时才进行发包探测。
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
	recvBuf := bytes.Buffer{}
	for {
		var n int
		n, e = conn.Read(buf[0:])
		// 收回包遇到错误，认为服务未恢复
		if e != nil && e != io.EOF {
			return retCode
		}
		if n == 0 || e == io.EOF {
			// 对端关闭连接，认为服务未恢复
			break
		}
		// check回包
		recvBuf.Write(buf[0:n])
		if recvBuf.Len() >= 4 {
			if bytes.Equal(recvBuf.Bytes(), g.CheckPackageBytes) {
				retCode = model.RetSuccess
				break
			}
			return retCode
		}
	}
	return retCode
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
