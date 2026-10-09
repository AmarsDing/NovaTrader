package market

// AuctionInput 是一只股票的竞价数据。
type AuctionInput struct {
	Open       Snapshot  // 09:25 撮合后的第一条快照
	PreMatch   *Snapshot // 09:20～09:25 最后一条快照，可为空
	MA5Volume  float64   // 前 5 日日均成交量，股
	FloatShare int64
	PrevAmount float64 // 昨日成交额，元
	UpLimit    float64 // 今日涨停价，0 表示无限制
}

// AuctionFactors 计算竞价因子，键见 M02 设计文档 7 节。
func AuctionFactors(in AuctionInput) Values {
	out := Values{}
	s := in.Open
	open := s.Open
	if open <= 0 {
		open = s.Last
	}
	if s.PreClose > 0 && open > 0 {
		out["auction_pct"] = (open - s.PreClose) / s.PreClose * 100
	}
	out["auction_amount"] = s.Amount
	if in.MA5Volume > 0 {
		out["auction_vol_ratio"] = float64(s.Volume) / (in.MA5Volume / BarsPerDay)
	}
	if in.FloatShare > 0 {
		out["auction_turnover"] = float64(s.Volume) / float64(in.FloatShare) * 100
	}
	if in.PrevAmount > 0 {
		out["auction_amount_prev"] = s.Amount / in.PrevAmount * 100
	}
	if in.PreMatch != nil {
		_, bv := in.PreMatch.Bid1()
		_, av := in.PreMatch.Ask1()
		out["auction_unmatched"] = float64(bv - av)
	}
	limitUp := 0.0
	if in.UpLimit > 0 && open >= in.UpLimit-priceEps {
		limitUp = 1
	}
	out["auction_limit_up"] = limitUp
	return out
}
