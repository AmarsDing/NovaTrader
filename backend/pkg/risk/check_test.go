package risk

import (
	"strings"
	"testing"
	"time"

	"server/pkg/tradecal"
)

// 2026-10-09 周五 10:00，连续竞价。
var now = time.Date(2026, 10, 9, 10, 0, 0, 0, tradecal.Shanghai())

func quote(sym string, prev float64) *Quote {
	return &Quote{
		Symbol: sym, Last: prev, PrevClose: prev, Bid1: prev - 0.01, Ask1: prev,
		Amount: 2e8, PrevAmount: 1e8, ListedDays: 1000, Time: now.Add(-time.Second),
	}
}

// base 是一张全部规则都能通过的买单。
func base() Input {
	return Input{
		Now:     now,
		Session: tradecal.Session{Phase: tradecal.AMTrading, Trading: true},
		Mode:    L1,
		Order: Order{
			ClientID: "c1", Account: SIM, Symbol: "000001.SZ", Side: Buy,
			Price: 10.00, Volume: 1000, Source: Auto, Sector: "银行",
		},
		Account: Account{
			Type: SIM, Equity: 1_000_000, Available: 500_000, DayStartEquity: 1_000_000,
			Holdings: map[string]Holding{}, AsOf: now.Add(-5 * time.Second),
		},
		Quote:  quote("000001.SZ", 10.00),
		Market: Market{Phase: "HOT"},
	}
}

func check(t *testing.T, in Input, p Params, wantRule string) Decision {
	t.Helper()
	d := Check(in, p)
	if wantRule == "" {
		if !d.Approved {
			t.Fatalf("want approved, got %s %s", d.Rule, d.Reason)
		}
		return d
	}
	if d.Approved || d.Rule != wantRule {
		t.Fatalf("want reject by %s, got approved=%v rule=%s reason=%s", wantRule, d.Approved, d.Rule, d.Reason)
	}
	return d
}

func TestBaseApproved(t *testing.T) {
	d := check(t, base(), Defaults(), "")
	if d.Volume != 1000 || d.Adjusted || len(d.Results) != 15 {
		t.Fatalf("%+v", d)
	}
	for i, r := range d.Results {
		if want := chain[i].id; r.Rule != want {
			t.Fatalf("rule %d is %s, want %s", i, r.Rule, want)
		}
	}
}

func TestInvalidInput(t *testing.T) {
	in := base()
	in.Order.Symbol = "abc"
	check(t, in, Defaults(), RuleInvalid)
	in = base()
	in.Order.Volume = 0
	check(t, in, Defaults(), RuleInvalid)
}

func TestFirstFailureStops(t *testing.T) {
	in := base()
	in.KillSwitch = true
	in.Quote.Time = now.Add(-time.Hour)
	d := check(t, in, Defaults(), "R01")
	if len(d.Results) != 1 {
		t.Fatal("chain should stop at R01")
	}
}

func TestAuth(t *testing.T) {
	p := Defaults()
	in := base()
	in.Mode = L0
	check(t, in, p, "R01")

	in = base()
	in.Order.Account = LIVE
	in.Mode = L1
	check(t, in, p, "R01")
	in.Mode = L2
	check(t, in, p, "R01") // 准入未通过
	p.LiveAdmitted = true
	check(t, in, p, "")

	in = base()
	in.Order.Source = Manual
	check(t, in, Defaults(), "R01") // 缺操作人
	in.Order.Operator = "owner"
	check(t, in, Defaults(), "")

	// Kill Switch：人工卖出放行，自动卖出拒绝。
	in = sellInput(1000, 1000)
	in.KillSwitch = true
	in.Mode = L0
	check(t, in, Defaults(), "R01")
	in.Order.Source = Manual
	in.Order.Operator = "owner"
	check(t, in, Defaults(), "")
}

func TestFresh(t *testing.T) {
	in := base()
	in.Quote.Time = now.Add(-11 * time.Second)
	check(t, in, Defaults(), "R02")
	in.Order.Source = Manual
	in.Order.Operator = "owner"
	check(t, in, Defaults(), "")
	in = base()
	in.Quote = nil
	check(t, in, Defaults(), "R02")
}

func TestTradable(t *testing.T) {
	p := Defaults()
	in := base()
	in.Session = tradecal.Session{Phase: tradecal.NoonBreak}
	check(t, in, p, "R03")

	in = base()
	in.Quote.Suspended = true
	check(t, in, p, "R03")

	in = base()
	in.Quote.ST = true
	check(t, in, p, "R03")

	in = base()
	in.Quote.ListedDays = 3
	check(t, in, p, "R03")

	in = base()
	p.Blacklist = map[string]bool{"000001.SZ": true}
	in.Order.Symbol = "sz000001"
	check(t, in, p, "R03")

	// ST 卖出不受限制。
	in = sellInput(1000, 1000)
	in.Quote.ST = true
	check(t, in, Defaults(), "")
}

// 集合竞价阶段只看涨跌停，不看价格笼子，便于单独验证各板块幅度。
func auctionBuy(sym string, prev, price float64, vol int, st bool) Input {
	in := base()
	in.Now = time.Date(2026, 10, 9, 9, 20, 0, 0, tradecal.Shanghai())
	in.Session = tradecal.Session{Phase: tradecal.CallAuction}
	in.Order.Symbol, in.Order.Price, in.Order.Volume = sym, price, vol
	in.Quote = quote(sym, prev)
	in.Quote.Time = in.Now
	in.Quote.ST = st
	in.Account.AsOf = in.Now
	return in
}

func TestLimitByBoard(t *testing.T) {
	p := Defaults()
	p.AllowSTBuy = true
	cases := []struct {
		name    string
		sym     string
		st      bool
		ok, bad float64
	}{
		{"主板 10%", "600519.SH", false, 11.00, 11.01},
		{"主板 ST 10%（2026-07-06 起）", "600519.SH", true, 11.00, 11.01},
		{"深主板 10%", "000001.SZ", false, 11.00, 11.01},
		{"创业板 20%", "300750.SZ", false, 12.00, 12.01},
		{"创业板 ST 20%", "300750.SZ", true, 12.00, 12.01},
		{"科创板 20%", "688981.SH", false, 12.00, 12.01},
		{"科创板 ST 20%", "688981.SH", true, 12.00, 12.01},
		{"北交所 30%", "830001.BJ", false, 13.00, 13.01},
	}
	for _, c := range cases {
		vol := 200
		in := auctionBuy(c.sym, 10.00, c.ok, vol, c.st)
		if d := Check(in, p); !d.Approved {
			t.Fatalf("%s: %.2f should pass: %s", c.name, c.ok, d.Reason)
		}
		in = auctionBuy(c.sym, 10.00, c.bad, vol, c.st)
		if d := Check(in, p); d.Approved || d.Rule != "R04" {
			t.Fatalf("%s: %.2f should be rejected by R04, got %s %s", c.name, c.bad, d.Rule, d.Reason)
		}
	}
	// 2026-07-06 之前主板 ST 是 5%。
	in := auctionBuy("600519.SH", 10.00, 10.51, 200, true)
	in.Now = time.Date(2026, 7, 3, 9, 20, 0, 0, tradecal.Shanghai())
	in.Quote.Time, in.Account.AsOf = in.Now, in.Now
	if d := Check(in, p); d.Rule != "R04" {
		t.Fatalf("main ST before 2026-07-06 is 5%%: %s %s", d.Rule, d.Reason)
	}
	// 跌停以下卖出也拒绝。
	in = auctionBuy("600519.SH", 10.00, 8.99, 200, false)
	in.Order.Side = Sell
	in.Account.Holdings = map[string]Holding{"600519.SH": {Symbol: "600519.SH", Quantity: 200, Available: 200}}
	check(t, in, p, "R04")
	// 非 0.01 价位。
	in = auctionBuy("600519.SH", 10.00, 10.005, 200, false)
	check(t, in, p, "R04")
}

func TestPriceCage(t *testing.T) {
	cases := []struct {
		sym     string
		ask     float64
		ok, bad float64
	}{
		{"000001.SZ", 10.00, 10.20, 10.21},
		{"000001.SZ", 3.00, 3.10, 3.11},    // 10 个价位兜底
		{"688981.SH", 3.00, 3.06, 3.07},    // 科创板没有兜底
		{"830001.BJ", 10.50, 11.02, 11.03}, // 北交所 5%
	}
	for _, c := range cases {
		mk := func(price float64) Input {
			in := base()
			in.Order.Symbol, in.Order.Price, in.Order.Volume = c.sym, price, 200
			in.Quote = quote(c.sym, c.ask)
			in.Quote.Ask1 = c.ask
			return in
		}
		check(t, mk(c.ok), Defaults(), "")
		check(t, mk(c.bad), Defaults(), "R04")
	}
	// 14:57 以后是收盘集合竞价，不检查笼子。
	in := base()
	in.Now = time.Date(2026, 10, 9, 14, 58, 0, 0, tradecal.Shanghai())
	in.Session = tradecal.Session{Phase: tradecal.PMTrading, Trading: true, Late: true}
	in.Quote.Time, in.Account.AsOf = in.Now, in.Now
	in.Order.Price = 10.50
	check(t, in, Defaults(), "")
	// 卖出笼子：买一 10.00 时下限 9.80。
	in = sellInput(1000, 1000)
	in.Quote.Bid1 = 10.00
	in.Order.Price = 9.79
	check(t, in, Defaults(), "R04")
	in.Order.Price = 9.80
	check(t, in, Defaults(), "")
}

func TestLot(t *testing.T) {
	in := base()
	in.Order.Volume = 150
	check(t, in, Defaults(), "R05")

	in = base()
	in.Order.Symbol, in.Order.Volume = "688981.SH", 180
	in.Quote = quote("688981.SH", 10)
	check(t, in, Defaults(), "R05")
	in.Order.Volume = 201
	check(t, in, Defaults(), "")

	in = base()
	in.Order.Symbol, in.Order.Volume = "830001.BJ", 101
	in.Quote = quote("830001.BJ", 10)
	check(t, in, Defaults(), "")

	in = base()
	in.Order.Symbol, in.Order.Volume = "300750.SZ", 300_100
	in.Quote = quote("300750.SZ", 10)
	check(t, in, Defaults(), "R05")
}

func sellInput(qty, avail int) Input {
	in := base()
	in.Order.Side = Sell
	in.Order.Volume = avail
	in.Order.Price = 10.00
	in.Account.Holdings = map[string]Holding{
		"000001.SZ": {Symbol: "000001.SZ", Quantity: qty, Available: avail, AvgCost: 9.5, MarketValue: float64(qty) * 10},
	}
	return in
}

func TestSellableT1(t *testing.T) {
	in := sellInput(1000, 0) // 今天买的，T+1 不能卖
	in.Order.Volume = 100
	check(t, in, Defaults(), "R06")

	in = sellInput(1000, 600)
	h := in.Account.Holdings["000001.SZ"]
	h.FrozenSell = 100
	in.Account.Holdings["000001.SZ"] = h
	in.Order.Volume = 600
	check(t, in, Defaults(), "R06")
	in.Order.Volume = 500
	check(t, in, Defaults(), "")

	// 余股：可卖 250，卖 130 拆开了余股。
	in = sellInput(250, 250)
	in.Order.Volume = 130
	check(t, in, Defaults(), "R06")
	in.Order.Volume = 150
	check(t, in, Defaults(), "")

	// 科创板：可卖 250，卖 30 不足 200。
	in = sellInput(250, 250)
	in.Order.Symbol = "688981.SH"
	in.Quote = quote("688981.SH", 10)
	in.Account.Holdings = map[string]Holding{"688981.SH": {Symbol: "688981.SH", Quantity: 250, Available: 250}}
	in.Order.Volume = 30
	check(t, in, Defaults(), "R06")
	in.Order.Volume = 250
	check(t, in, Defaults(), "")

	// 卖出不受仓位、资金、熔断限制。
	in = sellInput(1000, 1000)
	in.Account.Available = 0
	in.Breaker = "当日亏损"
	in.Counters.BuysToday = 100
	check(t, in, Defaults(), "")
}

func TestSingleShrinkAndScale(t *testing.T) {
	in := base()
	in.Account.Holdings["000001.SZ"] = Holding{Symbol: "000001.SZ", Quantity: 4500, MarketValue: 45_000, Sector: "银行"}
	d := check(t, in, Defaults(), "")
	if d.Volume != 500 || !d.Adjusted || !strings.Contains(d.Results[6].Note, "1000 → 500") {
		t.Fatalf("%+v", d.Results[6])
	}

	// 冰点系数 0.2：上限 10000，已有 9950，放不下一手。
	in = base()
	in.Market.Phase = "ICE"
	in.Account.Holdings["000001.SZ"] = Holding{Symbol: "000001.SZ", MarketValue: 9_950}
	check(t, in, Defaults(), "R07")

	// M02 发布的 position_scale 优先于查表：0.6 → 上限 30000。
	in = base()
	in.Market.PositionScale = 0.6
	in.Order.Volume = 4000
	if d := check(t, in, Defaults(), ""); d.Volume != 3000 {
		t.Fatal(d.Volume)
	}

	// 风险度 2：1.0 × 0.4 → 上限 20000。
	in = base()
	in.Market.RiskLevel = 2
	in.Order.Volume = 3000
	if d := check(t, in, Defaults(), ""); d.Volume != 2000 {
		t.Fatal(d.Volume)
	}

	// 情绪缺失按 0.5。
	in = base()
	in.Market.Phase = ""
	in.Order.Volume = 3000
	if d := check(t, in, Defaults(), ""); d.Volume != 2500 {
		t.Fatal(d.Volume)
	}

	in = base()
	in.Account.AsOf = time.Time{}
	check(t, in, Defaults(), "R07")
	in = base()
	in.Account.AsOf = now.Add(-2 * time.Minute)
	check(t, in, Defaults(), "R07")
}

func TestPendingAndReservations(t *testing.T) {
	in := base()
	in.Account.Open = []OpenOrder{{ClientID: "o1", Symbol: "000001.SZ", Side: Buy, Price: 10, Volume: 3000}}
	in.Reservations = []Reservation{
		{ClientID: "o1", Symbol: "000001.SZ", Amount: 30_000}, // 已在在途委托里，不重复计
		{ClientID: "r2", Symbol: "000001.SZ", Amount: 15_000},
	}
	// 单票上限 50000，已用 30000 + 15000，剩 5000。
	if d := check(t, in, Defaults(), ""); d.Volume != 500 {
		t.Fatal(d.Volume, d.Results[6].Note)
	}
	// 预留也从可用资金里扣。
	in = base()
	in.Account.Available = 20_000
	in.Reservations = []Reservation{{ClientID: "r3", Symbol: "600000.SH", Amount: 15_000}}
	if d := check(t, in, Defaults(), ""); d.Volume != 400 {
		t.Fatal(d.Volume, d.Results[11].Note)
	}
}

func TestTotalAndSector(t *testing.T) {
	in := base()
	in.Account.Holdings["600000.SH"] = Holding{Symbol: "600000.SH", MarketValue: 790_000, Sector: "其他"}
	in.Order.Volume = 2000
	if d := check(t, in, Defaults(), ""); d.Volume != 1000 {
		t.Fatal(d.Volume)
	}
	in.Account.Holdings["600000.SH"] = Holding{Symbol: "600000.SH", MarketValue: 799_999}
	check(t, in, Defaults(), "R08")

	in = base()
	in.Account.Holdings["600036.SH"] = Holding{Symbol: "600036.SH", MarketValue: 295_000, Sector: "银行"}
	if d := check(t, in, Defaults(), ""); d.Volume != 500 {
		t.Fatal(d.Volume)
	}
	in.Order.Sector = ""
	d := check(t, in, Defaults(), "")
	if d.Volume != 1000 || d.Results[8].Note == "" {
		t.Fatal("missing sector passes with a note")
	}
}

func TestDailyBuysLiquidityCash(t *testing.T) {
	in := base()
	in.Counters.BuysToday = 10
	check(t, in, Defaults(), "R10")

	in = base()
	in.Quote.Amount, in.Quote.PrevAmount = 1e7, 4e7
	check(t, in, Defaults(), "R11")
	// 当日成交额为 0 时用昨日成交额：6000 万 × 0.01% = 6000 元，缩到 600 股。
	in = base()
	in.Quote.Amount, in.Quote.PrevAmount = 0, 6e7
	p := Defaults()
	p.MaxParticipation = 0.0001
	if d := check(t, in, p, ""); d.Volume != 600 {
		t.Fatal(d.Volume)
	}

	in = base()
	in.Account.Available = 5_000
	d := check(t, in, Defaults(), "")
	if d.Volume != 400 {
		t.Fatal(d.Volume)
	}
	in.Account.Available = 900
	check(t, in, Defaults(), "R12")
}

func TestSelfTradeAndRate(t *testing.T) {
	in := base()
	in.Account.Open = []OpenOrder{{ClientID: "s1", Symbol: "000001.SZ", Side: Sell, Price: 9.99, Volume: 100}}
	check(t, in, Defaults(), "R13")
	in.Account.Open[0].Price = 10.01
	check(t, in, Defaults(), "")

	in = sellInput(1000, 1000)
	in.Account.Open = []OpenOrder{{ClientID: "b1", Symbol: "000001.SZ", Side: Buy, Price: 10.00, Volume: 100}}
	check(t, in, Defaults(), "R13")

	in = base()
	in.Counters.LastSecond = 5
	check(t, in, Defaults(), "R14")
	in = sellInput(1000, 1000)
	in.Counters.Today = 1000
	check(t, in, Defaults(), "R14")
}

func TestBreaker(t *testing.T) {
	in := base()
	in.Breaker = "当日亏损 2%"
	check(t, in, Defaults(), "R15")

	in = base()
	in.Account.Equity = 979_000
	check(t, in, Defaults(), "R15")
	if !DailyLossHit(in.Account, Defaults()) {
		t.Fatal("daily loss")
	}

	in = base()
	in.Market.IndexChange = -0.021
	check(t, in, Defaults(), "R15")
	in = base()
	in.Market.BurstRate = 0.6
	check(t, in, Defaults(), "R15")
	in = base()
	in.Market.RiskLevel = 3
	// 风险度 3 的仓位系数是 0，R07 先拦下。
	check(t, in, Defaults(), "R07")
}
