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

package mock

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	monitorpb "github.com/polarismesh/polaris-go/plugin/statreporter/pb/v1"
	"github.com/golang/protobuf/jsonpb"
	"github.com/golang/protobuf/ptypes/wrappers"
	"io"
	"sync"
)

const (
	//monitor的ip端口
	MonitorIp   = "127.0.0.1"
	MonitorPort = 8090
)

//监控server
type MonitorServer interface {
	monitorpb.GrpcAPIServer
	//获取monitor接收到的熔断状态
	GetCircuitBreakStatus(svcKey model.ServiceKey) []*monitorpb.ServiceCircuitbreak
	//设置monitor接收到的熔断状态
	SetCircuitBreakCache(data []*monitorpb.ServiceCircuitbreak)
	//获取上报的缓存信息
	GetCacheReport(svcKey model.ServiceKey) []*monitorpb.ServiceInfo
	//设置monitor中上报的缓存信息
	SetCacheReport(data []*monitorpb.ServiceInfo)
	//获取服务统计
	GetSvcStat() []*monitorpb.ServiceStatistics
	//获取sdk统计
	GetSdkStat() []*monitorpb.SDKAPIStatistics
	//获取sdk配置统计
	GetSdkCfg() []*monitorpb.SDKConfig
	//设置配置统计
	SetSdkCfg(data []*monitorpb.SDKConfig)
	//设置服务统计
	SetSvcStat(data []*monitorpb.ServiceStatistics)
	//设置sdk统计
	SetSdkStat(data []*monitorpb.SDKAPIStatistics)
	SetPluginStat(data []*monitorpb.PluginAPIStatistics)
	GetPluginStat() []*monitorpb.PluginAPIStatistics
	GetLbStat() []*monitorpb.ServiceLoadBalanceInfo
	SetLbStat(data []*monitorpb.ServiceLoadBalanceInfo)
	GetRateLimitRecords() []*monitorpb.RateLimitRecord
	SetRateLimitRecords(data []*monitorpb.RateLimitRecord)
	GetServiceRouteRecords() []*monitorpb.ServiceRouteRecord
	SetServiceRouteRecords(data []*monitorpb.ServiceRouteRecord)
	GetMeshConfigRecords() []*monitorpb.MeshResourceInfo
	SetMeshConfigRecords(data []*monitorpb.MeshResourceInfo)
}

//模拟monitor
type monitorServer struct {
	svcStat     []*monitorpb.ServiceStatistics
	sdkStat     []*monitorpb.SDKAPIStatistics
	sdkCfg      []*monitorpb.SDKConfig
	svcCache    []*monitorpb.ServiceInfo
	cbCache     []*monitorpb.ServiceCircuitbreak
	pluginStat  []*monitorpb.PluginAPIStatistics
	lbStat      []*monitorpb.ServiceLoadBalanceInfo
	rlStat      []*monitorpb.RateLimitRecord
	srStat      []*monitorpb.ServiceRouteRecord
	meshStat    []*monitorpb.MeshResourceInfo
	svcMutex    sync.Mutex
	sdkMutex    sync.Mutex
	cfgMutex    sync.Mutex
	cbMutex     sync.Mutex
	pluginMutex sync.Mutex
	lbMutex     sync.Mutex
	rlMutex     sync.Mutex
	srMutex     sync.Mutex
	meshMutex   sync.Mutex
}

//创建monitor
func NewMonitorServer() *monitorServer {
	return &monitorServer{}
}

//获取服务统计
func (m *monitorServer) GetSvcStat() []*monitorpb.ServiceStatistics {
	m.svcMutex.Lock()
	res := make([]*monitorpb.ServiceStatistics, 0, len(m.svcStat))
	res = append(res, m.svcStat...)
	m.svcMutex.Unlock()
	return res
}

//获取sdk统计
func (m *monitorServer) GetSdkStat() []*monitorpb.SDKAPIStatistics {
	m.sdkMutex.Lock()
	res := make([]*monitorpb.SDKAPIStatistics, 0, len(m.sdkStat))
	res = append(res, m.sdkStat...)
	m.sdkMutex.Unlock()
	return res
}

//获取sdk配置统计
func (m *monitorServer) GetSdkCfg() []*monitorpb.SDKConfig {
	m.cfgMutex.Lock()
	res := make([]*monitorpb.SDKConfig, 0, len(m.sdkStat))
	res = append(res, m.sdkCfg...)
	m.cfgMutex.Unlock()
	return res
}

//设置配置统计
func (m *monitorServer) SetSdkCfg(data []*monitorpb.SDKConfig) {
	m.cfgMutex.Lock()
	m.sdkCfg = data
	m.cfgMutex.Unlock()
}

//设置服务统计
func (m *monitorServer) SetSvcStat(data []*monitorpb.ServiceStatistics) {
	m.svcMutex.Lock()
	m.svcStat = data
	m.svcMutex.Unlock()
}

//设置sdk统计
func (m *monitorServer) SetSdkStat(data []*monitorpb.SDKAPIStatistics) {
	m.sdkMutex.Lock()
	m.sdkStat = data
	m.sdkMutex.Unlock()
}

//s
func (m *monitorServer) CollectServerStatistics(monitorpb.GrpcAPI_CollectServerStatisticsServer) error {
	return nil
}

//收集sdk统计
func (m *monitorServer) CollectSDKAPIStatistics(server monitorpb.GrpcAPI_CollectSDKAPIStatisticsServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.sdkMutex.Lock()
		m.sdkStat = append(m.sdkStat, req)
		m.sdkMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId().GetValue()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive sdk stat data: %v\n", req)
	}
}

//收集服务统计
func (m *monitorServer) CollectServiceStatistics(server monitorpb.GrpcAPI_CollectServiceStatisticsServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.svcMutex.Lock()
		m.svcStat = append(m.svcStat, req)
		m.svcMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId().GetValue()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive svc stat data: %v\n", req)
	}
}

//采集sdk配置信息
func (m *monitorServer) CollectSDKConfiguration(ctx context.Context,
	req *monitorpb.SDKConfig) (*monitorpb.StatResponse, error) {
	fmt.Printf("receive sdk config id %v\n", req.Token)
	fmt.Printf("receive sdk config data %v\n", req.Config)
	m.cfgMutex.Lock()
	m.sdkCfg = append(m.sdkCfg, req)
	m.cfgMutex.Unlock()
	resp := &monitorpb.StatResponse{
		Id:   &wrappers.StringValue{Value: req.GetToken().GetUid()},
		Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
		Info: &wrappers.StringValue{Value: "success"},
	}
	return resp, nil
}

//采集服务信息的变更
func (m *monitorServer) CollectSDKCache(server monitorpb.GrpcAPI_CollectSDKCacheServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.svcMutex.Lock()
		m.svcCache = append(m.svcCache, req)
		m.svcMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		resStr, _ := (&jsonpb.Marshaler{}).MarshalToString(req)
		fmt.Printf("receive svc cache data: %v\n", resStr)
	}
}

//采集熔断状态变化
func (m *monitorServer) CollectCircuitBreak(server monitorpb.GrpcAPI_CollectCircuitBreakServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.svcMutex.Lock()
		m.cbCache = append(m.cbCache, req)
		m.svcMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		resStr, _ := (&jsonpb.Marshaler{}).MarshalToString(req)
		fmt.Printf("receive svc circuitbreak data: %v\n", resStr)
	}
}

//收集插件调用信息
func (m *monitorServer) CollectPluginStatistics(server monitorpb.GrpcAPI_CollectPluginStatisticsServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.pluginMutex.Lock()
		m.pluginStat = append(m.pluginStat, req)
		m.pluginMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive plugin stat data: %v\n", req.String())
	}
}

//获取插件接口调用信息
func (m *monitorServer) GetPluginStat() []*monitorpb.PluginAPIStatistics {
	var res []*monitorpb.PluginAPIStatistics
	m.pluginMutex.Lock()
	defer m.pluginMutex.Unlock()
	for i := 0; i < len(m.pluginStat); i++ {
		res = append(res, m.pluginStat[i])
	}
	return res
}

//设置插件接口调用信息
func (m *monitorServer) SetPluginStat(data []*monitorpb.PluginAPIStatistics) {
	m.pluginMutex.Lock()
	defer m.pluginMutex.Unlock()
	m.pluginStat = data
}

//获取上报的缓存信息
func (m *monitorServer) GetCacheReport(svcKey model.ServiceKey) []*monitorpb.ServiceInfo {
	var res []*monitorpb.ServiceInfo
	m.svcMutex.Lock()
	defer m.svcMutex.Unlock()
	for i := 0; i < len(m.svcCache); i++ {
		if m.svcCache[i].Namespace == svcKey.Namespace && m.svcCache[i].Service == svcKey.Service {
			res = append(res, m.svcCache[i])
		}
	}
	return res
}

//设置monitor中上报的缓存信息
func (m *monitorServer) SetCacheReport(data []*monitorpb.ServiceInfo) {
	m.svcMutex.Lock()
	defer m.svcMutex.Unlock()
	m.svcCache = data
}

//获取monitor接收到的熔断状态
func (m *monitorServer) GetCircuitBreakStatus(svcKey model.ServiceKey) []*monitorpb.ServiceCircuitbreak {
	var res []*monitorpb.ServiceCircuitbreak
	m.cbMutex.Lock()
	defer m.cbMutex.Unlock()
	for i := 0; i < len(m.cbCache); i++ {
		if m.cbCache[i].Namespace == svcKey.Namespace && m.cbCache[i].Service == svcKey.Service {
			res = append(res, m.cbCache[i])
		}
	}
	return res
}

//设置monitor接收到的熔断状态
func (m *monitorServer) SetCircuitBreakCache(data []*monitorpb.ServiceCircuitbreak) {
	m.cbMutex.Lock()
	defer m.cbMutex.Unlock()
	m.cbCache = data
}

//采集负载均衡统计
func (m *monitorServer) CollectLoadBalanceInfo(server monitorpb.GrpcAPI_CollectLoadBalanceInfoServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.lbMutex.Lock()
		m.lbStat = append(m.lbStat, req)
		m.lbMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive loadbalance stat data: %v\n", req.String())
	}
}

//获取收集到的负载均衡统计数据
func (m *monitorServer) GetLbStat() []*monitorpb.ServiceLoadBalanceInfo {
	var res []*monitorpb.ServiceLoadBalanceInfo
	m.lbMutex.Lock()
	for i := 0; i < len(m.lbStat); i++ {
		res = append(res, m.lbStat[i])
	}
	m.lbMutex.Unlock()
	return res
}

//设置负载均衡数据
func (m *monitorServer) SetLbStat(data []*monitorpb.ServiceLoadBalanceInfo) {
	m.lbMutex.Lock()
	m.lbStat = data
	m.lbMutex.Unlock()
}

//获取采集到的限流记录
func (m *monitorServer) CollectRateLimitRecord(server monitorpb.GrpcAPI_CollectRateLimitRecordServer) error {
	for {
		req, err := server.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.rlMutex.Lock()
		m.rlStat = append(m.rlStat, req)
		m.rlMutex.Unlock()
		server.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive ratelimit record: %v\n", req.String())
	}
}

//获取限流记录
func (m *monitorServer) GetRateLimitRecords() []*monitorpb.RateLimitRecord {
	var res []*monitorpb.RateLimitRecord
	m.rlMutex.Lock()
	for i := 0; i < len(m.rlStat); i++ {
		res = append(res, m.rlStat[i])
	}
	m.rlMutex.Unlock()
	return res
}

//设置限流记录
func (m *monitorServer) SetRateLimitRecords(data []*monitorpb.RateLimitRecord) {
	m.rlMutex.Lock()
	m.rlStat = data
	m.rlMutex.Unlock()
}

//接收路由调用记录请求
func (m *monitorServer) CollectRouteRecord(svr monitorpb.GrpcAPI_CollectRouteRecordServer) error {
	for {
		req, err := svr.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.srMutex.Lock()
		m.srStat = append(m.srStat, req)
		m.srMutex.Unlock()
		svr.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive service route record: %v\n", req.String())
	}
}

//获取路由调用记录
func (m *monitorServer) GetServiceRouteRecords() []*monitorpb.ServiceRouteRecord {
	var res []*monitorpb.ServiceRouteRecord
	m.srMutex.Lock()
	for i := 0; i < len(m.srStat); i++ {
		res = append(res, m.srStat[i])
	}
	m.srMutex.Unlock()
	return res
}

//设置路由调用记录
func (m *monitorServer) SetServiceRouteRecords(data []*monitorpb.ServiceRouteRecord) {
	m.srMutex.Lock()
	m.srStat = data
	m.srMutex.Unlock()
}

//或许网格规则变更记录
func (m *monitorServer) GetMeshConfigRecords() []*monitorpb.MeshResourceInfo {
	var res []*monitorpb.MeshResourceInfo
	m.meshMutex.Lock()
	for i := 0; i < len(m.meshStat); i++ {
		res = append(res, m.meshStat[i])
	}
	m.meshMutex.Unlock()
	return res
}

//设置网格变更记录
func (m *monitorServer) SetMeshConfigRecords(data []*monitorpb.MeshResourceInfo) {
	m.meshMutex.Lock()
	m.meshStat = data
	m.meshMutex.Unlock()
}

//接收网格规则变更记录
func (m *monitorServer) CollectMeshResource(svr monitorpb.GrpcAPI_CollectMeshResourceServer) error {
	for {
		req, err := svr.Recv()
		if nil != err {
			if io.EOF == err {
				log.GetBaseLogger().Debugf("Monitor: server receive eof\n")
				return nil
			}
			log.GetBaseLogger().Debugf("Monitor: server recv error %v\n", err)
			return err
		}
		m.meshMutex.Lock()
		m.meshStat = append(m.meshStat, req)
		m.meshMutex.Unlock()
		svr.Send(&monitorpb.StatResponse{
			Id:   &wrappers.StringValue{Value: req.GetId()},
			Code: &wrappers.UInt32Value{Value: monitorpb.ReceiveSuccess},
			Info: &wrappers.StringValue{Value: "success"},
		})
		fmt.Printf("receive mesh record: %v\n", req.String())
	}
}
