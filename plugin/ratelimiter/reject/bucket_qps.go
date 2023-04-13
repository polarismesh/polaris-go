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

package reject

import (
	"math"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modern-go/reflect2"
	apitraffic "github.com/polarismesh/specification/source/go/api/v1/traffic_manage"

	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"github.com/polarismesh/polaris-go/plugin/ratelimiter/common"
)

// NewRemoteAwareQpsBucket 创建QPS远程限流窗口
func NewRemoteAwareQpsBucket(criteria *ratelimiter.InitCriteria) *RemoteAwareQpsBucket {
	raqb := &RemoteAwareQpsBucket{
		uniqueKey:      criteria.WindowKey,
		identifierPool: &sync.Pool{},
	}
	raqb.tokenBuckets = initTokenBuckets(criteria.DstRule, criteria.WindowKey)
	raqb.tokenBucketMap = make(map[int64]*TokenBucket, len(raqb.tokenBuckets))
	for _, tokenBucket := range raqb.tokenBuckets {
		raqb.tokenBucketMap[tokenBucket.validDurationMilli] = tokenBucket
	}
	return raqb
}

// RemoteAwareQpsBucket 远程配额分配的算法桶
type RemoteAwareQpsBucket struct {
	// 唯一的窗口标识
	uniqueKey string
	// 令牌桶数组，时间从小到大排列
	tokenBuckets TokenBuckets
	// 令牌桶map，用于索引
	tokenBucketMap map[int64]*TokenBucket
	// 存放[]UpdateIdentifier数据
	identifierPool *sync.Pool
}

const (
	// 单次分配的token数量
	tokenPerAlloc = 1
)

// poolGetIdentifier 从池子里获取标识数组
func (r *RemoteAwareQpsBucket) poolGetIdentifier() []UpdateIdentifier {
	value := r.identifierPool.Get()
	if !reflect2.IsNil(value) {
		return value.([]UpdateIdentifier)
	}
	result := make([]UpdateIdentifier, len(r.tokenBuckets))
	return result
}

type TokenBucketMode int

const (
	Unknown TokenBucketMode = iota
	Remote
	RemoteToLocal
	Local
)

// Allocate 执行配额分配操作
func (r *RemoteAwareQpsBucket) Allocate(curTimeMs int64, token uint32) *model.QuotaResponse {
	if len(r.tokenBuckets) == 0 {
		return &model.QuotaResponse{
			Code: model.QuotaResultOk,
			Info: "rule has no amount config",
		}
	}
	var stopIndex = -1
	var mode = Unknown
	identifiers := r.poolGetIdentifier()
	defer r.identifierPool.Put(identifiers)
	// 先尝试扣除
	var left int64
	for i, tokenBucket := range r.tokenBuckets {
		left, mode = tokenBucket.TryAllocateToken(tokenPerAlloc, curTimeMs, &identifiers[i], mode)
		if left < 0 {
			stopIndex = i
			break
		}
	}
	usedRemoteQuota := mode == Remote
	// 有一个扣除不成功，则进行限流
	if stopIndex >= 0 {
		// 出现了限流
		tokenBucket := r.tokenBuckets[stopIndex]
		if usedRemoteQuota {
			// 远程才记录滑窗, 滑窗用于上报
			tokenBucket.ConfirmLimited(token, curTimeMs)
		}
		// 归还配额
		for i := 0; i < stopIndex; i++ {
			tokenBucket := r.tokenBuckets[i]
			tokenBucket.GiveBackToken(&identifiers[i], tokenPerAlloc, mode)
		}
		return &model.QuotaResponse{
			Code: model.QuotaResultLimited,
		}
	}
	// 记录分配的配额
	for _, tokenBucket := range r.tokenBuckets {
		if usedRemoteQuota {
			tokenBucket.ConfirmPassed(token, curTimeMs)
		}
	}
	return &model.QuotaResponse{
		Code: model.QuotaResultOk,
	}
}

// Release 执行配额回收操作
func (r *RemoteAwareQpsBucket) Release() {
	// 对于QPS限流，无需进行释放
}

// SetRemoteQuota 设置通过限流服务端获取的远程QPS
func (r *RemoteAwareQpsBucket) SetRemoteQuota(remoteQuotas ratelimiter.RemoteQuotaResult) {
	clientTime := model.CurrentMillisecond()
	durationMilli := remoteQuotas.DurationMill
	curStartTimeMilli := common.CalculateStartTimeMilli(remoteQuotas.ClientTimeMilli, durationMilli)
	remoteStartTimeMilli := common.CalculateStartTimeMilli(remoteQuotas.ServerTimeMilli, durationMilli)
	tokenBucket := r.tokenBucketMap[remoteQuotas.DurationMill]
	if nil == tokenBucket {
		return
	}
	var updateClient = true
	if curStartTimeMilli != remoteStartTimeMilli {
		updateClient = false
		remoteLeft := remoteQuotas.Left
		if remoteStartTimeMilli+durationMilli == curStartTimeMilli {
			// 仅仅相差一个周期，可以认为是周期间切换导致，这时候可以直接更新配额为全量配额
			tokenBucket.UpdateRemoteClientCount(remoteQuotas)
			// 当前周期没有更新，则重置当前周期配额，避免出现时间周期开始时候的误限
			remoteQuotas.ServerTimeMilli = curStartTimeMilli
			remoteQuotas.Left = tokenBucket.GetRuleTotal()
			log.GetBaseLogger().Warnf("[RateLimit]reset remote quota, clientTime %d, "+
				"curTimeMilli %d(startMilli %d), remoteTimeMilli %d(startMilli %d), interval %d, remoteLeft is %d, "+
				"reset to %d", clientTime, remoteQuotas.ClientTimeMilli, curStartTimeMilli, remoteQuotas.ServerTimeMilli,
				remoteStartTimeMilli, durationMilli, remoteLeft, remoteQuotas.Left)
		} else {
			tokenBucket.UpdateRemoteClientCount(remoteQuotas)
			// 不在一个时间段内，丢弃
			log.GetBaseLogger().Warnf("[RateLimit]Drop remote quota, clientTime %d, "+
				"curTimeMilli %d(startMilli %d), remoteTimeMilli %d(startMilli %d), interval %d, remoteLeft %d",
				clientTime, remoteQuotas.ClientTimeMilli, curStartTimeMilli, remoteQuotas.ServerTimeMilli, remoteStartTimeMilli,
				durationMilli, remoteLeft)
			return
		}
	}
	tokenBucket.UpdateRemoteToken(remoteQuotas, updateClient)
}

func (r *RemoteAwareQpsBucket) GetQuotaUsed(serverTimeMilli int64) ratelimiter.UsageInfo {
	result := &ratelimiter.UsageInfo{
		Passed:       make(map[int64]uint32, len(r.tokenBuckets)),
		Limited:      make(map[int64]uint32, len(r.tokenBuckets)),
		CurTimeMilli: serverTimeMilli,
	}
	for _, tokenBucket := range r.tokenBucketMap {
		passed, limited, _ := tokenBucket.sliceWindow.AcquireCurrentValues(serverTimeMilli)
		result.Passed[tokenBucket.validDurationMilli] = passed
		result.Limited[tokenBucket.validDurationMilli] = limited
	}
	return *result
}

func (r *RemoteAwareQpsBucket) GetTokenBuckets() TokenBuckets {
	return r.tokenBuckets
}

// 多久没同步，则变成本地
const remoteExpireMilli = 1000

// BucketShareInfo 通用信息
type BucketShareInfo struct {
	// 是否单机均摊
	shareEqual bool
	// 是否本地配额
	local bool
	// 远程实效是否放通
	passOnRemoteFail bool
}

// UpdateIdentifier 令牌桶是否进行更新的凭证
type UpdateIdentifier struct {
	// 当前周期起始时间，本地限流有效
	stageStartMilli int64
	// 最近一次只更新远程客户端数量的时间点
	lastRemoteClientUpdateMilli int64
	// 最近一次远程完全更新时间点
	lastRemoteUpdateMilli int64
}

// TokenBucket 令牌桶
type TokenBucket struct {
	UpdateIdentifier
	windowKey string
	// 限流区间 单位毫秒
	validDurationMilli int64
	// 限流区间 单位秒
	validDurationSecond uint32
	// 规则中定义的变量
	ruleTokenAmount uint32
	// 每周期分配的配额总量
	tokenLeft int64
	// 远程降级到本地的剩余配额数
	remoteToLocalTokenLeft int64
	// 实例数，通过远程更新
	instanceCount uint32
	// 本地与远程更新并发控制
	mutex *sync.RWMutex
	// 统计滑窗
	sliceWindow *common.SlidingWindow
	// 共享的规则数据
	shareInfo *BucketShareInfo
}

// NewTokenBucket 创建令牌桶
func NewTokenBucket(
	windowKey string, validDuration time.Duration, tokenAmount uint32, shareInfo *BucketShareInfo) *TokenBucket {
	bucket := &TokenBucket{}
	bucket.windowKey = windowKey
	bucket.mutex = &sync.RWMutex{}
	bucket.validDurationMilli = model.ToMilliSeconds(validDuration)
	bucket.validDurationSecond = uint32(bucket.validDurationMilli / 1e3)
	bucket.ruleTokenAmount = tokenAmount
	bucket.tokenLeft = int64(tokenAmount)
	bucket.sliceWindow = common.NewSlidingWindow(1, int(bucket.validDurationMilli))
	bucket.shareInfo = shareInfo
	bucket.instanceCount = 1
	return bucket
}

// GetRuleTotal 获取限流总量
func (t *TokenBucket) GetRuleTotal() int64 {
	if !t.shareInfo.shareEqual || t.shareInfo.local {
		return int64(t.ruleTokenAmount)
	}
	instanceCount := atomic.LoadUint32(&t.instanceCount)
	return int64(t.ruleTokenAmount) * int64(instanceCount)
}

// GiveBackToken 归还配额
func (t *TokenBucket) GiveBackToken(identifier *UpdateIdentifier, token int64, mode TokenBucketMode) {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	// 相同则归还，否则忽略
	switch mode {
	case Remote:
		if atomic.LoadInt64(&t.lastRemoteUpdateMilli) == identifier.lastRemoteUpdateMilli {
			atomic.AddInt64(&t.tokenLeft, token)
		}
	case Local:
		if atomic.LoadInt64(&t.stageStartMilli) == identifier.stageStartMilli {
			atomic.AddInt64(&t.tokenLeft, token)
		}
	case RemoteToLocal:
		if atomic.LoadInt64(&t.stageStartMilli) == identifier.stageStartMilli {
			atomic.AddInt64(&t.remoteToLocalTokenLeft, token)
		}
	}
}

// UpdateRemoteClientCount 只更新远程客户端数量，不更新配额
func (t *TokenBucket) UpdateRemoteClientCount(remoteQuotas ratelimiter.RemoteQuotaResult) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	t.updateRemoteClientCount(remoteQuotas)
}

// updateRemoteClientCount 纯更新客户端数
func (t *TokenBucket) updateRemoteClientCount(remoteQuotas ratelimiter.RemoteQuotaResult) {
	lastRemoteClientUpdateMilli := atomic.LoadInt64(&t.lastRemoteClientUpdateMilli)
	if lastRemoteClientUpdateMilli < remoteQuotas.ServerTimeMilli {
		var lastClientCount uint32
		var curClientCount uint32
		if remoteQuotas.ClientCount == 0 {
			curClientCount = 1
		} else {
			curClientCount = remoteQuotas.ClientCount
		}
		lastClientCount = atomic.SwapUint32(&t.instanceCount, curClientCount)
		if lastClientCount != curClientCount {
			log.GetBaseLogger().Infof("[RateLimit]clientCount change from %d to %d, windowKey %s\n",
				lastClientCount, curClientCount, t.windowKey)
		}
		atomic.StoreInt64(&t.lastRemoteClientUpdateMilli, remoteQuotas.ServerTimeMilli)
	}
}

// UpdateRemoteToken 更新远程配额
func (t *TokenBucket) UpdateRemoteToken(remoteQuotas ratelimiter.RemoteQuotaResult, updateClient bool) {
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if updateClient {
		t.updateRemoteClientCount(remoteQuotas)
	}
	used, _ := t.sliceWindow.TouchCurrentPassed(remoteQuotas.ServerTimeMilli)
	quotaToUpdate := remoteQuotas.Left - int64(used)
	// 需要减去在上报期间使用的配额数
	atomic.StoreInt64(&t.tokenLeft, quotaToUpdate)
	atomic.StoreInt64(&t.lastRemoteUpdateMilli, remoteQuotas.ServerTimeMilli)
}

// remoteExpired 远程配额过期
func (t *TokenBucket) remoteExpired(nowMilli int64) bool {
	return nowMilli-atomic.LoadInt64(&t.lastRemoteUpdateMilli) > remoteExpireMilli
}

// initLocalStageOnLocalConfig 初始化本地配额
func (t *TokenBucket) initLocalStageOnLocalConfig(nowMilli int64) {
	nowStageMilli := t.calculateStageStart(nowMilli)
	if atomic.LoadInt64(&t.stageStartMilli) == nowStageMilli {
		return
	}
	t.mutex.Lock()
	defer t.mutex.Unlock()
	if !t.remoteExpired(nowMilli) {
		return
	}
	if atomic.LoadInt64(&t.stageStartMilli) == nowStageMilli {
		return
	}
	atomic.StoreInt64(&t.tokenLeft, int64(t.ruleTokenAmount))
	atomic.StoreInt64(&t.stageStartMilli, nowStageMilli)
}

// calculateStageStart 计算起始滑窗
func (t *TokenBucket) calculateStageStart(curTimeMs int64) int64 {
	return curTimeMs - curTimeMs%t.validDurationMilli
}

// tryAllocateLocal 本地分配
func (t *TokenBucket) tryAllocateLocal(
	token uint32, nowMilli int64, identifier *UpdateIdentifier) (int64, TokenBucketMode) {
	t.initLocalStageOnLocalConfig(nowMilli)
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	identifier.stageStartMilli = atomic.LoadInt64(&t.stageStartMilli)
	identifier.lastRemoteUpdateMilli = atomic.LoadInt64(&t.lastRemoteUpdateMilli)
	return atomic.AddInt64(&t.tokenLeft, 0-int64(token)), Local
}

// directAllocateRemoteToken 直接分配远程配额
func (t *TokenBucket) directAllocateRemoteToken(token uint32) int64 {
	return atomic.AddInt64(&t.tokenLeft, 0-int64(token))
}

// allocateRemoteReadOnly 尝试只读方式分配远程配额
func (t *TokenBucket) allocateRemoteReadOnly(
	token uint32, nowMilli int64, identifier *UpdateIdentifier) (bool, int64, TokenBucketMode) {
	t.mutex.RLock()
	defer t.mutex.RUnlock()
	// 远程配额，未过期
	if !t.remoteExpired(nowMilli) {
		return true, t.directAllocateRemoteToken(token), Remote
	}
	// 远程配额过期，配置了直接放通
	if t.shareInfo.passOnRemoteFail {
		return true, 0, RemoteToLocal
	}
	stageStartMilli := atomic.LoadInt64(&t.stageStartMilli)
	if stageStartMilli == t.calculateStageStart(nowMilli) {
		identifier.stageStartMilli = stageStartMilli
		identifier.lastRemoteUpdateMilli = atomic.LoadInt64(&t.lastRemoteUpdateMilli)
		return true, atomic.AddInt64(&t.remoteToLocalTokenLeft, 0-int64(token)), RemoteToLocal
	}
	return false, 0, RemoteToLocal
}

// allocateRemoteToLocal 以本地退化远程模式来进行分配
func (t *TokenBucket) allocateRemoteToLocal(token uint32, nowMilli int64, identifier *UpdateIdentifier) int64 {
	// 远程配额过期，配置了直接放通
	if t.shareInfo.passOnRemoteFail {
		return 0
	}
	stageStartMilli := atomic.LoadInt64(&t.stageStartMilli)
	allocReadOnly := func() (bool, int64) {
		t.mutex.RLock()
		defer t.mutex.RUnlock()
		if stageStartMilli == t.calculateStageStart(nowMilli) {
			identifier.stageStartMilli = stageStartMilli
			identifier.lastRemoteUpdateMilli = atomic.LoadInt64(&t.lastRemoteUpdateMilli)
			return true, atomic.AddInt64(&t.remoteToLocalTokenLeft, 0-int64(token))
		}
		return false, 0
	}
	success, left := allocReadOnly()
	if success {
		return left
	}
	// 重新构建窗口
	t.mutex.Lock()
	defer t.mutex.Unlock()
	return t.createRemoteToLocalTokens(nowMilli, token, identifier, stageStartMilli)
}

// createRemoteToLocalTokens 创建远程降级的token池
func (t *TokenBucket) createRemoteToLocalTokens(
	nowMilli int64, token uint32, identifier *UpdateIdentifier, stageStartMilli int64) int64 {
	nowStageMilli := t.calculateStageStart(nowMilli)
	if stageStartMilli == nowStageMilli {
		identifier.stageStartMilli = stageStartMilli
		return atomic.AddInt64(&t.remoteToLocalTokenLeft, 0-int64(token))
	}
	tokenPerInst := math.Ceil(float64(t.GetRuleTotal()) / float64(t.instanceCount))
	if tokenPerInst == 0 {
		tokenPerInst = 1
	}
	atomic.StoreInt64(&t.remoteToLocalTokenLeft, int64(tokenPerInst))
	atomic.StoreInt64(&t.stageStartMilli, nowStageMilli)
	identifier.stageStartMilli = nowStageMilli
	return atomic.AddInt64(&t.remoteToLocalTokenLeft, 0-int64(token))
}

// tryAllocateRemote 本地分配
func (t *TokenBucket) tryAllocateRemote(
	token uint32, nowMilli int64, identifier *UpdateIdentifier) (int64, TokenBucketMode) {
	ok, left, isRemote := t.allocateRemoteReadOnly(token, nowMilli, identifier)
	if ok {
		return left, isRemote
	}
	// 重新构建窗口
	t.mutex.Lock()
	defer t.mutex.Unlock()
	stageStartMilli := atomic.LoadInt64(&t.stageStartMilli)
	identifier.lastRemoteUpdateMilli = atomic.LoadInt64(&t.lastRemoteUpdateMilli)
	if !t.remoteExpired(nowMilli) {
		identifier.stageStartMilli = stageStartMilli
		return atomic.AddInt64(&t.tokenLeft, 0-int64(token)), Remote
	}
	return t.createRemoteToLocalTokens(nowMilli, token, identifier, stageStartMilli), RemoteToLocal
}

// TryAllocateToken 尝试分配配额
func (t *TokenBucket) TryAllocateToken(
	token uint32, nowMilli int64, identifier *UpdateIdentifier, mode TokenBucketMode) (int64, TokenBucketMode) {
	switch mode {
	case Local:
		return t.tryAllocateLocal(token, nowMilli, identifier)
	case Remote:
		return t.directAllocateRemoteToken(token), Remote
	case RemoteToLocal:
		return t.allocateRemoteToLocal(token, nowMilli, identifier), RemoteToLocal
	}
	// 自适应计算
	if t.shareInfo.local {
		return t.tryAllocateLocal(token, nowMilli, identifier)
	}
	return t.tryAllocateRemote(token, nowMilli, identifier)
}

// ConfirmPassed 记录真实分配配额
func (t *TokenBucket) ConfirmPassed(passed uint32, nowMilli int64) {
	t.sliceWindow.AddAndGetCurrentPassed(nowMilli, passed)
}

// ConfirmLimited 记录限流分配配额
func (t *TokenBucket) ConfirmLimited(limited uint32, nowMilli int64) {
	t.sliceWindow.AddAndGetCurrentLimited(nowMilli, limited)
}

// TokenBuckets 令牌桶序列
type TokenBuckets []*TokenBucket

// Len 数组长度
func (tbs TokenBuckets) Len() int {
	return len(tbs)
}

// Less 比较数组成员大小
func (tbs TokenBuckets) Less(i, j int) bool {
	// 逆序
	return tbs[i].validDurationMilli > tbs[j].validDurationMilli
}

// Swap 交换数组成员
func (tbs TokenBuckets) Swap(i, j int) {
	tbs[i], tbs[j] = tbs[j], tbs[i]
}

// initTokenBuckets 初始化令牌桶
func initTokenBuckets(rule *apitraffic.Rule, windowKey string) TokenBuckets {
	shareInfo := &BucketShareInfo{}
	if rule.GetAmountMode() == apitraffic.Rule_SHARE_EQUALLY {
		shareInfo.shareEqual = true
	}
	if rule.GetType() == apitraffic.Rule_LOCAL {
		shareInfo.local = true
	}
	if rule.GetFailover() == apitraffic.Rule_FAILOVER_PASS {
		shareInfo.passOnRemoteFail = true
	}
	amounts := rule.GetAmounts()
	buckets := make(TokenBuckets, 0, len(amounts))
	for _, amount := range amounts {
		goDuration, _ := pb.ConvertDuration(amount.GetValidDuration())
		bucket := NewTokenBucket(windowKey, goDuration, amount.GetMaxAmount().GetValue(), shareInfo)
		buckets = append(buckets, bucket)
	}
	if len(buckets) > 1 {
		sort.Sort(buckets)
	}
	return buckets
}
