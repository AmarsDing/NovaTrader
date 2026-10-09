// Package metrics 提供一个不依赖外部组件的文本指标。
package metrics

import (
	"fmt"
	"io"
	"math"
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

// Write 按 Prometheus 文本格式写出当前计数。
func Write(w io.Writer) error {
	mu.Lock()
	list := append([]*counter(nil), counters...)
	glist := append([]*gauge(nil), gauges...)
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
	return nil
}
