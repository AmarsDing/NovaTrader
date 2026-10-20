// ChipDistribution 筹码分布（CYQ，目录册增补 B3）。
// 换手衰减模型：前复权价 + 换手率百分数 → 成本分布与获利比例。
// 算法口径逐行对齐 research-worker/vibe-astock/calc/indicators.py 的 chip_distribution
// 纯函数（含全部输入校验纪律），作为计算型端点的确定性内核——不落库、无外部源。
//
// 数据纪律（没取到≠0）：状态机 ChipOK / ChipNotMeaningful / ChipError——
// 序列过短、参数非法、坏数据一律显式状态，绝不产出伪装成有效值的 0 结果。
package market

import (
	"fmt"
	"math"
	"sort"
	"time"
)

const (
	// ChipOK 分布有效，各统计列有值。
	ChipOK = "ok"
	// ChipNotMeaningful 序列不足以构成有意义的分布（K 线少于 MinPoints）。
	ChipNotMeaningful = "not_meaningful"
	// ChipError 输入非法（参数越域/坏数据）——调用侧应视为请求错误而非数据缺失。
	ChipError = "error"
)

const (
	chipDefaultGrid      = 300
	chipDefaultDecay     = 1.0
	chipDefaultMinPoints = 20
)

// ChipBar 是筹码分布的一行输入（前复权价 + 换手率百分数），按 Day 任意序传入，内部升序。
type ChipBar struct {
	Day   time.Time
	High  float64 // 前复权
	Low   float64
	Close float64
	// Turn 换手率百分数（0..100，0.31 = 0.31%）；停牌日不应出现在序列里。
	Turn float64
}

// ChipOptions 调参。零值字段取其默认：GridSize 300、Decay 1、MinPoints 20。
type ChipOptions struct {
	GridSize  int     // 成本网格数，合法域 50..2000
	Decay     float64 // 换手衰减系数，合法域 (0,5]
	MinPoints int     // 最少 K 线数，合法域 ≥2
}

// ChipResult 是筹码分布结果。Status!=ChipOK 时衍生列全为 nil（不落 0 冒充）。
type ChipResult struct {
	Status string
	Reason string

	Profit          *float64 // 获利比例（0..1，现价以下筹码占比；现价格点按半步容差计入）
	AvgCost         *float64 // 加权平均成本
	Cost90Lo        *float64 // 90% 成本区间 [P05, P95]
	Cost90Hi        *float64
	Cost70Lo        *float64 // 70% 成本区间 [P15, P85]
	Cost70Hi        *float64
	Concentration90 *float64 // (P95−P05)/(P95+P05)；分母为 0 的退化情形保持 nil（不落 0 冒充集中度）
	Concentration70 *float64 // 同上（P85−P15)/(P85+P15)
	PeakPrice       *float64 // 筹码峰价（存量最大的格点）

	Price          float64 // 现价 = 末行收盘（事实列；ChipOK 时有值）
	Days           int     // 实际入算 K 线数（事实列）
	CumTurnoverPct float64 // 窗口内换手率合计（事实列，非衍生）
	WindowStart    time.Time
	WindowEnd      time.Time
}

func chipErr(format string, args ...any) ChipResult {
	return ChipResult{Status: ChipError, Reason: fmt.Sprintf(format, args...)}
}

// normalize 归一参数（零值取默认）并核验合法域。
func (o ChipOptions) normalize() (grid int, decay float64, minPoints int, err *ChipResult) {
	grid = o.GridSize
	if grid == 0 {
		grid = chipDefaultGrid
	}
	if grid < 50 || grid > 2000 {
		r := chipErr("grid_size 须为 50..2000 的整数（0 取默认 300）")
		return 0, 0, 0, &r
	}
	decay = o.Decay
	if decay == 0 {
		decay = chipDefaultDecay
	}
	if decay <= 0 || decay > 5 {
		r := chipErr("decay 须在 (0, 5]（0 取默认 1）")
		return 0, 0, 0, &r
	}
	minPoints = o.MinPoints
	if minPoints == 0 {
		minPoints = chipDefaultMinPoints
	}
	if minPoints < 2 {
		r := chipErr("min_points 须为 ≥2 的整数（0 取默认 20）")
		return 0, 0, 0, &r
	}
	return grid, decay, minPoints, nil
}

// triWeights 当日成交在 [low,high] 上的三角分布权重（峰在均价 avg=hlc3），归一化。
// 振幅窄于网格步长时映射到最近格点（不丢换手衰减）。
func triWeights(grid []float64, low, high, avg float64) []float64 {
	w := make([]float64, len(grid))
	if high < low {
		return w
	}
	if high-low < 1e-9 {
		j := 0
		for i := range grid {
			if math.Abs(grid[i]-low) < math.Abs(grid[j]-low) {
				j = i
			}
		}
		w[j] = 1.0
		return w
	}
	avg = math.Min(math.Max(avg, low), high)
	for i, g := range grid {
		switch {
		case g >= low && g <= avg:
			if avg-low > 1e-9 {
				w[i] = (g - low) / (avg - low)
			} else {
				w[i] = 1.0
			}
		case g > avg && g <= high:
			if high-avg > 1e-9 {
				w[i] = (high - g) / (high - avg)
			} else {
				w[i] = 1.0
			}
		}
	}
	tot := 0.0
	for _, x := range w {
		tot += x
	}
	if tot > 0 {
		for i := range w {
			w[i] /= tot
		}
		return w
	}
	j := 0
	for i := range grid {
		if math.Abs(grid[i]-avg) < math.Abs(grid[j]-avg) {
			j = i
		}
	}
	w[j] = 1.0
	return w
}

// ChipDistribution 计算筹码分布。bars 校验失败或参数越域 → Status=ChipError（属请求错误）；
// 行数不足 MinPoints → ChipNotMeaningful（属数据不足）；否则 ChipOK。
func ChipDistribution(bars []ChipBar, opts ChipOptions) ChipResult {
	gridSize, decay, minPoints, perr := opts.normalize()
	if perr != nil {
		return *perr
	}
	if len(bars) == 0 {
		return chipErr("K 线序列为空")
	}
	rows := make([]ChipBar, len(bars))
	copy(rows, bars)
	sort.Slice(rows, func(i, j int) bool { return rows[i].Day.Before(rows[j].Day) })
	var cum float64
	lo, hi := math.Inf(1), math.Inf(-1)
	for i, r := range rows {
		if i > 0 && r.Day.Equal(rows[i-1].Day) {
			return chipErr("有重复日期 %s", r.Day.Format("2006-01-02"))
		}
		switch {
		case math.IsNaN(r.High) || math.IsNaN(r.Low) || math.IsNaN(r.Close) || math.IsNaN(r.Turn),
			math.IsInf(r.High, 0) || math.IsInf(r.Low, 0) || math.IsInf(r.Close, 0) || math.IsInf(r.Turn, 0):
			return chipErr("%s 存在非有限数", r.Day.Format("2006-01-02"))
		case r.High <= 0 || r.Low <= 0 || r.Close <= 0:
			return chipErr("%s 价格须为正", r.Day.Format("2006-01-02"))
		case r.High < r.Low:
			return chipErr("%s high < low", r.Day.Format("2006-01-02"))
		case r.Close < r.Low || r.Close > r.High:
			return chipErr("%s close 不在 [low, high] 内", r.Day.Format("2006-01-02"))
		case r.Turn < 0 || r.Turn > 100:
			return chipErr("%s turn 须在 0..100（百分数）", r.Day.Format("2006-01-02"))
		}
		lo = math.Min(lo, r.Low)
		hi = math.Max(hi, r.High)
		cum += r.Turn
	}
	if len(rows) < minPoints {
		return ChipResult{
			Status:         ChipNotMeaningful,
			Reason:         fmt.Sprintf("K 线仅 %d 根,少于 %d", len(rows), minPoints),
			Days:           len(rows),
			CumTurnoverPct: cum,
			WindowStart:    rows[0].Day,
			WindowEnd:      rows[len(rows)-1].Day,
		}
	}
	// 成本网格：[lo−pad, hi+pad]，pad = 振幅×2%（零振幅退 2% 价距,最小 0.01）。
	pad := (hi - lo) * 0.02
	if pad == 0 {
		pad = math.Max(lo*0.02, 0.01)
	}
	g0, g1 := lo-pad, hi+pad
	grid := make([]float64, gridSize)
	for i := range grid {
		grid[i] = g0 + (g1-g0)*float64(i)/float64(gridSize-1)
	}
	var chips []float64
	for _, r := range rows {
		t := r.Turn / 100.0 * decay
		t = math.Min(math.Max(t, 0), 1)
		w := triWeights(grid, r.Low, r.High, (r.High+r.Low+r.Close)/3.0)
		sum := 0.0
		for _, x := range w {
			sum += x
		}
		if sum <= 0 {
			continue
		}
		if chips == nil {
			chips = w // 首日 = 期初全部流通筹码
			continue
		}
		for i := range chips {
			chips[i] = chips[i]*(1.0-t) + w[i]*t
		}
	}
	if chips == nil {
		return chipErr("所有交易日的价格区间都无效,无法构建分布")
	}
	tot := 0.0
	for _, c := range chips {
		tot += c
	}
	if tot <= 0 {
		return chipErr("筹码总量为 0")
	}
	for i := range chips {
		chips[i] /= tot
	}
	price := rows[len(rows)-1].Close
	cumf := make([]float64, gridSize)
	acc := 0.0
	for i, c := range chips {
		acc += c
		cumf[i] = acc
	}
	priceAt := func(q float64) float64 {
		for i, cv := range cumf {
			if cv >= q {
				if i == 0 || cumf[i] == cumf[i-1] {
					return grid[i]
				}
				frac := (q - cumf[i-1]) / (cumf[i] - cumf[i-1])
				return grid[i-1] + (grid[i]-grid[i-1])*frac
			}
		}
		return grid[gridSize-1]
	}
	p05, p15, p85, p95 := priceAt(0.05), priceAt(0.15), priceAt(0.85), priceAt(0.95)
	peak := 0
	for i := range chips {
		if chips[i] > chips[peak] {
			peak = i
		}
	}
	step := (g1 - g0) / float64(gridSize-1)
	profit := 0.0
	avgCost := 0.0
	for i, c := range chips {
		if grid[i] <= price+step/2 {
			profit += c
		}
		avgCost += grid[i] * c
	}
	conc90 := new(float64)
	if p95+p05 != 0 {
		*conc90 = (p95 - p05) / (p95 + p05)
	} else {
		conc90 = nil
	}
	conc70 := new(float64)
	if p85+p15 != 0 {
		*conc70 = (p85 - p15) / (p85 + p15)
	} else {
		conc70 = nil
	}
	res := ChipResult{
		Status:          ChipOK,
		Reason:          "",
		Profit:          &profit,
		AvgCost:         &avgCost,
		Cost90Lo:        &p05,
		Cost90Hi:        &p95,
		Cost70Lo:        &p15,
		Cost70Hi:        &p85,
		Concentration90: conc90,
		Concentration70: conc70,
		PeakPrice:       new(float64),
		Price:           price,
		Days:            len(rows),
		CumTurnoverPct:  cum,
		WindowStart:     rows[0].Day,
		WindowEnd:       rows[len(rows)-1].Day,
	}
	*res.PeakPrice = grid[peak]
	return res
}
