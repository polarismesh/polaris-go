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

package quota

import (
	"time"

	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"
	slimiter "github.com/polarismesh/specification/source/go/api/v1/traffic_manage/ratelimiter"

	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
)

// DoAsyncRemoteInit 异步处理发送init
func (r *RateLimitWindow) DoAsyncRemoteInit() error {
	if r.Rule.GetType() == apitraffic.Rule_LOCAL || r.configMode == model.ConfigQuotaLocalMode {
		return nil
	}
	sender, err := r.AsyncRateLimitConnector().GetMessageSender(r.remoteCluster, r.hashValue)
	if err != nil {
		// 限频：FAILOVER_LOCAL 场景下远端拉不到是设计内的退化路径；
		// ticker 每 10ms 触发一次，不限频会让 ratelimit log 在几秒内被刷爆（500+条/min），淹没真正的限流判定行.
		// 首次必打，之后 5s 一条；累计被压制的次数一并报告，便于排查时知道仍然在反复重试.
		if shouldLog, suppressed := r.shouldLogRemoteErr(time.Now().UnixNano()); shouldLog {
			if suppressed > 0 {
				r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Errorf(
					"fail to call RateLimitService.GetMessageSender, service %s, error is %s (suppressed %d similar errors in last %s)",
					r.remoteCluster, err, suppressed, time.Duration(remoteErrLogIntervalNano))
			} else {
				r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Errorf(
					"fail to call RateLimitService.GetMessageSender, service %s, error is %s",
					r.remoteCluster, err)
			}
		}
		return err
	}
	timeDiff := sender.AdjustTime()
	r.UpdateTimeDiff(timeDiff)

	request := r.InitializeRequest()
	sender.SendInitRequest(request, r)
	return nil
}

// DoAsyncRemoteAcquire 异步发送 acquire
func (r *RateLimitWindow) DoAsyncRemoteAcquire() error {
	if r.Rule.GetType() == apitraffic.Rule_LOCAL || r.configMode == model.ConfigQuotaLocalMode {
		return nil
	}
	sender, err := r.AsyncRateLimitConnector().GetMessageSender(r.remoteCluster, r.hashValue)
	if err != nil {
		// 与 DoAsyncRemoteInit 共用同一个限频窗口（共享 r.remoteErrLogLastNano），
		// 避免两个调用点交替打日志破坏限频效果.
		if shouldLog, suppressed := r.shouldLogRemoteErr(time.Now().UnixNano()); shouldLog {
			if suppressed > 0 {
				r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Errorf(
					"fail to call RateLimitService.GetMessageSender, service %s, error is %s (suppressed %d similar errors in last %s)",
					r.remoteCluster, err, suppressed, time.Duration(remoteErrLogIntervalNano))
			} else {
				r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Errorf(
					"fail to call RateLimitService.GetMessageSender, service %s, error is %s",
					r.remoteCluster, err)
			}
		}
		return err
	}
	if !sender.HasInitialized(r.SvcKey, r.Labels) {
		r.SetStatus(Initializing)
		return r.DoAsyncRemoteInit()
	}

	timeDiff := sender.AdjustTime()
	r.UpdateTimeDiff(timeDiff)

	request := r.acquireRequest()
	err = sender.SendReportRequest(request)
	if err != nil {
		r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Errorf(
			"fail to call RateLimitService.Acquire, service %s, labels %s, error is %s",
			r.SvcKey, r.Labels, err)
		return err
	}
	return nil
}

// OnInitResponse 应答回调函数
func (r *RateLimitWindow) OnInitResponse(counter *slimiter.QuotaCounter, duration time.Duration, srvTimeMilli int64) {
	r.SetStatus(Initialized)
	r.WindowSet.flowAssistant.logCtx.GetRateLimitLogger().Infof("[RateLimit]window %s changed to initialized", r.uniqueKey)
	r.trafficShapingBucket.OnRemoteUpdate(ratelimiter.RemoteQuotaResult{
		Left:            counter.GetLeft(),
		ClientCount:     counter.GetClientCount(),
		ServerTimeMilli: srvTimeMilli,
		ClientTimeMilli: r.toServerTimeMilli(model.CurrentMillisecond()),
		DurationMill:    model.ToMilliSeconds(duration),
	})
}

// OnReportResponse 应答回调函数
func (r *RateLimitWindow) OnReportResponse(counter *slimiter.QuotaLeft, duration time.Duration, curTimeMilli int64) {
	r.trafficShapingBucket.OnRemoteUpdate(ratelimiter.RemoteQuotaResult{
		Left:            counter.GetLeft(),
		ClientCount:     counter.GetClientCount(),
		ServerTimeMilli: curTimeMilli,
		ClientTimeMilli: r.toServerTimeMilli(model.CurrentMillisecond()),
		DurationMill:    model.ToMilliSeconds(duration),
	})
}
