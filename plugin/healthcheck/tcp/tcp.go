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
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"

	"github.com/polarismesh/specification/source/go/api/v1/fault_tolerance"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/healthcheck"
)

// Detector TCP协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg                 *Config
	SendPackageBytes    []byte
	ReceivePackageBytes [][]byte
	timeout             time.Duration
	// 上下文日志
	logCtx *log.ContextLogger
	// lastDialErr 记录每个探测地址的上一次 TCP 连接错误信息，err 内容不变时静默不重复打印。
	lastDialErr map[string]string
	// lastDialErrMu 保护 lastDialErr 的并发访问。
	lastDialErrMu sync.Mutex
}

// Destroy 销毁插件，可用于释放资源
func (g *Detector) Destroy() error {
	return nil
}

// Type 插件类型
func (g *Detector) Type() common.Type {
	return common.TypeHealthCheck
}

// Name 插件名，一个类型下插件名唯一
func (g *Detector) Name() string {
	return config.DefaultTCPHealthCheck
}

// Init 初始化插件
func (g *Detector) Init(ctx *plugin.InitContext) (err error) {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetConsumer().GetHealthCheck().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	g.timeout = ctx.Config.GetConsumer().GetHealthCheck().GetTimeout()
	g.logCtx = ctx.ValueCtx.GetContextLogger()
	g.lastDialErr = make(map[string]string, 16)
	return nil
}

// DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance, rule *fault_tolerance.FaultDetectRule) (result healthcheck.DetectResult, err error) {
	start := time.Now()
	address := fmt.Sprintf("%s:%d", ins.GetHost(), ins.GetPort())
	if rule != nil && rule.GetPort() > 0 {
		address = fmt.Sprintf("%s:%d", ins.GetHost(), rule.GetPort())
	}
	success := g.doTCPDetect(address, rule)
	result = &healthcheck.DetectResultImp{
		Success:        success,
		DetectTime:     start,
		DetectInstance: ins,
		Code: func() string {
			if success {
				return "0"
			}
			return "-1"
		}(),
	}
	return result, nil
}

// doTCPDetect 执行一次探测逻辑
func (g *Detector) doTCPDetect(address string, rule *fault_tolerance.FaultDetectRule) bool {
	timeout := g.timeout
	if rule != nil {
		timeout = time.Duration(rule.GetTimeout()) * time.Millisecond
	}
	// 建立连接
	conn, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		errMsg := err.Error()
		// 探测异常收敛：err 内容不变时仅首次打印，避免连接超时/拒绝等重复刷屏。
		g.lastDialErrMu.Lock()
		if g.lastDialErr == nil {
			g.lastDialErr = make(map[string]string, 8)
		}
		if lastErr, ok := g.lastDialErr[address]; !ok || lastErr != errMsg {
			g.lastDialErr[address] = errMsg
			g.lastDialErrMu.Unlock()
			g.logCtx.GetDetectLogger().Errorf("[HealthCheck][tcp] fail to check %s, err is %v", address, err)
		} else {
			g.lastDialErrMu.Unlock()
		}
		return false
	}
	// err 恢复后清除记录，确保下次异常能重新打印。
	g.lastDialErrMu.Lock()
	if g.lastDialErr != nil {
		delete(g.lastDialErr, address)
	}
	g.lastDialErrMu.Unlock()
	defer func() {
		_ = conn.Close()
	}()
	// 探测连通成功，每个探测周期都会产生，使用 Debug 级别记录，便于确认探测真实发起。
	g.logCtx.GetDetectLogger().Debugf("[HealthCheck][tcp] connect success, address=%s", address)
	if rule == nil || rule.GetTcpConfig() == nil {
		return true
	}
	// 发送数据
	tcpCfg := rule.GetTcpConfig()
	if tcpCfg.Send == "" {
		return true
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return false
	}
	if _, err = conn.Write([]byte(tcpCfg.Send)); err != nil {
		return false
	}
	recvData, err := ioutil.ReadAll(conn)
	if err != nil && err != io.EOF {
		return false
	}
	actualData := string(recvData)
	found := false
	for i := range tcpCfg.Receive {
		if tcpCfg.Receive[i] == actualData {
			found = true
		}
	}
	return found
}

// Protocol .
func (g *Detector) Protocol() fault_tolerance.FaultDetectRule_Protocol {
	return fault_tolerance.FaultDetectRule_TCP
}

// IsEnable enable
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
