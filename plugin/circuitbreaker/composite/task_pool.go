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

package composite

import (
	"context"
	"hash/fnv"
	"math/rand"
	"sync"
	"sync/atomic"
	"time"
)

func newTaskExecutor(size int) *TaskExecutor {
	workers := make([]*worker, 0, size)
	for i := 0; i < size; i++ {
		workers = append(workers, &worker{
			close:      0,
			queue:      make(chan func(), 128),
			delayQueue: sync.Map{},
		})
	}

	ctx, cancel := context.WithCancel(context.Background())

	e := &TaskExecutor{
		cancel:  cancel,
		workers: workers,
		r:       rand.New(rand.NewSource(time.Now().Unix())),
	}

	for i := range workers {
		workers[i].mainLoop(ctx)
	}
	return e
}

type TaskExecutor struct {
	cancel  context.CancelFunc
	workers []*worker
	r       *rand.Rand
}

func (e *TaskExecutor) Stop() {
	e.cancel()
}

func (e *TaskExecutor) Execute(f func()) {
	e.workers[e.randIndex()].add(f)
}

func (e *TaskExecutor) IntervalExecute(interval time.Duration, f func()) {
	e.workers[e.randIndex()].addDelay(interval, f, true)
}

func (e *TaskExecutor) DelayExecute(delay time.Duration, f func()) {
	e.workers[e.randIndex()].addDelay(delay, f, false)
}

func (e *TaskExecutor) AffinityExecute(key string, f func()) {
	h := fnv.New64a()
	h.Write([]byte(key))
	ret := h.Sum64()

	index := int(ret % uint64(len(e.workers)-1))
	e.workers[index].add(f)
}

func (e *TaskExecutor) AffinityDelayExecute(key string, delay time.Duration, f func()) {
	h := fnv.New64a()
	h.Write([]byte(key))
	ret := h.Sum64()

	index := int(ret % uint64(len(e.workers)-1))
	e.workers[index].addDelay(delay, f, false)
}

func (e *TaskExecutor) randIndex() int32 {
	return e.r.Int31n(int32(len(e.workers)))
}

type worker struct {
	lock       sync.RWMutex
	close      int8
	queue      chan func()
	id         int64
	delayQueue sync.Map
}

func (w *worker) add(f func()) {
	w.lock.RLock()
	defer w.lock.RUnlock()

	if w.close == 1 {
		return
	}

	w.queue <- func() {
		defer func() {
			if err := recover(); err != nil {
				panic(err)
			}
		}()
		f()
	}
}

func (w *worker) addDelay(delay time.Duration, f func(), isInterval bool) {
	w.lock.RLock()
	defer w.lock.RUnlock()

	if w.close == 1 {
		return
	}

	wf := func() {
		defer func() {
			if err := recover(); err != nil {
				// do nothing
			}
		}()
		f()
	}

	id := atomic.AddInt64(&w.id, 1)
	if isInterval {
		w.delayQueue.Store(id, &tickTask{
			ticker: time.NewTicker(delay),
			f:      wf,
		})
	} else {
		w.delayQueue.Store(id, &delayTask{
			timer: time.NewTimer(delay),
			f:     wf,
		})
	}
}

func (w *worker) mainLoop(ctx context.Context) {
	go func() {
		for {
			select {
			case <-ctx.Done():
				w.lock.Lock()
				w.close = 1
				close(w.queue)
				w.lock.Unlock()
				return
			case f := <-w.queue:
				f()
			}
		}
	}()
	go func() {
		ticker := time.NewTicker(100 * time.Millisecond)
		for {
			select {
			case <-ctx.Done():
				ticker.Stop()
				w.delayQueue.Range(func(key, value interface{}) bool {
					switch t := value.(type) {
					case *delayTask:
						t.timer.Stop()
					case *tickTask:
						t.ticker.Stop()
					}
					return true
				})
				return
			case <-ticker.C:
				waitDel := make([]interface{}, 0, 8)
				w.delayQueue.Range(func(key, value interface{}) bool {
					switch t := value.(type) {
					case *delayTask:
						select {
						case <-t.timer.C:
							t.timer.Stop()
							t.f()
							waitDel = append(waitDel, key)
						default:
						}
					case *tickTask:
						select {
						case <-t.ticker.C:
							t.ticker.Stop()
							t.f()
							waitDel = append(waitDel, key)
						default:
						}
					}
					return true
				})

				for i := range waitDel {
					w.delayQueue.Delete(waitDel[i])
				}
			}
		}
	}()
}

type delayTask struct {
	timer *time.Timer
	f     func()
}

type tickTask struct {
	ticker *time.Ticker
	f      func()
}
