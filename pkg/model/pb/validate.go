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

package pb

import (
	"fmt"
	"reflect"

	"github.com/golang/protobuf/jsonpb"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/model"
)

var (
	eventTypeToProtoRequestType = map[model.EventType]apiservice.DiscoverRequest_DiscoverRequestType{
		model.EventInstances:       apiservice.DiscoverRequest_INSTANCE,
		model.EventRouting:         apiservice.DiscoverRequest_ROUTING,
		model.EventRateLimiting:    apiservice.DiscoverRequest_RATE_LIMIT,
		model.EventServices:        apiservice.DiscoverRequest_SERVICES,
		model.EventCircuitBreaker:  apiservice.DiscoverRequest_CIRCUIT_BREAKER,
		model.EventFaultDetect:     apiservice.DiscoverRequest_FAULT_DETECTOR,
		model.EventNearbyRouteRule: apiservice.DiscoverRequest_NEARBY_ROUTE_RULE,
	}

	protoRespTypeToEventType = map[apiservice.DiscoverResponse_DiscoverResponseType]model.EventType{
		apiservice.DiscoverResponse_INSTANCE:          model.EventInstances,
		apiservice.DiscoverResponse_ROUTING:           model.EventRouting,
		apiservice.DiscoverResponse_RATE_LIMIT:        model.EventRateLimiting,
		apiservice.DiscoverResponse_SERVICES:          model.EventServices,
		apiservice.DiscoverResponse_CIRCUIT_BREAKER:   model.EventCircuitBreaker,
		apiservice.DiscoverResponse_FAULT_DETECTOR:    model.EventFaultDetect,
		apiservice.DiscoverResponse_NEARBY_ROUTE_RULE: model.EventNearbyRouteRule,
	}
)

// GetProtoRequestType 通过事件类型获取请求类型
func GetProtoRequestType(event model.EventType) apiservice.DiscoverRequest_DiscoverRequestType {
	if reqType, ok := eventTypeToProtoRequestType[event]; ok {
		return reqType
	}
	return apiservice.DiscoverRequest_UNKNOWN
}

// GetEventType 通过应答类型获取事件类型
func GetEventType(respType apiservice.DiscoverResponse_DiscoverResponseType) model.EventType {
	if eventType, ok := protoRespTypeToEventType[respType]; ok {
		return eventType
	}
	return model.EventUnknown
}

// DiscoverError 从discover获取到了类似500的错误码
type DiscoverError struct {
	Code    int32
	Message string
}

// ServerErrorCodeTypeMap 获取server错误码类型的map
var ServerErrorCodeTypeMap = map[uint32]model.ErrCode{
	200: model.ErrCodeSuccess,
	400: model.ErrCodeInvalidRequest,
	401: model.ErrCodeUnauthorized,
	403: model.ErrCodeRequestLimit,
	404: model.ErrCodeCmdbNotFound,
	500: model.ErrCodeServerError,
}

// ConvertServerErrorToRpcError 将server返回码转化为服务调用的返回码
func ConvertServerErrorToRpcError(code uint32) model.ErrCode {
	typCode := code / 1000
	rpcCode, ok := ServerErrorCodeTypeMap[typCode]
	if !ok {
		return model.ErrCodeUnknownServerError
	}
	return rpcCode
}

// Error 将错误信息转化为string
func (d *DiscoverError) Error() string {
	return fmt.Sprintf("receive %d from discover, message is %s", d.Code, d.Message)
}

// ValidateMessage 校验消息
// 校验返回码为500或者消息类型不对
func ValidateMessage(eventKey *model.ServiceEventKey, message interface{}) error {
	respValue, ok := message.(*apiservice.DiscoverResponse)
	if !ok {
		return &DiscoverError{
			Code:    int32(model.ErrorCodeRpcError),
			Message: fmt.Sprintf("invalid message type %v", reflect.TypeOf(message)),
		}
	}
	if ConvertServerErrorToRpcError(respValue.GetCode().GetValue()) == model.ErrCodeServerError {
		return &DiscoverError{
			Code:    int32(model.ErrCodeServerError),
			Message: respValue.GetInfo().GetValue(),
		}
	}
	eventType := GetEventType(respValue.GetType())
	if eventType == model.EventUnknown {
		return &DiscoverError{
			Code:    int32(model.ErrCodeInvalidServerResponse),
			Message: fmt.Sprintf("invalid event type %v", respValue.GetType()),
		}
	}
	svc := respValue.GetService()
	if nil == svc {
		respJson, _ := (&jsonpb.Marshaler{}).MarshalToString(respValue)
		return &DiscoverError{
			Code:    int32(model.ErrCodeInvalidServerResponse),
			Message: fmt.Sprintf("service is empty, response text is %s", respJson),
		}
	}
	if nil != eventKey {
		discoverErr := &DiscoverError{
			Code:    int32(model.ErrCodeInvalidServerResponse),
			Message: "",
		}
		if svc.GetNamespace().GetValue() != eventKey.Namespace {
			discoverErr.Message = fmt.Sprintf("namespace not match, expect %s, found %s",
				eventKey.Namespace, svc.GetNamespace().GetValue())
			return discoverErr
		}
		if svc.GetName().GetValue() != eventKey.Service {
			discoverErr.Message = fmt.Sprintf("namespace not match, expect %s, found %s",
				eventKey.Namespace, svc.GetNamespace().GetValue())
			return discoverErr
		}
		if eventType != eventKey.Type {
			discoverErr.Message = fmt.Sprintf("eventType not match, expect %s, found %s", eventKey.Type, eventType)
			return discoverErr
		}
	}
	return nil
}
