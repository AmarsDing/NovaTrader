package ashare

import "testing"

func TestCage(t *testing.T) {
	cases := []struct {
		code  string
		sell  bool
		bench float64
		want  float64
	}{
		{"600519.SH", false, 10.00, 10.20}, // 2% 宽于 0.1 元
		{"600519.SH", false, 3.00, 3.10},   // 低价股用 10 个价位
		{"600519.SH", true, 10.00, 9.80},
		{"600519.SH", true, 3.00, 2.90},
		{"300750.SZ", false, 3.00, 3.10},
		{"688981.SH", false, 3.00, 3.06}, // 科创板没有 10 价位兜底
		{"688981.SH", true, 3.00, 2.94},
		{"830001.BJ", false, 10.50, 11.02}, // 11.025 向下取
		{"830001.BJ", true, 9.50, 9.03},    // 9.025 向上取
		{"830001.BJ", false, 1.00, 1.10},
		{"600519.SH", false, 10.01, 10.21}, // 10.2102 向下取
		{"600519.SH", true, 10.01, 9.81},   // 9.8098 向上取
	}
	for _, c := range cases {
		got, err := Cage(c.code, c.sell, c.bench)
		if err != nil || got != c.want {
			t.Fatalf("Cage(%s, sell=%v, %v)=%v want %v", c.code, c.sell, c.bench, got, c.want)
		}
	}
}

func TestCageBenchmarkAndLimits(t *testing.T) {
	if CageBenchmark(false, 9.99, 10.00, 9.98, 9.50) != 10.00 {
		t.Fatal("buy uses ask1")
	}
	if CageBenchmark(false, 9.99, 0, 9.98, 9.50) != 9.99 {
		t.Fatal("buy falls back to bid1")
	}
	if CageBenchmark(true, 0, 0, 0, 9.50) != 9.50 {
		t.Fatal("falls back to prev close")
	}
	for code, want := range map[string]int{"600519.SH": 1_000_000, "000001.SZ": 1_000_000, "300750.SZ": 300_000, "688981.SH": 100_000, "830001.BJ": 1_000_000} {
		if got, _ := MaxOrderVolume(code); got != want {
			t.Fatal(code, got)
		}
	}
	if !OnTick(10.01) || OnTick(10.015) {
		t.Fatal("tick")
	}
}
