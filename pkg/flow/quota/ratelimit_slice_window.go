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
	"sync"
	"sync/atomic"
)

//创建滑窗
func NewSlidingWindow(slideCount int, intervalMs int) *SlidingWindow {
	slidingWindow := &SlidingWindow{}
	slidingWindow.intervalMs = intervalMs
	slidingWindow.slideCount = slideCount
	slidingWindow.mutex = &sync.Mutex{}
	slidingWindow.windowLengthMs = intervalMs / slideCount
	slidingWindow.windowArray = make([]*Window, slideCount)
	for i := 0; i < slideCount; i++ {
		slidingWindow.windowArray[i] = &Window{}
	}
	return slidingWindow
}

//计算起始滑窗
func (s *SlidingWindow) calculateWindowStart(curTimeMs int64) int64 {
	return CalculateStartTimeMilli(curTimeMs, int64(s.windowLengthMs))
}

//计算起始滑窗
func CalculateStartTimeMilli(curTimeMs int64, interval int64) int64 {
	return curTimeMs - curTimeMs%interval
}

//计算时间下标
func (s *SlidingWindow) calculateTimeIdx(curTimeMs int64) int {
	timeId := curTimeMs / int64(s.windowLengthMs)
	return int(timeId % int64(s.slideCount))
}

//当前窗口
func (s *SlidingWindow) currentWindow(curTimeMs int64, reset bool) (*Window, *Window) {
	idx := s.calculateTimeIdx(curTimeMs)
	windowStart := s.calculateWindowStart(curTimeMs)
	oldWindow := s.windowArray[idx]
	oldWindowStart := atomic.LoadInt64(&oldWindow.WindowStart)
	if oldWindowStart == windowStart {
		return oldWindow, nil
	} else if !reset {
		return nil, nil
	} else {
		s.mutex.Lock()
		expiredWindow := oldWindow.reset(oldWindowStart, windowStart)
		s.mutex.Unlock()
		return oldWindow, expiredWindow
	}
}

//原子增加，并返回当前bucket
func (s *SlidingWindow) AddAndGetCurrentPassed(curTimeMs int64, value uint32) (uint32, *Window) {
	window, expiredWindow := s.currentWindow(curTimeMs, true)
	return window.addAndGetPassed(value), expiredWindow
}

//原子增加，并返回当前bucket
func (s *SlidingWindow) AddAndGetCurrentLimited(curTimeMs int64, value uint32) (uint32, *Window) {
	window, expiredWindow := s.currentWindow(curTimeMs, true)
	return window.addAndGetLimited(value), expiredWindow
}

//获取上报数据
func (s *SlidingWindow) AcquireCurrentValues(curTimeMs int64) (uint32, uint32, *Window) {
	window, expiredWindow := s.currentWindow(curTimeMs, true)
	passed := window.swapPassed()
	limited := window.swapLimited()
	return passed, limited, expiredWindow
}

//获取上报数据
func (s *SlidingWindow) TouchCurrentPassed(curTimeMs int64) (uint32, *Window) {
	window, expiredWindow := s.currentWindow(curTimeMs, true)
	passed := window.addAndGetPassed(0)
	return passed, expiredWindow
}

//滑窗通用实现
type SlidingWindow struct {
	//单个窗口长度
	windowLengthMs int
	//所有窗口总长度
	intervalMs int
	//更新锁
	mutex *sync.Mutex
	//滑窗列表
	windowArray []*Window
	//滑窗数
	slideCount int
}

//重置窗口,返回过期的窗口
func (w *Window) reset(oldWindowStart int64, windowStart int64) *Window {
	if atomic.CompareAndSwapInt64(&w.WindowStart, oldWindowStart, windowStart) {
		passedValue := atomic.SwapUint32(&w.PassedValue, 0)
		limitedValue := atomic.SwapUint32(&w.LimitedValue, 0)
		return &Window{
			WindowStart:  oldWindowStart,
			PassedValue:  passedValue,
			LimitedValue: limitedValue,
		}
	}
	return nil
}

//单个窗口
type Window struct {
	//起始时间
	WindowStart int64
	//通过数
	PassedValue uint32
	//被限流数
	LimitedValue uint32
}

//原子增加通过数
func (w *Window) addAndGetPassed(value uint32) uint32 {
	return atomic.AddUint32(&w.PassedValue, value)
}

//原子增加被限流数
func (w *Window) addAndGetLimited(value uint32) uint32 {
	return atomic.AddUint32(&w.LimitedValue, value)
}

//原子增加通过数
func (w *Window) swapPassed() uint32 {
	return atomic.SwapUint32(&w.PassedValue, 0)
}

//原子增加被限流数
func (w *Window) swapLimited() uint32 {
	return atomic.SwapUint32(&w.LimitedValue, 0)
}
