// library_test.go 是 B4 在册声明式策略仓储的隔离 PG 集成件：
// pending/空 code/NULL code 一律不进候选集，active 行按 name 升序回传。
package data

import (
	"testing"
)

func TestLibraryRepoActiveLibrary(t *testing.T) {
	d := newTestData(t)
	defer func() { _, _ = d.client.StrategyLibrary.Delete().Exec(ctx) }()
	put := func(name, code, status string) {
		t.Helper()
		c := d.client.StrategyLibrary.Create().SetName(name).SetStatus(status)
		if code != "" {
			c.SetCode(code)
		}
		if _, err := c.Save(ctx); err != nil {
			t.Fatal(err)
		}
	}
	code := `{"version":1,"name":"dsl_x","hold_days":3,"filter":{"op":"bool","bool":true},"score":{"base":40}}`
	put("zzz_active", code, "active")
	put("pending_one", code, "pending")
	put("empty_code", "", "active")      // NULL code
	put("aaa_active", code, "active")    // 次序钉：aaa 先
	put("deprecated_one", code, "deprecated")

	rows, err := NewLibraryRepo(d).ActiveLibrary(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 2 || rows[0].Name != "aaa_active" || rows[1].Name != "zzz_active" {
		t.Fatalf("rows %+v（只应回 active+code 非空两行，按 name 升序）", rows)
	}
	if rows[0].Code == "" {
		t.Fatal("code 丢失")
	}

	// 空 Data 退化为空集（不阻塞主链）。
	if got, err := NewLibraryRepo(&Data{}).ActiveLibrary(ctx); err != nil || len(got) != 0 {
		t.Fatalf("nil data: rows=%v err=%v", got, err)
	}
}
