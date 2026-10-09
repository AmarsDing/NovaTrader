// 策略注册表：回测按名称建策略，参数搜索和进化提案按 ParamSpec 取值。M07 使用。
package rules

import (
	"fmt"
	"math"
	"sort"
	"sync"
)

// ParamSpec 是一个可调参数。参数搜索和进化提案只能在 Min..Max 内按 Step 取值。
type ParamSpec struct {
	Name    string
	Title   string
	Default float64
	Min     float64
	Max     float64
	Step    float64
}

// Spec 描述一个可注册的策略。每次运行用 New 新建实例。
// Key 把参数名映射为 strategy_config 的键，为空时用 <Name>.<参数名>。进化提案批准时按它写参数表。
type Spec struct {
	Name   string
	Title  string
	Params []ParamSpec
	New    func(params map[string]float64) (Strategy, error)
	Key    func(param string) string
}

// ParamKey 返回参数在 strategy_config 里的键。
func (s Spec) ParamKey(param string) string {
	if s.Key != nil {
		return s.Key(param)
	}
	return s.Name + "." + param
}

var (
	mu       sync.RWMutex
	registry = map[string]Spec{}
)

// Register 注册策略。重名会 panic，便于在 init 阶段发现。
func Register(spec Spec) {
	if spec.Name == "" || spec.New == nil {
		panic("rules: spec needs name and constructor")
	}
	mu.Lock()
	defer mu.Unlock()
	if _, ok := registry[spec.Name]; ok {
		panic("rules: duplicate strategy " + spec.Name)
	}
	registry[spec.Name] = spec
}

// Find 按名称取已注册的策略。
func Find(name string) (Spec, bool) {
	mu.RLock()
	defer mu.RUnlock()
	s, ok := registry[name]
	return s, ok
}

// List 按名称排序返回全部策略。
func List() []Spec {
	mu.RLock()
	defer mu.RUnlock()
	out := make([]Spec, 0, len(registry))
	for _, s := range registry {
		out = append(out, s)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Resolve 用默认值补齐参数，并检查每个值都在 Min..Max 内。未知参数报错。不要求落在步长格点上，提案由 Snap 吸附。
func (s Spec) Resolve(in map[string]float64) (map[string]float64, error) {
	out := make(map[string]float64, len(s.Params))
	known := make(map[string]ParamSpec, len(s.Params))
	for _, p := range s.Params {
		known[p.Name] = p
		out[p.Name] = p.Default
	}
	for k, v := range in {
		p, ok := known[k]
		if !ok {
			return nil, fmt.Errorf("rules: %s has no parameter %q", s.Name, k)
		}
		if math.IsNaN(v) || v < p.Min-1e-9 || v > p.Max+1e-9 {
			return nil, fmt.Errorf("rules: %s.%s=%v out of [%v, %v]", s.Name, k, v, p.Min, p.Max)
		}
		out[k] = v
	}
	return out, nil
}

// Grid 返回参数在 Min..Max 内按 Step 的全部取值。Step 为 0 时只有默认值。
func (p ParamSpec) Grid() []float64 {
	if p.Step <= 0 || p.Max < p.Min {
		return []float64{p.Default}
	}
	var out []float64
	for i := 0; ; i++ {
		v := p.Min + float64(i)*p.Step
		if v > p.Max+p.Step*1e-6 {
			break
		}
		out = append(out, roundStep(v, p.Step))
	}
	return out
}

// Snap 把任意值吸附到最近的格点并限制在范围内。
func (p ParamSpec) Snap(v float64) float64 {
	if v < p.Min {
		v = p.Min
	}
	if v > p.Max {
		v = p.Max
	}
	if p.Step <= 0 {
		return v
	}
	n := math.Round((v - p.Min) / p.Step)
	return roundStep(p.Min+n*p.Step, p.Step)
}

// roundStep 去掉浮点累加误差，保留到步长的精度。
func roundStep(v, step float64) float64 {
	digits := 0
	for s := step; s < 1 && digits < 10; s *= 10 {
		digits++
	}
	scale := math.Pow(10, float64(digits+2))
	return math.Round(v*scale) / scale
}
