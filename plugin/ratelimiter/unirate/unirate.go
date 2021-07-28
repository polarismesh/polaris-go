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

package unirate

import (
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/clock"
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"math"
	"sync/atomic"
	"time"
)

//基于匀速排队策略的限流控制器
type RateLimiterUniformRate struct {
	*plugin.PluginBase
	cfg *Config
}

//Type 插件类型
func (g *RateLimiterUniformRate) Type() common.Type {
	return common.TypeRateLimiter
}

//Name 插件名，一个类型下插件名唯一
func (g *RateLimiterUniformRate) Name() string {
	return config.DefaultUniformRateLimiter
}

//Init 初始化插件
func (g *RateLimiterUniformRate) Init(ctx *plugin.InitContext) error {
	g.PluginBase = plugin.NewPluginBase(ctx)
	cfgValue := ctx.Config.GetProvider().GetRateLimit().GetPluginConfig(g.Name())
	if cfgValue != nil {
		g.cfg = cfgValue.(*Config)
	}
	return nil
}

//Destroy 销毁插件，可用于释放资源
func (g *RateLimiterUniformRate) Destroy() error {
	return nil
}

// enable ?
func (g *RateLimiterUniformRate) IsEnable(cfg config.Configuration) bool {
	if cfg.GetGlobal().GetSystem().GetMode() == model.ModeWithAgent {
		return false
	} else {
		return true
	}
}

//uniforme ratelimiter的窗口实现
type quotaWindow struct {
	//限流规则
	rule *namingpb.Rule
	//上次分配配额的时间戳
	lastGrantTime int64
	//等效配额
	effectiveAmount uint32
	//等效时间窗
	effectiveDuration time.Duration
	//为一个实例生成一个配额的平均时间
	effectiveRate int64
	//所有实例分配一个配额的平均时间
	totalRate float64
	//最大排队时间
	maxQueuingDuration int64
	//是不是有amount为0
	rejectAll bool
}

//服务实例发生变更时，改变effectiveRate
func (q *quotaWindow) OnInstancesChanged(instCount int) {
	newEffectiveRate := q.totalRate * float64(instCount)
	atomic.StoreInt64(&q.effectiveRate, int64(math.Round(newEffectiveRate)))
}

//分配配额
func (q *quotaWindow) GetQuota() (*ratelimiter.QuotaResult, error) {
	if q.rejectAll {
		return &ratelimiter.QuotaResult{
			Code: model.QuotaResultLimited,
			Info: "uniRate RateLimiter: reject for zero rule amount",
		}, nil
	}
	//需要多久产生这么请求的配额
	costDuration := atomic.LoadInt64(&q.effectiveRate)

	var waitDuration int64
	for {
		currentTime := clock.GetClock().Now().UnixNano()
		expectedTime := atomic.AddInt64(&q.lastGrantTime, costDuration)

		waitDuration = expectedTime - currentTime
		if waitDuration >= 0 {
			break
		}
		//首次访问，尝试更新时间间隔
		if atomic.CompareAndSwapInt64(&q.lastGrantTime, expectedTime, currentTime) {
			//更新时间成功，此时他是第一个进来的，等待时间归0
			waitDuration = 0
			break
		}
	}
	if waitDuration < clock.TimeStep().Nanoseconds() {
		return &ratelimiter.QuotaResult{
			Code: model.QuotaResultOk,
			Info: "uniRate RateLimiter: grant quota",
		}, nil
	}
	//如果等待时间在上限之内，那么放通
	if waitDuration <= q.maxQueuingDuration {
		//log.Printf("grant quota, waitDuration %v", waitDuration)
		return &ratelimiter.QuotaResult{
			Code:      model.QuotaResultOk,
			QueueTime: time.Duration(waitDuration),
		}, nil
	}
	//如果等待时间超过配置的上限，那么拒绝
	//归还等待间隔
	info := fmt.Sprintf(
		"uniRate RateLimiter: queueing time %d exceed maxQueuingTime %s",
		time.Duration(waitDuration), time.Duration(waitDuration))
	atomic.AddInt64(&q.lastGrantTime, 0-costDuration)
	return &ratelimiter.QuotaResult{
		Code: model.QuotaResultLimited,
		Info: info,
	}, nil
}

//返还token，这个限流器不用实现
func (q *quotaWindow) Release() {

}

//初始化并创建限流窗口
//主流程会在首次调用，以及规则对象变更的时候，调用该方法
func (g *RateLimiterUniformRate) InitQuota(criteria *ratelimiter.InitCriteria) (ratelimiter.QuotaBucket, error) {
	res := &quotaWindow{}
	res.rule = criteria.DstRule
	var instCount int
	instCount = 1
	effective := false
	effectiveRate := 0.0
	res.maxQueuingDuration = g.cfg.MaxQueuingTime.Nanoseconds()
	var maxDuration time.Duration
	for _, a := range res.rule.Amounts {
		if a.MaxAmount.GetValue() == 0 {
			res.rejectAll = true
			return res, nil
		}
		duration, err := pb.ConvertDuration(a.ValidDuration)
		if nil != err {
			return nil, model.NewSDKError(model.ErrCodeAPIInvalidArgument, err,
				"invalid rateLimit rule duration %v", a.ValidDuration)
		}
		//选出允许qps最低的amount和duration组合，作为effectiveAmount和effectiveDuration
		//在匀速排队限流器里面，就是每个请求都要间隔同样的时间，
		//如限制1s 10个请求，那么每个请求只有在上个请求允许过去100ms后才能通过下一个请求
		//这种机制下面，那么在多个amount组合里面，只要允许qps最低的组合生效，那么所有限制都满足了
		if !effective {
			res.effectiveAmount = a.MaxAmount.GetValue()
			res.effectiveDuration = duration
			maxDuration = duration
			effective = true
			effectiveRate = float64(res.effectiveDuration.Nanoseconds()) / float64(res.effectiveAmount)
		} else {
			newRate := float64(duration) / float64(a.MaxAmount.GetValue())
			if newRate > effectiveRate {
				res.effectiveAmount = a.MaxAmount.GetValue()
				res.effectiveDuration = duration
				effectiveRate = newRate
			}
			if duration > maxDuration {
				maxDuration = duration
			}
		}
	}
	effectiveRate = float64(res.effectiveDuration) / float64(res.effectiveAmount)
	res.totalRate = effectiveRate
	if res.rule.Type == namingpb.Rule_GLOBAL {
		effectiveRate *= float64(instCount)
	}
	res.effectiveRate = int64(math.Round(effectiveRate))
	return res, nil
}

//init 注册插件
func init() {
	plugin.RegisterConfigurablePlugin(&RateLimiterUniformRate{}, &Config{})
}
