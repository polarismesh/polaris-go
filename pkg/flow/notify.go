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

package flow

import (
	"context"
	"fmt"
	"github.com/polarismesh/polaris-go/pkg/log"
	"github.com/polarismesh/polaris-go/pkg/model"
	"github.com/polarismesh/polaris-go/pkg/plugin/common"
	"sync"
	"sync/atomic"
	"time"
)

const (
	keySourceRoute   = "sourceRoute"
	keyDstRoute      = "destinationRoute"
	keyDstRateLimit  = "destinationRateLimit"
	keyDstInstances  = "destinationInstances"
	keyDstMeshConfig = "destinationMeshConfig"
	keyDstServices   = "destinationServices"
	keyDstMesh       = "destinationMesh"
)

//上下文标识
type ContextKey struct {
	//服务信息
	ServiceKey *model.ServiceKey
	//操作信息
	Operation string
}

//ToString方法
func (c ContextKey) String() string {
	return fmt.Sprintf("{ServiceKey: %s, Operation: %s}", *c.ServiceKey, c.Operation)
}

//同步调用回调上下文
type SingleNotifyContext struct {
	name     *ContextKey
	notifier *common.Notifier
}

//创建回调上下文
func NewSingleNotifyContext(name *ContextKey, notifier *common.Notifier) *SingleNotifyContext {
	return &SingleNotifyContext{name: name, notifier: notifier}
}

//返回异常信息
func (s *SingleNotifyContext) Err() model.SDKError {
	return s.notifier.GetError()
}

//notify 异步任务执行回调函数
func (s *SingleNotifyContext) Wait(timeout time.Duration) bool {
	afterTimer := time.After(timeout)
	select {
	case <-afterTimer:
		return true
	case <-s.notifier.GetContext().Done():
		log.GetBaseLogger().Debugf("context %s has been notified", *s.name)
		return false
	}
}

//复合的回调上下文，等待所有的子回调都返回才会触发回调
type CombineNotifyContext struct {
	svcKey          *model.ServiceKey
	waitCount       int32
	notifiers       []*SingleNotifyContext
	doneContextKeys *model.SyncHashSet
}

//创建复合回调上下文
func NewCombineNotifyContext(svcKey *model.ServiceKey, notifiers []*SingleNotifyContext) *CombineNotifyContext {
	maxWaitCount := len(notifiers)
	combineCtx := &CombineNotifyContext{
		svcKey:          svcKey,
		notifiers:       notifiers,
		waitCount:       int32(maxWaitCount),
		doneContextKeys: model.NewSyncHashSet(),
	}
	return combineCtx
}

//是否已经完成
func (c *CombineNotifyContext) IsDone() bool {
	log.GetBaseLogger().Debugf("CombineNotifyContext waitCount %d", atomic.LoadInt32(&c.waitCount))
	return atomic.LoadInt32(&c.waitCount) <= 0
}

//获取错误信息集合
func (c *CombineNotifyContext) Errs() map[ContextKey]model.SDKError {
	var errs = make(map[ContextKey]model.SDKError, len(c.notifiers))
	for _, notifier := range c.notifiers {
		err := notifier.Err()
		if nil != err {
			errs[*notifier.name] = err
		}
	}
	return errs
}

//打印通知日志
func (c *CombineNotifyContext) logNotifier(operation string, notifier *SingleNotifyContext, restWait int32) {
	log.GetBaseLogger().Debugf("notifier %s of %s has been notified, rest %v", *notifier.name, c.svcKey, restWait)
}

//notify 异步任务执行回调函数
//返回值，是否超时
func (c *CombineNotifyContext) Wait(timeout time.Duration) (exceedTime bool) {
	var restWait = atomic.LoadInt32(&c.waitCount)
	if restWait == 0 {
		return false
	}
	log.GetBaseLogger().Debugf("notifiers of %s start to wait, rest %d", *c.svcKey, restWait)
	doneKeyChan := make(chan string)
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case key := <-doneKeyChan:
				c.doneContextKeys.Add(key)
			}
		}
	}()
	wg := &sync.WaitGroup{}
	wg.Add(int(restWait))
	for _, notifierValue := range c.notifiers {
		if c.doneContextKeys.Contains(notifierValue.name.Operation) {
			continue
		}
		go func(notifier *SingleNotifyContext) {
			defer wg.Done()
			afterTimer := time.After(timeout)
			select {
			case <-afterTimer:
				return
			case <-notifier.notifier.GetContext().Done():
				doneKeyChan <- notifier.name.Operation
				nextWait := atomic.AddInt32(&c.waitCount, -1)
				c.logNotifier(notifier.name.Operation, notifier, nextWait)
			}
		}(notifierValue)
	}
	wg.Wait()
	cancel()
	//如果没有waitCount还是大于0，说明还需要远程获取信息
	return atomic.LoadInt32(&c.waitCount) > 0
}
