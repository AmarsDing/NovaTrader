package market

import (
	"encoding/json"
	"fmt"
	"time"
)

// Phase 是情绪阶段。
type Phase string

const (
	PhaseIce     Phase = "ICE"
	PhaseRecover Phase = "RECOVER"
	PhaseWarm    Phase = "WARM"
	PhaseHot     Phase = "HOT"
	PhaseFade    Phase = "FADE"
)

// StockDay 是情绪截面里一只股票当天的状态。
type StockDay struct {
	Symbol      string
	PctChg      float64
	Amount      float64
	Suspended   bool
	NoLimit     bool   // 新股无涨跌幅限制期，不计入涨跌停
	UpStatus    string // SEALED / BROKEN / 空
	DownStatus  string
	Consecutive int
	PrevSealed  bool // 昨日收盘涨停
	OneWord     bool // 今日一字涨停
}

// Sentiment 是全市场情绪截面。口径见 M02 需求文档第 2 节。
type Sentiment struct {
	TradeDate     time.Time `json:"trade_date"`
	AsOf          time.Time `json:"as_of"`
	UpCount       int       `json:"up_count"`
	DownCount     int       `json:"down_count"`
	BrokenCount   int       `json:"broken_count"`
	BrokenRate    float64   `json:"broken_rate"`
	MaxHeight     int       `json:"max_height"`
	ProfitEffect  float64   `json:"profit_effect"`
	Advance       int       `json:"advance"`
	Decline       int       `json:"decline"`
	Flat          int       `json:"flat"`
	Amount        float64   `json:"amount"`
	Phase         Phase     `json:"phase"`
	ScoreCoef     float64   `json:"score_coef"`
	PositionScale float64   `json:"position_scale"`
	Stale         bool      `json:"stale"`
}

// ComputeSentiment 统计截面指标。阶段由 Rules.Classify 另行给出。
func ComputeSentiment(stocks []StockDay) Sentiment {
	var s Sentiment
	var effSum float64
	var effN int
	for _, st := range stocks {
		if st.Suspended {
			continue
		}
		s.Amount += st.Amount
		switch {
		case st.PctChg > 0:
			s.Advance++
		case st.PctChg < 0:
			s.Decline++
		default:
			s.Flat++
		}
		if st.PrevSealed && !st.OneWord {
			effSum += st.PctChg
			effN++
		}
		if st.NoLimit {
			continue
		}
		switch st.UpStatus {
		case StatusSealed:
			s.UpCount++
			if st.Consecutive > s.MaxHeight {
				s.MaxHeight = st.Consecutive
			}
		case StatusBroken:
			s.BrokenCount++
		}
		if st.DownStatus == StatusSealed {
			s.DownCount++
		}
	}
	if n := s.UpCount + s.BrokenCount; n > 0 {
		s.BrokenRate = float64(s.BrokenCount) / float64(n)
	}
	if effN > 0 {
		s.ProfitEffect = effSum / float64(effN)
	}
	return s
}

// PhaseRule 是一个阶段的系数。
type PhaseRule struct {
	ScoreCoef     float64 `json:"score_coef"`
	PositionScale float64 `json:"position_scale"`
}

// Rules 是情绪阶段的阈值和系数，存 strategy_config 的 market.sentiment.rules。
type Rules struct {
	IceMaxUp          int                 `json:"ice_max_up"`
	IceMinDown        int                 `json:"ice_min_down"`
	IceMaxProfit      float64             `json:"ice_max_profit"`
	FadeMinBrokenRate float64             `json:"fade_min_broken_rate"`
	HotMinUp          int                 `json:"hot_min_up"`
	HotMaxBrokenRate  float64             `json:"hot_max_broken_rate"`
	HotMinHeight      int                 `json:"hot_min_height"`
	HotMinProfit      float64             `json:"hot_min_profit"`
	WarmMinUp         int                 `json:"warm_min_up"`
	WarmMaxBrokenRate float64             `json:"warm_max_broken_rate"`
	WarmMinProfit     float64             `json:"warm_min_profit"`
	Phases            map[Phase]PhaseRule `json:"phases"`

	// 风险度（给 M08）：指数当日涨跌幅%、指数 5 分钟涨跌幅%、炸板率。
	IndexSymbol     string  `json:"index_symbol"`
	Risk3IndexPct   float64 `json:"risk3_index_pct"`
	Risk3Index5m    float64 `json:"risk3_index_5m"`
	Risk2IndexPct   float64 `json:"risk2_index_pct"`
	Risk2Index5m    float64 `json:"risk2_index_5m"`
	Risk1IndexPct   float64 `json:"risk1_index_pct"`
	Risk1BrokenRate float64 `json:"risk1_broken_rate"`

	// 急拉异动（给 M06 的 market.alert）：SurgeMinutes 分钟内涨幅 ≥ SurgePct%。
	SurgePct     float64 `json:"surge_pct"`
	SurgeMinutes int     `json:"surge_minutes"`
}

// RulesKey 是阈值在 strategy_config 里的键。
const RulesKey = "market.sentiment.rules"

// DefaultRules 是 M02 设计文档第 6 节的初值。
func DefaultRules() Rules {
	return Rules{
		IceMaxUp: 30, IceMinDown: 30, IceMaxProfit: -3,
		FadeMinBrokenRate: 0.40,
		HotMinUp:          80, HotMaxBrokenRate: 0.25, HotMinHeight: 5, HotMinProfit: 3,
		WarmMinUp: 50, WarmMaxBrokenRate: 0.35, WarmMinProfit: 0,
		// 仓位系数与 M08 设计文档 3.2 的 phase_scale 默认值一致；M08 以自己的配置为准。
		Phases: map[Phase]PhaseRule{
			PhaseIce:     {ScoreCoef: 0.80, PositionScale: 0.3},
			PhaseFade:    {ScoreCoef: 0.85, PositionScale: 0.3},
			PhaseHot:     {ScoreCoef: 1.05, PositionScale: 0.8},
			PhaseWarm:    {ScoreCoef: 1.00, PositionScale: 1.0},
			PhaseRecover: {ScoreCoef: 0.95, PositionScale: 0.6},
		},
		IndexSymbol:   "000001.SH",
		Risk3IndexPct: -3, Risk3Index5m: -1.5,
		Risk2IndexPct: -2, Risk2Index5m: -1.0,
		Risk1IndexPct: -1, Risk1BrokenRate: 0.4,
		SurgePct: 3, SurgeMinutes: 5,
	}
}

// RiskLevel 返回 0 正常 … 3 极高。阶段 ICE 至少为 2，FADE 至少为 1。
func (r Rules) RiskLevel(s Sentiment, indexPct, index5m float64) int {
	switch {
	case indexPct <= r.Risk3IndexPct || index5m <= r.Risk3Index5m:
		return 3
	case indexPct <= r.Risk2IndexPct || index5m <= r.Risk2Index5m || s.Phase == PhaseIce:
		return 2
	case indexPct <= r.Risk1IndexPct || s.BrokenRate >= r.Risk1BrokenRate || s.Phase == PhaseFade:
		return 1
	default:
		return 0
	}
}

// State 是 market.state 的载荷，给 M08 做仓位系数和熔断。
type State struct {
	TradeDate  time.Time `json:"trade_date"`
	AsOf       time.Time `json:"as_of"`
	Phase      Phase     `json:"phase"`
	RiskLevel  int       `json:"risk_level"`
	IndexPct   float64   `json:"index_pct"`
	Index5m    float64   `json:"index_pct_5m"`
	BrokenRate float64   `json:"broken_rate"`
	ScoreCoef  float64   `json:"score_coef"`
	Position   float64   `json:"position_scale"`
	Stale      bool      `json:"stale"`
}

// ParseRules 解析配置。缺的字段沿用默认值。
func ParseRules(raw string) (Rules, error) {
	r := DefaultRules()
	if raw == "" {
		return r, nil
	}
	if err := json.Unmarshal([]byte(raw), &r); err != nil {
		return DefaultRules(), fmt.Errorf("market: sentiment rules: %w", err)
	}
	if r.Phases == nil {
		r.Phases = map[Phase]PhaseRule{}
	}
	for p, v := range DefaultRules().Phases {
		if _, ok := r.Phases[p]; !ok {
			r.Phases[p] = v
		}
	}
	return r, nil
}

// Classify 按 ICE → FADE → HOT → WARM → RECOVER 的顺序取第一个满足的阶段，并填入系数。
// prev 是前一交易日收盘阶段。
func (r Rules) Classify(s Sentiment, prev Phase) Sentiment {
	switch {
	case (s.UpCount < r.IceMaxUp && s.DownCount >= r.IceMinDown) || s.ProfitEffect <= r.IceMaxProfit:
		s.Phase = PhaseIce
	case s.BrokenRate >= r.FadeMinBrokenRate || ((prev == PhaseWarm || prev == PhaseHot) && s.ProfitEffect < 0):
		s.Phase = PhaseFade
	case s.UpCount >= r.HotMinUp && s.BrokenRate < r.HotMaxBrokenRate && s.MaxHeight >= r.HotMinHeight && s.ProfitEffect >= r.HotMinProfit:
		s.Phase = PhaseHot
	case s.UpCount >= r.WarmMinUp && s.BrokenRate < r.WarmMaxBrokenRate && s.ProfitEffect > r.WarmMinProfit:
		s.Phase = PhaseWarm
	default:
		s.Phase = PhaseRecover
	}
	pr := r.Phases[s.Phase]
	s.ScoreCoef, s.PositionScale = pr.ScoreCoef, pr.PositionScale
	return s
}
