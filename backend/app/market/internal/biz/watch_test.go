package biz

import (
	"testing"
	"time"
)

// Leases 读口合同（GetWatchlist 底层）：owner 过滤、symbols/owner 排序、到期缺席且锁内剔除。
func TestWatchlistLeasesOwnerFilterAndExpiry(t *testing.T) {
	now := time.Now()
	w := NewWatchlist()
	w.Set("web", []string{"600519.SH", "000001.SZ"}, 10*time.Minute, now)
	w.Set("trade.abc", []string{"300750.SZ"}, 2*time.Minute, now)

	all := w.Leases(now, "")
	if len(all) != 2 {
		t.Fatalf("expected 2 live leases, got %d", len(all))
	}
	if all[0].Owner != "trade.abc" || all[1].Owner != "web" {
		t.Fatalf("owner sort wrong: %+v", all)
	}
	if got := all[1].Symbols; len(got) != 2 || got[0] != "000001.SZ" || got[1] != "600519.SH" {
		t.Fatalf("symbols not sorted: %v", got)
	}

	one := w.Leases(now, "trade.abc")
	if len(one) != 1 || one[0].Owner != "trade.abc" {
		t.Fatalf("owner filter failed: %+v", one)
	}

	// 到期缺席（惰性剔除）：trade.abc TTL 2 分钟，3 分钟后全量只剩 web。
	later := now.Add(3 * time.Minute)
	after := w.Leases(later, "")
	if len(after) != 1 || after[0].Owner != "web" {
		t.Fatalf("expired lease should be absent: %+v", after)
	}
	if got := w.Leases(later, "trade.abc"); len(got) != 0 {
		t.Fatalf("expired owner should be evicted: %+v", got)
	}
}
