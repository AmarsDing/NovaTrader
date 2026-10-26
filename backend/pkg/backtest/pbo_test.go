package backtest

import (
	"errors"
	"math"
	"testing"
)

// pbo 口径钉死（Bailey et al. 2014 CSCV）：S=4 的全组合手工追踪与三个退化形态，
// 调约定一旦漂移（秩方向反、λ≤0 阈值错、组合漏枚举）这些用例即红。

// 手工追踪：A:[3,3,0,0] B:[2,2,2,2] C:[1,1,1,1]，S=4 六种切分——
// {0,1}: IS 最优 A，OOS{2,3} 秩 3 → ω=0.25 → λ<0 过拟合；
// {2,3}: IS 最优 B，OOS{0,1} 中 A=3>B=2 → B 秩 2/3 → ω=0.5 → λ=0 计入过拟合；
// 其余四种切分 IS 最优 B 且 OOS 秩 1 → 非过拟合。
// PBO=2/6=1/3；logits 排序 [ln(1/3), 0, ln(3)×4]，中位数 = ln(3)。
func TestCSCVHandTracedSplits(t *testing.T) {
	m := [][]float64{
		{3, 3, 0, 0},
		{2, 2, 2, 2},
		{1, 1, 1, 1},
	}
	res, err := CSCV(m)
	if err != nil {
		t.Fatal(err)
	}
	if res.Splits != 6 || res.Segments != 4 || res.Candidates != 3 {
		t.Fatalf("%+v", res)
	}
	if math.Abs(res.PBO-1.0/3) > 1e-12 {
		t.Fatalf("pbo=%v want 1/3", res.PBO)
	}
	if math.Abs(res.MedianLogit-math.Log(3)) > 1e-12 {
		t.Fatalf("median=%v want ln(3)", res.MedianLogit)
	}
	// 确定性：同输入再算必同读数。
	again, err := CSCV(m)
	if err != nil || *again != *res {
		t.Fatalf("non-deterministic: %+v vs %+v", again, res)
	}
}

func TestCSCVDegenerateForms(t *testing.T) {
	// 全并列：无区分度 = 全是运气，每个切分 λ=0 计入过拟合（Bailey 取 Φ(0)）。
	tied := [][]float64{{1, 1, 1, 1}, {1, 1, 1, 1}, {1, 1, 1, 1}}
	res, err := CSCV(tied)
	if err != nil {
		t.Fatal(err)
	}
	if res.PBO != 1 || res.MedianLogit != 0 {
		t.Fatalf("tied: %+v", res)
	}
	// 完美一致：IS 最优恒为 OOS 最优 → PBO=0。
	consistent := [][]float64{{3, 3, 3, 3}, {2, 2, 2, 2}, {1, 1, 1, 1}}
	res, err = CSCV(consistent)
	if err != nil {
		t.Fatal(err)
	}
	if res.PBO != 0 {
		t.Fatalf("consistent: %+v", res)
	}
	// N=2 完全反相关：IS 最优 OOS 必垫底（含 λ=0 切分）→ PBO=1。
	adversarial := [][]float64{{3, 0, 3, 0}, {0, 3, 0, 3}}
	res, err = CSCV(adversarial)
	if err != nil {
		t.Fatal(err)
	}
	if res.PBO != 1 {
		t.Fatalf("adversarial: %+v", res)
	}
}

func TestCSCVInvalidShapes(t *testing.T) {
	if _, err := CSCV(nil); !errors.Is(err, ErrPBOInvalid) {
		t.Fatalf("nil: %v", err)
	}
	if _, err := CSCV([][]float64{{1, 2, 3}, {1, 2, 3}}); !errors.Is(err, ErrPBOInvalid) {
		t.Fatalf("odd segments: %v", err)
	}
	if _, err := CSCV([][]float64{{1, 2}, {1, 2}}); !errors.Is(err, ErrPBOInvalid) {
		t.Fatalf("S=2: %v", err)
	}
	if _, err := CSCV([][]float64{{1, 2, 3, 4}, {1, 2}}); !errors.Is(err, ErrPBOInvalid) {
		t.Fatalf("ragged: %v", err)
	}
	// 组合数超上限（S=22 → C(22,11)=705432 > PboSplitsMax）在枚举前就拒绝。
	big := [][]float64{make([]float64, 22), make([]float64, 22)}
	if _, err := CSCV(big); !errors.Is(err, ErrPBOInvalid) {
		t.Fatalf("cap: %v", err)
	}
}

func TestCSCVDropsNonFiniteRows(t *testing.T) {
	m := [][]float64{
		{3, 3, 3, 3},
		{2, math.NaN(), 2, 2}, // 半截数据不作 0 参与排序，整行剔除
		{1, 1, 1, math.Inf(1)},
		{0, 0, 0, 0},
	}
	res, err := CSCV(m)
	if err != nil {
		t.Fatal(err)
	}
	if res.Candidates != 2 {
		t.Fatalf("candidates=%d want 2", res.Candidates)
	}
	// 剩两个一致型候选：恒 IS 第一者 OOS 也第一 → PBO=0。
	if res.PBO != 0 {
		t.Fatalf("pbo=%v want 0", res.PBO)
	}
	// 剔除到不足 2 个 → 不可计算，显式错误而非假读数。
	if _, err := CSCV(m[:3]); !errors.Is(err, ErrPBOInsufficient) {
		t.Fatalf("insufficient: %v", err)
	}
	if _, err := CSCV([][]float64{{1, 2, 3, 4}}); !errors.Is(err, ErrPBOInsufficient) {
		t.Fatalf("single row: %v", err)
	}
}

func TestPBOHelpers(t *testing.T) {
	if binom(4, 2) != 6 || binom(16, 8) != 12870 {
		t.Fatal("binom")
	}
	if n := func() int {
		c, count := initialCombo(2), 0
		for ; c != nil; c = nextCombo(c, 4) {
			count++
		}
		return count
	}(); n != 6 {
		t.Fatalf("combo enumeration=%d want 6", n)
	}
	// midrank：降序秩，并列取平均。
	v := []float64{5, 5, 3, 1}
	if r := midrank(v, 0); r != 1.5 {
		t.Fatalf("midrank tie=%v want 1.5", r)
	}
	if r := midrank(v, 2); r != 3 {
		t.Fatalf("midrank=%v want 3", r)
	}
	if r := midrank(v, 3); r != 4 {
		t.Fatalf("midrank=%v want 4", r)
	}
}
