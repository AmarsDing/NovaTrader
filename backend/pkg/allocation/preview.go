package allocation

import (
	"fmt"
	"math"
	"sort"
	"strconv"
	"time"

	"server/pkg/accountstate"
	"server/pkg/ashare"
	"server/pkg/decision"
	"server/pkg/market"
)

type Asset struct {
	Known, Suspended, ST bool
	Sector               string
}
type Policy struct {
	Known                          bool
	MaxSingle, MaxTotal, MaxSector float64
	AllowSTBuy                     bool
}
type Input struct {
	Portfolio            Portfolio
	Account              accountstate.Snapshot
	AccountMatchesLedger bool
	Quotes               map[string]market.Snapshot
	Assets               map[string]Asset
	Policy               Policy
	ConfigRevision       int
	Fee                  ashare.Fee
	Now                  time.Time
}
type Constraint struct {
	Code    string `json:"code"`
	Status  string `json:"status"`
	Message string `json:"message"`
}
type Row struct {
	Symbol          string     `json:"symbol"`
	TargetWeight    float64    `json:"target_weight"`
	CurrentQuantity int        `json:"current_quantity"`
	AvailableToSell int        `json:"available_to_sell"`
	FrozenSell      int        `json:"frozen_sell"`
	T1Locked        int        `json:"t1_locked"`
	ReferencePrice  *float64   `json:"reference_price"`
	SourceTime      *time.Time `json:"source_time"`
	QuoteAsOf       *time.Time `json:"quote_as_of"`
	QuoteSource     string     `json:"quote_source"`
	TargetQuantity  *int       `json:"target_quantity"`
	DeltaQuantity   *int       `json:"delta_quantity"`
	BuyQuantity     int        `json:"buy_quantity"`
	SellQuantity    int        `json:"sell_quantity"`
	EstimatedFee    *float64   `json:"estimated_fee"`
	ProjectedWeight *float64   `json:"projected_weight"`
	Sector          string     `json:"sector"`
	Status          string     `json:"status"`
	Reasons         []string   `json:"reasons"`
}
type Summary struct {
	FreshEquity    *float64     `json:"fresh_equity"`
	AvailableCash  float64      `json:"available_cash"`
	FrozenCash     float64      `json:"frozen_cash"`
	CashWeight     float64      `json:"cash_weight"`
	RequiredCash   *float64     `json:"required_cash"`
	EstimatedFees  *float64     `json:"estimated_fees"`
	BuyCost        *float64     `json:"buy_cost"`
	SellProceeds   *float64     `json:"sell_proceeds"`
	ProjectedCash  *float64     `json:"projected_cash"`
	BuyFundingGap  *float64     `json:"buy_funding_gap"`
	ResidualWeight *float64     `json:"residual_weight"`
	Turnover       *float64     `json:"turnover"`
	Constraints    []Constraint `json:"constraints"`
}
type Preview struct {
	Version               int       `json:"version"`
	Scope                 string    `json:"scope"`
	State                 string    `json:"state"`
	AllocationReady       bool      `json:"allocation_ready"`
	CanExecute            bool      `json:"can_execute"`
	RiskChecked           bool      `json:"risk_checked"`
	Research              Portfolio `json:"research"`
	FormedAt              time.Time `json:"formed_at"`
	AccountType           string    `json:"account_type"`
	AccountID             string    `json:"account_id"`
	AccountSnapshotID     string    `json:"account_snapshot_id"`
	AccountGeneration     string    `json:"account_generation"`
	AccountStateHash      string    `json:"account_state_hash"`
	AccountSnapshotHash   string    `json:"account_snapshot_hash"`
	PriceRule             string    `json:"price_rule"`
	ConfigRevision        int       `json:"config_revision"`
	Summary               Summary   `json:"summary"`
	Rows                  []Row     `json:"rows"`
	Unknowns              []string  `json:"unknowns"`
	ExecutionRequirements []string  `json:"execution_requirements"`
}

func fptr(v float64) *float64 { return &v }
func iptr(v int) *int         { return &v }
func cents(v float64) float64 { return math.Round(v*100) / 100 }

// Explicit bounds keep monetary cents and share quantities exact in desktop
// JavaScript. Out-of-range authority is rejected, never rounded into a preview.
func ValidateAccountBounds(a accountstate.Snapshot) error {
	for _, n := range []float64{a.Cash, a.Available, a.FrozenCash, a.Equity, a.DayStartEquity} {
		if !finite(n) || n < 0 || n > 1e12 {
			return fmt.Errorf("账户金额超出预览安全边界")
		}
	}
	for _, h := range a.Holdings {
		if h.Quantity < 0 || h.Quantity > 1_000_000_000 || h.Available < 0 || h.Available > 1_000_000_000 || h.FrozenSell < 0 || h.FrozenSell > 1_000_000_000 {
			return fmt.Errorf("账户股数超出预览安全边界")
		}
	}
	return nil
}

// Calculate never scales a target to make an invalid constraint appear valid.
// Quantities are proposals with explicit blockers, not trade permissions.
func Calculate(in Input) Preview {
	p := Preview{Version: 1, Scope: "portfolio_shadow_preview", State: "blocked", Research: in.Portfolio, FormedAt: in.Now.UTC(), AccountType: string(in.Account.AccountType), AccountID: in.Account.AccountID, AccountSnapshotID: in.Account.SnapshotID, AccountGeneration: strconv.FormatInt(in.Account.Generation, 10), AccountStateHash: decision.AccountHash(in.Account), ConfigRevision: in.ConfigRevision, Rows: []Row{}, Unknowns: []string{"未调用有副作用的CheckOrder；动态市场风险缩放、申报速度、最终流动性与成交未知"}, ExecutionRequirements: []string{"有效独立交易信号", "当前账户重新核验", "独立条件计划及授权", "提交前最终风控"}}
	p.Summary = Summary{AvailableCash: in.Account.Available, FrozenCash: in.Account.FrozenCash, CashWeight: in.Portfolio.CashWeight, Constraints: []Constraint{}}
	p.AccountSnapshotHash = decision.Hash(in.Account)
	p.PriceRule = "server_current_last_rounded_to_tick_reference_only"
	add := func(code, status, msg string) {
		p.Summary.Constraints = append(p.Summary.Constraints, Constraint{code, status, msg})
	}
	if !finite(in.Policy.MaxSingle) || !finite(in.Policy.MaxTotal) || !finite(in.Policy.MaxSector) || in.Policy.MaxSingle < 0 || in.Policy.MaxSingle > 1 || in.Policy.MaxTotal < 0 || in.Policy.MaxTotal > 1 || in.Policy.MaxSector < 0 || in.Policy.MaxSector > 1 {
		in.Policy.Known = false
	}
	if len(in.Account.OpenOrders) > 0 {
		add("outstanding_orders", "blocked", "存在未完成委托，不能忽略其未来暴露或把建议视为已取消/成交")
	}
	accountOK := in.Account.AccountID == "default" && string(in.Account.AccountType) == "SIM" && in.Account.Ready(in.Now, 60*time.Second) && in.AccountMatchesLedger
	if !accountOK {
		add("account", "blocked", "账户必须为当前完整SIM/default快照、已对账、日初已知，且经济状态与账本一致")
	}
	if !in.Policy.Known {
		add("config", "unknown", "服务端配置版本或约束无法完整读取")
	}
	if in.Now.Before(time.Time{}) || in.Now.IsZero() {
		add("clock", "unknown", "服务器时钟不可用")
	}
	quantities, available, frozen := map[string]int{}, map[string]int{}, map[string]int{}
	set := map[string]bool{}
	for s := range in.Portfolio.Weights {
		set[s] = true
	}
	for _, h := range in.Account.Holdings {
		set[h.Symbol] = true
		quantities[h.Symbol] = h.Quantity
		available[h.Symbol] = h.Available
		frozen[h.Symbol] = h.FrozenSell
	}
	if len(set) > 200 {
		add("universe", "blocked", "组合及现持仓超过200只边界；拒绝部分预览")
		return p
	}
	syms := make([]string, 0, len(set))
	for s := range set {
		syms = append(syms, s)
	}
	sort.Strings(syms)
	prices := map[string]float64{}
	allHeldPrices := true
	for _, s := range syms {
		q, ok := in.Quotes[s]
		if ok && q.Symbol == s && !q.Expired(in.Now, 10*time.Second) && !q.Time.After(in.Now.Add(time.Second)) && !q.AsOf.After(in.Now.Add(time.Second)) && finite(q.Last) && finite(cents(q.Last)) && cents(q.Last) > 0 && cents(q.Last) <= 1e6 {
			prices[s] = cents(q.Last)
		} else if quantities[s] > 0 {
			allHeldPrices = false
		}
	}
	if allHeldPrices && accountOK {
		equity := in.Account.Cash
		for _, s := range syms {
			equity += prices[s] * float64(quantities[s])
		}
		if finite(equity) && equity > 0 && equity <= 1e12 {
			p.Summary.FreshEquity = fptr(cents(equity))
			p.Summary.RequiredCash = fptr(cents(equity * in.Portfolio.CashWeight))
		}
	}
	if p.Summary.FreshEquity == nil {
		add("valuation", "unknown", "缺少持仓新鲜源行情或可信账户，不能用旧估值折算全组合股数")
	}
	locked := map[string]bool{}
	for _, s := range in.Portfolio.Options.LockedSymbols {
		locked[s] = true
	}
	complete := true
	buyCost, sellProceeds, totalFees := 0., 0., 0.
	for _, s := range syms {
		r := Row{Symbol: s, TargetWeight: in.Portfolio.Weights[s], CurrentQuantity: quantities[s], FrozenSell: frozen[s], AvailableToSell: available[s] - frozen[s], T1Locked: quantities[s] - available[s], Status: "ready", Reasons: []string{}, Sector: in.Assets[s].Sector}
		block := func(msg string) { r.Status = "blocked"; r.Reasons = append(r.Reasons, msg) }
		price, known := prices[s]
		if !known {
			r.Status = "unknown"
			r.Reasons = append(r.Reasons, "真实源报价缺失、陈旧或未来时间异常")
			complete = false
		} else {
			r.ReferencePrice = fptr(price)
			q := in.Quotes[s]
			r.QuoteSource = q.Source
			at := q.Time
			r.SourceTime = &at
			if !q.AsOf.IsZero() {
				observed := q.AsOf
				r.QuoteAsOf = &observed
			}
		}
		meta := in.Assets[s]
		if !meta.Known {
			block("基础股身份未知")
		}
		if meta.Suspended {
			block("证券停牌")
		}
		if p.Summary.FreshEquity == nil || !known {
			p.Rows = append(p.Rows, r)
			continue
		}
		equity := *p.Summary.FreshEquity
		raw := equity * r.TargetWeight / price
		if !finite(raw) || raw > 1_000_000_000 {
			block("目标股数超出安全计算边界")
			complete = false
			p.Rows = append(p.Rows, r)
			continue
		}
		target, err := ashare.RoundBuy(s, int(math.Floor(raw)))
		if err != nil {
			block("证券整手规则不可计算")
			complete = false
			p.Rows = append(p.Rows, r)
			continue
		}
		r.TargetQuantity = iptr(target)
		delta := target - r.CurrentQuantity
		r.DeltaQuantity = iptr(delta)
		if r.TargetWeight > 0 && target == 0 {
			block("目标金额不足本板块最小买入单位")
		}
		if locked[s] && delta != 0 {
			block("研究锁定证券不能按新目标调仓")
		} else if delta > 0 {
			r.BuyQuantity, _ = ashare.RoundBuy(s, delta)
			if r.BuyQuantity != delta {
				block("目标差额受整手约束，无法精确达到权重")
			}
			if meta.ST && !in.Policy.AllowSTBuy {
				block("服务端规则不允许买入ST")
				r.BuyQuantity = 0
			}
		} else if delta < 0 {
			want := -delta
			r.SellQuantity = want
			if want > r.AvailableToSell {
				r.SellQuantity = r.AvailableToSell
				block("T+1或在途卖单冻结使目标卖出数量不可用")
			}
			if ok, _ := ashare.CanSell(s, r.AvailableToSell, r.SellQuantity); !ok {
				r.SellQuantity = 0
				block("目标卖出差额不符合整手或余股规则")
			}
		}
		// Blocked/unknown basic identity cannot be an actionable proposal.
		if !meta.Known || meta.Suspended {
			r.BuyQuantity, r.SellQuantity = 0, 0
		}
		fee := 0.
		if r.BuyQuantity > 0 {
			amount := price * float64(r.BuyQuantity)
			cost := in.Fee.Cost(amount, false)
			fee += cost
			buyCost += amount + cost
		}
		if r.SellQuantity > 0 {
			amount := price * float64(r.SellQuantity)
			cost := in.Fee.Cost(amount, true)
			fee += cost
			sellProceeds += amount - cost
		}
		r.EstimatedFee = fptr(cents(fee))
		totalFees += fee
		projected := r.CurrentQuantity + r.BuyQuantity - r.SellQuantity
		r.ProjectedWeight = fptr(float64(projected) * price / equity)
		cap := .3
		if in.Portfolio.Options.MaxWeight != nil {
			cap = *in.Portfolio.Options.MaxWeight
		}
		if c, ok := in.Portfolio.Options.MaxWeights[s]; ok {
			cap = c
		}
		if in.Policy.Known && in.Policy.MaxSingle < cap {
			cap = in.Policy.MaxSingle
		}
		if r.TargetWeight > cap+1e-6 || *r.ProjectedWeight > cap+1e-6 {
			block("目标或离散后单票权重超过研究/服务端集中度上限")
		}
		p.Rows = append(p.Rows, r)
	}
	if complete && p.Summary.FreshEquity != nil {
		equity := *p.Summary.FreshEquity
		p.Summary.EstimatedFees = fptr(cents(totalFees))
		p.Summary.BuyCost = fptr(cents(buyCost))
		p.Summary.SellProceeds = fptr(cents(sellProceeds))
		projectedCash := in.Account.Available - buyCost + sellProceeds
		p.Summary.ProjectedCash = fptr(cents(projectedCash))
		gap := math.Max(0, buyCost+*p.Summary.RequiredCash-in.Account.Available)
		p.Summary.BuyFundingGap = fptr(cents(gap))
		if gap > .005 {
			add("cash", "blocked", "现有可用现金不足买入及保留现金；未把尚未成交的卖出款视为已可用，未自动缩放权重")
		}
		if projectedCash+1e-6 < *p.Summary.RequiredCash {
			add("cash_floor", "blocked", "考虑费用和冻结后保留现金不足")
		}
		total, residual, turnover := 0., 0., 0.
		sectors := map[string]float64{}
		researchSectors := map[string]float64{}
		for _, r := range p.Rows {
			if r.ProjectedWeight == nil {
				continue
			}
			w := *r.ProjectedWeight
			total += w
			residual += math.Abs(w - r.TargetWeight)
			turnover += math.Abs(w - float64(r.CurrentQuantity)*prices[r.Symbol]/equity)
			if w > 0 {
				if r.Sector == "" {
					add("sector_unknown", "unknown", r.Symbol+" 缺少权威板块映射")
				} else {
					sectors[r.Sector] += w
				}
			}
			if sector := in.Portfolio.Options.Sectors[r.Symbol]; sector != "" {
				researchSectors[sector] += w
			}
		}
		turnover = (turnover + math.Abs((in.Account.Cash-buyCost+sellProceeds)/equity-in.Account.Cash/equity)) / 2
		p.Summary.ResidualWeight = fptr(residual)
		p.Summary.Turnover = fptr(turnover)
		if in.Policy.Known && total > in.Policy.MaxTotal+1e-6 {
			add("total_concentration", "blocked", "离散后总仓位超过服务端基础上限")
		}
		sectorNames := make([]string, 0, len(sectors))
		for s := range sectors {
			sectorNames = append(sectorNames, s)
		}
		sort.Strings(sectorNames)
		for _, s := range sectorNames {
			w := sectors[s]
			if in.Policy.Known && w > in.Policy.MaxSector+1e-6 {
				add("sector_concentration", "blocked", s+" 板块仓位超过服务端基础上限")
			}
		}
		capNames := make([]string, 0, len(in.Portfolio.Options.SectorCaps))
		for s := range in.Portfolio.Options.SectorCaps {
			capNames = append(capNames, s)
		}
		sort.Strings(capNames)
		for _, s := range capNames {
			cap := in.Portfolio.Options.SectorCaps[s]
			if researchSectors[s] > cap+1e-6 {
				add("research_sector_cap", "blocked", s+" 离散仓位超过研究板块上限")
			}
		}
		if cap := in.Portfolio.Options.MaxTurnover; cap != nil && turnover > *cap+1e-6 {
			add("turnover", "blocked", "离散后换手超过研究约束")
		}
	}
	ready := accountOK && in.Policy.Known && complete && p.Summary.FreshEquity != nil
	for _, r := range p.Rows {
		if r.Status != "ready" {
			ready = false
		}
	}
	for _, c := range p.Summary.Constraints {
		if c.Status != "passed" {
			ready = false
		}
	}
	if ready {
		p.State = "complete"
		p.AllocationReady = true
	}
	if len(p.Summary.Constraints) == 0 && ready {
		add("discrete_constraints", "passed", "只读离散约束检查完成，仍不构成最终风控许可")
	} else if len(p.Summary.Constraints) == 0 {
		add("rows", "blocked", "证券行约束未通过，请查看逐行原因")
	}
	return p
}
