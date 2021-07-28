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

package model

import (
	"fmt"
)

//ErrCode 错误码类型，Polaris SDK对外返回错误码都使用该类型
type ErrCode int32

const (
	BaseIndexErrCode = 1000
	//ErrCodeCount     = ErrCodeMax%BaseIndexErrCode + 1
)

const (
	//ErrCodeSuccess 没有发生错误
	ErrCodeSuccess ErrCode = 0
	//ErrCodeUnknown 未知错误
	ErrCodeUnknown ErrCode = BaseIndexErrCode
	//ErrCodeAPIInvalidArgument API参数非法的错误码
	ErrCodeAPIInvalidArgument ErrCode = BaseIndexErrCode + 1
	//ErrCodeAPIInvalidConfig 配置非法的错误码
	ErrCodeAPIInvalidConfig ErrCode = BaseIndexErrCode + 2
	//ErrCodePluginError 插件错误的错误码
	ErrCodePluginError ErrCode = BaseIndexErrCode + 3
	//ErrCodeAPITimeoutError API超时错误的错误码
	ErrCodeAPITimeoutError ErrCode = BaseIndexErrCode + 4
	//ErrCodeAPITimeoutError SDK已经destroy后，继续调API会出现的错误码
	ErrCodeInvalidStateError ErrCode = BaseIndexErrCode + 5
	//ErrCodeServerUserError 连接server时，server返回400错误信息
	ErrCodeServerUserError ErrCode = BaseIndexErrCode + 6
	//ErrCodeNetworkError 连接server时所出现的未知网络异常
	ErrCodeNetworkError ErrCode = BaseIndexErrCode + 7
	//ErrCodeCircuitBreakerError 服务熔断错误
	ErrCodeCircuitBreakerError ErrCode = BaseIndexErrCode + 8
	//实例信息有误，如服务权重信息为空
	ErrCodeInstanceInfoError ErrCode = BaseIndexErrCode + 9
	//ErrCodeAPIInstanceNotFOUND 服务实例获取失败
	ErrCodeAPIInstanceNotFound ErrCode = BaseIndexErrCode + 10
	//ErrCodeInvalidRule 路由规则非法
	ErrCodeInvalidRule ErrCode = BaseIndexErrCode + 11
	//ErrCodeRouteRuleNotMatch 路由规则匹配失败
	ErrCodeRouteRuleNotMatch ErrCode = BaseIndexErrCode + 12
	//ErrCodeInvalidResponse Server返回的消息不合法
	ErrCodeInvalidResponse ErrCode = BaseIndexErrCode + 13
	//ErrCodeInternalError 内部算法及系统错误
	ErrCodeInternalError ErrCode = BaseIndexErrCode + 14
	//ErrCodeServiceNotFound 服务不存在
	ErrCodeServiceNotFound ErrCode = BaseIndexErrCode + 15
	//ErrCodeServerException server返回500错误
	ErrCodeServerException ErrCode = BaseIndexErrCode + 16
	//ErrCodeLocationNotFound 获取地域信息失败
	ErrCodeLocationNotFound ErrCode = BaseIndexErrCode + 17
	//ErrCodeLocationMismatch 就近路由失败，在对应就近级别上面没有实例
	ErrCodeLocationMismatch ErrCode = BaseIndexErrCode + 18
	//ErrCodeDstMetaMismatch 目标规则元数据过滤失败
	ErrCodeDstMetaMismatch ErrCode = BaseIndexErrCode + 19
	//ErrCodeMeshConfigNotFound 目标类型的网格规则未找到
	ErrCodeMeshConfigNotFound ErrCode = BaseIndexErrCode + 20
	//ErrCodeConsumerInitCalleeError 初始化服务运行中需要的被调服务失败
	ErrCodeConsumerInitCalleeError ErrCode = BaseIndexErrCode + 21
	//接口错误码数量，每添加了一个错误码，将这个数值加1
	ErrCodeCount = 23
)

const (
	BaseServerErrCode = 2000
)

const (
	ErrCodeConnectError          ErrCode = BaseServerErrCode + 1
	ErrCodeServerError           ErrCode = BaseServerErrCode + 2
	ErrorCodeRpcError            ErrCode = BaseServerErrCode + 3
	ErrorCodeRpcTimeout          ErrCode = BaseServerErrCode + 4
	ErrCodeInvalidServerResponse ErrCode = BaseServerErrCode + 5
	ErrCodeInvalidRequest        ErrCode = BaseServerErrCode + 6
	ErrCodeUnauthorized          ErrCode = BaseServerErrCode + 7
	ErrCodeRequestLimit          ErrCode = BaseServerErrCode + 8
	ErrCodeCmdbNotFound          ErrCode = BaseServerErrCode + 9
	ErrCodeUnknownServerError    ErrCode = BaseServerErrCode + 100
)

const (
	BaseSDKInternalErrCode = 3000
)

const (
	ErrCodeDiskError = BaseSDKInternalErrCode + 1
)

//是否可重试的错误码
func (e ErrCode) Retryable() bool {
	return e == ErrCodeNetworkError || e == ErrCodeServerException
}

//SDK错误的总封装类型
type SDKError interface {
	//获取错误码
	ErrorCode() ErrCode
	//获取错误信息
	Error() string
	//获取服务端返回的错误码
	ServerCode() uint32
	//获取服务端返回的错误信息
	ServerInfo() string
}

//SDK错误类型实现
type sdkError struct {
	errCode    ErrCode
	errDetail  string
	cause      error
	serverCode uint32
	serverInfo string
}

//获取错误码
func (s *sdkError) ErrorCode() ErrCode {
	return s.errCode
}

//获取错误信息
func (s *sdkError) Error() string {
	errCodeStr := ErrCodeToString(s.ErrorCode())
	if nil != s.cause {
		return fmt.Sprintf(
			"Polaris-%v(%s): %s, cause: %s", s.ErrorCode(), errCodeStr, s.errDetail, s.cause.Error())
	}
	return fmt.Sprintf("Polaris-%v(%s): %s", s.ErrorCode(), errCodeStr, s.errDetail)
}

//服务端返回码
func (s *sdkError) ServerCode() uint32 {
	return s.serverCode
}

//服务端返回信息
func (s *sdkError) ServerInfo() string {
	return s.serverInfo
}

//输出字符串信息
func (s sdkError) String() string {
	return s.Error()
}

//NewSDKError SDK错误相关的类构建器
func NewSDKError(errCode ErrCode, cause error, msg string, args ...interface{}) SDKError {
	var errDetail = fmt.Sprintf(msg, args...)
	return &sdkError{
		errCode:   errCode,
		errDetail: errDetail,
		cause:     cause}
}

const (
	//返回码取模的底数
	RetCodeDivFactor = 1000
	//取模后的成功错误码
	SuccessRetCode = 200
	//取模后的server内部错误
	ServerExceptionRetCode = 500
)

//判断是否成功的错误码
func IsSuccessResultCode(retCode uint32) bool {
	return retCode/RetCodeDivFactor == SuccessRetCode
}

//判断是否为内部server错误
func IsServerException(retCode uint32) bool {
	return retCode/RetCodeDivFactor == ServerExceptionRetCode
}

//构造服务端相关错误
func NewServerSDKError(serverCode uint32, serverInfo string, cause error, msg string, args ...interface{}) SDKError {
	return &sdkError{
		errCode:    serverCodeToErrCode(serverCode),
		errDetail:  fmt.Sprintf(msg, args...),
		cause:      cause,
		serverCode: serverCode,
		serverInfo: serverInfo,
	}
}

//通过服务端的错误码转换成SDK错误码
func serverCodeToErrCode(retCode uint32) ErrCode {
	errCode := ErrCodeServerUserError
	if IsServerException(retCode) {
		errCode = ErrCodeServerException
	}
	return errCode
}

var errCodeString = map[ErrCode]string{
	ErrCodeSuccess:                 "Success",
	ErrCodeUnknown:                 "ErrCodeUnknown",
	ErrCodeAPIInvalidArgument:      "ErrCodeAPIInvalidArgument",
	ErrCodeAPIInvalidConfig:        "ErrCodeAPIInvalidConfig",
	ErrCodePluginError:             "ErrCodePluginError",
	ErrCodeAPITimeoutError:         "ErrCodeAPITimeoutError",
	ErrCodeInvalidStateError:       "ErrCodeInvalidStateError",
	ErrCodeServerUserError:         "ErrCodeServerUserError",
	ErrCodeNetworkError:            "ErrCodeNetworkError",
	ErrCodeCircuitBreakerError:     "ErrCodeCircuitBreakerError",
	ErrCodeInstanceInfoError:       "ErrCodeInstanceInfoError",
	ErrCodeAPIInstanceNotFound:     "ErrCodeAPIInstanceNotFound",
	ErrCodeInvalidRule:             "ErrCodeInvalidRule",
	ErrCodeRouteRuleNotMatch:       "ErrCodeRouteRuleNotMatch",
	ErrCodeInvalidResponse:         "ErrCodeInvalidResponse",
	ErrCodeServiceNotFound:         "ErrCodeServiceNotFound",
	ErrCodeInternalError:           "ErrCodeInternalError",
	ErrCodeServerException:         "ErrCodeServerException",
	ErrCodeLocationNotFound:        "ErrCodeLocationNotFound",
	ErrCodeLocationMismatch:        "ErrCodeLocationMismatch",
	ErrCodeDstMetaMismatch:         "ErrCodeDstMetaMismatch",
	ErrCodeMeshConfigNotFound:      "ErrCodeMeshConfigNotFound",
	ErrCodeConsumerInitCalleeError: "ErrCodeConsumerInitCalleeError",
}

var errCodeArray = []ErrCode{ErrCodeSuccess, ErrCodeUnknown, ErrCodeAPIInvalidArgument,
	ErrCodeAPIInvalidConfig, ErrCodePluginError, ErrCodeAPITimeoutError, ErrCodeInvalidStateError,
	ErrCodeServerUserError, ErrCodeNetworkError, ErrCodeCircuitBreakerError, ErrCodeInstanceInfoError,
	ErrCodeAPIInstanceNotFound, ErrCodeInvalidRule, ErrCodeRouteRuleNotMatch, ErrCodeInvalidResponse,
	ErrCodeInternalError, ErrCodeServiceNotFound, ErrCodeServerException, ErrCodeLocationNotFound,
	ErrCodeLocationMismatch, ErrCodeDstMetaMismatch, ErrCodeMeshConfigNotFound, ErrCodeConsumerInitCalleeError,
}

//根据错误码索引返回错误码
func ErrCodeFromIndex(i int) ErrCode {
	return errCodeArray[i]
}

//将错误码转换为字符串
func ErrCodeToString(ec ErrCode) string {
	res, ok := errCodeString[ec]
	if !ok {
		return "ErrCodeUnknown"
	}
	return res
}

//错误码类型
type ErrCodeType int

const (
	//北极星系统错误
	PolarisError ErrCodeType = 0
	//用户错误
	UserError ErrCodeType = 1
)

//错误码跟错误类型的映射
var errCodeTypeMap = map[ErrCode]ErrCodeType{
	ErrCodeUnknown:             PolarisError,
	ErrCodePluginError:         PolarisError,
	ErrCodeAPITimeoutError:     PolarisError,
	ErrCodeNetworkError:        PolarisError,
	ErrCodeInvalidResponse:     PolarisError,
	ErrCodeInternalError:       PolarisError,
	ErrCodeServerException:     PolarisError,
	ErrCodeCircuitBreakerError: PolarisError,
	ErrCodeLocationNotFound:    PolarisError,

	ErrCodeAPIInvalidArgument:      UserError,
	ErrCodeAPIInvalidConfig:        UserError,
	ErrCodeInvalidStateError:       UserError,
	ErrCodeServerUserError:         UserError,
	ErrCodeInstanceInfoError:       UserError,
	ErrCodeAPIInstanceNotFound:     UserError,
	ErrCodeInvalidRule:             UserError,
	ErrCodeServiceNotFound:         UserError,
	ErrCodeRouteRuleNotMatch:       UserError,
	ErrCodeLocationMismatch:        UserError,
	ErrCodeDstMetaMismatch:         UserError,
	ErrCodeMeshConfigNotFound:      UserError,
	ErrCodeConsumerInitCalleeError: UserError,
}

//获取错误码类型
func GetErrCodeType(e ErrCode) ErrCodeType {
	t, ok := errCodeTypeMap[e]
	if ok {
		return t
	}
	return PolarisError
}
