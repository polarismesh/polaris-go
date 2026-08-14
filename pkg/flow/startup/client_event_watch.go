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

package startup

import (
	"encoding/json"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	apiservice "github.com/polarismesh/specification/source/go/api/v1/service_manage"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"

	configflow "github.com/polarismesh/polaris-go/pkg/flow/configuration"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/plugin/serverconnector"
)

const (
	// watchInitialRetryDelay 初始重连退避
	watchInitialRetryDelay = 1 * time.Second
	// watchMaxRetryDelay 重连退避上限
	watchMaxRetryDelay = 30 * time.Second
	// watchLogSuppressAfter 连续失败超过该次数后降低日志频率，避免服务端长期不可用时刷屏
	watchLogSuppressAfter = 5
	// watchLogSuppressEvery 降频后每 N 次失败打印一条日志
	watchLogSuppressEvery = 10
	// watchNotFoundWarnCount 服务端 client 缓存未命中（NotFound）记 warn 的连续失败次数上限。
	// 服务端 ReportClient 异步落库、client 缓存按秒级周期从存储增量刷新，前几次建流可能早于
	// 刷新而拿到 NotFound，属预期内的启动竞态，退避重连即可自愈，记 warn 即可；
	// 超过该次数仍 NotFound 说明客户端上报确有异常，升为 error 以便被排查与告警发现。
	watchNotFoundWarnCount = 3
	// watchMaxAckContentBytes ACK 中 content 的最大字节数。
	// 超限则截断并置 content_truncated=true，避免超大配置触发 gRPC 消息体上限（服务端默认 4MB）
	// 导致 ACK 永远发不出、服务端 waiter 超时。
	watchMaxAckContentBytes = 512 * 1024
)

// ACK 中 applied=false 的原因取值，供服务端/运维区分具体场景
const (
	// reasonBadContent PUSH content 不是合法 JSON
	reasonBadContent = "bad_content"
	// reasonUnknownKind 未知的事件类型
	reasonUnknownKind = "unknown_kind"
	// reasonConfigDisabled 客户端未启用配置中心
	reasonConfigDisabled = "config_disabled"
	// reasonNotWatched 客户端未监听该配置文件
	reasonNotWatched = "not_watched"
	// reasonPending 客户端已监听该配置文件但尚未拉取生效（首次拉取失败/重试中）
	reasonPending = "pending"
)

var (
	// errWatcherClosed watcher 已收到关闭信号，用于中断建流流程
	errWatcherClosed = errors.New("client event watcher closed")
	// errHandlePushPanic 单条 PUSH 处理过程中发生 panic 并已被 recover
	errHandlePushPanic = errors.New("handle push event panicked")
)

// WatchedConfigFileProvider 配置文件监听元数据提供者，解耦 watcher 与具体 ConfigFileFlow，便于测试替换。
// 导出以便调用方（Engine）显式声明变量类型，避免把 typed-nil 指针装入接口——
// typed-nil 装箱后接口值非 nil，会绕过 nil 判断并在调用方法时解引用 nil receiver。
type WatchedConfigFileProvider interface {
	GetWatchedConfigFileMetadata() []configflow.ConfigFileMetadataItem
	// GetWatchedConfigFileContent 按 (namespace, group, fileName) 查询单个监听配置文件的元数据与内容。
	// 命中返回 (item, true)；未监听返回 (zero, false)。
	GetWatchedConfigFileContent(namespace, fileGroup, fileName string) (configflow.ConfigFileContentItem, bool)
}

// ClientEventWatcher 管理 WatchClientEvents 双向流生命周期。
// 启动时建流并发送 WATCH 首帧自证身份，持续接收服务端 PUSH 查询，
// 解析 content 按 kind 分发处理后回 ACK（index 回带 PUSH 的 index）。
// 流断开时指数退避重连，每次重连都重发 WATCH 首帧覆盖服务端旧 stream 绑定。
type ClientEventWatcher struct {
	connector  serverconnector.ServerConnector
	configFlow WatchedConfigFileProvider
	logCtx     *log.ContextLogger
	clientID   string
	closeCh    chan struct{}
	done       chan struct{}
	retryDelay time.Duration
	streamMu   sync.Mutex
	stream     serverconnector.ClientEventStream
	// failCount 连续建流/接收失败次数，用于日志降频（仅 runLoop 单协程读写）
	failCount int
}

// NewClientEventWatcher 创建客户端事件监听器。
// connector 用于建立 WatchClientEvents 流；configFlow 用于查询配置文件 version/md5，
// 配置中心未启用时传 nil（仍建流应答，但所有 config 查询回 applied=false）。
func NewClientEventWatcher(connector serverconnector.ServerConnector, clientID string,
	configFlow WatchedConfigFileProvider, logCtx *log.ContextLogger) *ClientEventWatcher {
	return &ClientEventWatcher{
		connector:  connector,
		clientID:   clientID,
		configFlow: configFlow,
		logCtx:     logCtx,
		closeCh:    make(chan struct{}),
		done:       make(chan struct{}),
	}
}

// Start 启动监听协程，非阻塞。
func (w *ClientEventWatcher) Start() {
	go w.runLoop()
}

// Close 关闭监听器，阻塞至 runLoop 退出。
// 先关闭 closeCh 通知 runLoop 停止，再主动关闭当前 stream 中断可能阻塞的 Recv，最后等待 runLoop 退出。
func (w *ClientEventWatcher) Close() {
	select {
	case <-w.closeCh:
		return
	default:
		close(w.closeCh)
	}
	// 中断可能阻塞的 Recv：关闭 stream 的 ctx 使 Recv 返回错误
	w.streamMu.Lock()
	s := w.stream
	w.stream = nil
	w.streamMu.Unlock()
	if s != nil {
		_ = s.Close()
	}
	<-w.done
}

// runLoop 主驱动循环：建流+发 WATCH → 接收循环，失败/断开指数退避重连。
// 顶层 recover 兜底：watcher 运行在独立协程，任何未捕获 panic 都会终止宿主进程，
// 故在此收敛为一条 error 日志并退出 watcher，SDK 其余能力不受影响。
func (w *ClientEventWatcher) runLoop() {
	defer close(w.done)
	defer func() {
		if r := recover(); r != nil {
			if l := w.logger(); l != nil {
				l.Errorf(
					"client event watcher panic recovered, watcher exited, clientID %s: %v\nstack: %s",
					w.clientID, r, string(debug.Stack()))
			}
		}
	}()
	w.retryDelay = watchInitialRetryDelay
	for {
		if w.isClosed() {
			return
		}
		stream, err := w.connectAndWatch()
		failed := err
		if failed == nil {
			// 建流与首帧发送成功——但 gRPC 双向流是惰性的，Unimplemented 等服务端错误
			// 往往要等到第一次 Recv 才暴露，故仍需进入 recvLoop 探测。
			// 进入 recvLoop 即说明建流阶段已通过，重置连续失败计数（之前的失败已过去）。
			w.failCount = 0
			w.retryDelay = watchInitialRetryDelay
			recvErr := w.recvLoop(stream)
			if cerr := stream.Close(); cerr != nil {
				if l := w.logger(); l != nil {
					l.Warnf("watch client events stream close error: %v", cerr)
				}
			}
			failed = recvErr
		}
		if w.isClosed() {
			return
		}
		if failed != nil {
			// 服务端不支持该接口（旧版本服务端）时重连无意义，直接退出避免无限重试与日志刷屏。
			// Unimplemented 可能从 connectAndWatch 或 recvLoop 任一路径返回，统一在此判断。
			if isUnimplemented(failed) {
				if l := w.logger(); l != nil {
					l.Warnf(
						"watch client events unimplemented by server, watcher disabled, clientID %s: %v",
						w.clientID, failed)
				}
				return
			}
			w.failCount++
			w.logConnectFailure(failed)
			if !w.backoffSleep() {
				return
			}
			continue
		}
		// recvLoop 正常退出（收到关闭信号），由 isClosed 分支处理
	}
}

// logConnectFailure 记录建流/接收失败，连续失败超过阈值后降频，避免服务端长期不可用时刷满日志。
// 日志级别：服务端 client 缓存未命中（NotFound）的前 watchNotFoundWarnCount 次记 warn——
// 该场景是启动期缓存刷新竞态，退避重连即可自愈；超出该次数或其余错误一律记 error。
func (w *ClientEventWatcher) logConnectFailure(err error) {
	if w.failCount > watchLogSuppressAfter && w.failCount%watchLogSuppressEvery != 0 {
		return
	}
	l := w.logger()
	if l == nil {
		return
	}
	msg := "watch client events failed (consecutive %d), retry after %v, clientID %s: %v"
	if shouldLogFailureAsWarn(w.failCount, err) {
		l.Warnf(msg, w.failCount, w.retryDelay, w.clientID, err)
		return
	}
	l.Errorf(msg, w.failCount, w.retryDelay, w.clientID, err)
}

// shouldLogFailureAsWarn 判断本次建流失败记 warn（true）还是 error（false）。
// failCount 为当前连续失败次数（从 1 开始计）；err 为本次失败原因。
// 瞬时网络错误（Unavailable/DeadlineExceeded，服务端滚动重启/网络抖动的典型表现）始终记 warn——
// 配置生效查询是旁路观测能力，其断连是主发现链路故障的伴生症状，真实 outage 由主链路告警暴露，
// 此处记 error 只会在发布窗口制造告警噪音；配合既有降频，长期不可用也不会刷屏。
// 服务端 client 缓存未命中（NotFound）仅在前 watchNotFoundWarnCount 次记 warn（启动竞态可自愈）；
// 超出该次数仍 NotFound 说明客户端上报确有异常，升 error 以便排查。其余错误一律记 error。
// 抽为包级纯函数便于单测，无需注入可捕获级别的 logger。
func shouldLogFailureAsWarn(failCount int, err error) bool {
	if isTransientNetworkError(err) {
		return true
	}
	return failCount <= watchNotFoundWarnCount && isClientNotFound(err)
}

// isTransientNetworkError 判断错误是否为瞬时网络错误（gRPC Unavailable/DeadlineExceeded）。
// 这类错误多由服务端滚动重启、负载摘除或网络抖动引起，退避重连即可自愈，不代表持续性故障。
func isTransientNetworkError(err error) bool {
	return hasGRPCCode(err, codes.Unavailable) || hasGRPCCode(err, codes.DeadlineExceeded)
}

// isUnimplemented 判断错误是否为 gRPC Unimplemented（服务端未实现该接口）。
func isUnimplemented(err error) bool {
	return hasGRPCCode(err, codes.Unimplemented)
}

// isClientNotFound 判断错误是否为服务端 client 缓存未命中（gRPC NotFound）。
// 服务端 ReportClient 异步落库、client 缓存按秒级周期从存储增量刷新，故 SDK 启动初期
// 建流可能早于缓存刷新而拿到该错误。这是预期内的启动竞态，退避重连后即可绑定成功，
// 不代表客户端或服务端故障。
func isClientNotFound(err error) bool {
	return hasGRPCCode(err, codes.NotFound)
}

// hasGRPCCode 判断错误是否携带指定的 gRPC status code。
// 服务端错误可能被 SDK 包装，故先直接判断，再逐层解包 errors 链后重试判断。
// err 为 nil 时返回 false。
func hasGRPCCode(err error, code codes.Code) bool {
	if err == nil {
		return false
	}
	if st, ok := status.FromError(err); ok && st.Code() == code {
		return true
	}
	// SDK 包装后的错误：逐层解包再判断
	for inner := err; inner != nil; {
		unwrapped, ok := inner.(interface{ Unwrap() error })
		if !ok {
			break
		}
		inner = unwrapped.Unwrap()
		if st, ok := status.FromError(inner); ok && st.Code() == code {
			return true
		}
	}
	return false
}

// connectAndWatch 建立双向流并发送 WATCH 首帧。每次重连都会调用。
func (w *ClientEventWatcher) connectAndWatch() (serverconnector.ClientEventStream, error) {
	stream, err := w.connector.WatchClientEvents()
	if err != nil {
		return nil, err
	}
	// 建流与 Close 存在竞态窗口：若此刻已收到关闭信号，Close 取到的是旧 stream，
	// 新建的流不会被释放，故主动关闭并退出，避免连接泄漏。
	if w.isClosed() {
		_ = stream.Close()
		return nil, errWatcherClosed
	}
	w.setStream(stream)
	// 发送 WATCH 首帧自证身份，client_id 与 ReportClient 上报一致
	if err := stream.Send(&apiservice.ClientEvent{
		Type:     apiservice.ClientEvent_WATCH,
		ClientId: w.clientID,
	}); err != nil {
		_ = stream.Close()
		return nil, err
	}
	if l := w.logger(); l != nil {
		l.Infof("watch client events stream established, clientID %s", w.clientID)
	}
	return stream, nil
}

// setStream 记录当前活跃流，供 Close 主动中断阻塞的 Recv。
func (w *ClientEventWatcher) setStream(s serverconnector.ClientEventStream) {
	w.streamMu.Lock()
	w.stream = s
	w.streamMu.Unlock()
}

// recvLoop 持续接收服务端 PUSH 并回 ACK，直到流关闭/出错。
func (w *ClientEventWatcher) recvLoop(stream serverconnector.ClientEventStream) error {
	for {
		if w.isClosed() {
			return nil
		}
		event, err := stream.Recv()
		if err != nil {
			return err
		}
		if event.GetType() != apiservice.ClientEvent_PUSH {
			// 忽略非 PUSH（服务端理论上只下发 PUSH）
			continue
		}
		if err := w.handlePush(stream, event); err != nil {
			// 单条处理失败不中断接收循环，后续 Recv 出错时再重连
			if l := w.logger(); l != nil {
				l.Warnf("handle push event failed, index %d: %v", event.GetIndex(), err)
			}
		}
	}
}

// handlePush 处理一条 PUSH 事件：按 kind 分发，组装 ACK content 并回带 index 上行。
// 单条 recover：畸形事件导致的 panic 只影响该条应答，不摧毁整个接收循环与宿主进程。
func (w *ClientEventWatcher) handlePush(stream serverconnector.ClientEventStream,
	event *apiservice.ClientEvent) (err error) {
	defer func() {
		if r := recover(); r != nil {
			if l := w.logger(); l != nil {
				l.Errorf(
					"handle push event panic recovered, index %d, clientID %s: %v\nstack: %s",
					event.GetIndex(), w.clientID, r, string(debug.Stack()))
			}
			err = errHandlePushPanic
		}
	}()
	ack := w.buildAck(event.GetContent())
	ackContent := w.marshalAck(ack)
	if err := stream.Send(&apiservice.ClientEvent{
		Type:     apiservice.ClientEvent_ACK,
		ClientId: w.clientID,
		Index:    event.GetIndex(),
		Content:  ackContent,
	}); err != nil {
		return err
	}
	// 运维主动查询才触发，频率低；生产环境需可见以便排查"查询结果为何如此"。
	// 直接使用已构造的 ack 结构体字段打日志，无需把 ackContent 再反序列化一遍。
	if l := w.logger(); l != nil {
		l.Infof("client event ack sent, index %d, clientID %s, namespace %s, group %s, "+
			"file %s, version %d, md5 %s, applied %v, reason %s, ackBytes %d",
			event.GetIndex(), w.clientID,
			ack.Config.Namespace, ack.Config.Group, ack.Config.FileName,
			ack.Version, ack.Md5, ack.Applied, ack.Reason, len(ackContent))
	}
	return nil
}

// buildAckContent 构造并序列化 ACK content JSON，为 buildAck + marshalAck 的便捷封装。
// 供需要字符串形式的调用方（含单测）使用；handlePush 走 buildAck 以避免序列化后再反解。
func (w *ClientEventWatcher) buildAckContent(pushContent string) string {
	return w.marshalAck(w.buildAck(pushContent))
}

// buildAck 解析 PUSH content 按 kind 分发，构造 ACK 结构体（未序列化）。
// kind=config：按 config.{namespace,group,file_name} 查本地监听文件，
// 命中且已生效回 version/md5/content/applied=true；content 超过上限时截断并置 content_truncated。
// 已监听但尚未拉取生效回 applied=false + reason=pending；未监听回 not_watched；
// 未知 kind 或解析失败：回带 reason 的最小 ACK（applied=false），保证不阻塞服务端 waiter。
func (w *ClientEventWatcher) buildAck(pushContent string) clientEventAck {
	var query clientEventQuery
	if err := json.Unmarshal([]byte(pushContent), &query); err != nil {
		if l := w.logger(); l != nil {
			l.Warnf("unmarshal push content failed, clientID %s: %v", w.clientID, err)
		}
		return clientEventAck{Applied: false, Reason: reasonBadContent}
	}
	if query.Kind != "config" {
		return clientEventAck{Kind: query.Kind, Applied: false, Reason: reasonUnknownKind}
	}
	ack := clientEventAck{Kind: query.Kind, Config: query.Config, Applied: false}
	if w.configFlow == nil {
		// 配置中心未启用
		ack.Reason = reasonConfigDisabled
		return ack
	}
	item, ok := w.configFlow.GetWatchedConfigFileContent(
		query.Config.Namespace, query.Config.Group, query.Config.FileName)
	if !ok {
		// 客户端未监听该配置文件
		ack.Reason = reasonNotWatched
		return ack
	}
	if !item.Pulled {
		// 已监听但尚未拉取到远端文件（首次拉取失败/重试中），配置未生效
		ack.Version = item.Version
		ack.Reason = reasonPending
		return ack
	}
	ack.Version = item.Version
	ack.Md5 = item.Md5
	ack.EffectiveTime = item.EffectiveTime
	ack.Applied = true
	// 超大配置截断：gRPC 服务端默认消息体上限 4MB，超限会导致 ACK 发送失败、服务端 waiter 超时。
	// md5 仍为完整内容的摘要，服务端可据此校验并按需另行拉取全量内容。
	if len(item.Content) > watchMaxAckContentBytes {
		ack.Content = item.Content[:watchMaxAckContentBytes]
		ack.ContentTruncated = true
		ack.ContentLength = len(item.Content)
		if l := w.logger(); l != nil {
			l.Warnf(
				"ack content truncated, file %s/%s/%s, total %d bytes, limit %d bytes",
				query.Config.Namespace, query.Config.Group, query.Config.FileName,
				len(item.Content), watchMaxAckContentBytes)
		}
	} else {
		ack.Content = item.Content
	}
	return ack
}

// marshalAck 序列化 ACK；失败时记日志并回退为带 reason 的最小应答，避免服务端收到无法诊断的空对象。
func (w *ClientEventWatcher) marshalAck(ack clientEventAck) string {
	data, err := json.Marshal(ack)
	if err != nil {
		if l := w.logger(); l != nil {
			l.Warnf("marshal ack content failed, clientID %s: %v", w.clientID, err)
		}
		return `{"applied":false,"reason":"marshal_failed"}`
	}
	return string(data)
}

// backoffSleep 指数退避等待，收到关闭信号时返回 false 终止重连。
func (w *ClientEventWatcher) backoffSleep() bool {
	select {
	case <-w.closeCh:
		return false
	case <-time.After(w.retryDelay):
	}
	w.retryDelay *= 2
	if w.retryDelay > watchMaxRetryDelay {
		w.retryDelay = watchMaxRetryDelay
	}
	return true
}

func (w *ClientEventWatcher) isClosed() bool {
	select {
	case <-w.closeCh:
		return true
	default:
		return false
	}
}

// logger 返回用于打印日志的 baseLogger，logCtx 或其 baseLogger 未注入时返回 nil。
// 调用方须就地判空后直接调用 l.Warnf/Errorf/Infof——不要再包一层 helper 转发：
// zaplog 使用 zap.AddCallerSkip(2)（plugin/logger/zaplog/logger.go）假定固定栈深度，
// 多一层转发会让日志中的 caller 指向该 helper 所在行，而非真正的业务调用点。
// 判空是必需的：watcher 运行在独立协程，日志本身 panic 会终止宿主进程。
func (w *ClientEventWatcher) logger() log.Logger {
	if w.logCtx == nil {
		return nil
	}
	return w.logCtx.GetBaseLogger()
}

// clientEventQuery 服务端 PUSH 下发的查询指令 JSON 结构
type clientEventQuery struct {
	Kind   string              `json:"kind"`
	Config clientEventQueryCfg `json:"config"`
}

// clientEventQueryCfg 查询目标配置文件三元组（snake_case 与服务端配置中心一致）
type clientEventQueryCfg struct {
	Namespace string `json:"namespace"`
	Group     string `json:"group"`
	FileName  string `json:"file_name"`
}

// clientEventAck 客户端 ACK 应答 JSON 结构
type clientEventAck struct {
	Kind    string              `json:"kind"`
	Config  clientEventQueryCfg `json:"config"`
	Version uint64              `json:"version,omitempty"`
	Md5     string              `json:"md5,omitempty"`
	// EffectiveTime 配置在客户端本地的实际生效时刻（int64 毫秒时间戳），
	// 取自客户端首次拉取或变更更新时记录的 time.Now().UnixMilli()。
	// applied=true 时输出；未拉取到或 applied=false 时 omitempty 省略。
	EffectiveTime int64 `json:"effective_time,omitempty"`
	// Content 为客户端当前持有的配置文件内容，命中监听文件时返回（即便为空串也显式输出，
	// 供服务端区分"内容为空"与"未返回内容"）。未命中场景(applied=false)不进入此分支。
	Content string `json:"content"`
	// ContentTruncated 标记 Content 是否因超过上限被截断；为 true 时 ContentLength 给出原始长度
	ContentTruncated bool `json:"content_truncated,omitempty"`
	// ContentLength 原始内容字节数，仅在截断时输出
	ContentLength int  `json:"content_length,omitempty"`
	Applied       bool `json:"applied"`
	// Reason applied=false 的具体原因，便于运维区分"未监听"与"配置中心未启用"等场景
	Reason string `json:"reason,omitempty"`
}
