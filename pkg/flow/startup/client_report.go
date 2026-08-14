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

// Package startup 提供 SDK 启动阶段的周期性任务回调，包括客户端状态上报、SDK 配置上报、
// 服务端服务同步以及客户端事件监听（WatchClientEvents）等。
package startup

import (
	"encoding/json"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/golang/protobuf/proto"
	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"

	"github.com/polarismesh/polaris-go/pkg/config"
	configflow "github.com/polarismesh/polaris-go/pkg/flow/configuration"
	"github.com/polarismesh/polaris-go/pkg/flow/data"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin"
	"github.com/polarismesh/polaris-go/pkg/plugin/localregistry"
	statreporter "github.com/polarismesh/polaris-go/pkg/plugin/metrics"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
	"github.com/polarismesh/polaris-go/pkg/sdk"
	"github.com/polarismesh/polaris-go/pkg/version"
)

// NewReportClientCallBack 创建上报回调。
// configFlow 为配置文件流，非 nil 时用于收集监听配置文件元数据上报；配置中心未启用时传 nil。
func NewReportClientCallBack(
	cfg config.Configuration, supplier plugin.Supplier, globalCtx sdk.ValueContext,
	configFlow *configflow.ConfigFileFlow) (*ReportClientCallBack, error) {
	var err error
	var callback = &ReportClientCallBack{}
	if callback.connector, err = data.GetServerConnector(cfg, supplier); err != nil {
		return nil, err
	}
	if callback.registry, err = data.GetRegistry(cfg, supplier); err != nil {
		return nil, err
	}
	if callback.reporterChain, err = data.GetStatReporterChain(cfg, supplier); err != nil {
		return nil, err
	}
	callback.configuration = cfg
	callback.globalCtx = globalCtx
	callback.configFlow = configFlow
	callback.interval = cfg.GetGlobal().GetAPI().GetReportInterval()
	callback.logCtx = globalCtx.GetContextLogger()
	callback.loadLocalClientReportResult()
	return callback, nil
}

// ReportClientCallBack 上报客户端状态任务回调
type ReportClientCallBack struct {
	connector     serverconnector.ServerConnector
	registry      localregistry.InstancesRegistry
	configuration config.Configuration
	globalCtx     sdk.ValueContext
	// configFlow 配置文件流，配置中心未启用时为 nil
	configFlow    *configflow.ConfigFileFlow
	interval      time.Duration
	reporterChain []statreporter.StatReporter
	logCtx        *log.ContextLogger
	// persistMu 保护 lastLocation 与 lastConfigMetadata 的并发读写。
	// persistHandlerWithLocationCheck 会被两条协程路径触发：定时任务 Process 与 TriggerNow 的
	// doReportNow 都同步调 connector.ReportClient，后者内部回调 PersistHandler；grpc 连接器每次
	// 独立取连接、不在调用间串行化，故这两个字段的读改写必须自行加锁。
	persistMu sync.Mutex
	// lastLocation 记录上次成功持久化的地域信息，用于对比判断是否需要重新写入 client_info.json
	lastLocation *model.Location
	// lastConfigMetadata 记录上次成功持久化的配置订阅元数据（ReportClient 响应中的 config_metadata），
	// 用于对比判断订阅列表是否变化、是否需要重新写入 client_info.json。
	// 订阅列表变化（新增/移除配置文件监听）后即使地域不变也需刷新文件，否则 config_watch 停留在过期快照。
	lastConfigMetadata string
	// reportPending 标记是否已有待执行的即时上报（debounce 合并用），0=无 1=有
	reportPending int32
}

const (
	// reportDebounceWindow 即时上报的合并窗口。
	// 批量订阅配置文件时，窗口内的多次 TriggerNow 合并为一次上报，避免上报风暴。
	reportDebounceWindow = 500 * time.Millisecond
)

const (
	// clientInfoPersistFilePrefix 地域信息持久化文件名前缀，
	// 实际文件名追加 clientID（如 client_info_host-1234-0.json），按 context 隔离避免互相覆盖。
	clientInfoPersistFilePrefix = "client_info"
	// clientInfoSharedFile 固定名共享文件，跨重启复用上次地域缓存。
	// 因为 clientID 含 PID（HostName/IP 档），每次重启 PID 变化会导致 client_info_<clientID>.json
	// 读不到上次缓存。启动时回退读此固定文件消除冷启动阻塞；持久化时双写一份保证其新鲜。
	// 多 context 同机地域相同，固定文件被覆盖无害；原子 temp+rename 写不会写坏文件。
	clientInfoSharedFile = "client_info.json"
)

// clientInfoFile 返回当前 context 的持久化文件名：client_info_<clientID>.json。
// 同进程多 context 各自写各自文件，互不覆盖；clientID 含的字符（PodName、host、pid、seq、UUID）均合法。
func (r *ReportClientCallBack) clientInfoFile() string {
	return clientInfoFileName(r.globalCtx.GetClientId())
}

// clientInfoFileName 按 clientID 拼接持久化文件名：client_info_<clientID>.json。
// 抽为包级函数便于单测，并确保文件名随 clientID 变化而唯一。
func clientInfoFileName(clientID string) string {
	return fmt.Sprintf("%s_%s.json", clientInfoPersistFilePrefix, clientID)
}

// clientInfoLoadCandidates 返回加载地域缓存时依次尝试的文件名列表（优先级从高到低）。
// 抽为包级函数便于单测：覆盖"按 context 隔离文件 → 固定名共享文件"的回退顺序，
// 顺序错误会导致重启换 PID 时读不到可复用的地域缓存。
func clientInfoLoadCandidates(clientID string) []string {
	return []string{clientInfoFileName(clientID), clientInfoSharedFile}
}

// loadLocalClientReportResult 从本地缓存加载上报结果信息。
// 优先读按 clientID 隔离的文件；读不到（重启换 PID、首次启动、升级）回退读固定名 client_info.json，
// 复用上次地域信息消除冷启动就近路由阻塞。两者均读不到才记 warn（首次部署正常现象）。
func (r *ReportClientCallBack) loadLocalClientReportResult() {
	logBase := r.logCtx.GetBaseLogger()
	resp := &apiservice.Response{}
	// 依次尝试：client_info_<clientID>.json → client_info.json
	candidates := clientInfoLoadCandidates(r.globalCtx.GetClientId())
	loaded := false
	for _, f := range candidates {
		if err := r.registry.LoadPersistedMessage(f, resp); err != nil {
			continue
		}
		loaded = true
		if f != candidates[0] {
			logBase.Infof("load local region info from shared %s (per-context file not found)", f)
		}
		break
	}
	if !loaded {
		logBase.Warnf("fail to load local region info from %s or %s",
			candidates[0], candidates[1])
		return
	}
	location := resp.GetClient().GetLocation()
	loc := &model.Location{
		Region: location.GetRegion().GetValue(),
		Zone:   location.GetZone().GetValue(),
		Campus: location.GetCampus().GetValue(),
	}
	// 初始化 lastLocation，避免首次上报时与缓存相同的 location 也触发重复写入
	// 初始化 lastConfigMetadata，避免重启后首次上报与缓存相同的 configMetadata 也触发重复写入
	// 构造期单协程调用，此处加锁仅为与运行期读写保持一致的同步约定
	r.persistMu.Lock()
	r.lastLocation = loc
	r.lastConfigMetadata = resp.GetClient().GetConfigMetadata().GetValue()
	r.persistMu.Unlock()
	r.updateLocation(loc, nil)
}

// reportClientRequest 客户端上报的请求
func (r *ReportClientCallBack) reportClientRequest() *model.ReportClientRequest {
	apiConfig := r.configuration.GetGlobal().GetAPI()
	clientHost := apiConfig.GetBindIP()
	reportClientReq := &model.ReportClientRequest{
		Version:        version.Version,
		Timeout:        r.configuration.GetGlobal().GetAPI().GetTimeout(),
		PersistHandler: r.persistHandlerWithLocationCheck,
	}
	if len(clientHost) > 0 {
		reportClientReq.Host = clientHost
	}

	infos := make([]model.StatInfo, 0, len(r.reporterChain))

	// 收集当前的所有metric插件链的元信息
	for i := range r.reporterChain {
		stat := r.reporterChain[i].Info()
		if stat.Empty() {
			continue
		}
		infos = append(infos, stat)
	}

	reportClientReq.StatInfos = infos
	reportClientReq.ID = r.globalCtx.GetClientId()
	r.fillConfigMetadata(reportClientReq)
	return reportClientReq
}

// persistHandlerWithLocationCheck 带地域信息与配置订阅元数据变更检查的持久化处理函数。
// 当服务端返回的地域信息或 config_metadata 与上次持久化的不同时（或首次写入），才执行写入操作，
// 避免不必要的磁盘 I/O。
// 将 config_metadata 纳入判断：订阅列表变化（新增/移除配置文件监听）后即使地域不变也需刷新文件，
// 否则 client_info.json 中的 config_watch 会停留在首次写入的过期快照，无法反映当前订阅状态。
// 写入时双写：client_info_<clientID>.json（按 context 隔离）+ client_info.json（固定名，
// 供下次重启 PID 变化时回退读取）。固定文件双写失败仅记 warn，不影响主流程。
func (r *ReportClientCallBack) persistHandlerWithLocationCheck(message proto.Message) error {
	cachedFile := r.clientInfoFile()
	resp, ok := message.(*apiservice.Response)
	if !ok {
		// 类型不匹配时直接持久化（双写）
		if err := r.registry.PersistMessage(cachedFile, message); err != nil {
			return err
		}
		r.persistShared(message)
		return nil
	}
	loc := resp.GetClient().GetLocation()
	newLocation := &model.Location{
		Region: loc.GetRegion().GetValue(),
		Zone:   loc.GetZone().GetValue(),
		Campus: loc.GetCampus().GetValue(),
	}
	newConfigMetadata := resp.GetClient().GetConfigMetadata().GetValue()
	// 检查-持久化-回写是一个读改写临界区，可能被 Process 与 doReportNow 并发进入，必须持锁
	r.persistMu.Lock()
	defer r.persistMu.Unlock()
	// 地域与配置订阅元数据均未变化时跳过写入，避免不必要的磁盘 I/O
	if !clientInfoNeedsPersist(r.lastLocation, newLocation, r.lastConfigMetadata, newConfigMetadata) {
		return nil
	}
	// 地域或订阅元数据发生变化（或首次写入），执行持久化（主文件 + 共享文件）
	if err := r.registry.PersistMessage(cachedFile, message); err != nil {
		return err
	}
	r.persistShared(message)
	r.lastLocation = newLocation
	r.lastConfigMetadata = newConfigMetadata
	r.logCtx.GetBaseLogger().Infof("%s updated, location {Region:%s, Zone:%s, Campus:%s}, configMetadataLen %d",
		cachedFile, newLocation.Region, newLocation.Zone, newLocation.Campus, len(newConfigMetadata))
	return nil
}

// clientInfoNeedsPersist 判断本次 ReportClient 响应相比上次持久化结果是否需要重新写入 client_info.json。
// lastLocation 为 nil 表示首次写入（本地缓存未加载到），必然需要写入。
// location 或 configMetadata 任一变化即需写入：configMetadata 纳入判断是为了让订阅列表变化
// （新增/移除配置文件监听、或已监听文件的 version/md5 变化）能刷新文件，避免 config_watch 停留在过期快照。
// configMetadata 采用「与数组顺序无关的集合语义」比较（见 configWatchSetEqual），对客户端 map 遍历序、
// 服务端回显重排都免疫，杜绝把同一份监听列表误判为变化而反复落盘；解析失败时回退为字符串比较。
// 抽为包级纯函数便于单测，无需构造 ReportClientCallBack 依赖。
func clientInfoNeedsPersist(lastLocation *model.Location, newLocation *model.Location,
	lastConfigMetadata, newConfigMetadata string) bool {
	// 地域变化（含首次写入）必然需要落盘
	if lastLocation == nil || *lastLocation != *newLocation {
		return true
	}
	// 订阅元数据按集合语义比较；无法解析时回退字符串比较
	if equal, ok := configWatchSetEqual(lastConfigMetadata, newConfigMetadata); ok {
		return !equal
	}
	return lastConfigMetadata != newConfigMetadata
}

// configWatchItem 用于语义比较的单个监听文件项，对应 config_metadata.config_watch 数组元素。
// 字段均可比较，可直接作为 map key 用于集合判等。
type configWatchItem struct {
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
	FileName  string `json:"file_name"`
	Version   uint64 `json:"version"`
	Md5       string `json:"md5"`
}

// configWatchSetEqual 以「与数组顺序无关的集合语义」比较两份 config_metadata 的 config_watch 是否一致。
// 仅当两者的监听文件集合（namespace/group/file_name/version/md5 五项）完全相同时返回 equal=true。
// ok=false 表示任一输入无法解析为预期的 config_watch 结构，调用方应回退为字符串比较。
func configWatchSetEqual(lastConfigMetadata, newConfigMetadata string) (equal bool, ok bool) {
	lastSet, ok1 := configWatchItemSet(lastConfigMetadata)
	newSet, ok2 := configWatchItemSet(newConfigMetadata)
	if !ok1 || !ok2 {
		return false, false
	}
	if len(lastSet) != len(newSet) {
		return false, true
	}
	for item := range lastSet {
		if _, exists := newSet[item]; !exists {
			return false, true
		}
	}
	return true, true
}

// configWatchItemSet 解析 config_metadata JSON，把 config_watch 数组转成可判等的 item 集合（去重、无序）。
// 空串视为合法的「无监听」快照，返回空集合；解析失败返回 (nil, false)。
func configWatchItemSet(configMetadata string) (map[configWatchItem]struct{}, bool) {
	if configMetadata == "" {
		return map[configWatchItem]struct{}{}, true
	}
	var payload struct {
		ConfigWatch []configWatchItem `json:"config_watch"`
	}
	if err := json.Unmarshal([]byte(configMetadata), &payload); err != nil {
		return nil, false
	}
	set := make(map[configWatchItem]struct{}, len(payload.ConfigWatch))
	for _, it := range payload.ConfigWatch {
		set[it] = struct{}{}
	}
	return set, true
}

// persistShared 写入固定名共享文件 client_info.json，供下次重启回退读取。
// 失败仅记 warn 不影响主流程：该文件只是缓存加速，缺失最多导致下次冷启动多等一次 ReportClient。
func (r *ReportClientCallBack) persistShared(message proto.Message) {
	if err := r.registry.PersistMessage(clientInfoSharedFile, message); err != nil {
		r.logCtx.GetBaseLogger().Warnf("persist shared client_info.json failed: %v", err)
	}
}

// Process 执行任务
func (r *ReportClientCallBack) Process(
	taskKey interface{}, taskValue interface{}, lastProcessTime time.Time) model.TaskResult {
	if !lastProcessTime.IsZero() && time.Since(lastProcessTime) < r.interval {
		return model.SKIP
	}
	reportClientReq := r.reportClientRequest()
	if err := reportClientReq.Validate(); err != nil {
		r.logCtx.GetBaseLogger().Errorf("report client request fatal validate error:%v", err)
		return model.TERMINATE
	}

	reportClientResp, err := r.connector.ReportClient(reportClientReq)
	if err != nil {
		r.logCtx.GetBaseLogger().Errorf("report client info:%+v, error:%v", reportClientReq, err)
		r.updateLocation(nil, err.(model.SDKError))
		// 发生错误也要重试，直到获取到地域信息为止
		return model.CONTINUE
	}

	r.updateLocation(&model.Location{
		Region: reportClientResp.Region,
		Zone:   reportClientResp.Zone,
		Campus: reportClientResp.Campus,
	}, nil)
	return model.CONTINUE
}

// OnTaskEvent 任务事件回调
func (r *ReportClientCallBack) OnTaskEvent(event model.TaskEvent) {

}

// updateLocation 更新区域属性
func (r *ReportClientCallBack) updateLocation(location *model.Location, lastErr model.SDKError) {
	// 如果SDK设置了本地获取 location，则忽略 ReportClient 的数据
	if len(r.configuration.GetGlobal().GetLocation().GetProviders()) != 0 {
		return
	}

	if nil != location {
		// 读取 lastLocation 判断是否需要打印变更日志；persistHandler 可能并发回写该字段，需持锁
		r.persistMu.Lock()
		locationChanged := r.lastLocation == nil || *r.lastLocation != *location
		r.persistMu.Unlock()
		// 只在地域信息首次获取或发生变化时打印日志，避免重复输出相同内容
		if locationChanged {
			r.logCtx.GetBaseLogger().Infof("current client area info is {Region:%s, Zone:%s, Campus:%s}",
				location.Region, location.Zone, location.Campus)
		}
	}
	if r.globalCtx.SetCurrentLocation(location, lastErr) {
		r.logCtx.GetBaseLogger().Infof("client area info is ready")
	}
}

// fillConfigMetadata 填充配置中心启用状态与监听文件列表元数据到上报请求。
// ConfigEnabled 取配置中心是否启用；启用且 configFlow 非 nil 时序列化监听文件列表为 config_metadata JSON。
// req 不能为 nil；配置中心未启用或 configFlow 为 nil 时仅设置 ConfigEnabled，ConfigMetadata 留空。
func (r *ReportClientCallBack) fillConfigMetadata(req *model.ReportClientRequest) {
	req.ConfigEnabled = r.configuration.GetConfigFile().IsEnable()
	if !req.ConfigEnabled || r.configFlow == nil {
		return
	}
	items := r.configFlow.GetWatchedConfigFileMetadata()
	payload := configMetadataPayload{Kind: "config", ConfigWatch: items}
	metadataJSON, err := json.Marshal(payload)
	if err != nil {
		r.logCtx.GetBaseLogger().Warnf("marshal config metadata failed: %v", err)
		return
	}
	req.ConfigMetadata = string(metadataJSON)
}

// TriggerNow 请求触发一次即时 ReportClient 上报，不等定时周期。
// 供配置文件监听列表变化时调用，使服务端尽快感知新的监听配置。
//
// 采用 debounce 合并：短时间内的多次调用只会产生一次实际上报——
// 应用启动时常批量订阅数十个配置文件，若每次都直接上报会瞬间打出等量并发 gRPC 请求，
// 造成单客户端上报风暴（服务端侧可能触发限流，客户端侧连接池竞争）。
// 首次调用会等待 reportDebounceWindow 让后续订阅合并进来，窗口结束后统一上报一次。
// 上报失败仅记日志，不返回错误，避免影响触发方（如配置订阅流程）。
func (r *ReportClientCallBack) TriggerNow() {
	// 已有待处理的合并请求时直接返回，由那次上报覆盖本次变化
	if !atomic.CompareAndSwapInt32(&r.reportPending, 0, 1) {
		return
	}
	go func() {
		defer atomic.StoreInt32(&r.reportPending, 0)
		// 合并窗口：等待期间到来的 TriggerNow 都被本次上报覆盖
		time.Sleep(reportDebounceWindow)
		r.doReportNow()
	}()
}

// doReportNow 立即执行一次 ReportClient 上报。
// 与定时任务 Process 可能并发调用 connector.ReportClient，依赖 connector 的线程安全
// （其实现每次独立获取连接）。
func (r *ReportClientCallBack) doReportNow() {
	reportClientReq := r.reportClientRequest()
	if err := reportClientReq.Validate(); err != nil {
		r.logCtx.GetBaseLogger().Errorf("trigger now report client validate error: %v", err)
		return
	}
	if _, err := r.connector.ReportClient(reportClientReq); err != nil {
		// 上报失败不影响业务，定时任务下一周期会重试，故仅 warn 不 error（避免触发告警噪音）
		r.logCtx.GetBaseLogger().Warnf("trigger now report client error: %v", err)
	}
}

// configMetadataPayload ReportClient 上报 config_metadata 字段的 JSON 结构
type configMetadataPayload struct {
	Kind        string                              `json:"kind"`
	ConfigWatch []configflow.ConfigFileMetadataItem `json:"config_watch"`
}
