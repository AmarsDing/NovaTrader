package backtest

import (
	"context"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"server/pkg/rules"
)

// 设计文档 §8 参数搜索/样本外/前推口径的包级钉死。engine_test.go 的
// TestOptimizeAndWalkForward 已钉 Optimize 点数/并发一致/样本内外不重叠、前推窗数与拼接长度、
// 网格超 max_runs 报错；本文件补：默认参数与目标映射、空间边界、网格边界与枚举序、
// 随机去重与种子确定、best 的有效性门槛、稳健性邻域中位数、三种 flag 的触发边界、
// min_trades 无效点的端到端语义、前推无有效点回退与拼接衰减、样本外衰减「不足一半」支
// （准入下游逐字消费）、前推拼接跨窗缩放与基准清零。

func flagSet(flags []string, want string) bool {
	for _, f := range flags {
		if f == want {
			return true
		}
	}
	return false
}

func TestSearchConfigNormalizeDefaults(t *testing.T) {
	// 设计 :208-211 的默认口径：max_runs 200（:208）、目标 sharpe 与 min_trades 20（:209）、
	// 样本内 70%（:210）、训练 120 / 测试 40（:211）——网格枚举/有效性/切分全部吃这一组默认值。
	sc := SearchConfig{}
	if err := sc.normalize(); err != nil {
		t.Fatal(err)
	}
	if sc.Method != "grid" || sc.Objective != "sharpe" || sc.MaxRuns != 200 || sc.MinTrades != 20 {
		t.Fatalf("defaults: %+v", sc)
	}
	if sc.ISRatio != 0.7 || sc.TrainDays != 120 || sc.TestDays != 40 || sc.Workers != 1 {
		t.Fatalf("defaults2: %+v", sc)
	}
	// 非正值一律回退默认，不是报错。
	sc2 := SearchConfig{MaxRuns: -3, MinTrades: -1}
	if err := sc2.normalize(); err != nil || sc2.MaxRuns != 200 || sc2.MinTrades != 20 {
		t.Fatalf("negative falls back to defaults: %+v %v", sc2, err)
	}
	// 显式值原样保留。
	sc3 := SearchConfig{Method: "random", Objective: "annual", MaxRuns: 5, MinTrades: 7, ISRatio: 0.5, TrainDays: 10, TestDays: 5, Workers: 4}
	if err := sc3.normalize(); err != nil {
		t.Fatal(err)
	}
	if sc3.Method != "random" || sc3.Objective != "annual" || sc3.MaxRuns != 5 || sc3.MinTrades != 7 {
		t.Fatalf("explicit values must survive: %+v", sc3)
	}
	for _, bad := range []SearchConfig{{Method: "anneal"}, {Objective: "sharpe2"}} {
		if err := bad.normalize(); err == nil || !strings.Contains(err.Error(), "backtest:") {
			t.Fatalf("bad config must error: %+v", bad)
		}
	}
}

func TestObjectiveMapping(t *testing.T) {
	m := Metrics{Sharpe: 1, AnnualReturn: 2, Calmar: 3}
	if objective("sharpe", m) != 1 || objective("annual", m) != 2 || objective("calmar", m) != 3 {
		t.Fatal("three named objectives")
	}
	// 未知目标落到 sharpe 分支（normalize 已拒掉非法值，这里是双保险）。
	if objective("", m) != 1 || objective("weird", m) != 1 {
		t.Fatal("unknown objective falls back to sharpe")
	}
}

func TestSpaceRangeValidation(t *testing.T) {
	spec, ok := rules.Find("breakout_ref")
	if !ok {
		t.Fatal("breakout_ref must be registered (refstrat)")
	}
	// 子范围按步长取格点，含两端（设计 :168 范围 5–60 / 5 内取 10–20 / 5）。
	dims, err := space(spec, map[string]Range{"lookback": {Min: 10, Max: 20, Step: 5}})
	if err != nil || len(dims) != 1 {
		t.Fatalf("subrange: %v %+v", err, dims)
	}
	if !reflect.DeepEqual(dims[0].values, []float64{10, 15, 20}) {
		t.Fatalf("grid values: %v", dims[0].values)
	}
	// Step 缺省回退策略注册的步长。
	dims2, err := space(spec, map[string]Range{"lookback": {Min: 10, Max: 20}})
	if err != nil || !reflect.DeepEqual(dims2[0].values, []float64{10, 15, 20}) {
		t.Fatalf("zero step falls back to spec step: %v %+v", err, dims2)
	}
	// 不存在的参数、越界、反向范围都报错。
	if _, err := space(spec, map[string]Range{"nope": {Min: 1, Max: 2, Step: 1}}); err == nil || !strings.Contains(err.Error(), "no parameter") {
		t.Fatalf("unknown param: %v", err)
	}
	if _, err := space(spec, map[string]Range{"lookback": {Min: 1, Max: 20, Step: 5}}); err == nil || !strings.Contains(err.Error(), "must be within") {
		t.Fatalf("out of spec bounds: %v", err)
	}
	if _, err := space(spec, map[string]Range{"lookback": {Min: 30, Max: 20, Step: 5}}); err == nil {
		t.Fatal("reversed range must error")
	}
	// 不给 Ranges：用策略注册的全部参数范围。
	dims3, err := space(spec, nil)
	if err != nil || len(dims3) != 5 {
		t.Fatalf("full space: %v %d", err, len(dims3))
	}
}

func TestPointsGridBoundaryOrderAndMessage(t *testing.T) {
	dims := []dim{{name: "a", values: []float64{1, 2}}, {name: "b", values: []float64{10, 20}}}
	// 边界：total == MaxRuns 放行（设计 :208 是「超过」才拒绝）。
	pts, err := points(dims, map[string]float64{"keep": 7}, SearchConfig{Method: "grid", MaxRuns: 4}, 0)
	if err != nil || len(pts) != 4 {
		t.Fatalf("total == max_runs must pass: %v %d", err, len(pts))
	}
	// 网格枚举序：最后一维走得最快；base 参数并进每个点。
	wantIdx := [][]int{{0, 0}, {0, 1}, {1, 0}, {1, 1}}
	for i, w := range wantIdx {
		if !reflect.DeepEqual(pts[i].index, w) {
			t.Fatalf("grid order %d: %v", i, pts[i].index)
		}
		if pts[i].Params["keep"] != 7 {
			t.Fatalf("base params must merge: %+v", pts[i].Params)
		}
	}
	// 超限报错的口径：给出总点数和上限，引导收窄范围或换随机。
	_, err = points(dims, nil, SearchConfig{Method: "grid", MaxRuns: 3}, 0)
	if err == nil || !strings.Contains(err.Error(), "grid has 4 points") || !strings.Contains(err.Error(), "max_runs 3") {
		t.Fatalf("overflow message: %v", err)
	}
}

func TestPointsRandomDedupAndDeterminism(t *testing.T) {
	tiny := []dim{{name: "a", values: []float64{1, 2}}, {name: "b", values: []float64{10, 20}}}
	// 设计 :208 随机去重：空间只有 4 点时要 10 个也只能给 4 个，且互不重复。
	pts, err := points(tiny, nil, SearchConfig{Method: "random", MaxRuns: 10}, 42)
	if err != nil || len(pts) != 4 {
		t.Fatalf("random floors to total space: %v %d", err, len(pts))
	}
	seen := map[string]bool{}
	for _, p := range pts {
		key := fmt.Sprintf("%v/%v", p.Params["a"], p.Params["b"])
		if seen[key] {
			t.Fatalf("duplicate draw: %v", key)
		}
		seen[key] = true
	}
	// 大空间按种子抽满 max_runs，去重后恰好 max_runs 个。
	var a, b []float64
	for i := 0; i < 10; i++ {
		a = append(a, float64(i))
		b = append(b, float64(i))
	}
	big := []dim{{name: "a", values: a}, {name: "b", values: b}}
	pts2, err := points(big, nil, SearchConfig{Method: "random", MaxRuns: 25}, 42)
	if err != nil || len(pts2) != 25 {
		t.Fatalf("random draws max_runs: %v %d", err, len(pts2))
	}
	seen2 := map[string]bool{}
	for _, p := range pts2 {
		key := fmt.Sprintf("%v/%v", p.Params["a"], p.Params["b"])
		if seen2[key] {
			t.Fatalf("duplicate in big space: %v", key)
		}
		seen2[key] = true
	}
	// 同一种子两次抽取序列完全一致（种子随任务固定，复现靠它）。
	again, _ := points(big, nil, SearchConfig{Method: "random", MaxRuns: 25}, 42)
	for i := range pts2 {
		if !reflect.DeepEqual(pts2[i].index, again[i].index) {
			t.Fatalf("seed must reproduce the draw sequence at %d", i)
		}
	}
	// 不同种子 42/7 实测序列不同：math/rand 种子源对编译期常量种子的输出是冻结事实
	// （非 §8 承诺的规格），钉它防种子混线重构。
	other, _ := points(big, nil, SearchConfig{Method: "random", MaxRuns: 25}, 7)
	same := true
	for i := range pts2 {
		if !reflect.DeepEqual(pts2[i].index, other[i].index) {
			same = false
			break
		}
	}
	if same {
		t.Fatal("seeds 42/7 must differ in practice on this frozen fixture")
	}
}

func TestBestValidOnly(t *testing.T) {
	// 设计 :209 回合数少于 min_trades 的点记为无效：best 只在 Valid 里挑最大目标值。
	pts := []Point{
		{Objective: 100, Valid: false}, // 无效点目标值再高也不算
		{Objective: 2, Valid: true},
		{Objective: 3, Valid: true},
		{Objective: 3, Valid: true}, // 同值取靠前的
	}
	if b := best(pts); b != 2 {
		t.Fatalf("best = %d", b)
	}
	for i := range pts {
		pts[i].Valid = false
	}
	if best(pts) != -1 {
		t.Fatal("no valid point -> -1")
	}
}

// gridDims3x3 / gridPtsCenter 构造 3×3 网格的纯层夹具。
func gridDims3x3() []dim {
	return []dim{{name: "a", values: []float64{0, 1, 2}}, {name: "b", values: []float64{0, 1, 2}}}
}

// centerGrid 九个点的 index 全给，中心 (1,1) 在切片位置 4。
func centerGrid() []Point {
	pts := make([]Point, 0, 9)
	for i := 0; i < 3; i++ {
		for j := 0; j < 3; j++ {
			pts = append(pts, Point{Valid: true, index: []int{i, j}})
		}
	}
	return pts
}

func TestRobustnessGridMedianAndExclusions(t *testing.T) {
	dims := gridDims3x3()
	pts := centerGrid()
	// 设计 :212 过尖的中位数口径：相邻点（恰好一维差一格）目标值中位数 / 最优值。
	// 四个相邻点 {8,6,4,2}，中位数 (4+6)/2=5，rob = 5/10 = 0.5 —— 恰好在边界上。
	// 四个对角点给 100 做诱饵：若误算进邻域，中位数立即变样——同时钉住对角排除。
	pts[0].Objective = 100 // (0,0) 对角
	pts[1].Objective = 8   // (0,1) 相邻
	pts[2].Objective = 100 // (0,2) 对角
	pts[3].Objective = 4   // (1,0) 相邻
	pts[4].Objective = 10  // (1,1) 最优
	pts[5].Objective = 6   // (1,2) 相邻
	pts[6].Objective = 100 // (2,0) 对角
	pts[7].Objective = 2   // (2,1) 相邻
	pts[8].Objective = 100 // (2,2) 对角
	if r := robustness(pts, 4, dims, true); !near(r, 0.5) {
		t.Fatalf("rob = %v, want 0.5（对角不得计入）", r)
	}
	// 无效相邻点按 0 计入（validObj）：{8,6,4,0} 的中位数仍是 (4+6)/2=5。
	pts[7].Valid = false
	if r := robustness(pts, 4, dims, true); !near(r, 0.5) {
		t.Fatalf("invalid neighbor counts as 0: rob = %v", r)
	}

	// 角落最优只有两个相邻点，偶数个取中间两值的均值：(6+2)/2 / 10 = 0.4。
	corner := centerGrid()
	corner[0].Objective = 10 // (0,0) 最优
	corner[1].Objective = 6  // (0,1) 相邻
	corner[3].Objective = 2  // (1,0) 相邻
	corner[2].Objective, corner[4].Objective = 100, 100
	corner[5].Objective, corner[6].Objective = 100, 100
	corner[7].Objective, corner[8].Objective = 100, 100
	if r := robustness(corner, 0, dims, true); !near(r, 0.4) {
		t.Fatalf("corner rob = %v, want 0.4", r)
	}
	// 最优点目标值不为正：稳健性直接是 0，不拿负数中位数凑数。
	corner[0].Objective = -3
	if r := robustness(corner, 0, dims, true); r != 0 {
		t.Fatalf("non-positive best rob = %v", r)
	}
	// 独点空间没有邻域：稳健性按 1 算（无邻域证据不退化为 0）。
	one := []Point{{Valid: true, Objective: 5, index: []int{0}}}
	if r := robustness(one, 0, []dim{{name: "a", values: []float64{0}}}, true); r != 1 {
		t.Fatalf("singleton rob = %v", r)
	}
}

func TestRobustnessRandomNearestByDistance(t *testing.T) {
	// 随机搜索的邻域：归一化距离最近的 2×维数个点（设计 :212）。
	dims := []dim{{name: "a", values: []float64{0, 1, 2, 3, 4}}}
	pts := []Point{
		{Valid: true, Objective: 100, index: []int{0}}, // 距离 2 格 → 不得入选
		{Valid: true, Objective: 6, index: []int{1}},   // 距离 1 格 → 入选
		{Valid: true, Objective: 10, index: []int{2}},  // 最优
		{Valid: true, Objective: 4, index: []int{3}},   // 距离 1 格 → 入选
		{Valid: true, Objective: 100, index: []int{4}}, // 距离 2 格 → 不得入选
	}
	// 最近的 2 个 {6,4}，中位数 5 → rob 0.5；两端 100 是诱饵。
	if r := robustness(pts, 2, dims, false); !near(r, 0.5) {
		t.Fatalf("random rob = %v, want 0.5（远端不得计入）", r)
	}
}

func TestSpreadFlagsBoundaries(t *testing.T) {
	const (
		fNoValid = "没有回合数达标的参数点"
		fNotPos  = "最优点目标值不为正"
		fSpiky   = "过尖：相邻参数表现明显变差"
		fFlat    = "不敏感：参数基本不影响结果"
	)
	positive := []Point{{Objective: 1.0, Valid: true}, {Objective: 0.4, Valid: true}, {Objective: 0.2, Valid: true}}
	// 无有效点：只带这一个 flag（提前返回，不再看不敏感/过尖）。
	if fs := spreadFlags(positive, -1, 0.1); len(fs) != 1 || fs[0] != fNoValid {
		t.Fatalf("b<0 flags: %v", fs)
	}
	// 过尖边界：rob 0.49 标，0.5 恰好不标，0.51 不标。
	if fs := spreadFlags(positive, 0, 0.49); !flagSet(fs, fSpiky) {
		t.Fatalf("rob 0.49 must flag spiky: %v", fs)
	}
	if fs := spreadFlags(positive, 0, 0.5); flagSet(fs, fSpiky) {
		t.Fatalf("rob 0.5 boundary must not flag: %v", fs)
	}
	// 最优点目标值不为正：取代过尖判断（else-if，rob 再低也不标过尖）。
	neg := []Point{{Objective: 0, Valid: true}, {Objective: -2, Valid: true}, {Objective: -1, Valid: true}}
	fs := spreadFlags(neg, 0, 0.1)
	if !flagSet(fs, fNotPos) || flagSet(fs, fSpiky) {
		t.Fatalf("non-positive best: %v", fs)
	}
	// 不敏感：有效点目标值极差 < 最大绝对值的 5%。{1.0, 0.99, 1.0} 极差 0.01 → 标。
	flat := []Point{{Objective: 1.0, Valid: true}, {Objective: 0.99, Valid: true}, {Objective: 1.0, Valid: true}}
	if fs := spreadFlags(flat, 0, 1); !flagSet(fs, fFlat) {
		t.Fatalf("flat objectives must flag: %v", fs)
	}
	// 无效点的极端目标值不可左右极差（再加一个无效 100 仍是扁平）。
	flatDecoy := append(append([]Point{}, flat...), Point{Objective: 100, Valid: false})
	if fs := spreadFlags(flatDecoy, 0, 1); !flagSet(fs, fFlat) {
		t.Fatalf("invalid points must not widen the range: %v", fs)
	}
	// 边界：极差恰等于 5% 不标（严格小于）；有效点不足 3 个也不标。
	edge := []Point{{Objective: 1.0, Valid: true}, {Objective: 0.95, Valid: true}, {Objective: 1.0, Valid: true}}
	if fs := spreadFlags(edge, 0, 1); flagSet(fs, fFlat) {
		t.Fatalf("<5%% strict boundary: %v", fs)
	}
	two := []Point{{Objective: 1.0, Valid: true}, {Objective: 0.99, Valid: true}}
	if fs := spreadFlags(two, 0, 1); flagSet(fs, fFlat) {
		t.Fatalf("insensitive needs >= 3 valid points: %v", fs)
	}
}

// scriptOptimizeCfg 让 t_script 在 market(…, false) 上跑 Optimize/WalkForward。
func scriptOptimizeCfg(m *Memory) Config {
	c := DefaultConfig()
	c.Strategy, c.Freq, c.Start, c.End = "t_script", Freq1d, dayKey(m.Days[0]), dayKey(m.Days[len(m.Days)-1])
	return c
}

func TestOptimizeMinTradesAndDecayFlag(t *testing.T) {
	m := market(5, 140, 5, false)

	// A. 边界：回合数恰等于 min_trades 记为有效（>=）；样本外零成交零收益也标「样本外衰减」。
	// buys 预算是全局的：样本内评估消耗唯一 1 笔后，样本外颗粒无收——t_script 是单例策略，
	// 这个「耗尽在样本内、样本外零回合」恰是衰减的确定见证。
	cur = &script{buy: map[string]bool{"600000.SH": true}, shares: 1000, buys: 1}
	cfg := scriptOptimizeCfg(m)
	got, oos, err := Optimize(context.Background(), cfg, SearchConfig{Method: "grid", MinTrades: 1, Workers: 2}, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got.Best == nil || oos == nil {
		t.Fatalf("valid boundary must pick a best and run OOS: %+v", got)
	}
	if len(got.Points) != 1 || !got.Points[0].Valid || got.Points[0].Rounds != 1 {
		t.Fatalf("rounds == min_trades must be valid: %+v", got.Points[0])
	}
	if got.OOS.Rounds != 0 || got.OOS.TotalReturn != 0 {
		t.Fatalf("budget exhausted before OOS: %+v", got.OOS)
	}
	// 设计 :210 样本外收益 ≤ 0 → 样本外衰减（零收益同样标，不只是亏损）。
	if !flagSet(got.Flags, "样本外衰减") {
		t.Fatalf("decay flag: %v", got.Flags)
	}

	// B. 回合数 < min_trades：点记为无效（Valid=false 但 Rounds 如实保留 1），
	// 没有任何有效点 → 无 Best、不跑样本外（返回的 *Result 为 nil）、唯一 flag。
	cur = &script{buy: map[string]bool{"600000.SH": true}, shares: 1000, buys: 1}
	got2, oos2, err := Optimize(context.Background(), cfg, SearchConfig{Method: "grid", MinTrades: 2}, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if got2.Best != nil || oos2 != nil {
		t.Fatalf("all-invalid must skip OOS: %+v", got2)
	}
	if got2.Points[0].Valid || got2.Points[0].Rounds != 1 {
		t.Fatalf("invalid must not erase the round count: %+v", got2.Points[0])
	}
	if len(got2.Flags) != 1 || got2.Flags[0] != "没有回合数达标的参数点" || got2.Robustness != 0 {
		t.Fatalf("no-valid flags: %v rob %v", got2.Flags, got2.Robustness)
	}
}

// 「样本外衰减」是双条件判定（设计 :210：OOS 收益 ≤ 0 ‖ OOS 目标值 < 样本内一半），
// 决策本体 decayedByOOS 在此钉死；下游 app/backtest task.go :394 逐字消费该 flag
// 出具准入 Pass/Fail，0.5 阈值与 isObj 守卫漂移必须红灯（端到端的收益≤0 支由
// TestOptimizeMinTradesAndDecayFlag 的 A 例覆盖）。
func TestDecayByOOSBothBranches(t *testing.T) {
	// 收益支：OOS 累计 ≤ 0 直接标，不看目标值——盈利目标放在一边。
	if !decayedByOOS("sharpe", Metrics{Sharpe: 100}, Metrics{TotalReturn: -0.01, Sharpe: 100}) {
		t.Fatal("negative OOS return must decay regardless of objectives")
	}
	// 零收益同样标（≤ 的边界），与 case A 的端到端钉互证。
	if !decayedByOOS("sharpe", Metrics{Sharpe: 100}, Metrics{}) {
		t.Fatal("zero OOS return must decay (boundary of <=)")
	}
	// 一半支：OOS 收益 > 0 才看它；恰好一半不标（严格小于）。
	if !decayedByOOS("sharpe", Metrics{Sharpe: 2}, Metrics{TotalReturn: 0.01, Sharpe: 0.9}) {
		t.Fatal("oos below half of IS must decay")
	}
	if decayedByOOS("sharpe", Metrics{Sharpe: 2}, Metrics{TotalReturn: 0.01, Sharpe: 1}) {
		t.Fatal("oos at exactly half must not decay (strict <)")
	}
	if decayedByOOS("sharpe", Metrics{Sharpe: 2}, Metrics{TotalReturn: 0.01, Sharpe: 1.1}) {
		t.Fatal("oos above half must not decay")
	}
	// 守卫：样本内目标值不为正时一半支不成立（与 0/负基数比一半没有意义）。
	if decayedByOOS("sharpe", Metrics{}, Metrics{TotalReturn: 0.01}) {
		t.Fatal("zero IS objective must not trigger the half branch")
	}
	if decayedByOOS("sharpe", Metrics{Sharpe: -1}, Metrics{TotalReturn: 0.01, Sharpe: -2}) {
		t.Fatal("negative IS objective must not trigger the half branch")
	}
	// 目标值随 objective 切换：annual 口径看 AnnualReturn，不看 Sharpe。
	if !decayedByOOS("annual", Metrics{AnnualReturn: 2}, Metrics{TotalReturn: 0.01, AnnualReturn: 0.9}) {
		t.Fatal("annual objective must read AnnualReturn")
	}
}

// 设计 :211「拼接测试窗权益得到前推指标」的承重语义：optimize.go :531-537 把每窗末权益
// 滚入下一窗（scale = 累计权益 / 初始资金）并把拼接点 Benchmark 清零。两窗都有非零成交时，
// 拼接曲线的总收益必须等于各窗收益的复利乘积——scale 恒 1 或漏清零在这里都必红。
func TestWalkForwardStitchScalesWindowsAndClearsBenchmark(t *testing.T) {
	m := market(5, 140, 5, false)
	cur = &script{buy: map[string]bool{sym: true}, shares: 1000, buys: 100000}
	cfg := scriptOptimizeCfg(m)
	wf, combined, err := WalkForward(context.Background(), cfg, SearchConfig{MinTrades: 1, TrainDays: 60, TestDays: 30}, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Windows) != 2 {
		t.Fatalf("windows = %d", len(wf.Windows))
	}
	r0, r1 := wf.Windows[0].Test.TotalReturn, wf.Windows[1].Test.TotalReturn
	if wf.Windows[0].Test.Rounds == 0 || wf.Windows[1].Test.Rounds == 0 {
		t.Fatalf("both test windows must trade: %+v / %+v", wf.Windows[0].Test, wf.Windows[1].Test)
	}
	if r0 == 0 {
		t.Fatalf("window0 return must be non-zero for the scale witness to bite: %+v", wf.Windows[0].Test)
	}
	if want := (1+r0)*(1+r1) - 1; !near(wf.Combined.TotalReturn, want) {
		t.Fatalf("stitched total return = %v, want compounded %v", wf.Combined.TotalReturn, want)
	}
	if len(combined.Equity) == 0 {
		t.Fatal("combined equity empty")
	}
	for i, p := range combined.Equity {
		if p.Benchmark != 0 {
			t.Fatalf("combined equity[%d].Benchmark = %v, stitch must zero it", i, p.Benchmark)
		}
	}
}

func TestWalkForwardFallbackAndDecayFlag(t *testing.T) {
	m := market(5, 140, 5, false)
	// t_script 全程不买：每个训练窗的唯一点无效。b < 0 时前推窗回退到任务参数
	// （optimize.go :516-518 的承载分支，设计文档未单列，属代码口径）。
	// t_script 零注册参数 ⇒ Normalize→Resolve 产出空但非 nil 的新 map；回退赋值
	// win.Best = cfg.Params 拿到的必非 nil——断言 w.Best != nil 且 len==0 才能区分
	// 「回退分支执行」与「把输入原样透传」。
	cur = &script{}
	cfg := scriptOptimizeCfg(m)
	cfg.Params = map[string]float64{}
	wf, combined, err := WalkForward(context.Background(), cfg, SearchConfig{MinTrades: 1, TrainDays: 60, TestDays: 30}, m, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(wf.Windows) != 2 {
		t.Fatalf("windows = %d", len(wf.Windows))
	}
	for i, w := range wf.Windows {
		if w.Best == nil || len(w.Best) != 0 || w.Robustness != 0 || w.Train != (Metrics{}) {
			t.Fatalf("window %d must fall back to task params with zero train metrics: %+v", i, w)
		}
	}
	// 拼接区间零成交，拼接指标的累计收益为 0 → 样本外衰减（optimize.go :557-559
	// 与单次 Optimize 同口径）。
	if combined == nil || len(combined.Trades) != 0 || wf.Combined.TotalReturn != 0 {
		t.Fatalf("combined must be flat: %+v", wf.Combined)
	}
	if len(wf.Flags) != 1 || wf.Flags[0] != "样本外衰减" {
		t.Fatalf("walkforward decay flag: %v", wf.Flags)
	}
}
