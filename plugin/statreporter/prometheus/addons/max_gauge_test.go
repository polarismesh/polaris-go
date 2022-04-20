package addons

import (
	"context"
	"math"
	"sync"
	"testing"
	"time"

	"github.com/polarismesh/polaris-go/pkg/algorithm/rand"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
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
	go func (ctx context.Context)  {
		for {
			select {
			case ret := <- chOne:
				if ret > float64(maxOne) {
					maxOne = math.Float64bits(ret)
				}
			case ret := <- chTwo:
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

	assert.Equal(t, maxOne, gauge.With(map[string]string{
		"key-1": "1",
		"key-2": "1",
	}).(*maxGauge).valBits)

	assert.Equal(t, maxTwo,gauge.With(map[string]string{
		"key-1": "2",
		"key-2": "2",
	}).(*maxGauge).valBits)
}
