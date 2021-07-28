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

package plugin

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/network"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"reflect"
	"sync/atomic"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/hashicorp/go-multierror"
)

var (
	pluginIndex int32
	//插件的接口类型
	pluginInterfaceTypes = make(map[common.Type]reflect.Type)
	//插件proxy类型
	pluginProxyTypes = make(map[common.Type]reflect.Type)
	//全局插件类型列表，由各插件包注册进来
	pluginTypes = make(map[common.Type]map[string]pluginType)
)

//插件类型封装
type pluginType struct {
	//插件ID
	pluginId int32
	//插件类型
	reflectType reflect.Type
}

//检查插件是否已经注册
func IsPluginRegistered(typ common.Type, name string) bool {
	var ok bool
	var plugins map[string]pluginType
	if plugins, ok = pluginTypes[typ]; !ok {
		return false
	}
	if _, ok = plugins[name]; !ok {
		return false
	}
	return true
}

/**
 * @brief 插件提供者
 */
type Supplier interface {
	//GetPlugin 获取插件实例
	GetPlugin(typ common.Type, name string) (Plugin, error)
	//GetPluginById 通过id获取插件实例
	GetPluginById(id int32) (Plugin, error)
	//GetPluginsByType 获取一个类型的加载了的插件名字
	GetPluginsByType(typ common.Type) []string
	//获取插件事件监听器
	GetEventSubscribers(event common.PluginEventType) []common.PluginEventHandler
	//注册插件事件监听器，必须在Plugin.Init方法中进行，否则会出现并发读写问题
	RegisterEventSubscriber(event common.PluginEventType, handler common.PluginEventHandler)
}

/**
 * @brief 插件管理器统一接口
 */
type Manager interface {
	Supplier
	//InitPlugins 初始化插件列表
	InitPlugins(initContext InitContext, types []common.Type, engine model.Engine, delegate func() error) (err error)
	//DestroyPlugins 销毁已初始化的插件列表
	DestroyPlugins() (err error)
	//StartPlugins 执行已经初始化完毕的插件
	StartPlugins() error
}

//插件实例包装类
type pluginWrapper struct {
	//插件唯一标识，与具体插件类型对应
	id int32
	//具体插件标识
	instance Plugin
}

//NewPluginManager 创建插件管理器实例
func NewPluginManager() Manager {
	return &manager{
		plugins:         make(map[common.Type]map[string]*pluginWrapper),
		eventSubscriber: make(map[common.PluginEventType][]common.PluginEventHandler),
		idToPlugins:     make(map[int32]Plugin),
	}
}

//Manager 插件管理器
type manager struct {
	plugins         map[common.Type]map[string]*pluginWrapper
	idToPlugins     map[int32]Plugin
	eventSubscriber map[common.PluginEventType][]common.PluginEventHandler
	//是否已经初始化，初始化后不允许修改任何数据结构
	initialized uint32
}

//判断是否实现了对应的接口
func instanceOf(value interface{}, interfaceType reflect.Type) bool {
	return reflect.PtrTo(reflect.TypeOf(value).Elem()).Implements(interfaceType)
}

//检查插件是否实现了对应的插件接口
func checkInterfaceType(plugin Plugin) error {
	intfType := pluginInterfaceTypes[plugin.Type()]
	if !instanceOf(plugin, intfType) {
		return model.NewSDKError(model.ErrCodePluginError, nil,
			"plugin %s for type %s must implement %s interface", plugin.Name(), plugin.Type(), intfType)
	}
	return nil
}

//注册插件接口类型
func RegisterPluginInterface(typ common.Type, plugin interface{}) {
	if _, ok := pluginInterfaceTypes[typ]; ok {
		//开发态错误，直接panic以便快速发现问题
		panic(fmt.Sprintf("duplicate register for plugin type %s", typ))
	}
	pluginInterfaceTypes[typ] = reflect.TypeOf(plugin).Elem()
}

//注册插件proxy类型
func RegisterPluginProxy(typ common.Type, proxy PluginProxy) {
	if _, ok := pluginProxyTypes[typ]; ok {
		panic(fmt.Sprintf("duplicate register for plugin proxy type %s", typ))
	}
	pluginProxyTypes[typ] = reflect.TypeOf(proxy).Elem()
}

//RegisterPlugin 注册插件到全局配置对象，插件名重复则返回错误
func RegisterPlugin(plugin Plugin) {
	RegisterConfigurablePlugin(plugin, nil)
}

//RegisterPlugin 注册插件到全局配置对象，并注册插件配置类型
func RegisterConfigurablePlugin(plugin Plugin, cfg config.BaseConfig) {
	if err := checkInterfaceType(plugin); nil != err {
		//插件注册失败则直接panic，让用户直接感知
		panic(err)
	}
	name := plugin.Name()
	typ := plugin.Type()
	plugs, exists := pluginTypes[typ]
	if !exists {
		plugs = make(map[string]pluginType)
		pluginTypes[typ] = plugs
	}
	pluginIdx := atomic.AddInt32(&pluginIndex, 1)
	plugs[name] = pluginType{
		pluginId:    pluginIdx,
		reflectType: reflect.TypeOf(plugin).Elem(),
	}
	config.RegisterPluginConfigType(typ, name, cfg)
}

//createPlugin 反射创建插件
func createPlugin(typ reflect.Type) Plugin {
	value := reflect.New(typ).Interface()
	return value.(Plugin)
}

//创建插件proxy
func createPluginProxy(typ common.Type) PluginProxy {
	t := pluginProxyTypes[typ]
	value := reflect.New(t).Interface()
	return value.(PluginProxy)
}

//InitPlugins 初始化所有已注册插件
func (m *manager) InitPlugins(
	ctx InitContext, types []common.Type, engine model.Engine, delegateInit func() error) (err error) {
	if atomic.LoadUint32(&m.initialized) > 0 {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "manager has been initialized")
	}
	pluginSlice := make([]*pluginWrapper, 0, len(types)*2)
	for _, typ := range types {
		plugs, ok := pluginTypes[typ]
		if !ok {
			return m.cleanupWhenError(model.NewSDKError(model.ErrCodePluginError, nil,
				"InitPlugins: invalid plugin type %v", typ))
		}
		plugInstances, ok := m.plugins[typ]
		if !ok {
			plugInstances = make(map[string]*pluginWrapper, 0)
			m.plugins[typ] = plugInstances
		}
		for _, plugClazz := range plugs {
			plug := createPlugin(plugClazz.reflectType)
			//if !pluginNames.Contains(plug.Name()) {
			//	continue
			//}
			proxy := createPluginProxy(typ)
			proxy.SetRealPlugin(plug, engine)
			if !plug.IsEnable(ctx.Config) {
				continue
			}
			wrapper := &pluginWrapper{
				id:       plugClazz.pluginId,
				instance: proxy,
			}
			plugInstances[proxy.Name()] = wrapper
			pluginSlice = append(pluginSlice, wrapper)
		}
	}
	//初始化必须保持有序
	for _, plug := range pluginSlice {
		ctx.PluginIndex = plug.id
		err = plug.instance.Init(&ctx)
		if nil != err {
			return m.cleanupWhenError(model.NewSDKError(model.ErrCodePluginError, err,
				"InitPlugins: fail to init plugin name %v:%s", plug.instance.Type(), plug.instance.Name()))
		}
		m.idToPlugins[plug.id] = plug.instance
		log.GetBaseLogger().Infof(
			"Initialized plugin type %v, name %s, id %d",
			plug.instance.Type(), plug.instance.Name(), ctx.PluginIndex)
	}
	if err = delegateInit(); nil != err {
		return m.cleanupWhenError(model.NewSDKError(model.ErrCodePluginError, err,
			"InitPlugins: fail to init delegate"))
	}
	atomic.StoreUint32(&m.initialized, 1)
	return nil
}

//启动所有插件
func (m *manager) StartPlugins() error {
	if atomic.LoadUint32(&m.initialized) == 0 {
		return model.NewSDKError(model.ErrCodeInvalidStateError, nil, "manager has not been initialized")
	}
	var err error
	startedPlugins := model.HashSet{}
	for id, plug := range m.idToPlugins {
		startedPlugins.Add(id)
		if err = plug.Start(); nil != err {
			log.GetBaseLogger().Errorf("fail to start plugin %s, err is %v", plug.Name(), err)
			break
		}
	}
	if nil != err && len(startedPlugins) > 0 {
		//回滚所有插件
		for idValue := range startedPlugins {
			m.idToPlugins[idValue.(int32)].Destroy()
		}
	}
	return err
}

//清理插件初始化结果，并返回输入错误
func (m *manager) cleanupWhenError(sdkErr model.SDKError) error {
	if nil == sdkErr {
		return nil
	}
	if len(m.plugins) == 0 {
		return sdkErr
	}
	for typ, plugInstances := range m.plugins {
		if len(plugInstances) == 0 {
			continue
		}
		for name, plugInst := range plugInstances {
			if err := plugInst.instance.Destroy(); nil != err {
				log.GetBaseLogger().Errorf("fail to destroy plugin %v:%s, err %v", typ, name, err)
			}
		}
	}
	return sdkErr
}

//DestroyPlugins 销毁已初始化的插件列表
func (m *manager) DestroyPlugins() (errs error) {
	var err error
	for typ, plugs := range m.plugins {
		for name, plug := range plugs {
			err = plug.instance.Destroy()
			if nil != err {
				errs = multierror.Append(errs, multierror.Prefix(err,
					fmt.Sprintf("DestroyPlugins: plugin %v:%s error, ", typ, name)))
			}
		}
	}
	if nil != errs {
		return model.NewSDKError(model.ErrCodePluginError, errs, "DestroyPlugins: plugins destroy errors")
	}
	return nil
}

//GetPlugin 获取插件
func (m *manager) GetPlugin(typ common.Type, name string) (Plugin, error) {
	plugins, exists := m.plugins[typ]
	if !exists {
		return nil, model.NewSDKError(model.ErrCodePluginError, nil,
			"GetPlugin: invalid plugin type %v", typ)
	}
	plug, exists := plugins[name]
	if !exists {
		return nil, model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"GetPlugin: plugin name %s not registered", name)
	}
	return plug.instance, nil
}

//获取一个类型的加载的插件名字
func (m *manager) GetPluginsByType(typ common.Type) []string {
	var res []string
	plugins, exists := m.plugins[typ]
	if !exists {
		return res
	}
	for k := range plugins {
		res = append(res, k)
	}
	return res
}

//通过id获取插件
func (m *manager) GetPluginById(id int32) (Plugin, error) {
	plugin, exists := m.idToPlugins[id]
	if !exists {
		return nil, model.NewSDKError(model.ErrCodePluginError, nil, "GetPluginById: not registered plugin id %d", id)
	}
	return plugin, nil
}

//GetPlugin 获取插件
func GetPluginId(typ common.Type, name string) (int32, error) {
	plugs, exists := pluginTypes[typ]
	if !exists {
		return 0, model.NewSDKError(model.ErrCodePluginError, nil,
			"GetPlugin: invalid plugin type %v", typ)
	}
	plug, exists := plugs[name]
	if !exists {
		return 0, model.NewSDKError(model.ErrCodeAPIInvalidConfig, nil,
			"GetPlugin: plugin name %s not registered", name)
	}
	return plug.pluginId, nil
}

//获取某个类型下注册的插件数量
func GetPluginCount(typ common.Type) int {
	plugs := pluginTypes[typ]
	return len(plugs)
}

//注册事件回调函数
func (m *manager) RegisterEventSubscriber(event common.PluginEventType, handler common.PluginEventHandler) {
	if atomic.LoadUint32(&m.initialized) > 0 {
		panic("manager has initialized")
	}
	if handlers, ok := m.eventSubscriber[event]; ok {
		handlers = append(handlers, handler)
		m.eventSubscriber[event] = handlers
		return
	}
	handlers := make([]common.PluginEventHandler, 0)
	handlers = append(handlers, handler)
	m.eventSubscriber[event] = handlers
}

//注册事件回调函数
func (m *manager) GetEventSubscribers(event common.PluginEventType) []common.PluginEventHandler {
	if atomic.LoadUint32(&m.initialized) == 0 {
		panic("manager has not initialized")
	}
	return m.eventSubscriber[event]
}

//用于插件初始化的上下文对象
type InitContext struct {
	Config       config.Configuration
	Plugins      Supplier
	ValueCtx     model.ValueContext
	ConnManager  network.ConnectionManager
	PluginIndex  int32
	SDKContextID string
}

////插件ID
//type IdAwarePlugin interface {
//	Plugin
//	//插件标识
//	ID() int32
//}

//Plugin 所有插件的基础接口
type Plugin interface {
	//插件类型
	Type() common.Type
	//插件id
	ID() int32
	//返回插件所属的sdkContext的uuid
	GetSDKContextID() string
	//插件名，一个类型下插件名唯一
	Name() string
	//初始化插件
	Init(ctx *InitContext) error
	//启动插件，对于需要依赖外部资源，以及启动协程的操作，在Start方法里面做
	Start() error
	//销毁插件，可用于释放资源
	Destroy() error
	//插件是否启用
	IsEnable(cfg config.Configuration) bool
}

// Plugin Base
type PluginBase struct {
	pluginIndex int32
	sdkID       string
}

// type
func (b *PluginBase) Type() common.Type {
	return common.TypePluginBase
}

// name
func (b *PluginBase) Name() string {
	return common.TypePluginBase.String()
}

// init
func (b *PluginBase) Init(ctx *InitContext) error {
	b.pluginIndex = ctx.PluginIndex
	b.sdkID = ctx.SDKContextID
	return nil
}

//插件id
func (b *PluginBase) ID() int32 {
	return b.pluginIndex
}

//获取所属sdkContext的uuid
func (b *PluginBase) GetSDKContextID() string {
	return b.sdkID
}

// destroy
func (b *PluginBase) Destroy() error {
	return nil
}

//启动插件
func (b *PluginBase) Start() error {
	//do nothing
	return nil
}

//创建pluginbase
func NewPluginBase(ctx *InitContext) *PluginBase {
	res := &PluginBase{}
	res.Init(ctx)
	return res
}

//Plugin的代理
type PluginProxy interface {
	Plugin
	SetRealPlugin(plugin Plugin, engine model.Engine)
}

// is enable
func (b *PluginBase) IsEnable(cfg config.Configuration) bool {
	return true
}
