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
package client

import (
	"github.com/polarismesh/polaris-go/pkg/model"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	connectorComm "github.com/polarismesh/polaris-go/plugin/serverconnector/common"
	"github.com/polarismesh/polaris-go/plugin/serverconnector/sidecar/dns"
)

//RegisterServiceHandler 注册服务监听
func (g *Connector) RegisterServiceHandler(svcEventHandler *serverconnector.ServiceEventHandler) error {
	return g.discoverConnector.RegisterServiceHandler(svcEventHandler)
}

//DeRegisterEventHandler 反注册事件监听器
func (g *Connector) DeRegisterServiceHandler(key *model.ServiceEventKey) error {
	return g.discoverConnector.DeRegisterServiceHandler(key)
}

//RegisterInstance 同步注册服务
func (g *Connector) RegisterInstance(request *model.InstanceRegisterRequest) (*model.InstanceRegisterResponse, error) {
	dnsMsgReq := newDefaultDnsMsg(g.getDnsMsgId())
	dnsMsgReq.Opcode = dns.OpCodePolarisRegisterInstance

	dnsMsgReq.Qdcount = 1
	question := dns.PolarisInstanceQuestion{}
	question.Qtype = dns.TypePolarisInstance
	question.Qclass = dns.ClassINET
	question.Req = connectorComm.RegisterRequestToProto(request)
	dnsMsgReq.Question = append(dnsMsgReq.Question, &question)

	rsp, _, err := g.SyncExchange(dnsMsgReq)
	if err != nil {
		return nil, err
	}
	registerRsp, err := convertRspDataToRegisterResp(rsp)
	if err != nil {
		return nil, err
	}
	return registerRsp, nil
}

//DeregisterInstance 同步反注册服务
func (g *Connector) DeregisterInstance(instance *model.InstanceDeRegisterRequest) error {
	dnsMsgReq := newDefaultDnsMsg(g.getDnsMsgId())
	dnsMsgReq.Opcode = dns.OpCodePolarisDeregisterInstance

	dnsMsgReq.Qdcount = 1
	question := dns.PolarisInstanceQuestion{}
	question.Qtype = dns.TypePolarisInstance
	question.Qclass = dns.ClassINET
	question.Req = connectorComm.DeregisterRequestToProto(instance)
	dnsMsgReq.Question = append(dnsMsgReq.Question, &question)

	rsp, _, err := g.SyncExchange(dnsMsgReq)
	if err != nil {
		return err
	}
	if rsp.RCode != 0 {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil, "")
	}
	return nil
}

// 心跳上报
func (g *Connector) Heartbeat(instance *model.InstanceHeartbeatRequest) error {
	dnsMsgReq := newDefaultDnsMsg(g.getDnsMsgId())
	dnsMsgReq.Opcode = dns.OpCodePolarisHeartbeat

	dnsMsgReq.Qdcount = 1
	question := dns.PolarisInstanceQuestion{}
	question.Qtype = dns.TypePolarisInstance
	question.Qclass = dns.ClassINET
	question.Req = connectorComm.HeartbeatRequestToProto(instance)
	dnsMsgReq.Question = append(dnsMsgReq.Question, &question)

	rsp, _, err := g.SyncExchange(dnsMsgReq)
	if err != nil {
		return err
	}
	if rsp.RCode != 0 {
		return model.NewSDKError(model.ErrCodeInvalidResponse, nil, "")
	}
	return nil
}

// 报客户端信息
func (g *Connector) ReportClient(request *model.ReportClientRequest) (*model.ReportClientResponse, error) {
	dnsMsgReq := newDefaultDnsMsg(g.getDnsMsgId())
	dnsMsgReq.Opcode = dns.OpCodePolarisReportClient

	dnsMsgReq.Qdcount = 1
	question := dns.PolarisReportClientQuestion{}
	question.Qtype = dns.TypePolarisSideCarLocation
	question.Qclass = dns.ClassINET
	question.Req = connectorComm.ReportClientRequestToProto(request)
	dnsMsgReq.Question = append(dnsMsgReq.Question, &question)

	rsp, _, err := g.SyncExchange(dnsMsgReq)
	if err != nil {
		return nil, err
	}
	reportClientRsp, err := convertRspDataToReportClientResp(rsp)
	if err != nil {
		return nil, err
	}
	return reportClientRsp, nil
}

// 更新服务端地址 sideCar模式目前无需实现
func (g *Connector) UpdateServers(key *model.ServiceEventKey) error {
	return nil
}

// 同步获取资源
func (g *Connector) SyncGetResourceReq(request *namingpb.DiscoverRequest) (*namingpb.DiscoverResponse, error) {
	dnsMsg, err := convertDiscoverRequestToDnsMsg(request, g.getDnsMsgId())
	rsp, _, err := g.SyncExchange(dnsMsg)
	if err != nil {
		return nil, err
	}

	discoverRsp, err := convertRspDataToDiscoverResponse(rsp)
	if err != nil {
		return nil, err
	}
	return discoverRsp, nil
}
