package biz

import (
	"context"
	"errors"
	"testing"
	"time"

	"server/pkg/market"

	"github.com/go-kratos/kratos/v2/log"
)

// BATCH-02 三件套的 biz 钉样：H5 批量日线（tail+复权+缺席）、E7 财务摘要参数管道、
// G2 联想四通道归组、B-2 报价 52 周前复权极值。

func batch02Usecase(repo *fakeRepo) *Usecase {
	return NewUsecase(repo, &fakeSnaps{}, &fakePub{}, Config{StaleAfter: 10 * time.Second}, log.DefaultLogger)
}

func batchBar(day time.Time, close, adj float64) BarRow {
	return BarRow{
		Bar: market.Bar{Symbol: "AAA", Time: day, Open: close - 1, High: close + 2, Low: close - 2, Close: close, Volume: 100},
		Adj: adj,
	}
}

// H5：升序取尾、前复权以窗口末根因子为基准、空行/缺码整组缺席、>500 截断。
func TestBarsBatchTailAdjustAbsent(t *testing.T) {
	d0 := time.Date(2026, 10, 19, 0, 0, 0, 0, time.Local)
	rows5 := make([]BarRow, 0, 5)
	for i := 0; i < 5; i++ {
		adj := 2.0 // d3 起除权：末根因子=2，前复权基准即它；d0..d2 因子 1 → k=1/2
		if i < 3 {
			adj = 1.0
		}
		rows5 = append(rows5, batchBar(d0.AddDate(0, 0, i), float64(10+i), adj))
	}
	repo := &fakeRepo{barsMulti: map[string][]BarRow{
		"AAA.SZ": rows5,
		"BBB.SZ": {}, // 窗口内无行：整组缺席
	}}
	uc := batch02Usecase(repo)
	out, err := uc.BarsBatch(context.Background(), []string{"AAA.SZ", "BBB.SZ", "MISS.SZ"},
		time.Time{}, d0.AddDate(0, 0, 9), market.AdjustForward, 4)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := out["BBB.SZ"]; ok {
		t.Fatalf("空行组应缺席: %+v", out)
	}
	if _, ok := out["MISS.SZ"]; ok {
		t.Fatalf("缺码应缺席: %+v", out)
	}
	got := out["AAA.SZ"]
	if len(got) != 4 {
		t.Fatalf("tail limit=4 后应 4 根, got %d", len(got))
	}
	// 保留 d1..d4：d1/d2 k=1/2 → 11→5.5、12→6；d3/d4 k=1 不变。
	wantClose := []float64{5.5, 6, 13, 14}
	for i, w := range wantClose {
		if got[i].Close != w {
			t.Fatalf("bar[%d] close want %v got %v", i, w, got[i].Close)
		}
	}
	if got[0].Time != d0.AddDate(0, 0, 1) {
		t.Fatalf("tail 首根应为 d1, got %v", got[0].Time)
	}
}

// H5：symbols 空整批报错；>500 截 500 参数管道钉样。
func TestBarsBatchSymbolsGuard(t *testing.T) {
	repo := &fakeRepo{}
	uc := batch02Usecase(repo)
	if _, err := uc.BarsBatch(context.Background(), nil, time.Time{}, time.Time{}, market.AdjustNone, 0); err == nil {
		t.Fatal("空 symbols 应报错")
	}
	syms := make([]string, 0, 600)
	for i := 0; i < 600; i++ {
		syms = append(syms, string(rune('A'+i%26))+string(rune('a'+i%26))+string(rune('0'+i%10)))
	}
	if _, err := uc.BarsBatch(context.Background(), syms, time.Time{}, time.Time{}, market.AdjustNone, 0); err != nil {
		t.Fatal(err)
	}
	if len(repo.multiSyms) != 500 {
		t.Fatalf("symbols 应截到 500, got %d", len(repo.multiSyms))
	}
}

// H5：数据层错误原样上浮，不降级逐码重试（T01 冻结语义）。
func TestBarsBatchRepoError(t *testing.T) {
	sentinel := errors.New("db down")
	uc := batch02Usecase(&fakeRepo{multiErr: sentinel})
	if _, err := uc.BarsBatch(context.Background(), []string{"AAA.SZ"}, time.Time{}, time.Time{}, market.AdjustNone, 0); !errors.Is(err, sentinel) {
		t.Fatalf("err want sentinel got %v", err)
	}
}

// E7：periods 默认 4、上限 12、错误上浮。
func TestFinanceBriefPeriods(t *testing.T) {
	repo := &fakeRepo{}
	uc := batch02Usecase(repo)
	if _, err := uc.FinanceBrief(context.Background(), "AAA.SZ", 0); err != nil || repo.finPeriods != 4 {
		t.Fatalf("periods≤0 应默认 4, err=%v got=%d", err, repo.finPeriods)
	}
	if _, err := uc.FinanceBrief(context.Background(), "AAA.SZ", 99); err != nil || repo.finPeriods != 12 {
		t.Fatalf("periods>12 应截 12, err=%v got=%d", err, repo.finPeriods)
	}
	sentinel := errors.New("db down")
	uc = batch02Usecase(&fakeRepo{finErr: sentinel})
	if _, err := uc.FinanceBrief(context.Background(), "AAA.SZ", 4); !errors.Is(err, sentinel) {
		t.Fatalf("err want sentinel got %v", err)
	}
}

func searchDict() []SecurityBasicRow {
	return []SecurityBasicRow{
		{Code: "600519.SH", Name: "贵州茅台", Market: "SH"},
		{Code: "000001.SZ", Name: "平安银行", Market: "SZ"},
		{Code: "600000.SH", Name: "浦发银行", Market: "SH"},
		{Code: "601318.SH", Name: "中国平安", Market: "SH"},
		{Code: "300750.SZ", Name: "宁德时代", Market: "SZ"},
	}
}

func matchesOf(t *testing.T, uc *Usecase, q string, limit int) []SecurityMatch {
	t.Helper()
	ms, err := uc.SearchSecurities(context.Background(), q, limit)
	if err != nil {
		t.Fatal(err)
	}
	return ms
}

// G2：通道归组 code 前缀 → 名称前缀 → 名称子串 → 拼音首字母前缀；命中通道显式。
func TestSearchSecuritiesGrouping(t *testing.T) {
	uc := batch02Usecase(&fakeRepo{secBasics: searchDict()})

	ms := matchesOf(t, uc, "6005", 20)
	if len(ms) != 1 || ms[0].Code != "600519.SH" || ms[0].MatchKind != "code" {
		t.Fatalf("code 前缀: %+v", ms)
	}

	ms = matchesOf(t, uc, "中国", 20)
	if len(ms) != 1 || ms[0].Code != "601318.SH" || ms[0].MatchKind != "name" {
		t.Fatalf("名称前缀: %+v", ms)
	}

	// “平安”：平安银行名称前缀在先，中国平安名称子串在后——同 kind、组序稳定。
	ms = matchesOf(t, uc, "平安", 20)
	if len(ms) != 2 || ms[0].Code != "000001.SZ" || ms[1].Code != "601318.SH" {
		t.Fatalf("名称前缀>子串归组序: %+v", ms)
	}

	// 小写拼音查询按首字母前缀命中；“行”按 GB2312 首排序位落 x 区
	//（pfyx/payx 多音字声明口径：用户按 háng 打 pfyh 走不通，前端 code/名称通道兜底）。
	ms = matchesOf(t, uc, "pfyx", 20)
	if len(ms) != 1 || ms[0].Code != "600000.SH" || ms[0].MatchKind != "pinyin" || ms[0].Pinyin != "pfyx" {
		t.Fatalf("拼音通道: %+v", ms)
	}
	ms = matchesOf(t, uc, "PA", 20)
	if len(ms) != 1 || ms[0].Code != "000001.SZ" || ms[0].MatchKind != "pinyin" || ms[0].Pinyin != "payx" {
		t.Fatalf("拼音大小写规整: %+v", ms)
	}

	// 空查询不报错、无结果；limit 截断发生在归组串行输出处。
	ms = matchesOf(t, uc, "  ", 20)
	if len(ms) != 0 {
		t.Fatalf("空查询应无结果: %+v", ms)
	}
	ms = matchesOf(t, uc, "6", 2)
	if len(ms) != 2 || ms[0].MatchKind != "code" {
		t.Fatalf("limit=2 截断: %+v", ms)
	}
}

// B-2：报价 52 周极值——前复权（基准=窗口末根有效因子）、缺因子行跳过。
func TestQuoteWeek52(t *testing.T) {
	d0 := time.Date(2026, 4, 1, 0, 0, 0, 0, time.Local)
	rows := make([]BarRow, 0, 60)
	for i := 0; i < 60; i++ {
		close := float64(100 + i)
		adj := 1.0
		if i < 30 {
			adj = 2.0 // 前半段因子 2（除权前），k=2/1=2 复权放大
		}
		r := batchBar(d0.AddDate(0, 0, i), close, adj)
		r.Bar.High, r.Bar.Low = close+2, close-2
		if i == 15 {
			r.Adj = 0           // 缺因子行：整行跳过
			r.Bar.High = 9999.0 // 若误参与会把 High52 顶穿
			r.Bar.Low = 0.0001
		}
		rows = append(rows, r)
	}
	repo := &fakeRepo{basic: quoteBasic(), dayBars: rows}
	uc := batch02Usecase(repo)
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.High52 == nil || q.Low52 == nil {
		t.Fatalf("52 周极值应有值: %+v", q)
	}
	// 前半段复权 high=(100+i+2)*2，i=29 → 262；后半段 low=(100+i-2)*1，i=30 → 128。
	// 前半段复权 low 最小 (100+0-2)*2=196 → 全局 min=128。
	if *q.High52 != 262 {
		t.Fatalf("High52 want 262 got %v", *q.High52)
	}
	if *q.Low52 != 128 {
		t.Fatalf("Low52 want 128 got %v", *q.Low52)
	}
}

// B-2：有效行不足 20 整体缺省；全缺因子亦缺省（不落 0）。
func TestQuoteWeek52Omit(t *testing.T) {
	d0 := time.Date(2026, 9, 1, 0, 0, 0, 0, time.Local)
	short := make([]BarRow, 0, 10)
	for i := 0; i < 10; i++ {
		short = append(short, batchBar(d0.AddDate(0, 0, i), 100+float64(i), 1))
	}
	repo := &fakeRepo{basic: quoteBasic(), dayBars: short}
	uc := batch02Usecase(repo)
	q, err := uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.High52 != nil || q.Low52 != nil {
		t.Fatalf("有效行<20 应缺省: %+v", q)
	}

	bad := make([]BarRow, 0, 30)
	for i := 0; i < 30; i++ {
		r := batchBar(d0.AddDate(0, 0, i), 100, 0)
		bad = append(bad, r)
	}
	uc = batch02Usecase(&fakeRepo{basic: quoteBasic(), dayBars: bad})
	q, err = uc.Quote(context.Background(), "600519.SH")
	if err != nil {
		t.Fatal(err)
	}
	if q.High52 != nil || q.Low52 != nil {
		t.Fatalf("全缺因子应缺省: %+v", q)
	}
}
