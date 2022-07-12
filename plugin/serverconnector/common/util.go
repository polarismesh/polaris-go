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
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/modern-go/reflect2"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"

	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/network"
)

type taskOp int

const (
	opAddListener taskOp = iota + 1
	opDelListener
)

const (
	reqIDPrefixRegisterInstance = iota + 1
	reqIDPrefixDeregisterInstance
	reqIDPrefixInstanceHeartbeat
	reqIDPrefixDiscover
	reqIDPrefixReportClient
	reqIDPrefixRateLimitInit
	reqIDPrefixRateLimitAcquire
	reqIDPrefixGetConfigFile
	reqIDPrefixWatchConfigFiles
)

const (
	OpKeyRegisterInstance      = "RegisterInstance"
	OpKeyDeregisterInstance    = "DeregisterInstance"
	OpKeyInstanceHeartbeat     = "InstanceHeartbeat"
	OpKeyDiscover              = "Discover"
	OpKeyReportClient          = "ReportClient"
	OpKeyRateLimitInit         = "RateLimitInit"
	OpKeyRateLimitAcquire      = "RateLimitAcquire"
	OpKeyRateLimitMetricInit   = "RateLimitMetricInit"
	OpKeyRateLimitMetricReport = "RateLimitMetricReport"
	OpKeyGetConfigFile         = "GetConfigFile"
	OpKeyWatchConfigFiles      = "WatchConfigFiles"
)

// NextDiscoverReqID 生成GetInstances调用的请求Id
func NextDiscoverReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixDiscover, uuid.New().ID())
}

// NextRegisterInstanceReqID 生成RegisterService调用的请求Id
func NextRegisterInstanceReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixRegisterInstance, uuid.New().ID())
}

// NextDeRegisterInstanceReqID 生成RegisterService调用的请求Id
func NextDeRegisterInstanceReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixDeregisterInstance, uuid.New().ID())
}

// NextHeartbeatReqID 生成RegisterService调用的请求Id
func NextHeartbeatReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixInstanceHeartbeat, uuid.New().ID())
}

// NextReportClientReqID 生成ReportClient调用的请求Id
func NextReportClientReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixReportClient, uuid.New().ID())
}

// NextRateLimitInitReqID 生成RateLimit初始化调用的请求Id
func NextRateLimitInitReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixRateLimitInit, uuid.New().ID())
}

// NextRateLimitAcquireReqID 生成RateLimit配额获取调用的请求Id
func NextRateLimitAcquireReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixRateLimitAcquire, uuid.New().ID())
}

// NextGetConfigFileReqID 生成GetConfigFile调用的请求Id
func NextGetConfigFileReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixGetConfigFile, uuid.New().ID())
}

// NextWatchConfigFilesReqID 生成WatchConfigFiles调用的请求Id
func NextWatchConfigFilesReqID() string {
	return fmt.Sprintf("%d%d", reqIDPrefixWatchConfigFiles, uuid.New().ID())
}

// GetConnErrorCode 获取连接错误码
func GetConnErrorCode(err error) int32 {
	code, ok := status.FromError(err)
	if ok {
		return int32(code.Code())
	}
	return int32(model.ErrCodeNetworkError)
}

// NetworkError 返回网络错误，并回收连接
func NetworkError(connManager network.ConnectionManager, conn *network.Connection,
	errCode int32, err error, startTime time.Time, msg string) model.SDKError {
	endTime := clock.GetClock().Now()
	if nil != conn {
		connManager.ReportFail(conn.ConnID, errCode, endTime.Sub(startTime))
		connManager.ReportConnectionDown(conn.ConnID)
	}
	return model.NewSDKError(model.ErrCodeNetworkError, err, msg)
}

// GetUpdateTaskRequestTime 获取一个updateTask的请求更新时间
func GetUpdateTaskRequestTime(updateTask *serviceUpdateTask) time.Duration {
	consumeTime := maxConnTimeout
	msgSendTimeValue := updateTask.msgSendTime.Load()
	if !reflect2.IsNil(msgSendTimeValue) {
		consumeTime = time.Since(msgSendTimeValue.(time.Time))
	}
	return consumeTime
}

// 创建传输grpc头的valueContext
// func CreateHeaderContext(timeout time.Duration, reqID string) context.Context {
//	md := metadata.New(map[string]string{headerRequestID: reqID})
//	var ctx context.Context
//	if timeout > 0 {
//		ctx, _ = context.WithTimeout(context.Background(), timeout)
//	} else {
//		ctx = context.Background()
//	}
//	return metadata.NewOutgoingContext(ctx, md)
// }

// CreateHeaderContext 创建传输grpc头的valueContext
func CreateHeaderContext(timeout time.Duration, headers map[string]string) (context.Context, context.CancelFunc) {
	md := metadata.New(headers)
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx = context.Background()
		cancel = nil
	}
	return metadata.NewOutgoingContext(ctx, md), cancel
}

// CreateHeaderContextWithReqId 创建传输grpc头的valueContext
func CreateHeaderContextWithReqId(timeout time.Duration, reqID string) (context.Context, context.CancelFunc) {
	md := metadata.New(map[string]string{headerRequestID: reqID})
	var ctx context.Context
	var cancel context.CancelFunc
	if timeout > 0 {
		ctx, cancel = context.WithTimeout(context.Background(), timeout)
	} else {
		ctx = context.Background()
		cancel = nil
	}
	return metadata.NewOutgoingContext(ctx, md), cancel
}

func AppendHeaderWithReqId(header map[string]string, reqID string) map[string]string {
	m := make(map[string]string, len(header)+1)
	for k, v := range header {
		m[k] = v
	}
	m[headerRequestID] = reqID
	return m
}
