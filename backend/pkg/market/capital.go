package market

// Seat 标签，与 seat_tag.tag 一致。
const (
	SeatInstitution = "INSTITUTION"
	SeatHotMoney    = "HOT_MONEY"
	SeatQuant       = "QUANT"
	SeatNorth       = "NORTH"
)

// Flow 是一天的资金流向，金额单位元。
type Flow struct {
	MainNet float64
}

// FlowFactors 计算资金因子。flows 按日期升序，最后一个是当天；amount 是当天成交额。
func FlowFactors(amount float64, flows []Flow) Values {
	out := Values{}
	if len(flows) == 0 {
		return out
	}
	today := flows[len(flows)-1]
	out["main_net"] = today.MainNet
	if amount > 0 {
		out["main_net_ratio"] = today.MainNet / amount
	}
	start := len(flows) - 5
	if start < 0 {
		start = 0
	}
	var sum float64
	for _, f := range flows[start:] {
		sum += f.MainNet
	}
	out["main_net_5d"] = sum
	return out
}

// Seat 是龙虎榜上一个席位的买卖额。
type Seat struct {
	Name string
	Buy  float64
	Sell float64
}

// LhbFactors 计算龙虎榜因子。tags 为席位名 → 标签；没有标签的席位只计入 lhb_net。
func LhbFactors(seats []Seat, tags map[string]string) Values {
	out := Values{}
	if len(seats) == 0 {
		return out
	}
	var net, inst, quant, north float64
	hot := 0
	for _, s := range seats {
		n := s.Buy - s.Sell
		net += n
		switch tags[s.Name] {
		case SeatInstitution:
			inst += n
		case SeatQuant:
			quant += n
		case SeatNorth:
			north += n
		case SeatHotMoney:
			if n > 0 {
				hot++
			}
		}
	}
	out["lhb_net"] = net
	out["lhb_inst_net"] = inst
	out["lhb_quant_net"] = quant
	out["lhb_north_net"] = north
	out["lhb_hot_buyers"] = float64(hot)
	return out
}
