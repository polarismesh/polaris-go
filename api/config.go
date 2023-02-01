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

// Package api is the core API of polaris-go, which provides the basic functions of service discovery and.
package api

import (
	"fmt"
	"io/ioutil"
	"net"
	"os"
	"strings"
	"sync/atomic"
	"time"

	"github.com/google/uuid"
	"github.com/hashicorp/go-multierror"
	"github.com/modern-go/reflect2"
	"gopkg.in/yaml.v2"

	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	_ "github.com/polarismesh/polaris-go/pkg/plugin/register"
	"github.com/polarismesh/polaris-go/pkg/version"
)

const (
	// LBPolicyWeightedRandom 权重随机负载均衡策略
	LBPolicyWeightedRandom = config.DefaultLoadBalancerWR
	// LBPolicyRingHash 权重一致性hash负载均衡策略
	LBPolicyRingHash = config.DefaultLoadBalancerRingHash
	// LBPolicyMaglev Maglev算法的一致性hash负载均衡策略
	LBPolicyMaglev = config.DefaultLoadBalancerMaglev
	// LBPolicyL5CST L5一致性Hash兼容算法，保证和L5产生相同的结果
	LBPolicyL5CST = config.DefaultLoadBalancerL5CST
)

// SDKContext .
// @brief SDK配置对象，每个API实例都会挂载一个context，包含：
// 插件实例列表
// 配置实例
// 执行流程引擎，包括定时器等
type SDKContext interface {
	// Destroy
	// @brief 销毁SDK上下文
	Destroy()

	// IsDestroyed
	// @brief SDK上下文是否已经销毁
	IsDestroyed() bool

	// GetConfig
	// @brief 获取全局配置信息
	GetConfig() config.Configuration

	// GetPlugins
	// @brief 获取插件列表
	GetPlugins() plugin.Manager

	// GetEngine
	// @brief 获取执行引擎
	GetEngine() model.Engine

	// GetValueContext
	// @brief 获取值上下文
	GetValueContext() model.ValueContext
}

// SDKOwner 获取SDK上下文接口
type SDKOwner interface {
	// SDKContext 获取SDK上下文
	SDKContext() SDKContext
}

// CheckAvailable 判断API是否可用
func CheckAvailable(owner SDKOwner) error {
	return checkAvailable(owner)
}

// checkAvailable 判断API是否可用
func checkAvailable(owner SDKOwner) error {
	if reflect2.IsNil(owner) {
		return model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "API can not be nil")
	}
	if owner.SDKContext().IsDestroyed() {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil,
			"api instance has been destroyed")
	}
	return nil
}

// sdkContext SDK上下文实现
type sdkContext struct {
	config       config.Configuration
	plugins      plugin.Manager
	engine       model.Engine
	valueContext model.ValueContext
	// 标识是否已经销毁，0未销毁，1已销毁
	destroyed uint32
}

// Destroy 销毁SDK上下文
func (s *sdkContext) Destroy() {
	var err error
	atomic.StoreUint32(&s.destroyed, 1)
	err = s.engine.Destroy()
	if err != nil {
		log.GetBaseLogger().Errorf("fail to destroy engine, error %+v", err)
	}
	err = s.plugins.DestroyPlugins()
	if err != nil {
		log.GetBaseLogger().Errorf("fail to destroy plugins, error %+v", err)
	}
}

// IsDestroyed SDK上下文是否已经销毁
func (s *sdkContext) IsDestroyed() bool {
	return atomic.LoadUint32(&s.destroyed) > 0
}

// GetConfig 获取全局配置信息
func (s *sdkContext) GetConfig() config.Configuration {
	return s.config
}

// GetPlugins 获取插件列表
func (s *sdkContext) GetPlugins() plugin.Manager {
	return s.plugins
}

// GetEngine 获取执行引擎
func (s *sdkContext) GetEngine() model.Engine {
	return s.engine
}

// GetValueContext 获取值上下文
func (s *sdkContext) GetValueContext() model.ValueContext {
	return s.valueContext
}

// InitContextByFile 通过配置文件新建服务消费者配置
func InitContextByFile(path string) (SDKContext, error) {
	if !model.IsFile(path) {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, nil, "invalid context file %s", path)
	}
	buff, err := ioutil.ReadFile(path)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, err, "fail to read context file %s", path)
	}
	return InitContextByStream(buff)
}

// InitContextByStream 通过YAML流新建服务消费者配置
func InitContextByStream(buf []byte) (SDKContext, error) {
	cfg, err := config.LoadConfiguration(buf)
	if err != nil {
		return nil, err
	}
	return InitContextByConfig(cfg)
}

// checkLoggersDir 检查日志目录是否可写
func checkLoggersDir() error {
	var (
		errs error
		err  error
	)
	if l, ok := log.GetBaseLogger().(log.DirLogger); ok && !l.IsLevelEnabled(log.NoneLog) {
		err = model.EnsureAndVerifyDir(l.GetLogDir())
		if err != nil {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create base logger dir: %s", l.GetLogDir())))
		}
	}
	if l, ok := log.GetDetectLogger().(log.DirLogger); ok && !l.IsLevelEnabled(log.NoneLog) {
		err = model.EnsureAndVerifyDir(l.GetLogDir())
		if err != nil {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create detect logger dir: %s", l.GetLogDir())))
		}
	}
	if l, ok := log.GetStatLogger().(log.DirLogger); ok && !l.IsLevelEnabled(log.NoneLog) {
		err = model.EnsureAndVerifyDir(l.GetLogDir())
		if err != nil {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create stat logger dir: %s", l.GetLogDir())))
		}
	}
	if l, ok := log.GetStatReportLogger().(log.DirLogger); ok && !l.IsLevelEnabled(log.NoneLog) {
		err = model.EnsureAndVerifyDir(l.GetLogDir())
		if err != nil {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create statReport logger dir: %s", l.GetLogDir())))
		}
	}
	if l, ok := log.GetNetworkLogger().(log.DirLogger); ok && !l.IsLevelEnabled(log.NoneLog) {
		err = model.EnsureAndVerifyDir(l.GetLogDir())
		if err != nil {
			errs = multierror.Append(errs, multierror.Prefix(err,
				fmt.Sprintf("fail to create network logger dir: %s", l.GetLogDir())))
		}
	}
	return errs
}

// getPodName 获取进程所处容器名字
func getPodName() string {
	var container string
	envList := config.GetContainerNameEnvList()
	for _, e := range envList {
		container = os.Getenv(e)
		if container != "" {
			break
		}
	}
	return container
}

// getHostName 从环境变量中获取HOSTNAME
func getHostName() string {
	hostName := os.Getenv("HOSTNAME")
	return hostName
}

// InitContextByConfig InitContextByStream 通过配置对象新建上下文
func InitContextByConfig(cfg config.Configuration) (SDKContext, error) {
	startTime := time.Now()
	globalCtx := model.NewValueContext()
	globalCtx.SetValue(model.ContextKeyTakeEffectTime, startTime)
	if logErr := checkLoggersDir(); nil != logErr {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, logErr, "logger init error")
	}
	if log.GetBaseLogger().IsLevelEnabled(log.DebugLog) {
		text, err := yaml.Marshal(cfg)
		if err != nil {
			return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to marshal input config")
		}
		log.GetBaseLogger().Debugf("Input config:\n%s", string(text))
	}

	cfg.SetDefault()
	if err := cfg.Verify(); err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to verify input config")
	}
	getSelfIP(cfg)
	token := model.SDKToken{
		IP:       cfg.GetGlobal().GetAPI().GetBindIP(),
		PID:      int32(os.Getpid()),
		UID:      strings.ToUpper(uuid.New().String()),
		Client:   version.ClientType,
		Version:  version.Version,
		PodName:  getPodName(),
		HostName: getHostName(),
	}
	log.GetBaseLogger().Infof("\n-------Start to init SDKContext of version %s, IP: %s, PID: %d, UID: %s, CONTAINER: "+"%s, HOSTNAME:%s-------",
		version.Version, token.IP, token.PID, token.UID, token.PodName, token.HostName)

	globalCtx.SetValue(model.ContextKeyToken, token)
	plugManager := plugin.NewPluginManager()
	globalCtx.SetValue(model.ContextKeyPlugins, plugManager)
	connManager, err := network.NewConnectionManager(cfg, globalCtx)
	if err != nil {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, err, "fail to create connectionManager")
	}
	initCtx := plugin.InitContext{Config: cfg, Plugins: plugManager, ValueCtx: globalCtx, ConnManager: connManager,
		SDKContextID: token.UID}
	engine := &flow.Engine{}
	// 初始化插件链
	err = plugManager.InitPlugins(initCtx, common.LoadedPluginTypes, engine, func() error {
		// 初始化流程引擎
		return flow.InitFlowEngine(engine, initCtx)
	})
	var finalErrs error
	if err != nil {
		finalErrs = multierror.Append(finalErrs, err)
	}
	text, terr := yaml.Marshal(cfg)
	if terr != nil {
		finalErrs = multierror.Append(finalErrs, model.NewSDKError(model.ErrCodeAPIInvalidConfig, terr,
			"fail to marshal input config"))
	}
	log.GetBaseLogger().Infof("\n%s, -------Configuration with default value-------\n%s", token.UID, string(text))
	if finalErrs != nil {
		return nil, finalErrs
	}
	log.GetBaseLogger().Infof("\n-------%s, All plugins and engine initialized successfully-------", token.UID)
	// 启动所有插件
	if err = plugManager.StartPlugins(); err != nil {
		return nil, err
	}
	if err = engine.Start(); err != nil {
		return nil, err
	}
	log.GetBaseLogger().Infof("\n-------%s, All plugins and engine started successfully-------", token.UID)
	ctx := &sdkContext{config: cfg, plugins: plugManager, engine: engine, valueContext: globalCtx}
	if err = onContextInitialized(ctx); err != nil {
		ctx.Destroy()
		return nil, err
	}
	globalCtx.SetValue(model.ContextKeyFinishInitTime, time.Now())
	log.GetBaseLogger().Infof("\n-------%s, SDKContext init successfully-------", token.UID)
	return ctx, nil
}

// getSelfIP 获取SDK自身的IP
func getSelfIP(cfg config.Configuration) {
	bindIP := cfg.GetGlobal().GetAPI().GetBindIP()
	bindIntf := cfg.GetGlobal().GetAPI().GetBindIntf()
	if len(bindIP) != 0 || len(bindIntf) != 0 {
		return
	}

	address := cfg.GetGlobal().GetServerConnector().GetAddresses()
	if len(address) == 0 {
		return
	}

	conn, _ := net.Dial("tcp", address[0])
	if conn != nil {
		cfg.GetGlobal().GetAPI().SetBindIP(conn.LocalAddr().String())
		_ = conn.Close()
	}
}

// onContextInitialized 在全局上下文初始化完成后，触发事件回调，可针对不同插件做一些阻塞等待某个事件完成的操作
func onContextInitialized(ctx SDKContext) error {
	eventHandlers := ctx.GetPlugins().GetEventSubscribers(common.OnContextStarted)
	event := &common.PluginEvent{
		EventType: common.OnContextStarted, EventObject: ctx}
	for _, handler := range eventHandlers {
		err := handler.Callback(event)
		if err != nil {
			return model.NewSDKError(model.ErrCodePluginError, err,
				"InitContextByConfig: fail to handle OnContextStarted event")
		}
	}
	return nil
}

// NewConfiguration 创建默认配置
func NewConfiguration() config.Configuration {
	return config.NewDefaultConfigurationWithDomain()
}
