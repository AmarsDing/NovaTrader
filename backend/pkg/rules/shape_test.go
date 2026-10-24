package rules

import (
	"math"
	"testing"
)

// TestShapeOf 表驱动：rising/holding/fading 三态 + 各取数缺口一律 missing。
func TestShapeOf(t *testing.T) {
	// 边界值用二进制可精确表示的比率（8/10/0.25），其余用宽余量挡浮点毛刺。
	cases := []struct {
		name   string
		series []float64
		recent int
		base   int
		band   float64
		want   Shape
	}{
		{"rising 上移超带", joinFloats(flatFloats(10, 8), flatFloats(3, 11)), 3, 10, 0.25, ShapeRising},   // dev=0.375
		{"fading 下移超带", joinFloats(flatFloats(10, 8), flatFloats(3, 4)), 3, 10, 0.25, ShapeFading},    // dev=-0.5
		{"holding 带内漂移", joinFloats(flatFloats(10, 10), flatFloats(3, 10.2)), 3, 10, 0.03, ShapeHolding}, // dev=0.02
		{"holding 压上边不改判", joinFloats(flatFloats(10, 8), flatFloats(3, 10)), 3, 10, 0.25, ShapeHolding}, // dev=0.25 压边
		{"holding 压下边不改判", joinFloats(flatFloats(10, 10), flatFloats(3, 7.5)), 3, 10, 0.25, ShapeHolding}, // dev=-0.25 压边
		{"missing 根数不足", flatFloats(12, 10), 3, 10, 0.03, ShapeMissing},
		{"missing 空序列", nil, 3, 10, 0.03, ShapeMissing},
		{"missing 近窗含 0 价", joinFloats(flatFloats(12, 10), []float64{0}), 3, 10, 0.03, ShapeMissing},
		{"missing 基线窗含 0 价", joinFloats([]float64{0}, flatFloats(12, 10)), 3, 10, 0.03, ShapeMissing},
		{"missing recent 非正", flatFloats(20, 10), 0, 10, 0.03, ShapeMissing},
		{"missing base 非正", flatFloats(20, 10), 3, 0, 0.03, ShapeMissing},
		{"missing band 为负", flatFloats(20, 10), 3, 10, -0.01, ShapeMissing},
		{"missing band NaN", flatFloats(20, 10), 3, 10, math.NaN(), ShapeMissing},
		{"band 0 微涨即 rising", joinFloats(flatFloats(10, 10), flatFloats(3, 10.1)), 3, 10, 0, ShapeRising},
		{"band 0 持平即 holding", flatFloats(13, 10), 3, 10, 0, ShapeHolding},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShapeOf(tc.series, tc.recent, tc.base, tc.band); got != tc.want {
				t.Fatalf("ShapeOf = %s, want %s", got, tc.want)
			}
		})
	}
}

// TestShapeOfSnapshot 钉当日价参与近窗：同一批 13 根已收盘日线，
// 有当日价判 rising、无当日价判 holding、当日 0 价视为未取到回落 holding。
func TestShapeOfSnapshot(t *testing.T) {
	bars := make([]Bar, 13)
	for i := range bars {
		bars[i] = Bar{Close: 10}
	}
	base := Snapshot{Bars: bars}

	rising := base
	rising.Today = &Bar{Close: 11.5}
	if got := ShapeOfSnapshot(rising, 3, 10, 0.03); got != ShapeRising {
		t.Fatalf("含当日 = %s, want rising（近窗 [10,10,11.5] dev=0.05）", got)
	}
	if got := ShapeOfSnapshot(base, 3, 10, 0.03); got != ShapeHolding {
		t.Fatalf("无当日 = %s, want holding", got)
	}
	zero := base
	zero.Today = &Bar{Close: 0}
	if got := ShapeOfSnapshot(zero, 3, 10, 0.03); got != ShapeHolding {
		t.Fatalf("当日 0 价 = %s, want holding（视为未取到，剔除不污染）", got)
	}
}

// TestShapeOfSnapshotMissing 快照可判长度不够或已收盘线含 0 价 → missing。
func TestShapeOfSnapshotMissing(t *testing.T) {
	short := Snapshot{Bars: make([]Bar, 12)}
	for i := range short.Bars {
		short.Bars[i] = Bar{Close: 10}
	}
	if got := ShapeOfSnapshot(short, 3, 10, 0.03); got != ShapeMissing {
		t.Fatalf("根数不足 = %s, want missing", got)
	}
	dirty := Snapshot{Bars: make([]Bar, 13)}
	for i := range dirty.Bars {
		dirty.Bars[i] = Bar{Close: 10}
	}
	dirty.Bars[5].Close = 0
	if got := ShapeOfSnapshot(dirty, 3, 10, 0.03); got != ShapeMissing {
		t.Fatalf("基线窗含 0 价 = %s, want missing", got)
	}
}

func TestLoadShape(t *testing.T) {
	def := LoadShape(nil)
	if def.Recent != 3 || def.Base != 10 || def.Band != 0.03 {
		t.Fatalf("defaults %+v", def)
	}
	get := func(vals map[string]float64) Lookup {
		return func(key string) (float64, bool) { v, ok := vals[key]; return v, ok }
	}
	p := LoadShape(get(map[string]float64{"m06.shape.recent": 5, "m06.shape.base": 20, "m06.shape.band": 0.05}))
	if p.Recent != 5 || p.Base != 20 || p.Band != 0.05 {
		t.Fatalf("override %+v", p)
	}
	// band=0 语义=扫描层关停本件：Loader 只透传，不裁决。
	off := LoadShape(get(map[string]float64{"m06.shape.band": 0}))
	if off.Band != 0 {
		t.Fatalf("band 透传 %+v", off)
	}
}

func flatFloats(n int, v float64) []float64 {
	xs := make([]float64, n)
	for i := range xs {
		xs[i] = v
	}
	return xs
}

func joinFloats(a, b []float64) []float64 {
	return append(append([]float64{}, a...), b...)
}
