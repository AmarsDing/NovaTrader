package biz

import (
	"fmt"
	"math"
	"regexp"
	"sort"
	"strings"
	"time"
)

var (
	factIDPattern  = regexp.MustCompile(`^F:[A-Za-z0-9_.]+$`)
	intelIDPattern = regexp.MustCompile(`^N:[A-Za-z0-9_.-]+$`)
)

const (
	barsForPack  = 30
	newsForPack  = 10
	newsWindow   = 24 * time.Hour
	conceptRunes = 60
)

// dimOf 按因子编号前缀推断维度。
func dimOf(id string) Dim {
	key := strings.TrimPrefix(id, "F:")
	switch {
	case strings.HasPrefix(key, "capital."):
		return Capital
	case strings.HasPrefix(key, "sector."), strings.HasPrefix(key, "external."), strings.HasPrefix(key, "sentiment."):
		return External
	case strings.HasPrefix(key, "news."):
		return News
	}
	return Technical
}

// checkCallerFacts 校验调用方传入的因子和情报编号，并补上维度。
func checkCallerFacts(facts []Fact, intel []Intel) ([]Fact, error) {
	out := make([]Fact, 0, len(facts))
	for _, f := range facts {
		if !factIDPattern.MatchString(f.ID) {
			return nil, fmt.Errorf("因子编号 %q 不合法，应为 F: 加字母、数字、_、.", f.ID)
		}
		if f.Label == "" {
			f.Label = strings.TrimPrefix(f.ID, "F:")
		}
		switch f.Dim {
		case Technical, News, Capital, External:
		default:
			f.Dim = dimOf(f.ID)
		}
		out = append(out, f)
	}
	for _, n := range intel {
		if !intelIDPattern.MatchString(n.ID) {
			return nil, fmt.Errorf("情报编号 %q 不合法，应为 N: 开头", n.ID)
		}
	}
	return out, nil
}

// buildPack 组装事实包。有 M02 因子时用因子，不再从日线重算指标；没有时退回日线临时计算。
// 同编号时调用方的因子和情报覆盖库里的。
func buildPack(symbol string, asOf time.Time, info *StockInfo, bars []Bar, factors map[string]float64, dbNews []Intel, in AnalyzeInput) FactPack {
	p := FactPack{Symbol: symbol, Name: in.Name, AsOf: asOf, Phase: strings.ToUpper(strings.TrimSpace(in.Phase))}
	if info != nil {
		if p.Name == "" {
			p.Name = info.Name
		}
		p.Facts = append(p.Facts, stockFacts(info)...)
	}
	if len(factors) > 0 {
		p.Facts = append(p.Facts, factorFacts(factors)...)
		p.Facts = append(p.Facts, rawBarFacts(bars)...)
	} else {
		p.Facts = append(p.Facts, barFacts(bars)...)
	}
	if p.Phase != "" {
		p.Facts = append(p.Facts, Fact{ID: "F:sentiment.phase", Label: "情绪阶段", Text: p.Phase, Dim: External})
	}
	p.Facts = mergeFacts(p.Facts, in.Facts)
	p.Intel = mergeIntel(dbNews, in.Intel)
	return p
}

func mergeFacts(base, override []Fact) []Fact {
	idx := map[string]int{}
	out := append([]Fact(nil), base...)
	for i, f := range out {
		idx[f.ID] = i
	}
	for _, f := range override {
		if i, ok := idx[f.ID]; ok {
			out[i] = f
			continue
		}
		idx[f.ID] = len(out)
		out = append(out, f)
	}
	return out
}

func mergeIntel(base, override []Intel) []Intel {
	idx := map[string]int{}
	out := append([]Intel(nil), base...)
	for i, n := range out {
		idx[n.ID] = i
	}
	for _, n := range override {
		if i, ok := idx[n.ID]; ok {
			out[i] = n
			continue
		}
		idx[n.ID] = len(out)
		out = append(out, n)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Importance > out[j].Importance })
	return out
}

func stockFacts(info *StockInfo) []Fact {
	var out []Fact
	if info.CircMV != nil {
		out = append(out, Fact{ID: "F:circ_mv", Label: "流通市值", Value: round(*info.CircMV/1e8, 2), Unit: "亿元", Dim: Capital})
	}
	if info.PE != nil {
		out = append(out, Fact{ID: "F:pe_ttm", Label: "市盈率TTM", Value: round(*info.PE, 2), Dim: Technical})
	}
	if info.Industry != "" {
		out = append(out, Fact{ID: "F:industry", Label: "行业", Text: info.Industry, Dim: External})
	}
	if info.Concept != "" {
		out = append(out, Fact{ID: "F:concept", Label: "概念", Text: clipRunes(info.Concept, conceptRunes), Dim: External})
	}
	return out
}

// barFacts 在还没有 M02 因子时，从日线临时计算指标。
// 价格按最新一根的复权因子做前复权，最新收盘价与不复权一致。
func barFacts(bars []Bar) []Fact {
	n := len(bars)
	if n == 0 {
		return nil
	}
	last := bars[n-1].AdjFactor
	if last <= 0 {
		last = 1
	}
	adj := func(i int, p float64) float64 {
		f := bars[i].AdjFactor
		if f <= 0 {
			f = 1
		}
		return p * f / last
	}
	cur := bars[n-1]
	out := []Fact{
		{ID: "F:close", Label: "最新收盘价", Value: round(cur.Close, 2), Unit: "元", Dim: Technical},
		{ID: "F:amount", Label: "成交额", Value: round(cur.Amount/1e8, 2), Unit: "亿元", Dim: Capital},
	}
	if n >= 2 {
		prev := adj(n-2, bars[n-2].Close)
		if prev > 0 {
			out = append(out,
				Fact{ID: "F:pct_chg", Label: "涨跌幅", Value: round((cur.Close-prev)/prev*100, 2), Unit: "%", Dim: Technical},
				Fact{ID: "F:amplitude", Label: "振幅", Value: round((cur.High-cur.Low)/prev*100, 2), Unit: "%", Dim: Technical},
			)
		}
	}
	if n >= 6 {
		var vol float64
		for i := n - 6; i < n-1; i++ {
			vol += float64(bars[i].Volume)
		}
		if vol > 0 {
			out = append(out, Fact{ID: "F:vol_ratio", Label: "量比(对前5日均量)", Value: round(float64(cur.Volume)/(vol/5), 2), Dim: Capital})
		}
		base := adj(n-6, bars[n-6].Close)
		if base > 0 {
			out = append(out, Fact{ID: "F:ret5", Label: "5日涨幅", Value: round((cur.Close-base)/base*100, 2), Unit: "%", Dim: Technical})
		}
	}
	for _, w := range []int{5, 10, 20} {
		if n < w {
			continue
		}
		var sum float64
		for i := n - w; i < n; i++ {
			sum += adj(i, bars[i].Close)
		}
		out = append(out, Fact{ID: fmt.Sprintf("F:ma%d", w), Label: fmt.Sprintf("MA%d", w), Value: round(sum/float64(w), 2), Unit: "元", Dim: Technical})
	}
	w := 20
	if n < w {
		w = n
	}
	hi, lo := math.Inf(-1), math.Inf(1)
	for i := n - w; i < n; i++ {
		hi = math.Max(hi, adj(i, bars[i].High))
		lo = math.Min(lo, adj(i, bars[i].Low))
	}
	out = append(out,
		Fact{ID: "F:high20", Label: fmt.Sprintf("近%d日最高", w), Value: round(hi, 2), Unit: "元", Dim: Technical},
		Fact{ID: "F:low20", Label: fmt.Sprintf("近%d日最低", w), Value: round(lo, 2), Unit: "元", Dim: Technical},
	)
	if n >= 15 {
		var tr float64
		for i := n - 14; i < n; i++ {
			h, l, pc := adj(i, bars[i].High), adj(i, bars[i].Low), adj(i-1, bars[i-1].Close)
			tr += math.Max(h-l, math.Max(math.Abs(h-pc), math.Abs(l-pc)))
		}
		out = append(out, Fact{ID: "F:atr14", Label: "ATR14", Value: round(tr/14, 2), Unit: "元", Dim: Technical})
	}
	return out
}

// rawBarFacts 只取最新一根的收盘和成交额。指标以 M02 因子为准。
func rawBarFacts(bars []Bar) []Fact {
	if len(bars) == 0 {
		return nil
	}
	cur := bars[len(bars)-1]
	return []Fact{
		{ID: "F:close", Label: "最新收盘价", Value: round(cur.Close, 2), Unit: "元", Dim: Technical},
		{ID: "F:amount", Label: "成交额", Value: round(cur.Amount/1e8, 2), Unit: "亿元", Dim: Capital},
	}
}

type factorSpec struct {
	label  string
	unit   string
	dim    Dim
	div    float64
	mul    float64
	places int
}

func spec(label, unit string, dim Dim, places int) factorSpec {
	return factorSpec{label: label, unit: unit, dim: dim, places: places}
}

// factorCatalog 的键与 M02 设计文档第 4 节、pkg/market 的输出一致。
var factorCatalog = map[string]factorSpec{
	"ma5": spec("MA5", "元", Technical, 2), "ma10": spec("MA10", "元", Technical, 2),
	"ma20": spec("MA20", "元", Technical, 2), "ma60": spec("MA60", "元", Technical, 2),
	"ema12": spec("EMA12", "元", Technical, 2), "ema26": spec("EMA26", "元", Technical, 2),
	"dif": spec("MACD DIF", "元", Technical, 3), "dea": spec("MACD DEA", "元", Technical, 3),
	"macd": spec("MACD", "元", Technical, 3),
	"rsi6": spec("RSI6", "", Technical, 2), "rsi12": spec("RSI12", "", Technical, 2), "rsi24": spec("RSI24", "", Technical, 2),
	"atr14": spec("ATR14", "元", Technical, 2),
	"boll_mid": spec("布林中轨", "元", Technical, 2), "boll_up": spec("布林上轨", "元", Technical, 2), "boll_low": spec("布林下轨", "元", Technical, 2),
	"pct_chg": spec("涨跌幅", "%", Technical, 2), "amplitude": spec("振幅", "%", Technical, 2),
	"vwap": spec("均价", "元", Technical, 2),
	"vol_ratio":     spec("量比", "", Capital, 2),
	"turnover":      spec("换手率", "%", Capital, 2),
	"main_net":      {label: "主力净流入", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"main_net_5d":   {label: "5日主力净流入", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"main_net_ratio": {label: "主力净流入占比", unit: "%", dim: Capital, mul: 100, places: 2},
	"lhb_net":       {label: "龙虎榜净额", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"lhb_inst_net":  {label: "龙虎榜机构净额", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"lhb_quant_net": {label: "龙虎榜量化净额", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"lhb_north_net": {label: "龙虎榜北向净额", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"lhb_hot_buyers": spec("龙虎榜知名游资买入家数", "", Capital, 0),
	"auction_pct":       spec("竞价涨幅", "%", Technical, 2),
	"auction_amount":    {label: "竞价成交额", unit: "亿元", dim: Capital, div: 1e8, places: 2},
	"auction_vol_ratio": spec("竞价量比", "", Capital, 2),
	"auction_turnover":  spec("竞价换手", "%", Capital, 2),
	"auction_amount_prev": spec("竞价额相对昨日", "%", Capital, 2),
	"auction_unmatched": spec("竞价未匹配量", "股", Capital, 0),
	"auction_limit_up":  spec("竞价涨停", "", Technical, 0),
}

func factorFacts(values map[string]float64) []Fact {
	keys := make([]string, 0, len(values))
	for k := range values {
		if factIDPattern.MatchString("F:" + k) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	out := make([]Fact, 0, len(keys))
	for _, k := range keys {
		s, ok := factorCatalog[k]
		if !ok {
			s = factorSpec{label: k, dim: dimOf("F:" + k), places: 4}
		}
		v := values[k]
		if s.div > 0 {
			v /= s.div
		}
		if s.mul > 0 {
			v *= s.mul
		}
		out = append(out, Fact{ID: "F:" + k, Label: s.label, Value: round(v, s.places), Unit: s.unit, Dim: s.dim})
	}
	return out
}

// hasDimData 表示该维度有可供模型引用的数据。没有时系统直接给中性分，不调模型。
func hasDimData(p *FactPack, d Dim) bool {
	if d == News {
		return len(p.Intel) > 0
	}
	for _, f := range p.Facts {
		if f.Dim == d {
			return true
		}
	}
	return false
}

func round(v float64, places int) float64 {
	m := math.Pow(10, float64(places))
	return math.Round(v*m) / m
}

func clipRunes(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}
