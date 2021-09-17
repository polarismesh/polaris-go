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

package network

import (
	"context"
	"fmt"
	"github.com/google/uuid"
	"github.com/modern-go/reflect2"
	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"math"
	"sync"
	"sync/atomic"
	"time"
)

var (
	flowId uint64
)

const (
	defaultService     = "polaris-default"
	serviceReadyStatus = 2
	getAddressTimeout  = 300 * time.Millisecond
)

//服务地址列表
type ServerAddressList struct {
	//所属服务
	service config.ClusterService
	//获取不到服务地址是否使用预埋IP
	useDefault bool
	//当前生效连接，存放的是Connection对象
	curConn atomic.Value
	//当前的index，只对预埋地址生效，用于轮询
	curIndex int
	//预埋地址列表
	addresses []string
	//首次连接控制锁
	connectMutex sync.Mutex
	//全局管理对象指针
	manager *connectionManager
}

//获取并进行连接
func (s *ServerAddressList) getAndConnectServer(
	force bool, svc config.ClusterService, timeout time.Duration) *Connection {
	s.connectMutex.Lock()
	defer s.connectMutex.Unlock()
	address, instance, err := s.getServerAddress(s.manager.GetHashKey())
	if nil != err {
		log.GetNetworkLogger().Errorf("fail get server address from service %s, error %v", svc, err)
		return nil
	}
	conn, err := s.connectServer(force, address, instance, svc, timeout)
	if nil != err {
		log.GetNetworkLogger().Errorf("fail get connect %s from service %s, error %v", address, svc, err)
		return nil
	}
	return conn
}

//与远程server进行连接
func (s *ServerAddressList) getServerAddress(hashKey []byte) (string, model.Instance, error) {
	var targetAddress string
	var instance model.Instance
	if s.service.ClusterType == config.BuiltinCluster {
		serverCount := len(s.addresses)
		targetAddress = s.addresses[s.curIndex%serverCount]
		if s.curIndex == math.MaxInt32 {
			s.curIndex = 0
		} else {
			s.curIndex++
		}
	} else {
		engineValue, ok := s.manager.valueCtx.GetValue(model.ContextKeyEngine)
		if !ok {
			return "", nil, fmt.Errorf("flow engine is not ready")
		}
		engine := engineValue.(model.Engine)
		//返回错误，使得外部流程可以使用埋点进行发现
		if s.useDefault && atomic.LoadUint32(&s.manager.ready) < serviceReadyStatus {
			return "", nil, fmt.Errorf("discover service %s is not ready", s.service)
		}
		req := &model.GetOneInstanceRequest{
			FlowID:    atomic.AddUint64(&flowId, 1),
			Namespace: s.service.Namespace,
			Service:   s.service.Service,
			//SourceService: &model.ServiceInfo{
			//	Metadata: map[string]string{"protocol": s.manager.protocol},
			//},
			Metadata: map[string]string{"protocol": s.manager.protocol},
			HashKey:  hashKey,
		}
		//获取系统服务，不重试，超时时间设为300ms
		req.SetRetryCount(0)
		req.SetTimeout(getAddressTimeout)
		resp, err := engine.SyncGetOneInstance(req)
		if nil != err {
			return "", nil, err
		}
		instance = resp.Instances[0]
		targetAddress = fmt.Sprintf("%s:%d", instance.GetHost(), instance.GetPort())
	}
	return targetAddress, instance, nil
}

//获取服务当前连接
func (s *ServerAddressList) loadCurrentConnection() *Connection {
	connValue := s.curConn.Load()
	if reflect2.IsNil(connValue) {
		return nil
	}
	return connValue.(*Connection)
}

//根据地址进行连接
func (s *ServerAddressList) connectServer(force bool, addr string, instance model.Instance,
	service config.ClusterService, timeout time.Duration) (*Connection, error) {
	var lastConn = s.loadCurrentConnection()
	if !force && IsAvailableConnection(lastConn) && lastConn.Address == addr {
		log.GetNetworkLogger().Debugf("address %s not changed, no need to switch server", addr)
		//服务地址没有发生变更，无需切换
		return lastConn, nil
	}
	connectTime := time.Now()
	tcpConn, err := s.manager.creator.CreateConnection(addr, timeout, &s.manager.ClientInfo)
	connID := ConnID{
		ID:       uuid.New().ID(),
		Service:  service,
		Address:  addr,
		instance: instance,
	}
	connectDuration := time.Now().Sub(connectTime)
	if nil != err {
		if !reflect2.IsNil(instance) {
			s.manager.ReportFail(connID, int32(model.ErrCodeConnectError), connectDuration)
		}
		return nil, fmt.Errorf("fail to connect to %s, timeout is %v, service is %s, because %s",
			addr, connectDuration, s.service, err.Error())
	}

	if nil != lastConn {
		//延迟释放连接
		lastConn.lazyClose(false)
	}

	conn := &Connection{
		Conn:   tcpConn,
		ConnID: connID,
	}
	if ctrl, ok := DefaultServerServiceToConnectionControl[s.service.ClusterType]; ok && ctrl == ConnectionLong {
		log.GetNetworkLogger().Infof("long connection %v, target address %s: create", conn.ConnID, addr)
	} else {
		log.GetNetworkLogger().Debugf("short connection %v, target address %s: create", conn.ConnID, addr)
	}
	s.curConn.Store(conn)
	return conn, nil
}

func (s *ServerAddressList) ConnectServerByAddrOnly(addr string, timeout time.Duration,
	clsService config.ClusterService, instance model.Instance) (*Connection, error) {
	connectTime := time.Now()
	tcpConn, err := s.manager.creator.CreateConnection(addr, timeout, &s.manager.ClientInfo)
	connectDuration := time.Now().Sub(connectTime)
	if nil != err {
		return nil, fmt.Errorf("fail to connect to %s, timeout is %v, service is %s, because %s",
			addr, connectDuration, s.service, err.Error())
	}
	connID := ConnID{
		ID:       uuid.New().ID(),
		Service:  clsService,
		Address:  addr,
		instance: instance,
	}
	conn := &Connection{
		Conn:   tcpConn,
		ConnID: connID,
	}
	conn.acquire(addr)
	return conn, nil
}

//与远程server进行连接
func (s *ServerAddressList) tryGetConnection(timeout time.Duration, hashKey []byte) (*Connection, error) {
	curConnValue := s.loadCurrentConnection()
	if IsAvailableConnection(curConnValue) {
		//log.GetBaseLogger().Debugf("[CheckConnection]traceCheck IsAvailableConnection")
		return curConnValue, nil
	}
	s.connectMutex.Lock()
	defer s.connectMutex.Unlock()
	curConnValue = s.loadCurrentConnection()
	if IsAvailableConnection(curConnValue) {
		return curConnValue, nil
	}
	address, instance, err := s.getServerAddress(hashKey)
	if nil != err {
		return nil, err
	}
	return s.connectServer(false, address, instance, s.service, timeout)
}

//关闭当前连接
func (s *ServerAddressList) closeCurrentConnection(force bool) {
	conn := s.loadCurrentConnection()
	if IsAvailableConnection(conn) {
		log.GetNetworkLogger().Debugf("current connection for %s has been closed", s.service)
		conn.lazyClose(force)
	}
}

//连接管理器实现
type connectionManager struct {
	//客户端信息
	ClientInfo
	//连接超时时间
	connectTimeout time.Duration
	//连接切换周期
	switchInterval time.Duration
	ctx            context.Context
	cancel         context.CancelFunc
	//发现服务
	discoverService model.ServiceKey
	//发现服务的事件集合，相同事件不去更新
	discoverEventSet map[model.EventType]bool
	//并发更新锁
	discoverEventMutex sync.Mutex
	//是否已经准备完成, 0代表未完成，1代表完成
	ready uint32
	//系统服务信息
	serverServices map[config.ClusterType]*ServerAddressList
	//全局上下文信息
	valueCtx model.ValueContext
	//当前使用的协议
	protocol string
	//连接创建器
	creator ConnCreator
}

//创建连接管理器
func NewConnectionManager(
	cfg config.Configuration, valueCtx model.ValueContext) (ConnectionManager, error) {
	addresses := cfg.GetGlobal().GetServerConnector().GetAddresses()
	switchInterval := cfg.GetGlobal().GetServerConnector().GetServerSwitchInterval()
	connectTimeout := cfg.GetGlobal().GetServerConnector().GetConnectTimeout()
	protocol := cfg.GetGlobal().GetServerConnector().GetProtocol()
	manager := &connectionManager{
		connectTimeout:   connectTimeout,
		switchInterval:   switchInterval,
		serverServices:   make(map[config.ClusterType]*ServerAddressList),
		valueCtx:         valueCtx,
		protocol:         protocol,
		discoverEventSet: make(map[model.EventType]bool, 0),
	}
	serverServices := config.GetServerServices(cfg)
	for _, svc := range serverServices {
		svcList := &ServerAddressList{
			service:    svc,
			useDefault: config.DefaultServerServiceToUseDefault[svc.ClusterType],
			manager:    manager,
		}
		if svc.ClusterType == config.DiscoverCluster {
			manager.discoverService = svc.ServiceKey
		}
		manager.serverServices[svc.ClusterType] = svcList
	}
	builtInAddrList := &ServerAddressList{
		service: config.ClusterService{
			ServiceKey:  model.ServiceKey{Namespace: config.ServerNamespace, Service: defaultService},
			ClusterType: config.BuiltinCluster,
		},
		useDefault: false,
		manager:    manager,
		addresses:  addresses,
		curIndex:   rand.Intn(len(addresses)),
	}
	manager.serverServices[config.BuiltinCluster] = builtInAddrList
	if len(manager.discoverService.Service) == 0 {
		manager.discoverService = builtInAddrList.service.ServiceKey
		manager.ready = serviceReadyStatus
	}

	manager.ctx, manager.cancel = context.WithCancel(context.Background())
	go manager.doSwitchRoutine()
	return manager, nil
}

//设置当前协议的连接创建器
func (c *connectionManager) SetConnCreator(creator ConnCreator) {
	c.creator = creator
}

//尝试获取连接
func (c *connectionManager) tryGetConnection(clusterType config.ClusterType, hashKey []byte) (*Connection, error) {
	serverList, ok := c.serverServices[clusterType]
	if !ok {
		var useDefault, ok bool
		if useDefault, ok = config.DefaultServerServiceToUseDefault[clusterType]; !ok {
			return nil, fmt.Errorf("cluster %v is invalid", clusterType)
		}
		if !useDefault {
			return nil, fmt.Errorf("service name for cluster %v is not config", clusterType)
		}
		serverList = c.serverServices[config.BuiltinCluster]
	}
	return serverList.tryGetConnection(c.connectTimeout, hashKey)
}

//获取并占用连接
func (c *connectionManager) GetConnection(opKey string, clusterType config.ClusterType) (*Connection, error) {
	return c.GetConnectionByHashKey(opKey, clusterType, c.GetHashKey())
}

//获取并占用连接
func (c *connectionManager) GetConnectionByHashKey(
	opKey string, clusterType config.ClusterType, hashKey []byte) (*Connection, error) {
	for {
		conn, err := c.tryGetConnection(clusterType, hashKey)
		if nil != err {
			log.GetNetworkLogger().Errorf(
				"fail to get connection, opKey is %s, cluster %v, error is %s", opKey, clusterType, err)
			return nil, err
		}
		if conn.acquire(opKey) {
			return conn, nil
		}
	}
}

func (c *connectionManager) GetHashExpectedInstance(clusterType config.ClusterType,
	hash []byte) (string, model.Instance, error) {
	serverList, ok := c.serverServices[clusterType]
	if !ok {
		panic(fmt.Sprintf("connectionManager has no clusterType %s", clusterType))
	}
	addr, ins, err := serverList.getServerAddress(hash)
	return addr, ins, err
}

func (c *connectionManager) ConnectByAddr(clusterType config.ClusterType, addr string,
	instance model.Instance) (*Connection, error) {
	serverList, ok := c.serverServices[clusterType]
	if !ok {
		panic(fmt.Sprintf("connectionManager has no clusterType %s", clusterType))
	}
	return serverList.ConnectServerByAddrOnly(addr, time.Millisecond*500, serverList.service, instance)
}

//上报服务成功
func (c *connectionManager) ReportSuccess(connID ConnID, retCode int32, timeout time.Duration) {
	log.GetBaseLogger().Debugf("service %s: reported success", connID.Service)
	var err error
	if !reflect2.IsNil(connID.instance) {
		engineValue, ok := c.valueCtx.GetValue(model.ContextKeyEngine)
		if ok {
			engine := engineValue.(model.Engine)
			result := &model.ServiceCallResult{
				CalledInstance: connID.instance,
				RetStatus:      model.RetSuccess}
			result.SetDelay(timeout)
			result.SetRetCode(retCode)
			err = engine.SyncUpdateServiceCallResult(result)
		}
	}
	if nil != err {
		log.GetBaseLogger().Errorf(
			"error to update success call result for connection %s, %s", connID.String(), err)
	}
}

//上报服务失败
func (c *connectionManager) ReportFail(connID ConnID, retCode int32, timeout time.Duration) {
	log.GetBaseLogger().Warnf("connection %s: reported fail", connID)
	var err error
	if !reflect2.IsNil(connID.instance) && connID.Service.ClusterType != config.BuiltinCluster {
		engineValue, ok := c.valueCtx.GetValue(model.ContextKeyEngine)
		if ok {
			engine := engineValue.(model.Engine)
			result := &model.ServiceCallResult{
				CalledInstance: connID.instance,
				RetStatus:      model.RetFail}
			result.SetDelay(timeout)
			result.SetRetCode(retCode)
			err = engine.SyncUpdateServiceCallResult(result)
		}
	}
	if nil != err {
		log.GetBaseLogger().Errorf(
			"error to update fail call result for connection %s, %s", connID.String(), err)
	}
}

//报告连接故障
func (c *connectionManager) ReportConnectionDown(connID ConnID) {
	log.GetBaseLogger().Tracef("connection %s: reported down", connID)
	var svc = connID.Service
	var serverList *ServerAddressList
	var ok bool
	serverList, ok = c.serverServices[svc.ClusterType]
	if !ok {
		log.GetBaseLogger().Warnf("connection %s down received from unknown service %s", connID, svc)
		return
	}
	log.GetBaseLogger().Infof("connection %s down received from service %s", connID, svc.String())
	curConn := serverList.loadCurrentConnection()
	if nil != curConn && connID.ID != curConn.ConnID.ID {
		//已经切换新连接，忽略
		return
	}
	if IsAvailableConnection(curConn) {
		curConn.lazyClose(false)
	}
}

//销毁连接管理器
func (c *connectionManager) Destroy() {
	c.cancel()
}

//执行切换流程，只有当初次连接成功后才启动
func (c *connectionManager) doSwitchRoutine() {
	//服务端定期切换
	switchTicker := time.NewTicker(c.switchInterval)
	buildInCloseTicker := time.NewTicker(config.DefaultBuiltInServerConnectionCloseTimeout)
	defer func() {
		buildInCloseTicker.Stop()
		switchTicker.Stop()
		//退出后清理连接
		for _, serverList := range c.serverServices {
			//destroy的话，就要强制关闭了
			serverList.closeCurrentConnection(true)
		}
	}()
	for {
		select {
		case <-c.ctx.Done():
			log.GetBaseLogger().Infof("doSwitchRoutine of connection manager has been terminated")
			return
		case <-buildInCloseTicker.C:
			serverList := c.serverServices[config.BuiltinCluster]
			serverList.closeCurrentConnection(false)
		case <-switchTicker.C:
			for clusterType, serverList := range c.serverServices {
				if clusterType == config.BuiltinCluster {
					continue
				}
				if ctrl, ok := DefaultServerServiceToConnectionControl[clusterType]; ok && ctrl == ConnectionLong {
					//只有长连接模式才切换server
					curConn := serverList.loadCurrentConnection()
					if IsAvailableConnection(curConn) {
						//只有成功后，才进行切换
						log.GetNetworkLogger().Infof("start switch for %s", serverList.service.ServiceKey)
						conn := serverList.getAndConnectServer(false, serverList.service, c.connectTimeout)
						if nil != conn {
							log.GetNetworkLogger().Infof("discover server switched to %s", conn.Address)
						}
						continue
					}
					log.GetNetworkLogger().Infof("skip switch for %s", serverList.service.ServiceKey)
				}
			}
		}
	}
}

//更新系统服务
func (c *connectionManager) UpdateServers(svcEventKey model.ServiceEventKey) {
	svc := model.ServiceKey{Namespace: svcEventKey.Namespace, Service: svcEventKey.Service}
	if c.discoverService == svc {
		if !c.isAvailableUpdate(svcEventKey.Type) {
			return
		}
		value := atomic.AddUint32(&c.ready, 1)
		log.GetBaseLogger().Infof("discover server updated to ready %v, event is %s", value, svcEventKey)
	}
}

//是否有效的事件更新
func (c *connectionManager) isAvailableUpdate(event model.EventType) bool {
	c.discoverEventMutex.Lock()
	defer c.discoverEventMutex.Unlock()
	if _, ok := c.discoverEventSet[event]; ok {
		return false
	}
	c.discoverEventSet[event] = true
	return true
}

//获取当前客户端信息
func (c *connectionManager) GetClientInfo() *ClientInfo {
	return &c.ClientInfo
}

//discover是否已经就绪
func (c *connectionManager) IsReady() bool {
	return atomic.LoadUint32(&c.ready) == serviceReadyStatus
}
