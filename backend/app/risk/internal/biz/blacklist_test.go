package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"server/pkg/risk"
)

// TestBlacklistTableJoinsRuleR03 表内新增符号不做任何参数变更，30s 节拍后被 R03 拒绝。
func TestBlacklistTableJoinsRuleR03(t *testing.T) {
	repo := &fakeRepo{mode: risk.L1, haveMode: true, blacklist: map[string]bool{}}
	r := newRig(t, repo)
	ctx := context.Background()
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("a", 100)); !d.Approved {
		t.Fatal("clean symbol must pass before the ban", d.Reason)
	}
	// 表内新增：无需动 risk.blacklist 参数。
	repo.mu.Lock()
	repo.blacklist["000001.SZ"] = true
	repo.mu.Unlock()
	// 节拍未到时仍放行，生效只走 30s 刷新。
	r.clock = r.clock.Add(blacklistRefresh / 2)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 500_000)
	r.e.Tick(ctx)
	if d, _ := r.e.Check(ctx, buy("b", 100)); !d.Approved {
		t.Fatal("table set must only apply after the 30s refresh", d.Reason)
	}
	r.clock = r.clock.Add(blacklistRefresh)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 500_000)
	r.e.Tick(ctx)
	if d, _ := r.e.Check(ctx, buy("c", 100)); d.Approved || d.Rule != "R03" {
		t.Fatal("table blacklist must reject via R03 without param change", d.Rule, d.Reason)
	}
	// 参数集没有被改写。
	if repo.params["risk.blacklist"] != "" {
		t.Fatal("param blacklist must stay untouched", repo.params)
	}
	// 移出表后节拍刷新恢复放行。先把已批准的 a、b 记进账本清掉预留。
	repo.mu.Lock()
	delete(repo.blacklist, "000001.SZ")
	repo.mu.Unlock()
	r.clock = r.clock.Add(blacklistRefresh + time.Second)
	r.q.m["000001.SZ"] = r.snap(10)
	r.accountWithOrders(1_000_000, 500_000, []string{"a", "b"})
	r.e.Tick(ctx)
	if d, _ := r.e.Check(ctx, buy("d", 100)); !d.Approved {
		t.Fatal("removal must take effect after refresh", d.Reason)
	}
}

// TestBlacklistReadFailureDegrades 表读取失败：显式告警、保留参数集与上次成功的内存集，不静默空集。
func TestBlacklistReadFailureDegrades(t *testing.T) {
	repo := &fakeRepo{mode: risk.L1, haveMode: true, blacklist: map[string]bool{"000001.SZ": true}}
	r := newRig(t, repo)
	ctx := context.Background()
	r.account(1_000_000, 500_000)
	if d, _ := r.e.Check(ctx, buy("a", 100)); d.Approved || d.Rule != "R03" {
		t.Fatal("start load must already apply the table set", d.Rule, d.Reason)
	}
	// 参数集加另一只票；读表开始持续失败。
	if _, err := r.e.UpdateParam(ctx, "blacklist", "600519.SH", "owner"); err != nil {
		t.Fatal(err)
	}
	repo.mu.Lock()
	repo.blacklistErr = errors.New("stock_blacklist read down")
	repo.mu.Unlock()
	r.clock = r.clock.Add(blacklistRefresh + time.Second)
	r.q.m["000001.SZ"] = r.snap(10)
	r.q.m["600519.SH"] = Snapshot{Symbol: "600519.SH", Time: r.clock, AsOf: r.clock, PreClose: 10, Last: 10, Bid1: 9.99, Ask1: 10, Amount: 3e8}
	r.account(1_000_000, 500_000)
	r.e.Tick(ctx)
	// 参数集仍生效（不静默空集）。
	d, _ := r.e.Check(ctx, risk.Order{ClientID: "p", Account: risk.SIM, Symbol: "600519.SH", Side: risk.Buy, Price: 10, Volume: 100, Source: risk.Auto, Sector: "白酒"})
	if d.Approved || d.Rule != "R03" {
		t.Fatal("param blacklist must survive table read failure", d.Rule, d.Reason)
	}
	// 上次成功的内存集也保留。
	if d, _ := r.e.Check(ctx, buy("b", 100)); d.Approved || d.Rule != "R03" {
		t.Fatal("last known table set must survive read failure", d.Rule, d.Reason)
	}
	// 读表恢复后新集合覆盖生效：老票放行。
	repo.mu.Lock()
	repo.blacklistErr = nil
	repo.blacklist = map[string]bool{}
	repo.mu.Unlock()
	r.clock = r.clock.Add(blacklistRefresh + time.Second)
	r.q.m["000001.SZ"] = r.snap(10)
	r.account(1_000_000, 500_000)
	r.e.Tick(ctx)
	if d, _ := r.e.Check(ctx, buy("c", 100)); !d.Approved {
		t.Fatal("recovered read must re-apply the table set", d.Reason)
	}
}
