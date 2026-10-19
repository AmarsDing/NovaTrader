package sim

import (
	"context"
	"math"
	"strings"
	"testing"

	"server/pkg/ashare"
)

// Account 钉 FR-09-04「费用」与需求 66 手工推演（T+1 卖出被拒、费用）的资金侧语义：
// 佣金最低 5 元、卖出含印花税、买卖都收过户费、科创板逐股步进、失败不改账户。

func almost(got, want float64) bool { return math.Abs(got-want) < 1e-9 }

func TestAffordableMainBoardStepsDownByLot(t *testing.T) {
	a := NewAccount(10_000, ashare.DefaultFee())
	// 10_000 / 10 = 1000 股，但 1000 股成交额 10_000 加费用超出余额，须缩到 900：
	// 900 股额 9000 + 佣金最低 5 + 过户 0.09 = 9005.09 ≤ 10_000。
	if got := a.Affordable("600519.SH", 1_000_000, 10); got != 900 {
		t.Fatalf("want=∞ → 900 with min commission: %d", got)
	}
	// 科创板按 1 股步进：most=floor(10_000/9.99)=1001，但 1001 股额 9999.99+费用 5.10
	// 超出，须退 1 股到 1000（额 9990+费 5.10=9995.10 ≤ 10_000）——步进单位是 1 而非 100。
	star := NewAccount(10_000, ashare.DefaultFee())
	if got := star.Affordable("688001.SH", 1_000_000, 9.99); got != 1000 {
		t.Fatalf("STAR per-share should reach 1000 (1001 fees over): %d", got)
	}
}

func TestAffordableStarKeepsPerSharePrecision(t *testing.T) {
	// 现金 2015.02、价 10：科创板 most=201，费用 5+0.02=5.02 恰好 2010+5.02 ≤ 现金 → 201。
	a := NewAccount(2015.02, ashare.DefaultFee())
	if got := a.Affordable("688001.SH", 300, 10); got != 201 {
		t.Fatalf("STAR should keep per-share 201: %d", got)
	}
	// 主板同参数只能落到整手 200。
	b := NewAccount(2015.02, ashare.DefaultFee())
	if got := b.Affordable("600519.SH", 300, 10); got != 200 {
		t.Fatalf("main board should floor to 200: %d", got)
	}
}

func TestAffordableZeroGuards(t *testing.T) {
	a := NewAccount(10_000, ashare.DefaultFee())
	if got := a.Affordable("600519.SH", 1000, 0); got != 0 {
		t.Fatalf("zero price: %d", got)
	}
	if got := a.Affordable("600519.SH", 0, 10); got != 0 {
		t.Fatalf("zero want: %d", got)
	}
	broke := NewAccount(0, ashare.DefaultFee())
	if got := broke.Affordable("600519.SH", 1000, 10); got != 0 {
		t.Fatalf("zero cash: %d", got)
	}
	// 连一手带费用都买不起：50 元买 10 元股 → 0。
	poor := NewAccount(50, ashare.DefaultFee())
	if got := poor.Affordable("600519.SH", 1000, 10); got != 0 {
		t.Fatalf("unaffordable: %d", got)
	}
}

func TestAccountBuyDeductsAmountFeeAndLocksT1(t *testing.T) {
	a := NewAccount(10_000, ashare.DefaultFee())
	exec, err := a.Buy(context.Background(), "b1", "600519.SH", 100, 10)
	if err != nil {
		t.Fatal(err)
	}
	// 费用：佣金 max(1000×0.00025, 5)=5 + 过户 0.01 = 5.01。
	if exec.Qty != 100 || !almost(exec.Price, 10) || !almost(exec.Amount, 1000) || !almost(exec.Fee, 5.01) {
		t.Fatalf("exec fields: %+v", exec)
	}
	if !almost(a.Cash, 10_000-1005.01) {
		t.Fatalf("cash after buy: %.6f", a.Cash)
	}
	pos := a.Book.Position("600519.SH")
	if pos.Quantity != 100 || pos.Available != 0 {
		t.Fatalf("T+1 must lock same-day buy: %+v", pos)
	}
}

func TestAccountBuyInsufficientLeavesNoTrace(t *testing.T) {
	a := NewAccount(100, ashare.DefaultFee())
	if _, err := a.Buy(context.Background(), "b1", "600519.SH", 100, 10); err == nil ||
		!strings.Contains(err.Error(), "cash") {
		t.Fatalf("insufficient cash must fail with cash context: %v", err)
	}
	if !almost(a.Cash, 100) {
		t.Fatalf("failed buy must not move cash: %.6f", a.Cash)
	}
	if pos := a.Book.Position("600519.SH"); pos.Quantity != 0 {
		t.Fatalf("failed buy must not create a position: %+v", pos)
	}
	// 同一个 clientID 不残留委托记录：资金补足后可原样重报。
	a.Credit(9_900)
	exec, err := a.Buy(context.Background(), "b1", "600519.SH", 100, 10)
	if err != nil || exec.Qty != 100 {
		t.Fatalf("clientID reuse after rejected buy must succeed: %v %+v", err, exec)
	}
}

func TestAccountSellCreditsNetOfStampTax(t *testing.T) {
	a := NewAccount(10_000, ashare.DefaultFee())
	if _, err := a.Buy(context.Background(), "b1", "600519.SH", 100, 10); err != nil {
		t.Fatal(err)
	}
	a.Book.RollDay()
	exec, err := a.Sell(context.Background(), "s1", "600519.SH", 100, 11)
	if err != nil {
		t.Fatal(err)
	}
	// 卖出费用：佣金 5 + 印花税 1100×0.0005=0.55 + 过户 0.011 → 5.56；入账净额 1094.44。
	if !almost(exec.Fee, 5.56) || !almost(exec.Amount, 1100) {
		t.Fatalf("sell fee breakdown: %+v", exec)
	}
	if !almost(a.Cash, 10_000-1005.01+1094.44) {
		t.Fatalf("cash after round trip: %.6f", a.Cash)
	}
	pos := a.Book.Position("600519.SH")
	if pos.Quantity != 0 || !almost(pos.AvgCost, 0) {
		t.Fatalf("flat position must reset avg cost: %+v", pos)
	}
}

func TestAccountSellSameDayRejectedByT1(t *testing.T) {
	a := NewAccount(10_000, ashare.DefaultFee())
	if _, err := a.Buy(context.Background(), "b1", "600519.SH", 100, 10); err != nil {
		t.Fatal(err)
	}
	before := a.Cash
	if _, err := a.Sell(context.Background(), "s1", "600519.SH", 100, 10); err == nil {
		t.Fatal("same-day sell must be rejected (T+1)")
	}
	if !almost(a.Cash, before) {
		t.Fatalf("rejected sell must not move cash: %.6f", a.Cash)
	}
	a.Book.RollDay()
	if _, err := a.Sell(context.Background(), "s1", "600519.SH", 100, 10); err != nil {
		t.Fatalf("sell after roll day must succeed: %v", err)
	}
}
