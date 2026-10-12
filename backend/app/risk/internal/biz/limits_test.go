package biz

import (
	"testing"

	"server/pkg/risk"
)

func findLimit(items []LimitItem, key string) LimitItem {
	for _, item := range items {
		if item.Key == key {
			return item
		}
	}
	return LimitItem{Limit: -2, Used: -2, Ratio: -2}
}

func TestLimitsUsage(t *testing.T) {
	r := newRig(t, nil)
	// 权益 100 万，持仓市值 30 万（银行 20 万 + 券商 10 万）
	r.account(1_000_000, 500_000,
		risk.Holding{Symbol: "000001.SZ", MarketValue: 200_000, Sector: "银行"},
		risk.Holding{Symbol: "600030.SH", MarketValue: 100_000, Sector: "券商"},
	)

	items := r.e.Limits("")

	single := findLimit(items, "risk.max_single_pct")
	if single.Used != 200_000 {
		t.Fatalf("单票用量 = %v，want 200000", single.Used)
	}
	// HOT 相位系数 1.0：单票上限 = 1e6 * 0.05
	if single.Limit != 50_000 {
		t.Fatalf("单票上限 = %v，want 50000", single.Limit)
	}

	total := findLimit(items, "risk.max_total_pct")
	if total.Used != 300_000 {
		t.Fatalf("总仓用量 = %v，want 300000", total.Used)
	}
	if total.Limit != 800_000 {
		t.Fatalf("总仓上限 = %v，want 800000", total.Limit)
	}
	if total.Ratio < 0.37 || total.Ratio > 0.38 {
		t.Fatalf("总仓占比 = %v，want ≈0.375", total.Ratio)
	}

	sector := findLimit(items, "risk.max_sector_pct")
	if sector.Used != 200_000 {
		t.Fatalf("板块用量 = %v，want 200000（最大单板块）", sector.Used)
	}
	if sector.Limit != 300_000 {
		t.Fatalf("板块上限 = %v，want 300000", sector.Limit)
	}

	buys := findLimit(items, "risk.max_daily_buys")
	if buys.Used != 0 || buys.Limit != 10 {
		t.Fatalf("开仓笔数 = %v/%v，want 0/10", buys.Used, buys.Limit)
	}

	loss := findLimit(items, "risk.daily_loss_pct")
	if loss.Used != 0 {
		t.Fatalf("当日亏损用量 = %v，want 0（权益=开盘权益）", loss.Used)
	}
	if loss.Limit != 0.02 {
		t.Fatalf("亏损上限 = %v，want 0.02", loss.Limit)
	}
}

func TestLimitsDailyLoss(t *testing.T) {
	r := newRig(t, nil)
	// 开盘 100 万，现在 97.9 万 → 亏 2.1%
	r.account(979_000, 500_000)
	items := r.e.Limits("")
	loss := findLimit(items, "risk.daily_loss_pct")
	if loss.Used < 0.020 || loss.Used > 0.022 {
		t.Fatalf("当日亏损用量 = %v，want ≈0.021", loss.Used)
	}
	if loss.Ratio <= 1 {
		t.Fatalf("亏损占比 = %v，want >1（已破线）", loss.Ratio)
	}
}

func TestLimitsAccountFilter(t *testing.T) {
	r := newRig(t, nil)
	r.account(1_000_000, 500_000, risk.Holding{Symbol: "000001.SZ", MarketValue: 100_000})
	items := r.e.Limits("SIM")
	total := findLimit(items, "risk.max_total_pct")
	if total.Used != 100_000 {
		t.Fatalf("SIM 总仓用量 = %v，want 100000", total.Used)
	}
	// 选 LIVE：没有 LIVE 账户快照，权益为 0，上限随之归零
	live := r.e.Limits("LIVE")
	lt := findLimit(live, "risk.max_total_pct")
	if lt.Used != 0 || lt.Limit != 0 {
		t.Fatalf("LIVE 总仓 = %v/%v，want 0/0（无快照）", lt.Used, lt.Limit)
	}
}
