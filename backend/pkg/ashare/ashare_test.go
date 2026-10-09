package ashare

import (
	"testing"
	"time"
)

// 交易所口径：前收盘 × (1 ± 比例)，四舍五入到分。这里用整数分逐价位核对。
func TestLimitExactCents(t *testing.T) {
	for cents := int64(100); cents <= 100000; cents++ {
		for _, pct := range []int64{5, 10, 20, 30} {
			up, down := Limit(float64(cents)/100, float64(pct)/100)
			wantUp := (cents*(100+pct) + 50) / 100
			wantDown := (cents*(100-pct) + 50) / 100
			if int64(up*100+0.5) != wantUp || int64(down*100+0.5) != wantDown {
				t.Fatalf("prev=%d pct=%d up=%v down=%v want %d %d", cents, pct, up, down, wantUp, wantDown)
			}
		}
	}
	if up, _ := Limit(1.15, 0.10); up != 1.27 {
		t.Fatal(up)
	}
}

func TestLimitBoards(t *testing.T) {
	main, err := Ratio("600519.SH", false)
	if err != nil || main != 0.10 {
		t.Fatal(main, err)
	}
	before := time.Date(2026, 7, 3, 0, 0, 0, 0, time.Local)
	after := time.Date(2026, 7, 6, 0, 0, 0, 0, time.Local)
	if st, _ := RatioOn("600519.SH", true, before); st != 0.05 {
		t.Fatal(st)
	}
	if st, _ := RatioOn("600519.SH", true, after); st != 0.10 {
		t.Fatal(st)
	}
	if st, _ := RatioOn("300001.SZ", true, before); st != 0.20 {
		t.Fatal("gem st", st)
	}
	if st, _ := RatioOn("688001.SH", true, before); st != 0.20 {
		t.Fatal("star st", st)
	}
	if st, _ := RatioOn("830001.BJ", true, before); st != 0.30 {
		t.Fatal("bj st", st)
	}
	gem, _ := Ratio("300750.SZ", false)
	if gem != 0.20 {
		t.Fatal(gem)
	}
	star, _ := Ratio("688981.SH", false)
	if star != 0.20 {
		t.Fatal(star)
	}
	bj, _ := Ratio("830001.BJ", false)
	if bj != 0.30 {
		t.Fatal(bj)
	}
	up, down := Limit(10.03, 0.10)
	if up != 11.03 || down != 9.03 {
		t.Fatalf("up=%v down=%v", up, down)
	}
	if Clamp(12, up, down) != up || Clamp(1, up, down) != down {
		t.Fatal("clamp")
	}
}

func TestLotsAndSell(t *testing.T) {
	n, err := RoundBuy("600519.SH", 250)
	if err != nil || n != 200 {
		t.Fatal(n, err)
	}
	n, _ = RoundBuy("688981.SH", 180)
	if n != 0 {
		t.Fatal(n)
	}
	n, _ = RoundBuy("688981.SH", 201)
	if n != 201 {
		t.Fatal(n)
	}
	if n, _ = RoundBuy("830001.BJ", 99); n != 0 {
		t.Fatal("bj below 100", n)
	}
	if n, _ = RoundBuy("830001.BJ", 101); n != 101 {
		t.Fatal("bj step by share", n)
	}
	sells := []struct {
		code       string
		have, want int
		ok         bool
	}{
		{"000001.SZ", 250, 50, true},   // 余股单独一次卖出
		{"000001.SZ", 250, 150, true},  // 余股连同整手
		{"000001.SZ", 250, 130, false}, // 拆开余股
		{"000001.SZ", 300, 50, false},
		{"000001.SZ", 250, 250, true},
		{"300750.SZ", 250, 200, true},
		{"688981.SH", 250, 30, false}, // 科创板单笔不足 200
		{"688981.SH", 250, 210, true},
		{"688981.SH", 250, 250, true},
		{"688981.SH", 150, 150, true}, // 不足 200 时一次卖完
		{"688981.SH", 150, 100, false},
		{"830001.BJ", 150, 50, false},
		{"830001.BJ", 150, 101, true},
		{"830001.BJ", 60, 60, true},
		{"600519.SH", 100, 200, false},
	}
	for _, c := range sells {
		ok, err := CanSell(c.code, c.have, c.want)
		if err != nil || ok != c.ok {
			t.Fatalf("CanSell(%s, %d, %d)=%v want %v", c.code, c.have, c.want, ok, c.ok)
		}
	}
}

func TestFeeAndT1(t *testing.T) {
	fee := DefaultFee()
	if fee.Cost(1000, false) != 5.01 {
		t.Fatal(fee.Cost(1000, false))
	}
	sell := fee.Cost(100000, true)
	if sell < 50 {
		t.Fatal(sell)
	}
	if Sellable(1000, 400) != 600 || Sellable(100, 100) != 0 {
		t.Fatal("t+1")
	}
}
