package addons

import (
	"fmt"
	"math"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"google.golang.org/protobuf/proto"
)

type MaxGauge interface {
	prometheus.Gauge
}

func NewMaxGauge(opts prometheus.GaugeOpts) MaxGauge {
	desc := prometheus.NewDesc(
		prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
		opts.Help,
		nil,
		opts.ConstLabels,
	)

	holder := reflect.ValueOf(desc)
	result := &maxGauge{desc: desc, labelPairs: holder.FieldByName("constLabelPairs").Interface().([]*dto.LabelPair)}
	result.init(result)
	return result
}

type maxGauge struct {
	valBits uint64

	selfCollector

	desc       *prometheus.Desc
	labelPairs []*dto.LabelPair
}

func (g *maxGauge) Desc() *prometheus.Desc {
	return g.desc
}

func (g *maxGauge) Set(val float64) {
	newVal := math.Float64bits(val)
	for {
		data := atomic.LoadUint64(&g.valBits)
		saveVal := math.Float64frombits(data)
		if saveVal >= val {
			return
		}
		if atomic.CompareAndSwapUint64(&g.valBits, data, newVal) {
			return
		}
	}
}

func (g *maxGauge) SetToCurrentTime() {
	g.Set(float64(time.Now().UnixNano()) / 1e9)
}

func (g *maxGauge) Inc() {
}

func (g *maxGauge) Dec() {
}

func (g *maxGauge) Add(val float64) {
}

func (g *maxGauge) Sub(val float64) {
}

func (g *maxGauge) Write(out *dto.Metric) error {
	val := math.Float64frombits(atomic.LoadUint64(&g.valBits))
	return populateMetric(prometheus.GaugeValue, val, g.labelPairs, nil, out)
}

func populateMetric(
	t prometheus.ValueType,
	v float64,
	labelPairs []*dto.LabelPair,
	e *dto.Exemplar,
	m *dto.Metric,
) error {
	m.Label = labelPairs
	switch t {
	case prometheus.CounterValue:
		m.Counter = &dto.Counter{Value: proto.Float64(v), Exemplar: e}
	case prometheus.GaugeValue:
		m.Gauge = &dto.Gauge{Value: proto.Float64(v)}
	case prometheus.UntypedValue:
		m.Untyped = &dto.Untyped{Value: proto.Float64(v)}
	default:
		return fmt.Errorf("encountered unknown type %v", t)
	}
	return nil
}

type MaxGaugeVec struct {
	*prometheus.MetricVec
}

func NewMaxGaugeVec(opts prometheus.GaugeOpts, labelNames []string) *MaxGaugeVec {
	desc := prometheus.NewDesc(
		prometheus.BuildFQName(opts.Namespace, opts.Subsystem, opts.Name),
		opts.Help,
		labelNames,
		opts.ConstLabels,
	)
	return &MaxGaugeVec{
		MetricVec: prometheus.NewMetricVec(desc, func(lvs ...string) prometheus.Metric {
			result := &maxGauge{desc: desc, labelPairs: prometheus.MakeLabelPairs(desc, lvs)}
			result.init(result) // Init self-collection.
			return result
		}),
	}
}

func (v *MaxGaugeVec) GetMetricWithLabelValues(lvs ...string) (MaxGauge, error) {
	metric, err := v.MetricVec.GetMetricWithLabelValues(lvs...)
	if metric != nil {
		return metric.(MaxGauge), err
	}
	return nil, err
}

func (v *MaxGaugeVec) GetMetricWith(labels prometheus.Labels) (MaxGauge, error) {
	metric, err := v.MetricVec.GetMetricWith(labels)
	if metric != nil {
		return metric.(MaxGauge), err
	}
	return nil, err
}

func (v *MaxGaugeVec) WithLabelValues(lvs ...string) MaxGauge {
	g, err := v.GetMetricWithLabelValues(lvs...)
	if err != nil {
		panic(err)
	}
	return g
}

func (v *MaxGaugeVec) With(labels prometheus.Labels) MaxGauge {
	g, err := v.GetMetricWith(labels)
	if err != nil {
		panic(err)
	}
	return g
}

func (v *MaxGaugeVec) CurryWith(labels prometheus.Labels) (*MaxGaugeVec, error) {
	vec, err := v.MetricVec.CurryWith(labels)
	if vec != nil {
		return &MaxGaugeVec{vec}, err
	}
	return nil, err
}

func (v *MaxGaugeVec) MustCurryWith(labels prometheus.Labels) *MaxGaugeVec {
	vec, err := v.CurryWith(labels)
	if err != nil {
		panic(err)
	}
	return vec
}

type selfCollector struct {
	self prometheus.Metric
}

func (c *selfCollector) init(self prometheus.Metric) {
	c.self = self
}

func (c *selfCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.self.Desc()
}

func (c *selfCollector) Collect(ch chan<- prometheus.Metric) {
	ch <- c.self
}
