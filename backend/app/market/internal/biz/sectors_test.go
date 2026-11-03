package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/market"
)

func today() time.Time { return market.DateOf(time.Now()) }

/* ---------- BE-05 板块归属反向查询 ---------- */

// 多板块按代码排序、请求内每只 symbol 都有键、无归属券对应空切片（不是错误）。
func TestSectorMembershipGrouping(t *testing.T) {
	repo := &fakeRepo{membership: []SectorMembershipRow{
		{Symbol: "600519.SH", SectorCode: "BK0477", SectorName: "酿酒行业"},
		{Symbol: "600519.SH", SectorCode: "BK0001", SectorName: "上证指数成分"},
		{Symbol: "000001.SZ", SectorCode: "BK0475", SectorName: "银行"},
	}}
	uc := quoteUsecase(repo, &fakeSnaps{})
	out, err := uc.SectorMembership(context.Background(),
		[]string{"600519.SH", "300750.SZ", "000001.SZ"}, today())
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 3 {
		t.Fatalf("every requested symbol must have a key: %+v", out)
	}
	m := out["600519.SH"]
	if len(m) != 2 || m[0].Code != "BK0001" || m[1].Code != "BK0477" {
		t.Fatalf("sectors must be sorted by code: %+v", m)
	}
	if m[1].Name != "酿酒行业" {
		t.Fatalf("sector name carried: %+v", m[1])
	}
	if len(out["000001.SZ"]) != 1 {
		t.Fatalf("single membership: %+v", out["000001.SZ"])
	}
	if got := out["300750.SZ"]; got == nil || len(got) != 0 {
		t.Fatalf("no attribution must be an empty slice, not nil/absent: %+v", got)
	}
}

// 全量无归属：每只 symbol 都回空切片，回包不缺席。
func TestSectorMembershipEmpty(t *testing.T) {
	uc := quoteUsecase(&fakeRepo{}, &fakeSnaps{})
	out, err := uc.SectorMembership(context.Background(), []string{"600519.SH", "000001.SZ"}, today())
	if err != nil {
		t.Fatal(err)
	}
	for _, sym := range []string{"600519.SH", "000001.SZ"} {
		if got, ok := out[sym]; !ok || got == nil || len(got) != 0 {
			t.Fatalf("symbol %s must map to empty slice: %+v", sym, out)
		}
	}
}

// 板块名缺失（缺名为空串）透传，后端不编名字。
func TestSectorMembershipNameMissing(t *testing.T) {
	repo := &fakeRepo{membership: []SectorMembershipRow{
		{Symbol: "600519.SH", SectorCode: "BK9999", SectorName: ""},
	}}
	uc := quoteUsecase(repo, &fakeSnaps{})
	out, err := uc.SectorMembership(context.Background(), []string{"600519.SH"}, today())
	if err != nil {
		t.Fatal(err)
	}
	if len(out["600519.SH"]) != 1 || out["600519.SH"][0].Name != "" {
		t.Fatalf("missing name passes through as empty: %+v", out["600519.SH"])
	}
}

/* ---------- BE-10 三指数快照 ---------- */

func indexSnap(code string, last, preClose float64) market.Snapshot {
	now := time.Now()
	return market.Snapshot{
		Symbol: code, Time: now, AsOf: now, Source: "tdx",
		PreClose: preClose, High: last + 20, Low: last - 10, Last: last,
	}
}

// 快照路径：三只固定顺序回包，涨跌/振幅按 PreClose 计算，口径与单券 Quote 一致。
func TestIndexSnapshotSnapPath(t *testing.T) {
	snaps := &fakeSnaps{snaps: []market.Snapshot{
		indexSnap("399006.SZ", 2200, 2180),
		indexSnap("000001.SH", 3050, 3000),
		indexSnap("399001.SZ", 9800, 9900),
	}}
	uc := quoteUsecase(&fakeRepo{}, snaps)
	rows, err := uc.IndexSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("fixed three indices: %+v", rows)
	}
	if rows[0].Code != "000001.SH" || rows[1].Code != "399001.SZ" || rows[2].Code != "399006.SZ" {
		t.Fatalf("fixed order SH,SZ,CYB: %+v", rows)
	}
	sh := rows[0]
	if !sh.Found || sh.Source != "snap" || sh.Name != "上证指数" || sh.Stale {
		t.Fatalf("sh row: %+v", sh)
	}
	if sh.Last != 3050 || sh.Chg != 50 {
		t.Fatalf("chg: %+v", sh)
	}
	if got := sh.PctChg; got < 1.66 || got > 1.67 {
		t.Fatalf("pct_chg want ~1.667 got %v", got)
	}
	// 振幅 = (High-Low)/PreClose = (3070-3040)/3000 = 1%。
	if got := sh.Amplitude; got < 0.99 || got > 1.01 {
		t.Fatalf("amplitude want ~1.0 got %v", got)
	}
	// 跌的指数涨跌幅为负。
	if rows[1].PctChg >= 0 {
		t.Fatalf("down index must be negative: %+v", rows[1])
	}
}

// 无快照回退最近 1d 收盘行：source=1d、不标陈旧。
func TestIndexSnapshotDailyFallback(t *testing.T) {
	repo := &fakeRepo{dayBars: []BarRow{
		{Bar: market.Bar{Time: time.Now().AddDate(0, 0, -1), Open: 2990, High: 3060, Low: 2980, Close: 3050, Volume: 1, Amount: 1}, PreClose: 3000},
	}}
	uc := quoteUsecase(repo, &fakeSnaps{})
	rows, err := uc.IndexSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rows {
		if !r.Found || r.Source != "1d" || r.Last != 3050 || r.PreClose != 3000 || r.Stale {
			t.Fatalf("fallback row %s: %+v", r.Code, r)
		}
	}
}

// 快照与 1d 都缺：Found=false、Source 空，不编数、不报错。
func TestIndexSnapshotMissing(t *testing.T) {
	uc := quoteUsecase(&fakeRepo{}, &fakeSnaps{})
	rows, err := uc.IndexSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("fixed three indices even when missing: %+v", rows)
	}
	for _, r := range rows {
		if r.Found || r.Source != "" || r.Last != 0 {
			t.Fatalf("missing row must stay zero: %+v", r)
		}
		if r.Code == "" || r.Name == "" {
			t.Fatalf("code/name always present: %+v", r)
		}
	}
}

// 跨日残留快照弃用，回退 1d；快照标陈旧时 Stale 透出但仍采用。
func TestIndexSnapshotCrossDaySkippedAndStale(t *testing.T) {
	stale := indexSnap("000001.SH", 3050, 3000)
	stale.Time = time.Now().AddDate(0, 0, -1)
	stale.AsOf = stale.Time
	fresh := indexSnap("399001.SZ", 9800, 9900)
	fresh.Stale = true
	repo := &fakeRepo{dayBars: []BarRow{
		{Bar: market.Bar{Time: time.Now().AddDate(0, 0, -1), Close: 3040}, PreClose: 3000},
	}}
	uc := quoteUsecase(repo, &fakeSnaps{snaps: []market.Snapshot{stale, fresh}})
	rows, err := uc.IndexSnapshot(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// 000001.SH 的跨日快照弃用 → 走 1d 回退。
	if rows[0].Source != "1d" || rows[0].Last != 3040 {
		t.Fatalf("cross-day snapshot must be skipped: %+v", rows[0])
	}
	// 399001.SZ 的当日陈旧快照仍采用，Stale 透出。
	if rows[1].Source != "snap" || !rows[1].Stale || !rows[1].Found {
		t.Fatalf("stale snapshot adopted with stale flag: %+v", rows[1])
	}
	// 399006.SZ 快照缺、有 1d 回退行。
	if rows[2].Source != "1d" || !rows[2].Found {
		t.Fatalf("cyb fallback: %+v", rows[2])
	}
}
