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
	"fmt"
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

// Detector UDP 协议的实例健康探测器
type Detector struct {
	*plugin.PluginBase
	cfg                 *Config
	SendPackageBytes    []byte
	ReceivePackageBytes [][]byte
	timeout             time.Duration
	// 上下文日志
	logCtx *log.ContextLogger
	// lastErr 记录每个探测地址的上一次错误信息（按错误类型分 key），err 内容不变时静默不重复打印。
	lastErr map[string]string
	// lastErrMu 保护 lastErr 的并发访问。
	lastErrMu sync.Mutex
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
	return config.DefaultUDPHealthCheck
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
	g.lastErr = make(map[string]string, 16)
	return nil
}

// DetectInstance 探测服务实例健康
func (g *Detector) DetectInstance(ins model.Instance, rule *fault_tolerance.FaultDetectRule) (result healthcheck.DetectResult, err error) {
	start := time.Now()
	address := fmt.Sprintf("%s:%d", ins.GetHost(), ins.GetPort())
	if rule != nil && rule.GetPort() > 0 {
		address = fmt.Sprintf("%s:%d", ins.GetHost(), rule.GetPort())
	}
	success := g.doUDPDetect(address, rule)
	result = &healthcheck.DetectResultImp{
		Success:        success,
		DetectTime:     start,
		DetectInstance: ins,
		// 与 TCP 探测器一致：成功置 "0"、失败置 "-1"。"-1" 是熔断器 block_counter.parseRetStatus
		// 的失败哨兵（RET_CODE 条件下 RetCode=="-1" 直接判 RetFail），否则探测失败的 stat
		// 因 RetCode 为空不命中 RANGE 500~599 规则，会被误判为成功、无法维持 OPEN。
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
func (g *Detector) doUDPDetect(address string, rule *fault_tolerance.FaultDetectRule) bool {
	timeout := g.timeout
	if rule != nil {
		timeout = time.Duration(rule.GetTimeout()) * time.Millisecond
	}
	// 建立连接
	conn, err := net.DialTimeout("udp", address, timeout)
	if err != nil {
		g.logConvergedErr(address, "dial", err, "fail to check")
		return false
	}
	defer func() {
		_ = conn.Close()
	}()
	// 探测连通成功，每个探测周期都会产生，使用 Debug 级别记录，便于确认探测真实发起。
	g.logCtx.GetDetectLogger().Debugf("[HealthCheck][udp] connect success, address=%s", address)
	if rule == nil || rule.GetUdpConfig() == nil {
		delete(g.lastErr, address+"|dial")
		delete(g.lastErr, address+"|write")
		delete(g.lastErr, address+"|deadline")
		delete(g.lastErr, address+"|read")
		return true
	}
	udpCfg := rule.GetUdpConfig()
	if udpCfg.Send == "" {
		delete(g.lastErr, address+"|dial")
		delete(g.lastErr, address+"|write")
		delete(g.lastErr, address+"|deadline")
		delete(g.lastErr, address+"|read")
		return true
	}
	if _, err = conn.Write([]byte(udpCfg.Send)); err != nil {
		g.logConvergedErr(address, "write", err, "fail to write send body")
		return false
	}
	// UDP 无连接、无 EOF：
	//   - 对端不回包时若用 ioutil.ReadAll 会永久阻塞、占死探测 goroutine；
	//   - 用 ReadAll + deadline 时，即便读到了响应，ReadAll 也会一直等到 deadline 才返回，
	//     且返回的 err 是 i/o timeout（非 io.EOF），导致"已收到正确响应"也被误判为失败。
	// 因此改用单次 conn.Read：读到一个 UDP 响应包即返回，能区分"超时无响应（失败）"与
	// "收到响应（按内容匹配）"。
	if err = conn.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		g.logConvergedErr(address, "deadline", err, "fail to set read deadline")
		return false
	}
	recvBuf := make([]byte, 1024)
	n, err := conn.Read(recvBuf)
	if err != nil {
		// 超时（i/o timeout）或其它读错误：对端未在 timeout 内回包，探测失败。
		g.logConvergedErr(address, "read", err, "fail to read receive data")
		return false
	}
	actualData := string(recvBuf[:n])
	found := false
	for i := range udpCfg.Receive {
		if udpCfg.Receive[i] == actualData {
			found = true
		}
	}
	if found {
		// 探测完全成功，清除所有 err 记录，确保下次异常能重新打印。
		delete(g.lastErr, address+"|dial")
		delete(g.lastErr, address+"|write")
		delete(g.lastErr, address+"|deadline")
		delete(g.lastErr, address+"|read")
	}
	return found
}

// logConvergedErr 探测异常收敛打印：首次出现或 err 内容变化时打印 Errorf，err 不变时静默。
func (g *Detector) logConvergedErr(address, stage string, err error, msg string) {
	g.lastErrMu.Lock()
	defer g.lastErrMu.Unlock()
	if g.lastErr == nil {
		g.lastErr = make(map[string]string, 8)
	}
	errKey := address + "|" + stage
	errMsg := err.Error()
	if lastErr, ok := g.lastErr[errKey]; !ok || lastErr != errMsg {
		g.lastErr[errKey] = errMsg
		g.logCtx.GetDetectLogger().Errorf("[HealthCheck][udp] %s address=%s, err=%v", msg, address, err)
	}
}

// Protocol .
func (g *Detector) Protocol() fault_tolerance.FaultDetectRule_Protocol {
	return fault_tolerance.FaultDetectRule_UDP
}

// IsEnable enable
func (g *Detector) IsEnable(cfg config.Configuration) bool {
	return cfg.GetGlobal().GetSystem().GetMode() != model.ModeWithAgent
}

// init 注册插件信息
func init() {
	plugin.RegisterConfigurablePlugin(&Detector{}, &Config{})
}
