package data

import (
	"context"
	"testing"
	"time"

	"server/pkg/tradecal"
)

// SecurityBasics（名称字典）：codes 谓词/全量、stock_code 升序、缺字段透传
// （industry/concept NULL→空串/空列表，估值 0——「没取到≠0」后端不伪造零值冒充缺失的
// 反向纪律是「缺披露≠撤披露」，此处是读侧原样透传）、maxUpdate 取结果内最大 update_time。
func TestSecurityBasicsRead(t *testing.T) {
	c := openTestDB(t)
	ctx := context.Background()
	if _, err := c.StockBasic.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	r := NewRepo(c)
	old := time.Date(2026, 10, 10, 12, 0, 0, 0, tradecal.Shanghai())
	newer := time.Date(2026, 10, 20, 16, 5, 0, 0, tradecal.Shanghai())

	// 三行：全字段行 / 缺 industry+concept 行 / 中文逗号概念行。有意乱 stock_code 序插入。
	if _, err := c.StockBasic.Create().
		SetStockCode("600519.SH").SetStockName("贵州茅台").SetMarket("SH").
		SetIndustry("白酒").SetConcept("国企改革，融资融券").SetStFlag(false).SetSuspendFlag(false).
		SetPeTtm(22.5).SetPb(8.8).SetCircMv(2.1e12).SetUpdateTime(newer).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StockBasic.Create().
		SetStockCode("000001.SZ").SetStockName("平安银行").SetMarket("SZ").
		SetUpdateTime(old).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.StockBasic.Create().
		SetStockCode("430047.BJ").SetStockName("诺思兰德").SetMarket("BJ").
		SetIndustry("医药").SetConcept("创新药,基因检测").
		SetStFlag(true).SetUpdateTime(old.Add(-time.Hour)).
		Save(ctx); err != nil {
		t.Fatal(err)
	}

	// 全量：按 stock_code 升序；maxUpdate=三行中最大 update_time。
	rows, maxUp, err := r.SecurityBasics(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 3 {
		t.Fatalf("rows=%d want 3", len(rows))
	}
	if rows[0].Code != "000001.SZ" || rows[1].Code != "430047.BJ" || rows[2].Code != "600519.SH" {
		t.Fatalf("排序=%v want stock_code 升序", []string{rows[0].Code, rows[1].Code, rows[2].Code})
	}
	if !maxUp.Equal(newer) {
		t.Fatalf("maxUpdate=%v want %v（结果内最大 update_time）", maxUp, newer)
	}

	// 全字段行往返。
	most := rows[2]
	if most.Name != "贵州茅台" || most.Industry != "白酒" || most.ST || most.Suspended {
		t.Fatalf("贵州茅台行不对: %+v", most)
	}
	if most.PeTtm != 22.5 || most.Pb != 8.8 || most.CircMv != 2.1e12 {
		t.Fatalf("估值字段不对: %+v", most)
	}
	if len(most.Concepts) != 2 || most.Concepts[0] != "国企改革" || most.Concepts[1] != "融资融券" {
		t.Fatalf("中文逗号概念切分不对: %+v", most.Concepts)
	}
	// 缺字段透传：industry/concept NULL→空串/空列表，估值 0，不得伪造。
	bank := rows[0]
	if bank.Industry != "" || len(bank.Concepts) != 0 || bank.PeTtm != 0 || bank.Pb != 0 || bank.CircMv != 0 {
		t.Fatalf("平安银行缺字段应空透传: %+v", bank)
	}
	// ST 旗标往返 + 英文逗号概念切分。
	north := rows[1]
	if !north.ST || len(north.Concepts) != 2 || north.Concepts[0] != "创新药" || north.Concepts[1] != "基因检测" {
		t.Fatalf("诺思兰德行不对: %+v", north)
	}

	// codes 子集 + maxUpdate 随子集收缩。
	sub, subMax, err := r.SecurityBasics(ctx, []string{"000001.SZ", "430047.BJ"})
	if err != nil || len(sub) != 2 {
		t.Fatalf("子集 err=%v rows=%d", err, len(sub))
	}
	if !subMax.Equal(old) {
		t.Fatalf("子集 maxUpdate=%v want %v", subMax, old)
	}

	// 空结果：maxUpdate 零值不是错误。
	none, zeroUp, err := r.SecurityBasics(ctx, []string{"999999.SH"})
	if err != nil || len(none) != 0 || !zeroUp.IsZero() {
		t.Fatalf("空结果 err=%v rows=%v maxUp=%v want 零值", err, none, zeroUp)
	}
}
