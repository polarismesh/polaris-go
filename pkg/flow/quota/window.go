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
	"github.com/polarismesh/polaris-go/pkg/config"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/model/pb"
	rlimitV2 "github.com/polarismesh/polaris-go/pkg/model/pb/metric/v2"
	namingpb "github.com/polarismesh/polaris-go/pkg/model/pb/v1"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"github.com/polarismesh/polaris-go/pkg/plugin/ratelimiter"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/modern-go/reflect2"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

//限流分配窗口的缓存
type RateLimitWindowSet struct {
	//更新锁
	updateMutex sync.RWMutex
	//限流窗口列表，key为revision，value为WindowContainer
	windowByRule map[string]*WindowContainer
	//储存FlowQuotaAssistant
	flowAssistant *FlowQuotaAssistant
	//最近一次超时检查时间
	lastPurgeTimeMilli int64
}

//构造函数
func NewRateLimitWindowSet(assistant *FlowQuotaAssistant) *RateLimitWindowSet {
	return &RateLimitWindowSet{
		windowByRule:       make(map[string]*WindowContainer),
		flowAssistant:      assistant,
		lastPurgeTimeMilli: model.CurrentMillisecond(),
	}
}

//拷贝一份只读数据
func (rs *RateLimitWindowSet) GetRateLimitWindows() []*RateLimitWindow {
	rs.updateMutex.RLock()
	defer rs.updateMutex.RUnlock()
	result := make([]*RateLimitWindow, 0, len(rs.windowByRule))
	for _, container := range rs.windowByRule {
		result = append(result, container.GetRateLimitWindows()...)
	}
	return result
}

//获取限流窗口
func (rs *RateLimitWindowSet) GetRateLimitWindow(rule *namingpb.Rule, flatLabels string) *RateLimitWindow {
	//访问前进行一次窗口淘汰检查
	rs.PurgeWindows(model.CurrentMillisecond())
	rs.updateMutex.RLock()
	defer rs.updateMutex.RUnlock()
	container := rs.windowByRule[rule.GetRevision().GetValue()]
	if nil == container {
		return nil
	}
	if nil != container.MainWindow {
		return container.MainWindow
	}
	return container.WindowByLabel[flatLabels]
}

//执行窗口淘汰
func (rs *RateLimitWindowSet) PurgeWindows(nowMilli int64) {
	lastPurgeTimeMilli := atomic.LoadInt64(&rs.lastPurgeTimeMilli)
	if nowMilli-lastPurgeTimeMilli < rs.flowAssistant.purgeIntervalMilli {
		//未达到检查时间
		return
	}
	swapped := atomic.CompareAndSwapInt64(&rs.lastPurgeTimeMilli, lastPurgeTimeMilli, nowMilli)
	if !swapped {
		return
	}
	windows := rs.GetRateLimitWindows()
	for _, window := range windows {
		//超时触发删除操作
		if window.Expired(nowMilli) {
			rs.OnWindowExpired(nowMilli, window)
		}
	}
}

//规则是否还有正则表达式匹配逻辑
func HasRegex(rule *namingpb.Rule) bool {
	labels := rule.GetLabels()
	if len(labels) == 0 {
		return false
	}
	for _, matcher := range labels {
		if matcher.GetType() == namingpb.MatchString_REGEX {
			return true
		}
	}
	return false
}

//添加限流窗口
func (rs *RateLimitWindowSet) AddRateLimitWindow(
	commonRequest *data.CommonRateLimitRequest, rule *namingpb.Rule, flatLabels string) (*RateLimitWindow, error) {
	//判断是否正则扩散
	hasRegex := HasRegex(rule)
	rs.updateMutex.Lock()
	defer rs.updateMutex.Unlock()
	container := rs.windowByRule[rule.GetRevision().GetValue()]
	if nil == container {
		container = NewWindowContainer()
		rs.windowByRule[rule.GetRevision().GetValue()] = container
	}
	var window *RateLimitWindow
	if hasRegex {
		window = container.WindowByLabel[flatLabels]
	} else {
		window = container.MainWindow
	}
	if nil != window {
		return window, nil
	}
	var err error
	window, err = NewRateLimitWindow(rs, rule, commonRequest, flatLabels)
	if nil != err {
		return nil, err
	}
	if hasRegex {
		container.WindowByLabel[flatLabels] = window
	} else {
		container.MainWindow = window
	}
	rs.flowAssistant.AddWindowCount()
	return window, nil
}

//窗口过期
func (rs *RateLimitWindowSet) OnWindowExpired(nowMilli int64, window *RateLimitWindow) bool {
	rs.updateMutex.Lock()
	defer rs.updateMutex.Unlock()
	if !window.Expired(nowMilli) {
		return false
	}
	log.GetBaseLogger().Infof("[RateLimit]window expired, key=%s, nowMilli=%d, expireDuration=%d",
		window.uniqueKey, nowMilli, model.ToMilliSeconds(window.expireDuration))
	revision := window.Rule.GetRevision().GetValue()
	container := rs.windowByRule[revision]
	if nil != container {
		if container.MainWindow == window {
			delete(rs.windowByRule, revision)
		} else {
			delete(container.WindowByLabel, window.Labels)
		}
	}
	rs.deleteWindow(window)
	return true
}

//服务更新回调
func (rs *RateLimitWindowSet) OnServiceUpdated(svcEventObject *common.ServiceEventObject) {
	var updatedRules *common.RateLimitDiffInfo
	if svcEventObject.SvcEventKey.Type == model.EventRateLimiting {
		updatedRules = svcEventObject.DiffInfo.(*common.RateLimitDiffInfo)
	}
	if nil == updatedRules {
		return
	}
	rs.updateMutex.Lock()
	defer rs.updateMutex.Unlock()
	switch svcEventObject.SvcEventKey.Type {
	case model.EventRateLimiting:
		if len(updatedRules.DeletedRules) > 0 {
			for _, revision := range updatedRules.DeletedRules {
				rs.deleteContainer(revision)
			}
		}
		for _, revisionChange := range updatedRules.UpdatedRules {
			rs.deleteContainer(revisionChange.OldRevision)
		}
	}
}

//删除容器对象
func (rs *RateLimitWindowSet) deleteContainer(revision string) {
	container := rs.windowByRule[revision]
	delete(rs.windowByRule, revision)
	log.GetBaseLogger().Infof("[RateLimit]container %s has deleted", revision)
	if nil == container {
		return
	}
	if nil != container.MainWindow {
		rs.deleteWindow(container.MainWindow)
		log.GetBaseLogger().Infof(
			"[RateLimit]container main window %s has deleted", container.MainWindow.uniqueKey)
	}
	if len(container.WindowByLabel) == 0 {
		return
	}
	for _, window := range container.WindowByLabel {
		rs.deleteWindow(window)
		log.GetBaseLogger().Infof(
			"[RateLimit]container spread window %s has deleted", window.uniqueKey)
	}
}

//从RateLimitWindowSet中删除一个RateLimitWindow
func (rs *RateLimitWindowSet) deleteWindow(window *RateLimitWindow) {
	window.SetStatus(Deleted)
	rs.flowAssistant.DelWindowCount()
	//旧有的窗口被删除了，那么进行一次上报
	rs.flowAssistant.engine.SyncReportStat(model.RateLimitStat, &RateLimitGauge{
		EmptyInstanceGauge: model.EmptyInstanceGauge{},
		Window:             window,
		Type:               WindowDeleted,
	})
}

//窗口容器
type WindowContainer struct {
	//主窗口，非正则表达式的适用
	MainWindow *RateLimitWindow
	//适用于正则表达式展开的
	WindowByLabel map[string]*RateLimitWindow
}

//获取限流滑窗
func (w *WindowContainer) GetRateLimitWindows() []*RateLimitWindow {
	windows := make([]*RateLimitWindow, 0, len(w.WindowByLabel))
	if nil != w.MainWindow {
		windows = append(windows, w.MainWindow)
	} else if len(w.WindowByLabel) > 0 {
		for _, window := range w.WindowByLabel {
			windows = append(windows, window)
		}
	}
	return windows
}

//创建窗口容器
func NewWindowContainer() *WindowContainer {
	return &WindowContainer{
		WindowByLabel: make(map[string]*RateLimitWindow),
	}
}

const (
	//刚创建， 无需进行后台调度
	Created int64 = iota
	//已获取调度权，准备开始调度
	Initializing
	//已经在远程初始化结束
	Initialized
	//已经删除
	Deleted
)

// 远程同步相关参数
type RemoteSyncParam struct {
	// 连接相关参数
	model.ControlParam
}

// 配额使用信息
type UsageInfo struct {
	//配额使用时间
	CurTimeMilli int64
	//配额使用详情
	Passed map[int64]uint32
	//限流情况
	Limited map[int64]uint32
}

// 远程下发配额
type RemoteQuotaResult struct {
	Left            int64
	ClientCount     uint32
	ServerTimeMilli int64
	DurationMill    int64
}

// 远程配额分配的令牌桶
type RemoteAwareBucket interface {
	// 父接口，执行用户配额分配操作
	model.QuotaAllocator
	//设置通过限流服务端获取的远程配额
	SetRemoteQuota(*RemoteQuotaResult)
	// 获取已经分配的配额
	GetQuotaUsed(curTimeMilli int64) *UsageInfo
	//获取TokenBuckets
	GetTokenBuckets() TokenBuckets
	//更新时间间隔
	UpdateTimeDiff(timeDiff int64)
}

// 限流窗口
type RateLimitWindow struct {
	//配额窗口集合
	WindowSet *RateLimitWindowSet
	// 服务信息
	SvcKey model.ServiceKey
	// 正则对应的label
	Labels string
	//窗口的唯一标识，服务名+labels
	uniqueKey string
	//通过服务名+labels计算出来的hash值，用于选上报服务器
	hashValue uint64
	//最后一次获取限流配额时间
	lastQuotaAccessNano int64
	//最近一次拉取远程配额返回的时间,单位ms
	lastRecvTimeNano int64
	//最近一次发送acquire远程同步配额的时间, 单位ns
	lastSentTimeNano int64
	//最近一次获取配额时间
	lastAccessTimeMilli int64
	// 已经匹配到的限流规则，没有匹配则为空
	// 由于可能会出现规则并没有发生变化，但是缓存对象更新的情况，因此这里使用原子变量
	Rule *namingpb.Rule
	//其他插件在这里添加的相关数据，一般是统计插件使用
	PluginData map[int32]interface{}
	//淘汰周期，取最大统计周期+1s
	expireDuration time.Duration
	// 远程同步参数
	syncParam RemoteSyncParam
	// 流量整形算法桶
	trafficShapingBucket ratelimiter.QuotaBucket
	// 执行正式分配的令牌桶
	allocatingBucket RemoteAwareBucket
	// 限流插件
	rateLimiter ratelimiter.ServiceRateLimiter
	//初始化后指定的限流模式（本地或远程）
	configMode model.ConfigMode
	//远程同步的集群，本地限流无此配置
	remoteCluster model.ServiceKey
	// 窗口状态
	status int64
}

//超过多长时间后进行淘汰，淘汰后需要重新init
var (
	// 淘汰因子，过期时间=MaxDuration + ExpireFactor
	ExpireFactor = 1 * time.Second

	DefaultStatisticReportPeriod = 1 * time.Second
)

//计算淘汰周期
func getExpireDuration(rule *namingpb.Rule) time.Duration {
	return getMaxDuration(rule) + ExpireFactor
}

//获取最大的限流周期
func getMaxDuration(rule *namingpb.Rule) time.Duration {
	var maxDuration time.Duration
	for _, amount := range rule.GetAmounts() {
		pbDuration := amount.GetValidDuration()
		duration, _ := pb.ConvertDuration(pbDuration)
		if duration > maxDuration {
			maxDuration = duration
		}
	}
	return maxDuration
}

// 创建限流窗口
func NewRateLimitWindow(windowSet *RateLimitWindowSet, rule *namingpb.Rule,
	commonRequest *data.CommonRateLimitRequest, labels string) (*RateLimitWindow, error) {
	window := &RateLimitWindow{}
	window.WindowSet = windowSet
	window.SvcKey.Service = rule.GetService().GetValue()
	window.SvcKey.Namespace = rule.GetNamespace().GetValue()
	window.Labels = labels
	window.uniqueKey, window.hashValue = window.buildQuotaHashValue()
	window.Rule = rule
	window.expireDuration = getExpireDuration(rule)

	window.syncParam.ControlParam = commonRequest.ControlParam

	window.rateLimiter = createBehavior(windowSet.flowAssistant.supplier, rule.GetAction().GetValue())
	//初始化流量整形窗口
	var err error
	criteria := &commonRequest.Criteria
	window.trafficShapingBucket, err = window.rateLimiter.InitQuota(criteria)
	if nil != err {
		return nil, err
	}
	window.allocatingBucket = NewRemoteAwareQpsBucket(window)

	window.status = Created
	window.lastQuotaAccessNano = time.Now().UnixNano()

	window.PluginData = make(map[int32]interface{})
	window.buildRemoteConfigMode(windowSet, rule)

	//创建对应
	handlers := windowSet.flowAssistant.supplier.GetEventSubscribers(common.OnRateLimitWindowCreated)
	if len(handlers) > 0 {
		eventObj := &common.PluginEvent{
			EventType:   common.OnRateLimitWindowCreated,
			EventObject: window,
		}
		for _, h := range handlers {
			h.Callback(eventObj)
		}
	}
	return window, nil
}

//构建限流模式及集群
func (r *RateLimitWindow) buildRemoteConfigMode(windowSet *RateLimitWindowSet, rule *namingpb.Rule) {
	//解析限流集群配置
	if rule.GetType() == namingpb.Rule_LOCAL {
		r.configMode = model.ConfigQuotaLocalMode
		return
	}
	r.remoteCluster.Namespace = rule.GetCluster().GetNamespace().GetValue()
	r.remoteCluster.Service = rule.GetCluster().GetService().GetValue()
	if len(r.remoteCluster.Namespace) == 0 || len(r.remoteCluster.Service) == 0 {
		if !reflect2.IsNil(windowSet.flowAssistant.remoteClusterByConfig) {
			r.remoteCluster.Namespace = windowSet.flowAssistant.remoteClusterByConfig.GetNamespace()
			r.remoteCluster.Service = windowSet.flowAssistant.remoteClusterByConfig.GetService()
		}
	}
	if len(r.remoteCluster.Namespace) == 0 || len(r.remoteCluster.Service) == 0 {
		r.configMode = model.ConfigQuotaLocalMode
	} else {
		r.configMode = model.ConfigQuotaGlobalMode
	}
}

//构建限流窗口的索引值
func (r *RateLimitWindow) buildQuotaHashValue() (string, uint64) {
	//<服务名>#<命名空间>#<labels if exists>
	builder := &strings.Builder{}
	builder.WriteString(r.SvcKey.Service)
	builder.WriteString(config.DefaultNamesSeparator)
	builder.WriteString(r.SvcKey.Namespace)
	if len(r.Labels) > 0 {
		builder.WriteString(config.DefaultNamesSeparator)
		builder.WriteString(r.Labels)
	}
	uniqueKey := builder.String()
	value, _ := model.HashStr(uniqueKey)
	return uniqueKey, value
}

//根据限流行为名获取限流算法插件
func createBehavior(supplier plugin.Supplier, behaviorName string) ratelimiter.ServiceRateLimiter {
	//因为构造缓存时候已经校验过，所以这里可以直接忽略错误
	plug, _ := supplier.GetPlugin(common.TypeRateLimiter, behaviorName)
	return plug.(ratelimiter.ServiceRateLimiter)
}

//校验输入的元数据是否符合规则
func matchLabels(ruleMetaKey string, ruleMetaValue *namingpb.MatchString,
	labels map[string]string, ruleCache model.RuleCache) bool {
	if len(labels) == 0 {
		return false
	}
	var value string
	var ok bool
	if value, ok = labels[ruleMetaKey]; !ok {
		//集成的路由规则不包含这个key，就不匹配
		return false
	}
	ruleMetaValueStr := ruleMetaValue.GetValue().GetValue()
	switch ruleMetaValue.Type {
	case namingpb.MatchString_REGEX:
		regexObj := ruleCache.GetRegexMatcher(ruleMetaValueStr)
		if !regexObj.MatchString(value) {
			return false
		}
		return true
	default:
		return value == ruleMetaValueStr
	}
}

//上下文的键类型
type contextKey struct {
	name string
}

//ToString方法
func (k *contextKey) String() string { return "rateLimit context value " + k.name }

//key，用于共享错误信息
var errKey = &contextKey{name: "ctxError"}

//错误容器，用于传递上下文错误信息
type errContainer struct {
	err atomic.Value
}

// 初始化限流窗口
func (r *RateLimitWindow) Init() {
	if !r.CasStatus(Created, Initializing) {
		//确保初始化一次
		return
	}
	if r.configMode == model.ConfigQuotaLocalMode {
		//本地限流，则直接可用
		r.SetStatus(Initialized)
		return
	}
	//加入轮询队列，走异步调度
	r.WindowSet.flowAssistant.taskValues.AddValue(r.uniqueKey, r)
}

func (r *RateLimitWindow) buildInitTargetStr() string {
	target := rlimitV2.LimitTarget{
		Namespace: r.SvcKey.Namespace,
		Service:   r.SvcKey.Service,
		Labels:    r.Labels,
	}
	return target.String()
}

//获取SDK引擎
func (r *RateLimitWindow) Engine() model.Engine {
	return r.WindowSet.flowAssistant.engine
}

//获取异步连接器
func (r *RateLimitWindow) AsyncRateLimitConnector() serverconnector.AsyncRateLimitConnector {
	return r.WindowSet.flowAssistant.asyncRateLimitConnector
}

//转换成限流PB初始化消息
func (r *RateLimitWindow) InitializeRequest() *rlimitV2.RateLimitInitRequest {
	clientId := r.Engine().GetContext().GetClientId()
	initReq := &rlimitV2.RateLimitInitRequest{}
	initReq.ClientId = clientId
	initReq.Target = &rlimitV2.LimitTarget{}
	initReq.Target.Namespace = r.SvcKey.Namespace
	initReq.Target.Service = r.SvcKey.Service
	initReq.Target.Labels = r.Labels

	quotaMode := rlimitV2.QuotaMode(r.Rule.GetAmountMode())
	tokenBuckets := r.allocatingBucket.GetTokenBuckets()
	for _, tokenBucket := range tokenBuckets {
		quotaTotal := &rlimitV2.QuotaTotal{
			Mode:      quotaMode,
			Duration:  tokenBucket.validDurationSecond,
			MaxAmount: tokenBucket.ruleTokenAmount,
		}
		initReq.Totals = append(initReq.Totals, quotaTotal)
	}
	return initReq
}

// 远程访问的错误信息
type RemoteErrorContainer struct {
	sdkErr atomic.Value
}

//比较两个窗口是否相同
func (r *RateLimitWindow) CompareTo(another interface{}) int {
	return strings.Compare(r.uniqueKey, another.(*RateLimitWindow).uniqueKey)
}

//删除前进行检查，返回true才删除，该检查是同步操作
func (r *RateLimitWindow) EnsureDeleted(value interface{}) bool {
	//只有过期才删除
	return r.GetStatus() == Deleted
}

//转换成限流PB上报消息
func (r *RateLimitWindow) acquireRequest() *rlimitV2.ClientRateLimitReportRequest {
	reportReq := &rlimitV2.ClientRateLimitReportRequest{
		Service:   r.SvcKey.Service,
		Namespace: r.SvcKey.Namespace,
		Labels:    r.Labels,
		QuotaUsed: make(map[time.Duration]*rlimitV2.QuotaSum),
	}
	curTimeMilli := model.CurrentMillisecond()
	usageInfo := r.allocatingBucket.GetQuotaUsed(curTimeMilli)
	reportReq.Timestamp = usageInfo.CurTimeMilli
	for durationMilli, passed := range usageInfo.Passed {
		reportReq.QuotaUsed[time.Duration(durationMilli)*time.Millisecond] = &rlimitV2.QuotaSum{
			Used:    passed,
			Limited: usageInfo.Limited[durationMilli],
		}
	}
	return reportReq
}

// 原子获取状态
func (r *RateLimitWindow) GetStatus() int64 {
	return atomic.LoadInt64(&r.status)
}

// 设置状态
func (r *RateLimitWindow) SetStatus(status int64) {
	atomic.StoreInt64(&r.status, status)
}

// CAS设置状态
func (r *RateLimitWindow) CasStatus(oldStatus int64, status int64) bool {
	return atomic.CompareAndSwapInt64(&r.status, oldStatus, status)
}

// 分配配额
func (r *RateLimitWindow) AllocateQuota() (*model.QuotaFutureImpl, error) {
	nowMilli := model.CurrentMillisecond()
	atomic.StoreInt64(&r.lastAccessTimeMilli, nowMilli)
	shapingResult, err := r.trafficShapingBucket.GetQuota()
	if nil != err {
		return nil, err
	}
	deadline := time.Unix(0, nowMilli*1e6)
	if shapingResult.Code == model.QuotaResultLimited {
		//如果结果是拒绝了分配，那么进行一次上报
		//TODO：检查是否上报了充足信息
		mode := r.Rule.GetType()
		var limitType LimitMode
		if mode == namingpb.Rule_GLOBAL {
			limitType = LimitGlobalMode
		} else {
			limitType = LimitLocalMode
		}
		gauge := &RateLimitGauge{
			EmptyInstanceGauge: model.EmptyInstanceGauge{},
			Window:             r,
			Type:               TrafficShapingLimited,
			LimitModeType:      limitType,
		}
		r.Engine().SyncReportStat(model.RateLimitStat, gauge)

		resp := &model.QuotaResponse{
			Code: model.QuotaResultLimited,
			Info: shapingResult.Info,
		}
		return model.NewQuotaFuture(resp, deadline, nil), nil
	}
	if shapingResult.QueueTime > 0 {
		deadlineMilli := nowMilli + model.ToMilliSeconds(shapingResult.QueueTime)
		deadline = time.Unix(0, deadlineMilli*1e6)
	}
	return model.NewQuotaFuture(nil, deadline, r.allocatingBucket), nil
}

//获取最近访问时间
func (r *RateLimitWindow) GetLastAccessTimeMilli() int64 {
	return atomic.LoadInt64(&r.lastAccessTimeMilli)
}

//是否已经过期
func (r *RateLimitWindow) Expired(nowMilli int64) bool {
	return nowMilli-r.GetLastAccessTimeMilli() > model.ToMilliSeconds(r.expireDuration)
}
