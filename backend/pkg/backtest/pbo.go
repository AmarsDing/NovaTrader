package backtest

import (
	"errors"
	"fmt"
	"math"
	"sort"
)

// PboResult 是一次 CSCV（组合对称交叉验证）的 PBO 读数。
//
// 口径（Bailey et al. 2014, "The Probability of Backtest Overfitting"）：
// 把样本期切成 S 段（偶数），枚举全部 C(S, S/2) 种「一半段做样本内（IS）、
// 另一半做样本外（OOS）」的划分；每个划分用 IS 平均绩效选出最优候选，
// 再看它在 OOS 平均绩效中的降序秩 r（1=OOS 最优，并列取平均秩），换算
// Bailey 的相对秩 ω = (N+1-r)/(N+1)（ω→1 表示 OOS 最优），
// logit λ = ln(ω/(1-ω))。PBO = 满足 λ ≤ 0（即 OOS 秩落入后半）的划分占比。
// PBO 越高，最优参数是运气成分的概率越大。
type PboResult struct {
	// PBO 是过拟合概率读数，∈[0,1]；Splits>0 时才有意义。
	PBO float64 `json:"pbo"`
	// MedianLogit 是全部划分 logit 的中位数（负值偏未过拟合）。
	MedianLogit float64 `json:"median_logit"`
	// Splits 是实际计算的组合数（= C(S, S/2)）。
	Splits int `json:"splits"`
	// Segments 是段数 S。
	Segments int `json:"segments"`
	// Candidates 是剔除无效行后实际参与比较的候选数。
	Candidates int `json:"candidates"`
}

// PboSplitsMax 是 CSCV 直接调用的组合数硬上限（S=20 → C(20,10)=184756 已远超该值；
// 组合枚举是 O(splits×N log N)，上限只防失控输入）。超出视为参数错误，
// 调用方自行降 S 而不是截断统计（截断的 PBO 是假读数）。生产链路经 CSCVSegments
// 定档（阶梯上限 8 → C(8,4)=70），永不触顶；该闸守的是任意矩阵直调 CSCV 的旁路。
const PboSplitsMax = 300000

// CSCVSegments 为一次参数搜索挑选 CSCV 的段数 S（W8c 接线：evaluate 采集段指标前定档）。
//
// 口径：S 越大统计功效越高，唯一硬约束是每段装得下 ≥2 个交易日（单日产不出
// 离散度，段 Sharpe 必退化为 NaN）。CSCV 的组合枚举量只取决于段数——C(S,S/2)
// 在 S=8 时也仅 70 个划分，候选数只线性放大每趟开销（O(splits×N log N)），
// 故段数定档与候选数无关（审查批 wf_9932525c 勘正：早版用 binom(候选数,S/2)
// 当闸看错了量，把默认 200 点网格无谓压到 S=4）。
// 阶梯 {8, 6, 4} 取满足 isDays >= 2*S 的最大值；全不满足返回 0（不强行采段）。
// 段数在评估前选定，不随候选有效性剔除漂移，保证同一份任务输入永远得到同一份
// 矩阵形状（computeTrustBar 纯函数语义延续到采集侧）。
func CSCVSegments(isDays int) int {
	for _, s := range []int{8, 6, 4} {
		if isDays < 2*s {
			continue
		}
		return s
	}
	return 0
}

// errPBO 族供 errors.Is 判定，调用方据此把「不可计算」显成「未验证」而非 0。
var (
	// ErrPBOInvalid 是输入形态非法（段数奇数/<4、行不齐、无行）。
	ErrPBOInvalid = errors.New("pbo: invalid matrix shape")
	// ErrPBOInsufficient 是剔除无效行后候选不足 2 个，无法比较。
	ErrPBOInsufficient = errors.New("pbo: fewer than 2 valid candidates")
)

// CSCV 计算候选×段绩效矩阵的 PBO。
//
// matrix[c][s] = 候选 c 在段 s 的绩效（用哪种指标由调用方定，如段内收益）；
// 全部由调用方事先按同一口径算好，本函数不接触行情/时钟，同输入同输出。
//
// 约束：S ≥ 4 且为偶数；每行恰好 S 段；行内 NaN/±Inf 使该候选整行失效
// （「没取到≠0」：半截数据不作 0 参与排序）；剔除后有效候选 < 2 报错。
func CSCV(matrix [][]float64) (*PboResult, error) {
	if len(matrix) == 0 {
		return nil, fmt.Errorf("%w: no rows", ErrPBOInvalid)
	}
	s := len(matrix[0])
	if s < 4 || s%2 != 0 {
		return nil, fmt.Errorf("%w: segments must be even and >= 4, got %d", ErrPBOInvalid, s)
	}
	splits := binom(s, s/2)
	if splits > PboSplitsMax {
		return nil, fmt.Errorf("%w: C(%d,%d)=%d exceeds cap %d; reduce segments", ErrPBOInvalid, s, s/2, splits, PboSplitsMax)
	}
	// 校验等长并剔除含非有限值的行（索引保持原序，结果确定性不依赖输入顺序之外的因素）。
	valid := make([][]float64, 0, len(matrix))
	for c, row := range matrix {
		if len(row) != s {
			return nil, fmt.Errorf("%w: row %d has %d segments, want %d", ErrPBOInvalid, c, len(row), s)
		}
		ok := true
		for _, v := range row {
			if math.IsNaN(v) || math.IsInf(v, 0) {
				ok = false
				break
			}
		}
		if ok {
			valid = append(valid, row)
		}
	}
	n := len(valid)
	if n < 2 {
		return nil, fmt.Errorf("%w: %d valid of %d", ErrPBOInsufficient, n, len(matrix))
	}

	half := s / 2
	isAvg := make([]float64, n)
	oosAvg := make([]float64, n)
	logits := make([]float64, 0, splits)
	// idx 为当前 IS 组合（升序、长 half），迭代全组合；J 与其补集各算一次
	// （同一对切分的两个方向的最优可能不同，Bailey 原实现同样全枚举）。
	var overfit float64
	for combo := initialCombo(half); combo != nil; combo = nextCombo(combo, s) {
		inCombo := make([]bool, s)
		for _, j := range combo {
			inCombo[j] = true
		}
		for c := 0; c < n; c++ {
			isSum, oosSum := 0.0, 0.0
			for j, v := range valid[c] {
				if inCombo[j] {
					isSum += v
				} else {
					oosSum += v
				}
			}
			isAvg[c] = isSum / float64(half)
			oosAvg[c] = oosSum / float64(s-half)
		}
		best := argmax(isAvg)
		r := midrank(oosAvg, best) // 降序秩，1=OOS 最优
		omega := (float64(n) - r + 1) / float64(n+1) // Bailey 相对秩：OOS 最优→N/(N+1)
		lambda := math.Log(omega / (1 - omega))
		logits = append(logits, lambda)
		if lambda <= 0 {
			overfit++
		}
	}
	sort.Float64s(logits)
	return &PboResult{
		PBO:         overfit / float64(len(logits)),
		MedianLogit: median(logits),
		Splits:      len(logits),
		Segments:    s,
		Candidates:  n,
	}, nil
}

// binom 计算 C(n, k)，k>n/2 时对称化；超 int 范围前本函数的调用方已用 PboSplitsMax 拦截。
func binom(n, k int) int {
	if k > n-k {
		k = n - k
	}
	b := 1
	for i := 0; i < k; i++ {
		b = b * (n - i) / (i + 1)
	}
	return b
}

// initialCombo 返回升序起点组合 [0..k-1]；k<=0 返回 nil。
func initialCombo(k int) []int {
	if k <= 0 {
		return nil
	}
	c := make([]int, k)
	for i := range c {
		c[i] = i
	}
	return c
}

// nextCombo 用字典序递进组合；到顶返回 nil。组合 c 必须升序且元素 < n。
func nextCombo(c []int, n int) []int {
	k := len(c)
	for i := k - 1; i >= 0; i-- {
		if c[i] < n-k+i {
			next := make([]int, k)
			copy(next, c)
			next[i]++
			for j := i + 1; j < k; j++ {
				next[j] = next[j-1] + 1
			}
			return next
		}
	}
	return nil
}

// argmax 返回最大值下标；并列取最先（确定性，避免最优漂移）。
func argmax(v []float64) int {
	best := 0
	for i := 1; i < len(v); i++ {
		if v[i] > v[best] {
			best = i
		}
	}
	return best
}

// midrank 返回 v[i] 在降序排名中的秩（1=最大）；并列 t 个取平均秩 g+1+(t-1)/2（g=严格更大数）。
func midrank(v []float64, i int) float64 {
	lo, hi := 1, 1 // 并列段最优/最劣秩（1 基）；初值含自身占位
	for j, x := range v {
		if j == i {
			continue
		}
		if x > v[i] {
			lo++
			hi++
		} else if x == v[i] {
			hi++
		}
	}
	return float64(lo+hi) / 2
}

func median(sorted []float64) float64 {
	n := len(sorted)
	if n == 0 {
		return 0
	}
	if n%2 == 1 {
		return sorted[n/2]
	}
	return (sorted[n/2-1] + sorted[n/2]) / 2
}
