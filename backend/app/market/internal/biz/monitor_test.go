package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/market"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

/* monitor.go（竞价截面 + 连板梯队）的单元测试。fakeRepo 不能按日取数，这里各写一个小桩。 */

type auctionRepo struct {
	fakeRepo
	rows []FactorRecord
}

func (r *auctionRepo) FactorsAt(context.Context, time.Time, string, time.Time, []string) ([]FactorRecord, error) {
	return r.rows, nil
}

type ladderRepo struct {
	fakeRepo
	byDay map[string]map[string]int
}

func (r *ladderRepo) SealedOn(_ context.Context, day time.Time) (map[string]int, error) {
	return r.byDay[day.Format("2006-01-02")], nil
}

func TestAuctionCross(t *testing.T) {
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, tradecal.Shanghai())
	repo := &auctionRepo{rows: []FactorRecord{
		{FactorRow: market.FactorRow{Symbol: "A", AsOf: day.Add(9*time.Hour + 25*time.Minute), Values: market.Values{
			"auction_pct": 9.98, "auction_amount": 5e7, "auction_vol_ratio": 12, "auction_turnover": 3.1,
			"auction_limit_up": 1, "auction_unmatched": 2e5,
		}}, Kind: KindAuction},
		{FactorRow: market.FactorRow{Symbol: "B", AsOf: day.Add(9*time.Hour + 25*time.Minute), Values: market.Values{
			"auction_pct": 2.5, "auction_amount": 8e7, "auction_vol_ratio": 6, "auction_limit_up": 0,
		}}, Kind: KindAuction},
		{FactorRow: market.FactorRow{Symbol: "C", AsOf: day.Add(9*time.Hour + 25*time.Minute), Values: market.Values{
			"auction_pct": -1.2, "auction_amount": 3e7,
		}}, Kind: KindAuction},
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)

	summary, err := uc.AuctionCross(context.Background(), day, 10)
	if err != nil {
		t.Fatal(err)
	}
	if !summary.Found {
		t.Fatal("expected found=true")
	}
	if summary.TotalAmount != 1.6e8 {
		t.Fatalf("total = %v, want 1.6e8", summary.TotalAmount)
	}
	if summary.LimitUpCount != 1 || len(summary.LimitUps) != 1 || summary.LimitUps[0].Symbol != "A" {
		t.Fatalf("limit ups = %+v", summary.LimitUps)
	}
	// 抢筹按金额倒序：B(8e7) > A(5e7) > C(3e7)
	if len(summary.TopRush) != 3 || summary.TopRush[0].Symbol != "B" || summary.TopRush[2].Symbol != "C" {
		t.Fatalf("top rush = %+v", summary.TopRush)
	}
}

func TestAuctionCrossEmpty(t *testing.T) {
	uc := NewUsecase(&auctionRepo{}, nil, nil, NewConfig(nil), log.DefaultLogger)
	summary, err := uc.AuctionCross(context.Background(), time.Date(2026, 10, 13, 0, 0, 0, 0, tradecal.Shanghai()), 0)
	if err != nil {
		t.Fatal(err)
	}
	if summary.Found || summary.TotalAmount != 0 || len(summary.TopRush) != 0 {
		t.Fatalf("empty day should be found=false with zeros: %+v", summary)
	}
}

func TestLadder(t *testing.T) {
	// 2026-10-12 周一、2026-10-13 周二都是交易日
	prev := time.Date(2026, 10, 12, 0, 0, 0, 0, tradecal.Shanghai())
	cur := time.Date(2026, 10, 13, 0, 0, 0, 0, tradecal.Shanghai())
	repo := &ladderRepo{byDay: map[string]map[string]int{
		"2026-10-12": {"A": 1, "B": 1, "C": 2, "D": 3},
		"2026-10-13": {"A": 2, "C": 3, "D": 5}, // A 1进2 成，B 失败；C 2进3 成；D 3进4+ 成
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)

	days, totals, err := uc.Ladder(context.Background(), cur, 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 {
		t.Fatalf("days = %d, want 1", len(days))
	}
	steps := days[0].Steps
	// 1进2：样本 A、B，成 1
	if steps[0].Sample != 2 || steps[0].Succeeded != 1 || steps[0].Rate != 0.5 {
		t.Fatalf("1进2 = %+v", steps[0])
	}
	// 2进3：样本 C，成 1
	if steps[1].Sample != 1 || steps[1].Succeeded != 1 {
		t.Fatalf("2进3 = %+v", steps[1])
	}
	// 3进4：样本 D，成 1（D 到了 5 板，≥4 算晋级）
	if steps[2].Sample != 1 || steps[2].Succeeded != 1 {
		t.Fatalf("3进4 = %+v", steps[2])
	}
	if totals[0].Sample != 2 || totals[0].Succeeded != 1 {
		t.Fatalf("totals = %+v", totals)
	}
	_ = prev
}

func TestLadderNoSample(t *testing.T) {
	// to=10-12：prev=10-09 无数据（基准日没采集）→ 跳过 10-12；
	// cur=10-09、prev=10-08 有 A 一板、10-09 无数据 → 考察日落在 10-09，A 记 1进2 失败。
	repo := &ladderRepo{byDay: map[string]map[string]int{
		"2026-10-08": {"A": 1},
		// 2026-10-09、2026-10-12 无连板数据
	}}
	uc := NewUsecase(repo, nil, nil, NewConfig(nil), log.DefaultLogger)
	days, _, err := uc.Ladder(context.Background(), time.Date(2026, 10, 12, 0, 0, 0, 0, tradecal.Shanghai()), 1)
	if err != nil {
		t.Fatal(err)
	}
	if len(days) != 1 || days[0].Day.Format("2006-01-02") != "2026-10-09" {
		t.Fatalf("days = %+v", days)
	}
	if days[0].Steps[0].Sample != 1 || days[0].Steps[0].Succeeded != 0 {
		t.Fatalf("1进2 = %+v（样本 1 晋级 0）", days[0].Steps[0])
	}
}
/* ---------- EmotionPath：盘中情绪路径 ---------- */

type pathRepo struct {
	fakeRepo
	auction []FactorRecord
	sealed  map[string]int
	trail   []market.Sentiment
}

func (r *pathRepo) FactorsAt(_ context.Context, _ time.Time, _ string, _ time.Time, symbols []string) ([]FactorRecord, error) {
	if len(symbols) == 0 {
		return r.auction, nil
	}
	out := []FactorRecord{}
	want := map[string]bool{}
	for _, s := range symbols {
		want[s] = true
	}
	for _, row := range r.auction {
		if want[row.Symbol] {
			out = append(out, row)
		}
	}
	return out, nil
}
func (r *pathRepo) SealedOn(context.Context, time.Time) (map[string]int, error) { return r.sealed, nil }
func (r *pathRepo) SentimentTrail(context.Context, time.Time) ([]market.Sentiment, error) {
	return r.trail, nil
}

func TestEmotionPath(t *testing.T) {
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, tradecal.Shanghai())
	auc925 := day.Add(9*time.Hour + 25*time.Minute)
	repo := &pathRepo{
		auction: []FactorRecord{
			{FactorRow: market.FactorRow{Symbol: "A", AsOf: auc925, Values: market.Values{"auction_pct": 5.003, "auction_amount": 3e7, "auction_limit_up": 1}}, Kind: KindAuction},
			{FactorRow: market.FactorRow{Symbol: "B", AsOf: auc925, Values: market.Values{"auction_pct": 3.001, "auction_amount": 2e7}}, Kind: KindAuction},
		},
		sealed: map[string]int{"A": 1, "B": 1, "C": 1},
		trail: []market.Sentiment{
			{TradeDate: day, AsOf: day.Add(10 * time.Hour), UpCount: 20, DownCount: 3, BrokenCount: 5, Phase: "WARM"},
			{TradeDate: day, AsOf: day.Add(15 * time.Hour), UpCount: 45, DownCount: 5, BrokenCount: 9, Phase: "HOT"},
		},
	}
	uc := NewUsecase(repo, nil, nil, Config{}, log.DefaultLogger)
	p, err := uc.EmotionPath(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if !p.AuctionFound || p.AuctionLimitUpCount != 1 {
		t.Fatalf("auction = %+v", p)
	}
	// 昨涨停 3 只，今日竞价覆盖 A/B 两只：(5.003+3.001)/2 = 4.002
	if p.PrevTierAuctionPct == nil || *p.PrevTierAuctionPct != 4.0 {
		t.Fatalf("prevTierPct = %v", p.PrevTierAuctionPct)
	}
	if p.PrevTierSample != 2 {
		t.Fatalf("sample = %d", p.PrevTierSample)
	}
	if p.Missing == nil || p.Missing[0] != "auction_prev_tier_partial" {
		t.Fatalf("missing = %v", p.Missing)
	}
	if !p.Partial {
		t.Fatalf("partial expected（竞价只覆盖部分昨涨停）")
	}
	if len(p.Points) != 3 || !p.Points[0].Auction || p.Points[0].Time != "09:25" {
		t.Fatalf("points = %+v", p.Points)
	}
	if p.Points[1].Up != 20 || p.Points[2].Phase != "HOT" {
		t.Fatalf("trail points wrong: %+v", p.Points[1:])
	}
}

func TestEmotionPathEmpty(t *testing.T) {
	day := time.Date(2026, 10, 13, 0, 0, 0, 0, tradecal.Shanghai())
	uc := NewUsecase(&pathRepo{}, nil, nil, Config{}, log.DefaultLogger)
	p, err := uc.EmotionPath(context.Background(), day)
	if err != nil {
		t.Fatal(err)
	}
	if !p.Partial || len(p.Points) != 0 || p.PrevTierAuctionPct != nil {
		t.Fatalf("empty day path = %+v", p)
	}
	seen := map[string]bool{}
	for _, m := range p.Missing {
		seen[m] = true
	}
	if !seen["auction"] || !seen["market_sentiment"] {
		t.Fatalf("missing = %v", p.Missing)
	}
}
