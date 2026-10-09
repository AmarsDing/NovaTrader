package rules

import "strings"

// Templates 按参数表构造第一版的两个模板。实盘从这里取；回测按名称从注册表取，两边是同一份实现。
func Templates(get Lookup) []Strategy {
	price := LoadPrice(get)
	return []Strategy{
		NewFirstBoard(LoadFirstBoard(get), price),
		NewPullback(LoadPullback(get), price),
	}
}

// ByName 在列表里按名称找模板，找不到返回 nil。
func ByName(list []Strategy, name string) Strategy {
	for _, s := range list {
		if s.Name() == name {
			return s
		}
	}
	return nil
}

// priceSpecs 是两个模板共用的价格参数，注册表里不带 m06. 前缀。
func priceSpecs() []ParamSpec {
	d := DefaultPrice()
	return []ParamSpec{
		{Name: "atr_period", Title: "ATR 周期", Default: float64(d.ATRPeriod), Min: 5, Max: 30, Step: 1},
		{Name: "stop_atr", Title: "止损 ATR 倍数", Default: d.StopATR, Min: 1, Max: 4, Step: 0.5},
		{Name: "take_atr", Title: "止盈 ATR 倍数", Default: d.TakeATR, Min: 1, Max: 6, Step: 0.5},
		{Name: "max_stop_pct", Title: "最大止损幅度", Default: d.MaxStopPct, Min: 0.03, Max: 0.10, Step: 0.01},
	}
}

// specLookup 把注册表参数名映射回参数表键：m06.<prefix>.<name> 或 m06.<name>。
func specLookup(prefix string, p map[string]float64) Lookup {
	return func(key string) (float64, bool) {
		name := strings.TrimPrefix(key, "m06.")
		name = strings.TrimPrefix(name, prefix)
		v, ok := p[name]
		return v, ok
	}
}

// specKey 是 specLookup 的反向：注册表参数名 → 参数表键。价格参数不带模板前缀。
func specKey(prefix string) func(string) string {
	return func(name string) string {
		for _, p := range priceSpecs() {
			if p.Name == name {
				return "m06." + name
			}
		}
		return "m06." + prefix + name
	}
}

func init() {
	fb := DefaultFirstBoard()
	Register(Spec{
		Name:  NameFirstBoard,
		Title: "模板一：首板 / 弱转强",
		Params: append([]ParamSpec{
			{Name: "no_limit_days", Title: "首板前无涨停天数", Default: float64(fb.NoLimitDays), Min: 3, Max: 20, Step: 1},
			{Name: "amount_ratio", Title: "昨日放量倍数下限", Default: fb.AmountRatio, Min: 1, Max: 4, Step: 0.25},
			{Name: "gap_max", Title: "开盘涨幅上限", Default: fb.GapMax, Min: 0.02, Max: 0.09, Step: 0.01},
			{Name: "strength_min", Title: "现涨幅下限", Default: fb.StrengthMin, Min: 0, Max: 0.06, Step: 0.01},
			{Name: "hold_days", Title: "最长持有交易日", Default: float64(fb.HoldDays), Min: 1, Max: 5, Step: 1},
		}, priceSpecs()...),
		Key: specKey("fb."),
		New: func(p map[string]float64) (Strategy, error) {
			get := specLookup("fb.", p)
			return NewFirstBoard(LoadFirstBoard(get), LoadPrice(get)), nil
		},
	})
	pd := DefaultPullback()
	Register(Spec{
		Name:  NamePullback,
		Title: "模板二：强势回踩",
		Params: append([]ParamSpec{
			{Name: "lookback", Title: "强势回看天数", Default: float64(pd.Lookback), Min: 5, Max: 20, Step: 1},
			{Name: "dd_min", Title: "回撤下限", Default: pd.DDMin, Min: 0.04, Max: 0.12, Step: 0.01},
			{Name: "dd_max", Title: "回撤上限", Default: pd.DDMax, Min: 0.12, Max: 0.30, Step: 0.01},
			{Name: "shrink_max", Title: "缩量比上限", Default: pd.ShrinkMax, Min: 0.4, Max: 1, Step: 0.05},
			{Name: "hold_days", Title: "最长持有交易日", Default: float64(pd.HoldDays), Min: 1, Max: 10, Step: 1},
		}, priceSpecs()...),
		Key: specKey("pd."),
		New: func(p map[string]float64) (Strategy, error) {
			get := specLookup("pd.", p)
			return NewPullback(LoadPullback(get), LoadPrice(get)), nil
		},
	})
}
