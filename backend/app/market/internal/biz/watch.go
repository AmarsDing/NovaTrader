package biz

import (
	"sort"
	"sync"
	"time"
)

// Watchlist 是各下游登记的候选池。同一 owner 再次登记整体替换；租约到期自动失效。
type Watchlist struct {
	mu     sync.Mutex
	owners map[string]lease
}

type lease struct {
	symbols map[string]struct{}
	expires time.Time
}

func NewWatchlist() *Watchlist {
	return &Watchlist{owners: map[string]lease{}}
}

// Set 登记候选池，返回到期时刻。
func (w *Watchlist) Set(owner string, symbols []string, ttl time.Duration, now time.Time) time.Time {
	set := make(map[string]struct{}, len(symbols))
	for _, s := range symbols {
		set[s] = struct{}{}
	}
	exp := now.Add(ttl)
	w.mu.Lock()
	for key, old := range w.owners {
		if !now.Before(old.expires) {
			delete(w.owners, key)
		}
	}
	if len(set) == 0 {
		delete(w.owners, owner)
	} else {
		w.owners[owner] = lease{symbols: set, expires: exp}
	}
	w.mu.Unlock()
	return exp
}

// Symbols 返回所有未到期的候选标的，去重排序。
func (w *Watchlist) Symbols(now time.Time) []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	all := map[string]struct{}{}
	for owner, l := range w.owners {
		if !now.Before(l.expires) {
			delete(w.owners, owner)
			continue
		}
		for s := range l.symbols {
			all[s] = struct{}{}
		}
	}
	out := make([]string, 0, len(all))
	for s := range all {
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// LeaseInfo 单个 owner 的未到期租约快照（GetWatchlist 读口形状）。
type LeaseInfo struct {
	Owner   string
	Symbols []string // 排序后的规范代码
	Expires time.Time
}

// Leases 返回未到期租约清单（owner 过滤，空=全部，按 owner 排序）；
// 到期者锁内惰性剔除，与 Symbols 同一纪律——缺席即租约不存在。
func (w *Watchlist) Leases(now time.Time, owner string) []LeaseInfo {
	w.mu.Lock()
	defer w.mu.Unlock()
	out := make([]LeaseInfo, 0, len(w.owners))
	for key, l := range w.owners {
		if !now.Before(l.expires) {
			delete(w.owners, key)
			continue
		}
		if owner != "" && key != owner {
			continue
		}
		syms := make([]string, 0, len(l.symbols))
		for s := range l.symbols {
			syms = append(syms, s)
		}
		sort.Strings(syms)
		out = append(out, LeaseInfo{Owner: key, Symbols: syms, Expires: l.expires})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Owner < out[j].Owner })
	return out
}
