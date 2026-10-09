package backtest

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/pkg/rules"
)

// Range 缩小一个参数的搜索范围。必须落在策略注册的 Min..Max 内。
type Range struct {
	Min  float64 `json:"min"`
	Max  float64 `json:"max"`
	Step float64 `json:"step"`
}

// SearchConfig 是参数搜索、前推、进化共用的设置。见设计文档第 8 节。
type SearchConfig struct {
	Method    string           `json:"method"`
	Objective string           `json:"objective"`
	MaxRuns   int              `json:"max_runs"`
	MinTrades int              `json:"min_trades"`
	Ranges    map[string]Range `json:"ranges"`
	ISRatio   float64          `json:"is_ratio"`
	TrainDays int              `json:"train_days"`
	TestDays  int              `json:"test_days"`
	Workers   int              `json:"workers"`
}

func (s *SearchConfig) normalize() error {
	if s.Method == "" {
		s.Method = "grid"
	}
	if s.Method != "grid" && s.Method != "random" {
		return fmt.Errorf("backtest: method must be grid or random")
	}
	if s.Objective == "" {
		s.Objective = "sharpe"
	}
	if s.Objective != "sharpe" && s.Objective != "annual" && s.Objective != "calmar" {
		return fmt.Errorf("backtest: objective must be sharpe, annual or calmar")
	}
	if s.MaxRuns <= 0 {
		s.MaxRuns = 200
	}
	if s.MinTrades <= 0 {
		s.MinTrades = 20
	}
	if s.ISRatio <= 0 || s.ISRatio >= 1 {
		s.ISRatio = 0.7
	}
	if s.TrainDays <= 0 {
		s.TrainDays = 120
	}
	if s.TestDays <= 0 {
		s.TestDays = 40
	}
	if s.Workers <= 0 {
		s.Workers = 1
	}
	return nil
}

func objective(name string, m Metrics) float64 {
	switch name {
	case "annual":
		return m.AnnualReturn
	case "calmar":
		return m.Calmar
	default:
		return m.Sharpe
	}
}

// Point 是一组参数在一段区间上的结果。
type Point struct {
	Params      map[string]float64 `json:"params"`
	Objective   float64            `json:"objective"`
	Valid       bool               `json:"valid"`
	TotalReturn float64            `json:"total_return"`
	MaxDrawdown float64            `json:"max_drawdown"`
	Sharpe      float64            `json:"sharpe"`
	Rounds      int                `json:"rounds"`
	index       []int
}

// OptimizeResult 是样本内搜索、样本外验证的结果。
type OptimizeResult struct {
	Method     string             `json:"method"`
	Objective  string             `json:"objective"`
	ISStart    string             `json:"is_start"`
	ISEnd      string             `json:"is_end"`
	OOSStart   string             `json:"oos_start"`
	OOSEnd     string             `json:"oos_end"`
	Points     []Point            `json:"points"`
	Best       map[string]float64 `json:"best"`
	BestIS     Metrics            `json:"best_is"`
	OOS        Metrics            `json:"oos"`
	Robustness float64            `json:"robustness"`
	Flags      []string           `json:"flags"`
}

// Window 是滚动前推的一窗。
type Window struct {
	TrainStart string             `json:"train_start"`
	TrainEnd   string             `json:"train_end"`
	TestStart  string             `json:"test_start"`
	TestEnd    string             `json:"test_end"`
	Best       map[string]float64 `json:"best"`
	Train      Metrics            `json:"train"`
	Test       Metrics            `json:"test"`
	Robustness float64            `json:"robustness"`
}

// WalkForwardResult 是全部测试窗拼接后的表现。
type WalkForwardResult struct {
	Windows  []Window `json:"windows"`
	Combined Metrics  `json:"combined"`
	Flags    []string `json:"flags"`
}

// EvolveResult 是自省触发的有界参数进化：样本内选最优候选，样本外与当前参数比较。
type EvolveResult struct {
	ISStart   string             `json:"is_start"`
	ISEnd     string             `json:"is_end"`
	OOSStart  string             `json:"oos_start"`
	OOSEnd    string             `json:"oos_end"`
	Base      map[string]float64 `json:"base"`
	BaseOOS   Metrics            `json:"base_oos"`
	Best      map[string]float64 `json:"best"`
	BestIS    Metrics            `json:"best_is"`
	BestOOS   Metrics            `json:"best_oos"`
	Points    []Point            `json:"points"`
	Improved  bool               `json:"improved"`
	Objective string             `json:"objective"`
	Reason    string             `json:"reason"`
}

type dim struct {
	name   string
	values []float64
}

// space 列出参与搜索的维度。没有给 Ranges 时用策略注册的全部参数范围。
func space(spec rules.Spec, ranges map[string]Range) ([]dim, error) {
	known := map[string]rules.ParamSpec{}
	for _, p := range spec.Params {
		known[p.Name] = p
	}
	var dims []dim
	if len(ranges) == 0 {
		for _, p := range spec.Params {
			dims = append(dims, dim{name: p.Name, values: p.Grid()})
		}
		return dims, nil
	}
	names := make([]string, 0, len(ranges))
	for n := range ranges {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		p, ok := known[n]
		if !ok {
			return nil, fmt.Errorf("backtest: %s has no parameter %q", spec.Name, n)
		}
		r := ranges[n]
		if r.Min < p.Min-1e-9 || r.Max > p.Max+1e-9 || r.Max < r.Min {
			return nil, fmt.Errorf("backtest: range of %s must be within [%v, %v]", n, p.Min, p.Max)
		}
		step := r.Step
		if step <= 0 {
			step = p.Step
		}
		sub := rules.ParamSpec{Name: n, Default: p.Default, Min: r.Min, Max: r.Max, Step: step}
		dims = append(dims, dim{name: n, values: sub.Grid()})
	}
	return dims, nil
}

// points 生成参数点：网格为笛卡尔积；随机按种子抽格点并去重。
func points(dims []dim, base map[string]float64, sc SearchConfig, seed int64) ([]Point, error) {
	total := 1
	for _, d := range dims {
		total *= len(d.values)
		if total > 1_000_000 {
			break
		}
	}
	mk := func(idx []int) Point {
		p := Point{Params: map[string]float64{}, index: append([]int(nil), idx...)}
		for k, v := range base {
			p.Params[k] = v
		}
		for i, d := range dims {
			p.Params[d.name] = d.values[idx[i]]
		}
		return p
	}
	var out []Point
	if sc.Method == "grid" {
		if total > sc.MaxRuns {
			return nil, fmt.Errorf("backtest: grid has %d points, more than max_runs %d; narrow the ranges or use random", total, sc.MaxRuns)
		}
		idx := make([]int, len(dims))
		for {
			out = append(out, mk(idx))
			k := len(dims) - 1
			for k >= 0 {
				idx[k]++
				if idx[k] < len(dims[k].values) {
					break
				}
				idx[k] = 0
				k--
			}
			if k < 0 {
				break
			}
		}
		return out, nil
	}
	rng := rand.New(rand.NewSource(seed))
	seen := map[string]bool{}
	want := sc.MaxRuns
	if total < want {
		want = total
	}
	for tries := 0; len(out) < want && tries < want*50; tries++ {
		idx := make([]int, len(dims))
		var key strings.Builder
		for i, d := range dims {
			idx[i] = rng.Intn(len(d.values))
			key.WriteString(strconv.Itoa(idx[i]) + ",")
		}
		if seen[key.String()] {
			continue
		}
		seen[key.String()] = true
		out = append(out, mk(idx))
	}
	return out, nil
}

// evaluate 在 [start, end] 上跑全部参数点。结果按输入顺序返回，与并发数无关。
func evaluate(ctx context.Context, cfg Config, src Source, pts []Point, start, end time.Time, sc SearchConfig, progress func()) error {
	var wg sync.WaitGroup
	jobs := make(chan int)
	errs := make([]error, len(pts))
	for w := 0; w < sc.Workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := range jobs {
				c := cfg
				c.Params = pts[i].Params
				c.Start, c.End = dayKey(start), dayKey(end)
				res, err := Run(ctx, c, src, nil)
				if err != nil {
					errs[i] = err
				} else {
					m := Compute(c.InitialCash, c.RiskFree, res.Equity, res.Rounds, res.Trades)
					pts[i].Objective = objective(sc.Objective, m)
					pts[i].Valid = m.Rounds >= sc.MinTrades
					pts[i].TotalReturn, pts[i].MaxDrawdown, pts[i].Sharpe, pts[i].Rounds = m.TotalReturn, m.MaxDrawdown, m.Sharpe, m.Rounds
				}
				if progress != nil {
					progress()
				}
			}
		}()
	}
	for i := range pts {
		if ctx.Err() != nil {
			break
		}
		jobs <- i
	}
	close(jobs)
	wg.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	for _, err := range errs {
		if err != nil {
			return err
		}
	}
	return nil
}

// best 返回有效点里目标值最大的下标（同值取靠前的），没有有效点返回 -1。
func best(pts []Point) int {
	b := -1
	for i, p := range pts {
		if p.Valid && (b < 0 || p.Objective > pts[b].Objective) {
			b = i
		}
	}
	return b
}

// robustness 是最优点相邻点目标值中位数 / 最优值。网格：某一维差一格；随机：归一化距离最近的 2×维数个点。
func robustness(pts []Point, b int, dims []dim, grid bool) float64 {
	bp := pts[b]
	if bp.Objective <= 0 {
		return 0
	}
	var vals []float64
	if grid {
		for i, p := range pts {
			if i == b {
				continue
			}
			diff, ok := 0, true
			for k := range dims {
				d := p.index[k] - bp.index[k]
				if d != 0 {
					diff++
					if d != 1 && d != -1 {
						ok = false
					}
				}
			}
			if ok && diff == 1 {
				vals = append(vals, validObj(p))
			}
		}
	} else {
		type nd struct {
			d float64
			v float64
		}
		var near []nd
		for i, p := range pts {
			if i == b {
				continue
			}
			dist := 0.0
			for k, d := range dims {
				if n := len(d.values) - 1; n > 0 {
					x := float64(p.index[k]-bp.index[k]) / float64(n)
					dist += x * x
				}
			}
			near = append(near, nd{d: dist, v: validObj(p)})
		}
		sort.SliceStable(near, func(i, j int) bool { return near[i].d < near[j].d })
		k := 2 * len(dims)
		for i := 0; i < len(near) && i < k; i++ {
			vals = append(vals, near[i].v)
		}
	}
	if len(vals) == 0 {
		return 1
	}
	sort.Float64s(vals)
	med := vals[len(vals)/2]
	if len(vals)%2 == 0 {
		med = (vals[len(vals)/2-1] + vals[len(vals)/2]) / 2
	}
	return med / bp.Objective
}

// validObj 无效点按 0 计入邻域。
func validObj(p Point) float64 {
	if !p.Valid {
		return 0
	}
	return p.Objective
}

func spreadFlags(pts []Point, b int, rob float64) []string {
	var flags []string
	if b < 0 {
		return []string{"没有回合数达标的参数点"}
	}
	if pts[b].Objective <= 0 {
		flags = append(flags, "最优点目标值不为正")
	} else if rob < 0.5 {
		flags = append(flags, "过尖：相邻参数表现明显变差")
	}
	lo, hi, n := math.Inf(1), math.Inf(-1), 0
	for _, p := range pts {
		if p.Valid {
			lo, hi, n = math.Min(lo, p.Objective), math.Max(hi, p.Objective), n+1
		}
	}
	if n >= 3 && hi-lo < 0.05*math.Max(math.Abs(hi), math.Abs(lo)) {
		flags = append(flags, "不敏感：参数基本不影响结果")
	}
	return flags
}

func prepare(ctx context.Context, cfg *Config, sc *SearchConfig, src Source) (rules.Spec, *Cache, error) {
	if err := sc.normalize(); err != nil {
		return rules.Spec{}, nil, err
	}
	if _, _, err := cfg.Normalize(); err != nil {
		return rules.Spec{}, nil, err
	}
	spec, _ := rules.Find(cfg.Strategy)
	cache, err := NewCache(ctx, src, *cfg)
	if err != nil {
		return rules.Spec{}, nil, err
	}
	return spec, cache, nil
}

// Optimize 在前 is_ratio 的交易日搜索参数，用最优点在剩余交易日验证。返回结果和样本外那次运行。
func Optimize(ctx context.Context, cfg Config, sc SearchConfig, src Source, progress Progress) (*OptimizeResult, *Result, error) {
	spec, cache, err := prepare(ctx, &cfg, &sc, src)
	if err != nil {
		return nil, nil, err
	}
	days := cache.Days()
	split := int(float64(len(days)) * sc.ISRatio)
	if split < 20 || len(days)-split < 10 {
		return nil, nil, fmt.Errorf("backtest: %d 个交易日不够切样本内外（样本内至少 20、样本外至少 10）", len(days))
	}
	dims, err := space(spec, sc.Ranges)
	if err != nil {
		return nil, nil, err
	}
	pts, err := points(dims, cfg.Params, sc, cfg.Seed)
	if err != nil {
		return nil, nil, err
	}
	done, total := 0, len(pts)+1
	var mu sync.Mutex
	tick := func() {
		mu.Lock()
		done++
		if progress != nil {
			progress(done, total)
		}
		mu.Unlock()
	}
	if err := evaluate(ctx, cfg, cache, pts, days[0], days[split-1], sc, tick); err != nil {
		return nil, nil, err
	}
	out := &OptimizeResult{
		Method: sc.Method, Objective: sc.Objective,
		ISStart: dayKey(days[0]), ISEnd: dayKey(days[split-1]),
		OOSStart: dayKey(days[split]), OOSEnd: dayKey(days[len(days)-1]),
		Points: pts,
	}
	b := best(pts)
	if b >= 0 {
		out.Robustness = robustness(pts, b, dims, sc.Method == "grid")
	}
	out.Flags = spreadFlags(pts, b, out.Robustness)
	if b < 0 {
		return out, nil, nil
	}
	out.Best = pts[b].Params
	isCfg := cfg
	isCfg.Params, isCfg.Start, isCfg.End = out.Best, out.ISStart, out.ISEnd
	isRes, err := Run(ctx, isCfg, cache, nil)
	if err != nil {
		return nil, nil, err
	}
	out.BestIS = Compute(cfg.InitialCash, cfg.RiskFree, isRes.Equity, isRes.Rounds, isRes.Trades)
	oosCfg := cfg
	oosCfg.Params, oosCfg.Start, oosCfg.End = out.Best, out.OOSStart, out.OOSEnd
	oos, err := Run(ctx, oosCfg, cache, nil)
	if err != nil {
		return nil, nil, err
	}
	tick()
	out.OOS = Compute(cfg.InitialCash, cfg.RiskFree, oos.Equity, oos.Rounds, oos.Trades)
	isObj, oosObj := objective(sc.Objective, out.BestIS), objective(sc.Objective, out.OOS)
	if out.OOS.TotalReturn <= 0 || (isObj > 0 && oosObj < 0.5*isObj) {
		out.Flags = append(out.Flags, "样本外衰减")
	}
	return out, oos, nil
}

// WalkForward 按 train_days 训练、test_days 测试滚动，测试窗步进。返回结果和拼接后的测试窗运行。
func WalkForward(ctx context.Context, cfg Config, sc SearchConfig, src Source, progress Progress) (*WalkForwardResult, *Result, error) {
	spec, cache, err := prepare(ctx, &cfg, &sc, src)
	if err != nil {
		return nil, nil, err
	}
	days := cache.Days()
	if len(days) < sc.TrainDays+sc.TestDays {
		return nil, nil, fmt.Errorf("backtest: %d 个交易日不够一窗（训练 %d + 测试 %d）", len(days), sc.TrainDays, sc.TestDays)
	}
	dims, err := space(spec, sc.Ranges)
	if err != nil {
		return nil, nil, err
	}
	windows := (len(days) - sc.TrainDays) / sc.TestDays
	out := &WalkForwardResult{}
	combined := &Result{Config: cfg, RejectWhy: map[string]int{}}
	equity := cfg.InitialCash
	for w := 0; w < windows; w++ {
		tr0, tr1 := w*sc.TestDays, w*sc.TestDays+sc.TrainDays-1
		te0, te1 := tr1+1, tr1+sc.TestDays
		pts, err := points(dims, cfg.Params, sc, cfg.Seed+int64(w))
		if err != nil {
			return nil, nil, err
		}
		if err := evaluate(ctx, cfg, cache, pts, days[tr0], days[tr1], sc, nil); err != nil {
			return nil, nil, err
		}
		win := Window{TrainStart: dayKey(days[tr0]), TrainEnd: dayKey(days[tr1]), TestStart: dayKey(days[te0]), TestEnd: dayKey(days[te1])}
		b := best(pts)
		if b < 0 {
			win.Best = cfg.Params
		} else {
			win.Best = pts[b].Params
			win.Robustness = robustness(pts, b, dims, sc.Method == "grid")
			win.Train = Metrics{TotalReturn: pts[b].TotalReturn, MaxDrawdown: pts[b].MaxDrawdown, Sharpe: pts[b].Sharpe, Rounds: pts[b].Rounds}
		}
		tc := cfg
		tc.Params, tc.Start, tc.End = win.Best, win.TestStart, win.TestEnd
		res, err := Run(ctx, tc, cache, nil)
		if err != nil {
			return nil, nil, err
		}
		win.Test = Compute(cfg.InitialCash, cfg.RiskFree, res.Equity, res.Rounds, res.Trades)
		out.Windows = append(out.Windows, win)
		scale := equity / cfg.InitialCash
		for _, p := range res.Equity {
			p.Equity, p.Cash, p.Benchmark = p.Equity*scale, p.Cash*scale, 0
			combined.Equity = append(combined.Equity, p)
		}
		if n := len(combined.Equity); n > 0 {
			equity = combined.Equity[n-1].Equity
		}
		base := len(combined.Trades)
		for _, t := range res.Trades {
			t.Seq += base
			combined.Trades = append(combined.Trades, t)
		}
		combined.Rounds = append(combined.Rounds, res.Rounds...)
		combined.Rejects += res.Rejects
		for k, v := range res.RejectWhy {
			combined.RejectWhy[k] += v
		}
		combined.Warnings = res.Warnings
		combined.DataHash, combined.DataCutoff = res.DataHash, res.DataCutoff
		if progress != nil {
			progress(w+1, windows)
		}
	}
	combined.FillsHash = FillsHash(combined.Trades)
	out.Combined = Compute(cfg.InitialCash, cfg.RiskFree, combined.Equity, combined.Rounds, combined.Trades)
	if out.Combined.TotalReturn <= 0 {
		out.Flags = append(out.Flags, "样本外衰减")
	}
	return out, combined, nil
}

// Evolve 在最后 oosDays 个交易日之前的 isDays 个交易日里选候选，再在样本外与当前参数比较。
func Evolve(ctx context.Context, cfg Config, sc SearchConfig, candidates []map[string]float64, isDays, oosDays int, src Source) (*EvolveResult, error) {
	_, cache, err := prepare(ctx, &cfg, &sc, src)
	if err != nil {
		return nil, err
	}
	days := cache.Days()
	if isDays <= 0 || oosDays <= 0 || len(days) < isDays+oosDays {
		return nil, fmt.Errorf("backtest: %d 个交易日不够进化（样本内 %d + 样本外 %d）", len(days), isDays, oosDays)
	}
	days = days[len(days)-isDays-oosDays:]
	out := &EvolveResult{
		ISStart: dayKey(days[0]), ISEnd: dayKey(days[isDays-1]),
		OOSStart: dayKey(days[isDays]), OOSEnd: dayKey(days[len(days)-1]),
		Base: cfg.Params, Objective: sc.Objective,
	}
	pts := make([]Point, len(candidates))
	for i, c := range candidates {
		pts[i] = Point{Params: c}
	}
	if err := evaluate(ctx, cfg, cache, pts, days[0], days[isDays-1], sc, nil); err != nil {
		return nil, err
	}
	out.Points = pts
	b := best(pts)
	if b < 0 {
		out.Reason = "样本内没有回合数达标的候选"
		return out, nil
	}
	out.Best = pts[b].Params
	out.BestIS = Metrics{TotalReturn: pts[b].TotalReturn, MaxDrawdown: pts[b].MaxDrawdown, Sharpe: pts[b].Sharpe, Rounds: pts[b].Rounds}
	run := func(p map[string]float64) (Metrics, error) {
		c := cfg
		c.Params, c.Start, c.End = p, out.OOSStart, out.OOSEnd
		res, err := Run(ctx, c, cache, nil)
		if err != nil {
			return Metrics{}, err
		}
		return Compute(c.InitialCash, c.RiskFree, res.Equity, res.Rounds, res.Trades), nil
	}
	if out.BaseOOS, err = run(cfg.Params); err != nil {
		return nil, err
	}
	if out.BestOOS, err = run(out.Best); err != nil {
		return nil, err
	}
	bo, co := objective(sc.Objective, out.BaseOOS), objective(sc.Objective, out.BestOOS)
	switch {
	case out.BestOOS.Rounds < sc.MinTrades:
		out.Reason = fmt.Sprintf("样本外回合数 %d 少于 %d", out.BestOOS.Rounds, sc.MinTrades)
	case co <= bo:
		out.Reason = fmt.Sprintf("样本外目标值 %.4f 不高于当前参数 %.4f", co, bo)
	default:
		out.Improved = true
		out.Reason = fmt.Sprintf("样本外目标值 %.4f 高于当前参数 %.4f", co, bo)
	}
	return out, nil
}

// Neighbors 生成有界候选：每个参数在当前值上下各一个步长内取值，最多 max 组，按 seed 抽样，不含当前参数本身。
func Neighbors(spec rules.Spec, current map[string]float64, max int, seed int64) []map[string]float64 {
	var dims []dim
	for _, p := range spec.Params {
		cur := p.Snap(current[p.Name])
		vals := []float64{cur}
		if p.Step > 0 {
			if v := cur - p.Step; v >= p.Min-1e-9 {
				vals = append(vals, p.Snap(v))
			}
			if v := cur + p.Step; v <= p.Max+1e-9 {
				vals = append(vals, p.Snap(v))
			}
		}
		dims = append(dims, dim{name: p.Name, values: vals})
	}
	all, _ := points(dims, current, SearchConfig{Method: "grid", MaxRuns: math.MaxInt32}, 0)
	var out []map[string]float64
	for _, p := range all {
		same := true
		for _, d := range dims {
			if p.Params[d.name] != d.values[0] {
				same = false
				break
			}
		}
		if !same {
			out = append(out, p.Params)
		}
	}
	if len(out) > max {
		rng := rand.New(rand.NewSource(seed))
		rng.Shuffle(len(out), func(i, j int) { out[i], out[j] = out[j], out[i] })
		out = out[:max]
	}
	return out
}
