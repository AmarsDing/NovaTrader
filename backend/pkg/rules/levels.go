package rules

// BuyLevels 与实盘 M06 扫描一致：以计划价为参考算价位，买入限价取入场区间上沿。回测引擎在下单决策时调用。
func (f *FirstBoard) BuyLevels(s Snapshot, ref float64) (limit, stop, take float64, err error) {
	return buyLevels(f.Price, s, ref)
}

func (p *Pullback) BuyLevels(s Snapshot, ref float64) (limit, stop, take float64, err error) {
	return buyLevels(p.Price, s, ref)
}

func buyLevels(p PriceParams, s Snapshot, ref float64) (float64, float64, float64, error) {
	lv, err := p.Buy(s, ref)
	if err != nil {
		return 0, 0, 0, err
	}
	return lv.EntryHigh, lv.StopLoss, lv.TakeProfit, nil
}
