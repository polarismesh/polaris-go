// Tencent is pleased to support the open source community by making polaris-go available.
//
// Copyright (C) 2019 THL A29 Limited, a Tencent company. All rights reserved.
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
// specific language governing permissionsr and limitations under the License.
//

package addons

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"

	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
)

func Test_maxGauge_Set(t *testing.T) {
	gauge := NewMaxGaugeVec(prometheus.GaugeOpts{
		Name: "Test_maxGauge_Set",
		Help: "Test_maxGauge_Set",
	}, []string{"key-1", "key-2"})

	totalCnt := 10

	chOne := make(chan float64, 1024)
	chTwo := make(chan float64, 1024)
	maxOne := uint64(0)
	maxTwo := uint64(0)

	ctx, cancel := context.WithCancel(context.Background())
	go func(ctx context.Context) {
		for {
			select {
			case ret := <-chOne:
				if ret > float64(maxOne) {
					maxOne = math.Float64bits(ret)
				}
			case ret := <-chTwo:
				if ret > float64(maxTwo) {
					maxTwo = math.Float64bits(ret)
				}
			case <-ctx.Done():
				return
			}
		}
	}(ctx)

	wg := &sync.WaitGroup{}
	wg.Add(totalCnt)
	randPool := rand.NewScalableRand()

	for i := 0; i < totalCnt; i++ {
		go func() {
			defer wg.Done()
			for p := 0; p < 1000; p++ {
				val := randPool.Intn(math.MaxInt64)
				gauge.With(map[string]string{
					"key-1": "1",
					"key-2": "1",
				}).Set(float64(val))
				chOne <- float64(val)

				val = randPool.Intn(math.MaxInt64)
				gauge.With(map[string]string{
					"key-1": "2",
					"key-2": "2",
				}).Set(float64(val))
				chTwo <- float64(val)
			}
		}()
	}

	wg.Wait()
	time.Sleep(time.Second)
	cancel()

	t.Logf("max one : %d", maxOne)
	t.Logf("max two : %d", maxTwo)

	assert.Equal(t, maxOne, gauge.With(map[string]string{
		"key-1": "1",
		"key-2": "1",
	}).(*maxGauge).valBits)

	assert.Equal(t, maxTwo, gauge.With(map[string]string{
		"key-1": "2",
		"key-2": "2",
	}).(*maxGauge).valBits)
}
