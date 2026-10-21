package biz

import (
	"testing"
)

// M13-T12：market 截面写入的 sector.*/external.* 键，factorCatalog 必须注册成
// 外围维度事实（标签/单位/精度对齐），消费键名与写入键一一对应。
func TestFactorFactsExternalCatalog(t *testing.T) {
	facts := factorFacts(map[string]float64{
		"sector.heat":         5.4,
		"sector.pct_chg":      2.345,
		"external.usdcnh_chg": 0.04321,
		"external.gold_chg":   1.5,
	})
	byID := map[string]Fact{}
	for _, f := range facts {
		byID[f.ID] = f
	}
	cases := []struct {
		id, label, unit string
		value           float64
	}{
		{"F:sector.heat", "板块热度", "", 5.4},
		{"F:sector.pct_chg", "板块平均涨幅", "%", 2.35},
		{"F:external.usdcnh_chg", "离岸人民币涨跌幅", "%", 0.043},
		{"F:external.gold_chg", "黄金涨跌幅", "%", 1.5},
	}
	for _, c := range cases {
		f, ok := byID[c.id]
		if !ok {
			t.Fatalf("catalog must consume %s", c.id)
		}
		if f.Dim != External {
			t.Fatalf("%s dim must be External, got %v", c.id, f.Dim)
		}
		if f.Label != c.label || f.Unit != c.unit {
			t.Fatalf("%s label/unit = %q/%q, want %q/%q", c.id, f.Label, f.Unit, c.label, c.unit)
		}
		if f.Value != c.value {
			t.Fatalf("%s value = %v, want %v", c.id, f.Value, c.value)
		}
	}
	// 外围维度有数据判定：命中 sector/external 键即视为有外围数据（不送中性分）。
	p := FactPack{Facts: facts}
	if !hasDimData(&p, External) {
		t.Fatal("external keys must satisfy hasDimData(External)")
	}
}
