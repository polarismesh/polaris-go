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

package common

import (
	"github.com/golang/protobuf/ptypes/wrappers"

	"github.com/polarismesh/polaris-go/pkg/model"
	apimodel "github.com/polarismesh/specification/source/go/api/v1/model"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
)

// RegisterRequestToProto 将用户的API注册请求结构转换成为server端需要的proto结构
func RegisterRequestToProto(request *model.InstanceRegisterRequest) (pbInstance *apiservice.Instance) {
	pbInstance = assembleNamingPbInstance(request.Namespace, request.Service, request.Host,
		request.Port, request.ServiceToken, request.InstanceId)
	if nil != request.Protocol {
		pbInstance.Protocol = &wrappers.StringValue{Value: *request.Protocol}
	}
	if nil != request.Weight {
		pbInstance.Weight = &wrappers.UInt32Value{Value: uint32(*request.Weight)}
	}
	if nil != request.Priority {
		pbInstance.Priority = &wrappers.UInt32Value{Value: uint32(*request.Priority)}
	}
	if nil != request.Version {
		pbInstance.Version = &wrappers.StringValue{Value: *request.Version}
	}
	if nil != request.Metadata {
		pbInstance.Metadata = request.Metadata
	}
	if nil != request.Healthy {
		pbInstance.Healthy = &wrappers.BoolValue{Value: *request.Healthy}
	}
	if nil != request.Isolate {
		pbInstance.Isolate = &wrappers.BoolValue{Value: *request.Isolate}
	}
	if nil != request.Location {
		pbInstance.Location = &apimodel.Location{
			Region: &wrappers.StringValue{Value: request.Location.Region},
			Zone:   &wrappers.StringValue{Value: request.Location.Zone},
			Campus: &wrappers.StringValue{Value: request.Location.Campus},
		}
	}
	// 开启了远程健康检查
	if nil != request.TTL {
		pbInstance.HealthCheck = &apiservice.HealthCheck{
			Type: apiservice.HealthCheck_HEARTBEAT,
			Heartbeat: &apiservice.HeartbeatHealthCheck{
				Ttl: &wrappers.UInt32Value{Value: uint32(*request.TTL)},
			},
		}
	}
	return pbInstance
}

func assembleNamingPbInstance(namespace string, service string, host string,
	port int, serviceToken string, instanceId string) *apiservice.Instance {
	pbInstance := apiservice.Instance{
		Namespace:    &wrappers.StringValue{Value: namespace},
		Service:      &wrappers.StringValue{Value: service},
		Host:         &wrappers.StringValue{Value: host},
		Port:         &wrappers.UInt32Value{Value: uint32(port)},
		ServiceToken: &wrappers.StringValue{Value: serviceToken},
	}
	if len(instanceId) > 0 {
		pbInstance.Id = &wrappers.StringValue{Value: instanceId}
	}
	return &pbInstance
}

// HeartbeatRequestToProto 将用户心跳请转化为服务端需要的proto
func HeartbeatRequestToProto(request *model.InstanceHeartbeatRequest) (pbInstance *apiservice.Instance) {
	pbInstance = assembleNamingPbInstance(request.Namespace, request.Service, request.Host,
		request.Port, request.ServiceToken, request.InstanceID)
	return pbInstance
}

// DeregisterRequestToProto 将用户反注册请求转化为服务端需要的proto
func DeregisterRequestToProto(request *model.InstanceDeRegisterRequest) (pbInstance *apiservice.Instance) {
	pbInstance = assembleNamingPbInstance(request.Namespace, request.Service, request.Host,
		request.Port, request.ServiceToken, request.InstanceID)
	return pbInstance
}

// ReportClientRequestToProto 将客户端上报请转化为服务端需要的proto
func ReportClientRequestToProto(request *model.ReportClientRequest) (pbInstance *apiservice.Client) {
	pbInstance = &apiservice.Client{
		Id: &wrappers.StringValue{Value: request.ID},
		Host: &wrappers.StringValue{
			Value: request.Host,
		},
		Type: apiservice.Client_SDK,
		Version: &wrappers.StringValue{
			Value: request.Version,
		},
		Stat: statInfoToProto(request.StatInfos),
	}
	return pbInstance
}

func statInfoToProto(infos []model.StatInfo) []*apiservice.StatInfo {
	ret := make([]*apiservice.StatInfo, 0, len(infos))

	for i := range infos {
		ret = append(ret, &apiservice.StatInfo{
			Target:   &wrappers.StringValue{Value: infos[i].Target},
			Protocol: &wrappers.StringValue{Value: infos[i].Protocol},
			Path:     &wrappers.StringValue{Value: infos[i].Path},
			Port:     &wrappers.UInt32Value{Value: infos[i].Port},
		})
	}

	return ret
}
