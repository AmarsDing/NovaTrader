package risk

import (
	"testing"
	"time"

	"server/pkg/tradecal"
)

func TestParamsSetAndValues(t *testing.T) {
	p := Defaults()
	if err := p.Set("risk.max_single_pct", "0.03"); err != nil || p.MaxSinglePct != 0.03 {
		t.Fatal(err, p.MaxSinglePct)
	}
	if err := p.Set("max_orders_per_sec", "300"); err == nil {
		t.Fatal("must stay below the 300/s HFT line")
	}
	if err := p.Set("max_orders_per_day", "20000"); err == nil {
		t.Fatal("must stay below the 20000/day HFT line")
	}
	if err := p.Set("max_total_pct", "1.5"); err == nil {
		t.Fatal("pct > 1")
	}
	if err := p.Set("nope", "1"); err == nil {
		t.Fatal("unknown key")
	}
	if err := p.Set("phase_scale", `{"ice":0.2,"warm":1}`); err != nil || p.PhaseScale["ICE"] != 0.2 || p.PhaseScale[""] != 0.5 {
		t.Fatal(err, p.PhaseScale)
	}
	if err := p.Set("risk_scale", `{"0":1,"4":1}`); err == nil {
		t.Fatal("risk level 4")
	}
	if err := p.Set("quote_max_delay_ms", "3000"); err != nil || p.QuoteMaxDelay != 3*time.Second {
		t.Fatal(err)
	}
	if err := p.Set("blacklist", " 600519.sh, 000001.SZ ,"); err != nil || !p.Blacklist["600519.SH"] || len(p.Blacklist) != 2 {
		t.Fatal(p.Blacklist)
	}

	// Values 写出的值可以原样读回。
	vals := p.Values()
	q, errs := FromConfig(vals)
	if len(errs) != 0 {
		t.Fatal(errs)
	}
	for k, v := range vals {
		if got, _ := q.Get(k); got != v {
			t.Fatalf("%s: %s != %s", k, got, v)
		}
	}
	if len(vals) != len(Keys()) {
		t.Fatal("values and keys disagree")
	}
}

func TestFromConfigKeepsGoodValues(t *testing.T) {
	p, errs := FromConfig(map[string]string{
		"risk.max_single_pct": "0.04",
		"risk.max_total_pct":  "abc",
		"position_per_stock":  "0.05", // 不是 risk. 前缀，忽略
	})
	if len(errs) != 1 || p.MaxSinglePct != 0.04 || p.MaxTotalPct != 0.80 {
		t.Fatal(errs, p.MaxSinglePct, p.MaxTotalPct)
	}
	c := p.Clone()
	c.PhaseScale["WARM"] = 0
	if p.PhaseScale["WARM"] != 0.8 {
		t.Fatal("clone shares maps")
	}
}

// TestFromConfigSkipsDrillReceipts 锁 M13-T15 同源纪律：risk.drill.* 演练回执不是
// 引擎参数，引擎热载/trade 预览绝不能因为它标 failed/Policy.Known=false——哪怕是脏值。
func TestFromConfigSkipsDrillReceipts(t *testing.T) {
	p, errs := FromConfig(map[string]string{
		"risk.max_single_pct":           "0.04",
		"risk.drill.risk_drill.receipt": `{"at":"2026-10-22T09:30:00+08:00","operator":"ops","result":"pass","evidence_hash":"abc"}`,
		"risk.drill.broker.receipt":     "not json",
	})
	if len(errs) != 0 || p.MaxSinglePct != 0.04 {
		t.Fatal(errs, p.MaxSinglePct)
	}
}

func TestKillSwitch(t *testing.T) {
	var k KillState
	k, changed := k.Trigger(KillFromClient, "手动", "owner", now)
	if !changed || !k.Active {
		t.Fatal("trigger")
	}
	k2, changed := k.Trigger(KillFromMarket, "行情中断", "", now.Add(time.Second))
	if changed || k2.Source != KillFromClient {
		t.Fatal("second trigger keeps the first source")
	}
	if _, err := k.Reset("owner", "confirm", "", now); err == nil {
		t.Fatal("confirm text is case sensitive")
	}
	if _, err := k.Reset("", ResetConfirm, "", now); err == nil {
		t.Fatal("operator required")
	}
	r, err := k.Reset("owner", ResetConfirm, "已核对", now)
	if err != nil || r.Active {
		t.Fatal(err)
	}
	p := Defaults()
	if ModeAllowed(L1, k, p) == nil {
		t.Fatal("kill switch active blocks L1")
	}
	if ModeAllowed(L1, r, p) != nil {
		t.Fatal("L1 after reset")
	}
	if ModeAllowed(L2, r, p) == nil {
		t.Fatal("L2 needs live admission")
	}
	p.LiveAdmitted = true
	if ModeAllowed(L2, r, p) != nil {
		t.Fatal("L2 with admission")
	}
	if m, err := ParseMode("l2"); err != nil || m != L2 || m.String() != "L2" {
		t.Fatal(m, err)
	}
}

func TestRate(t *testing.T) {
	var r Rate
	t0 := time.Date(2026, 10, 9, 10, 0, 0, 0, tradecal.Shanghai())
	for i := 0; i < 5; i++ {
		r.Record(t0.Add(time.Duration(i) * 100 * time.Millisecond))
	}
	if s, d := r.Counts(t0.Add(450 * time.Millisecond)); s != 5 || d != 5 {
		t.Fatal(s, d)
	}
	if s, d := r.Counts(t0.Add(1250 * time.Millisecond)); s != 2 || d != 5 {
		t.Fatal(s, d)
	}
	if s, d := r.Counts(t0.Add(24 * time.Hour)); s != 0 || d != 0 {
		t.Fatal("new day", s, d)
	}
}

// TestProtectionParams 覆盖 R16 三个参数的取值与边界（分钟单位）。
func TestProtectionParams(t *testing.T) {
	p := Defaults()
	// 默认全关：连亏不锁、冷却关闭，锁定时长 1 小时兜底。
	if p.ProtectionLossStreak != 0 || p.ProtectionCooldown != 0 || p.ProtectionLock != time.Hour {
		t.Fatalf("unexpected defaults: %+v", p)
	}
	if err := p.Set("protection_loss_streak", "3"); err != nil || p.ProtectionLossStreak != 3 {
		t.Fatal(err, p.ProtectionLossStreak)
	}
	if err := p.Set("protection_loss_streak", "21"); err == nil {
		t.Fatal("streak above 20 must be rejected")
	}
	if err := p.Set("protection_lock_min", "30"); err != nil || p.ProtectionLock != 30*time.Minute {
		t.Fatal(err, p.ProtectionLock)
	}
	if err := p.Set("protection_lock_min", "0"); err == nil {
		t.Fatal("lock below 1 minute must be rejected")
	}
	if err := p.Set("protection_cooldown_min", "15"); err != nil || p.ProtectionCooldown != 15*time.Minute {
		t.Fatal(err, p.ProtectionCooldown)
	}
	if err := p.Set("protection_cooldown_min", "0"); err != nil || p.ProtectionCooldown != 0 {
		t.Fatal("cooldown 0 closes the rule")
	}
	// 三个键都在 Keys/Values 里（配置面板可见）。
	for _, k := range []string{"protection_loss_streak", "protection_lock_min", "protection_cooldown_min"} {
		if _, ok := p.Get("risk." + k); !ok {
			t.Fatalf("%s missing from param surface", k)
		}
	}
}
