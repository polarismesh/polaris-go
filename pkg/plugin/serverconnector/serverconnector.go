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

package serverconnector

import (
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	rlimitV2 "github.com/polarismesh/polaris-go/pkg/model/pb/metric/v2"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/golang/protobuf/proto"
	"time"
)

//ServiceEvent 事件对象
type ServiceEvent struct {
	//服务
	model.ServiceEventKey
	//事件对象值
	Value proto.Message
	//服务错误
	Error model.SDKError
}

//EventHandler 事件回调handler
type EventHandler interface {
	//回调函数接口
	//返回缓存值是否已经被清理(对于服务被剔除，或者首次服务拉取失败，会返回true)
	OnServiceUpdate(*ServiceEvent) (deleted bool)
	//获取缓存版本号
	GetRevision() string
	//获取网格请求的resource
	GetMeshResource() *namingpb.MeshResource
	GetMeshConfig() *namingpb.MeshConfig
	//获取业务
	GetBusiness() string
}

//服务事件回调结构
type ServiceEventHandler struct {
	*model.ServiceEventKey
	//目标发现集群，对于系统服务，需要使用默认集群来进行发现
	TargetCluster config.ClusterType
	//服务的定期刷新时间，默认1s
	RefreshInterval time.Duration
	//服务事件处理句柄
	Handler EventHandler
}

//stream模式的PB消息回调
type MessageCallBack interface {
	//收到应答后回调
	OnResponse(proto.Message)
}

//应答回调函数
type ResponseCallBack interface {
	//应答回调函数
	OnInitResponse(counter *rlimitV2.QuotaCounter, duration time.Duration, curTimeMilli int64)
	//应答回调函数
	OnReportResponse(counter *rlimitV2.QuotaLeft, duration time.Duration, curTimeMilli int64)
}

//限流消息同步器
type RateLimitMsgSender interface {
	//是否已经初始化
	HasInitialized(svcKey model.ServiceKey, labels string) bool
	//发送初始化请求
	SendInitRequest(request *rlimitV2.RateLimitInitRequest, callback ResponseCallBack)
	//发送上报请求
	SendReportRequest(request *rlimitV2.ClientRateLimitReportRequest) error
	//同步时间
	AdjustTime() int64
}

//异步限流连接器
type AsyncRateLimitConnector interface {
	//初始化限流控制信息
	GetMessageSender(svcKey model.ServiceKey, hashValue uint64) (RateLimitMsgSender, error)
	//销毁
	Destroy()
}

//ServerConnector 【扩展点接口】server代理，封装了server对接的逻辑
type ServerConnector interface {
	plugin.Plugin
	//注册服务监听器
	//异常场景：当key不合法或者sdk已经退出过程中，则返回error
	RegisterServiceHandler(*ServiceEventHandler) error
	//反注册事件监听器
	//异常场景：当sdk已经退出过程中，则返回error
	DeRegisterServiceHandler(*model.ServiceEventKey) error
	//同步注册服务
	RegisterInstance(*model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error)
	// 同步反注册服务
	DeregisterInstance(instance *model.InstanceDeRegisterRequest) error
	// 心跳上报
	Heartbeat(instance *model.InstanceHeartbeatRequest) error
	// 上报客户端信息
	// 异常场景：当sdk已经退出过程中，则返回error
	// 异常场景：当服务端不可用或者上报失败，则返回error，调用者需进行重试
	ReportClient(*model.ReportClientRequest) (*model.ReportClientResponse, error)
	// 更新服务端地址
	// 异常场景：当地址列表为空，或者地址全部连接失败，则返回error，调用者需进行重试
	UpdateServers(key *model.ServiceEventKey) error
	//获取限流server连接器
	GetAsyncRateLimitConnector() AsyncRateLimitConnector
}

//初始化
func init() {
	plugin.RegisterPluginInterface(common.TypeServerConnector, new(ServerConnector))
}
