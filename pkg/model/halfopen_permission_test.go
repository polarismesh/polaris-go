// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2021 Tencent. All rights reserved.
//
// Licensed under the BSD 3-Clause License (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// https://opensource.org/licenses/BSD-3-Clause
//
// Unless required by applicable law or agreed to in writing, software distributed
// under the License is distributed on an "AS IS" BASIS, WITHOUT WARRANTIES OR
// CONDITIONS OF ANY KIND, either express or implied. See the License for the
// specific language governing permissions and limitations under the License.

package model

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestHalfOpenStatusCancelPermissionReturnsQuota(t *testing.T) {
	status := NewHalfOpenStatus("probe", time.Now(), 1).(*HalfOpenStatus)
	require.True(t, status.AcquirePermission())
	require.False(t, status.AcquirePermission())
	require.True(t, status.CancelPermission())
	require.Zero(t, status.AllocatedCount())
	require.True(t, status.AcquirePermission())
	require.Equal(t, int64(1), status.AllocatedCount())
}

func TestHalfOpenStatusCancelPermissionDoesNotReport(t *testing.T) {
	status := NewHalfOpenStatus("probe", time.Now(), 1).(*HalfOpenStatus)
	require.True(t, status.AcquirePermission())
	require.True(t, status.CancelPermission())
	require.Zero(t, status.FinishedCount())
	require.Empty(t, status.calledResult)
	require.False(t, status.triggered)
	require.Zero(t, status.scheduled)
	require.Equal(t, HalfOpen, status.CalNextStatus())

	require.True(t, status.AcquirePermission())
	require.True(t, status.Release(true))
	require.Equal(t, Close, status.CalNextStatus())
}

func TestHalfOpenStatusCancelPermissionWithoutReservation(t *testing.T) {
	for _, maxRequest := range []int{-1, 0, 1} {
		status := NewHalfOpenStatus("probe", time.Now(), maxRequest).(*HalfOpenStatus)
		require.False(t, status.CancelPermission())
		require.False(t, status.CancelPermission())
		require.Zero(t, status.AllocatedCount())
	}
}

func TestHalfOpenStatusCancelPermissionPreservesRealResults(t *testing.T) {
	for _, success := range []bool{true, false} {
		status := NewHalfOpenStatus("probe", time.Now(), 2).(*HalfOpenStatus)
		require.True(t, status.AcquirePermission())
		require.True(t, status.AcquirePermission())
		require.False(t, status.Report(true))
		require.True(t, status.CancelPermission())
		require.Equal(t, int64(1), status.AllocatedCount())
		require.Equal(t, []bool{true}, status.calledResult)
		require.Equal(t, HalfOpen, status.CalNextStatus())
		require.True(t, status.AcquirePermission())
		require.True(t, status.Report(success))
		if success {
			require.Equal(t, Close, status.CalNextStatus())
		} else {
			require.Equal(t, Open, status.CalNextStatus())
		}
	}
}

func TestHalfOpenStatusCancelPermissionKeepsOtherReservation(t *testing.T) {
	status := NewHalfOpenStatus("probe", time.Now(), 2).(*HalfOpenStatus)
	require.True(t, status.AcquirePermission())
	var workers sync.WaitGroup
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 1000; j++ {
				if status.AcquirePermission() && !status.CancelPermission() {
					t.Error("reserved permission could not be canceled")
				}
			}
		}()
	}
	workers.Wait()
	require.Equal(t, int64(1), status.AllocatedCount())
	require.Zero(t, status.FinishedCount())
	require.Equal(t, HalfOpen, status.CalNextStatus())
}

func TestHalfOpenStatusCancelPermissionDoesNotAffectNewState(t *testing.T) {
	oldStatus := NewHalfOpenStatus("probe", time.Now(), 1).(*HalfOpenStatus)
	newStatus := NewHalfOpenStatus("probe", time.Now(), 1).(*HalfOpenStatus)
	require.True(t, oldStatus.AcquirePermission())
	require.True(t, newStatus.AcquirePermission())
	require.True(t, oldStatus.CancelPermission())
	require.Zero(t, oldStatus.AllocatedCount())
	require.Equal(t, int64(1), newStatus.AllocatedCount())
}

func TestHalfOpenStatusConcurrentPermissionCountStaysWithinQuota(t *testing.T) {
	const quota = 4
	status := NewHalfOpenStatus("probe", time.Now(), quota).(*HalfOpenStatus)
	var workers sync.WaitGroup
	stop := make(chan struct{})
	checked := make(chan struct{})
	go func() {
		defer close(checked)
		for {
			allocated := status.AllocatedCount()
			if allocated < 0 || allocated > quota {
				t.Errorf("allocated count %d is outside quota [0, %d]", allocated, quota)
				return
			}
			select {
			case <-stop:
				return
			default:
			}
		}
	}()
	for i := 0; i < 16; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 1000; j++ {
				if status.AcquirePermission() && !status.CancelPermission() {
					t.Error("reserved permission could not be canceled")
				}
			}
		}()
	}
	workers.Wait()
	close(stop)
	<-checked
	require.Zero(t, status.AllocatedCount())
	require.Zero(t, status.FinishedCount())
	require.Empty(t, status.calledResult)
}

func TestHalfOpenStatusCancelPermissionConcurrentWithRealResult(t *testing.T) {
	status := NewHalfOpenStatus("probe", time.Now(), 2).(*HalfOpenStatus)
	require.True(t, status.AcquirePermission())
	var workers sync.WaitGroup
	var cancellations int64
	workers.Add(1)
	go func() {
		defer workers.Done()
		if status.Release(true) {
			t.Error("one real result must not close a two-probe state")
		}
	}()
	for i := 0; i < 8; i++ {
		workers.Add(1)
		go func() {
			defer workers.Done()
			for j := 0; j < 1000; j++ {
				if status.AcquirePermission() {
					if !status.CancelPermission() {
						t.Error("reserved permission could not be canceled")
					}
					atomic.AddInt64(&cancellations, 1)
				}
			}
		}()
	}
	workers.Wait()
	require.Positive(t, atomic.LoadInt64(&cancellations))
	require.Equal(t, int64(1), status.AllocatedCount())
	require.Equal(t, int64(1), status.FinishedCount())
	require.Equal(t, []bool{true}, status.calledResult)
	require.Equal(t, HalfOpen, status.CalNextStatus())
	require.True(t, status.AcquirePermission())
	require.True(t, status.Release(true))
	require.Equal(t, Close, status.CalNextStatus())
}
