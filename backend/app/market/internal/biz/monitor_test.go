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
