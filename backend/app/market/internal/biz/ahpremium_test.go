package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"
)

/* AH 溢价编排测试（目录册增补 B5，T1–T11 + 未装配门卫）。
 * fakeAHSource 独立小 fake：记 pair/triple 调用计数与入参——T3（no_pair 短路
 * 时 AHTriple 调用计数恒 0）与 T9/T10（事实列透传钉）都要这个可观测面。 */

type fakeAHSource struct {
	pair        AHPairing
	pairErr     error
	triple      AHTriple
	tripleErr   error
	pairCalls   int
	tripleCalls int
	lastSymbol  string
	lastHC      string
}

func (f *fakeAHSource) AHPair(_ context.Context, symbol string) (AHPairing, error) {
	f.pairCalls++
	f.lastSymbol = symbol
	return f.pair, f.pairErr
}

func (f *fakeAHSource) AHTriple(_ context.Context, aCode, aMarket, hCode string) (AHTriple, error) {
	f.tripleCalls++
	f.lastHC = hCode
	return f.triple, f.tripleErr
}

func newAHUsecase(src AHSource) *Usecase {
	cfg := NewConfig(nil)
	cfg.ah = src
	return NewUsecase(nil, nil, nil, cfg, log.DefaultLogger)
}

func f64(v float64) *float64 { return &v }

func okTriple() AHTriple {
	return AHTriple{
		A:       AHLeg{Price: f64(10), Time: time.Date(2026, 10, 19, 16, 14, 36, 0, time.UTC)},
		H:       AHLeg{Price: f64(10), Time: time.Date(2026, 10, 16, 16, 8, 22, 0, time.UTC)},
		FX:      AHLeg{Price: f64(0.92), Time: time.Date(2026, 10, 20, 6, 59, 12, 0, time.UTC)},
		ATraded: true,
	}
}

// T1+T2：衍生钉数与折价防恒正盲区（负溢价合法不钳 0）。
func TestAHCompute(t *testing.T) {
	ratio, premium := ahCompute(10, 10, 0.92)
	if ratio != 1.0870 || premium != 8.70 {
		t.Fatalf("T1 钉数失败：ratio=%v premium=%v，想 1.0870/8.70", ratio, premium)
	}
	// 折价：a=9.00, h=10, fx=0.95 → h*fx=9.5 → ratio=0.9474 → premium=-5.26（不得钳 0）
	ratio, premium = ahCompute(9.00, 10, 0.95)
	if ratio != 0.9474 || premium != -5.26 {
		t.Fatalf("T2 折价钉数失败：ratio=%v premium=%v，想 0.9474/-5.26（负合法不钳）", ratio, premium)
	}
}

func TestAHPremiumOK(t *testing.T) {
	f := &fakeAHSource{
		pair:   AHPairing{Pair: true, AName: "建设银行", HCode: "00939", HName: "建设银行"},
		triple: okTriple(),
	}
	rep, err := newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != AHStatusOK || rep.Ratio == nil || rep.Premium == nil {
		t.Fatalf("status=%s ratio=%v premium=%v", rep.Status, rep.Ratio, rep.Premium)
	}
	if *rep.Ratio != 1.0870 || *rep.Premium != 8.70 {
		t.Fatalf("T1 编排钉数：ratio=%v premium=%v", *rep.Ratio, *rep.Premium)
	}
	// T11：逐腿时间透传（源内 tick 时刻直出编排层）。
	if rep.A.Time.IsZero() || rep.H.Time.IsZero() || rep.FX.Time.IsZero() {
		t.Fatalf("三腿时刻须透传：%+v", rep)
	}
	// T10：配对事实列透传。
	if rep.Pairing.ACode != "601939" || rep.Pairing.AMarket != "SH" ||
		rep.Pairing.AName != "建设银行" || rep.Pairing.HCode != "00939" || !rep.ATraded {
		t.Fatalf("事实列透传失败：%+v", rep.Pairing)
	}
	if len(rep.Missing) != 0 {
		t.Fatalf("ok 态 missing 必须空：%+v", rep.Missing)
	}
}

// T3：no_pair 短路——AHTriple 调用计数恒 0（省一次外抓，也防无配对券污染报价面）。
func TestAHPremiumNoPairShortCircuit(t *testing.T) {
	f := &fakeAHSource{pair: AHPairing{Pair: false, AName: "平安银行"}}
	rep, err := newAHUsecase(f).AHPremium(context.Background(), "000001.SZ")
	if err != nil {
		t.Fatal(err)
	}
	if rep.Status != AHStatusNoPair {
		t.Fatalf("status=%s want no_pair", rep.Status)
	}
	if f.tripleCalls != 0 {
		t.Fatalf("T3：no_pair 短路时 AHTriple 调用计数必须 0，实际 %d", f.tripleCalls)
	}
	if rep.Pairing.AName != "平安银行" || rep.Ratio != nil {
		t.Fatalf("no_pair 事实列/衍生列边界：%+v", rep)
	}
}

// T4+T5+T6：缺腿逐项（why 颗粒度）与衍生列整体缺省。
func TestAHPremiumMissingLegs(t *testing.T) {
	base := okTriple()
	base.H = AHLeg{Why: AHLegNAValue} // H 价格字段显 "0"
	f := &fakeAHSource{
		pair:   AHPairing{Pair: true, AName: "建设银行", HCode: "00939", HName: "建设银行"},
		triple: base,
	}
	rep, _ := newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if rep.Status != AHStatusMissing || rep.Ratio != nil || rep.Premium != nil {
		t.Fatalf("T4：缺 h 腿必须 missing 且衍生缺省：%+v", rep)
	}
	if len(rep.Missing) != 1 || rep.Missing[0].Leg != "h_price" || rep.Missing[0].Why != AHLegNAValue {
		t.Fatalf("T4：missing 逐项 why 颗粒度失败：%+v", rep.Missing)
	}
	if rep.A.Price == nil {
		t.Fatal("T4：在场腿照常回波")
	}

	base = okTriple()
	base.H = AHLeg{Why: AHLegNoData}
	base.FX = AHLeg{Why: AHLegNoData}
	f.triple = base
	rep, _ = newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if rep.Status != AHStatusMissing || len(rep.Missing) != 2 {
		t.Fatalf("T6：双缺集合：%+v", rep.Missing)
	}
	if rep.Missing[0].Leg != "h_price" || rep.Missing[1].Leg != "hkdcny" {
		t.Fatalf("T6：缺腿顺序钉：%+v", rep.Missing)
	}
}

// T7：停牌照算——ATraded=false 透出，衍生列照常有值。
func TestAHPremiumSuspendedStillComputes(t *testing.T) {
	triple := okTriple()
	triple.ATraded = false
	f := &fakeAHSource{
		pair:   AHPairing{Pair: true, AName: "建设银行", HCode: "00939"},
		triple: triple,
	}
	rep, _ := newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if rep.Status != AHStatusOK || rep.ATraded || rep.Ratio == nil {
		t.Fatalf("T7：停牌照算透出失败：status=%s traded=%v", rep.Status, rep.ATraded)
	}
}

// T8：配对刷新失败 → pair_error（配对未知不产溢价数）。
func TestAHPremiumPairError(t *testing.T) {
	f := &fakeAHSource{pairErr: errors.New("【报表轮换】RPT_F10_BASIC_ORGINFO 不可用")}
	rep, _ := newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if rep.Status != AHStatusPairError || rep.Reason == "" {
		t.Fatalf("T8：%+v", rep)
	}
	if rep.Ratio != nil || f.tripleCalls != 0 {
		t.Fatalf("T8：pair_error 不得有衍生/不得抓报价：%+v", rep)
	}
}

// T9：报价请求级失败 → source_error，配对事实列仍回波。
func TestAHPremiumSourceErrorEchoesPairing(t *testing.T) {
	f := &fakeAHSource{
		pair:      AHPairing{Pair: true, AName: "建设银行", HCode: "00939", HName: "建设银行"},
		tripleErr: errors.New("【疑似限流/封禁】HTTP 403"),
	}
	rep, _ := newAHUsecase(f).AHPremium(context.Background(), "601939.SH")
	if rep.Status != AHStatusSourceError {
		t.Fatalf("T9：status=%s want source_error", rep.Status)
	}
	if rep.Pairing.ACode != "601939" || rep.Pairing.HCode != "00939" || rep.Pairing.AName != "建设银行" {
		t.Fatalf("T9：配对事实列必须照常回波：%+v", rep.Pairing)
	}
}

// 未装配源 → 显式错误（不裸 nil 静默敞口）。
func TestAHPremiumUnwired(t *testing.T) {
	_, err := newAHUsecase(nil).AHPremium(context.Background(), "601939.SH")
	if err == nil {
		t.Fatal("未装配 AHSource 必须返回明确错误")
	}
}
