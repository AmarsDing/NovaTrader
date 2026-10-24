// Package metrics 提供一个不依赖外部组件的文本指标。
package metrics

import (
	"fmt"
	"io"
	"math"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
)

type counter struct {
	name string
	help string
	n    atomic.Int64
}

type gauge struct {
	name string
	help string
	bits atomic.Uint64
}

var (
	mu       sync.Mutex
	counters []*counter
	gauges   []*gauge
	vecs     []*gaugeVec
)

// Counter 注册一个只增计数器。
func Counter(name, help string) *counter {
	c := &counter{name: name, help: help}
	mu.Lock()
	counters = append(counters, c)
	mu.Unlock()
	return c
}

func (c *counter) Inc() { c.n.Add(1) }

// Gauge 注册一个可增可减的瞬时值，例如最近一轮的延迟。
func Gauge(name, help string) *gauge {
	g := &gauge{name: name, help: help}
	mu.Lock()
	gauges = append(gauges, g)
	mu.Unlock()
	return g
}

func (g *gauge) Set(v float64) { g.bits.Store(math.Float64bits(v)) }

func (g *gauge) Value() float64 { return math.Float64frombits(g.bits.Load()) }

// gaugeVec 是带单一标签的 gauge 组，例如按域分维的待发年龄。
type gaugeVec struct {
	name   string
	help   string
	label  string
	mu     sync.RWMutex
	series map[string]float64
}

// GaugeVec 注册一个带 label 的 gauge 组。
func GaugeVec(name, help, label string) *gaugeVec {
	v := &gaugeVec{name: name, help: help, label: label, series: map[string]float64{}}
	mu.Lock()
	vecs = append(vecs, v)
	mu.Unlock()
	return v
}

func (v *gaugeVec) Set(labelValue string, val float64) {
	v.mu.Lock()
	v.series[labelValue] = val
	v.mu.Unlock()
}

// Replace 用整张快照替换序列；消失的 label 值随之清出，不残留陈旧分维。
func (v *gaugeVec) Replace(snapshot map[string]float64) {
	next := make(map[string]float64, len(snapshot))
	for k, val := range snapshot {
		next[k] = val
	}
	v.mu.Lock()
	v.series = next
	v.mu.Unlock()
}

// Values 返回当前分维快照，供同包写出与跨包测试断言使用。
func (v *gaugeVec) Values() map[string]float64 {
	v.mu.RLock()
	defer v.mu.RUnlock()
	out := make(map[string]float64, len(v.series))
	for k, val := range v.series {
		out[k] = val
	}
	return out
}

// Write 按 Prometheus 文本格式写出当前计数。
func Write(w io.Writer) error {
	mu.Lock()
	list := append([]*counter(nil), counters...)
	glist := append([]*gauge(nil), gauges...)
	vlist := append([]*gaugeVec(nil), vecs...)
	mu.Unlock()
	for _, c := range list {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s counter\n%s %d\n", c.name, c.help, c.name, c.name, c.n.Load()); err != nil {
			return err
		}
	}
	for _, g := range glist {
		v := strconv.FormatFloat(g.Value(), 'g', -1, 64)
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n%s %s\n", g.name, g.help, g.name, g.name, v); err != nil {
			return err
		}
	}
	for _, vec := range vlist {
		if _, err := fmt.Fprintf(w, "# HELP %s %s\n# TYPE %s gauge\n", vec.name, vec.help, vec.name); err != nil {
			return err
		}
		snap := vec.Values()
		keys := make([]string, 0, len(snap))
		for k := range snap {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			v := strconv.FormatFloat(snap[k], 'g', -1, 64)
			if _, err := fmt.Fprintf(w, "%s{%s=%q} %s\n", vec.name, vec.label, k, v); err != nil {
				return err
			}
		}
	}
	return nil
}
