package data

import (
	"context"
	"testing"

	"server/app/trade/internal/biz"
	"server/ent/eventdisposition"
	"server/ent/tradefill"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

// M13-T04 持久端口集成测试（隔离 PG）：(book, exec_id) PG 唯一约束兜底
// （业务侧 HasFillExec 预检之外的第二道闸）与 event_disposition 去重。

func TestTradeFillExecIDUniqueBackstopAndDispositionDedupe(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	book := "rp_" + uuid.NewString()[:8]
	sid := "rp_" + uuid.NewString()
	execID := "EX" + uuid.NewString()[:16]
	t.Cleanup(func() {
		_, _ = c.TradeFill.Delete().Where(tradefill.BookEQ(book)).Exec(ctx)
		_, _ = c.EventDisposition.Delete().Where(eventdisposition.EventIDHasPrefix("broker-report:")).Exec(ctx)
	})

	fill := biz.Fill{
		ClientOrderID: sid, Book: book, Symbol: "600000.SH", Side: "buy",
		Qty: 100, Price: 10, Amount: 1000, Fee: 5, ExecID: execID,
	}
	// 第一次入账必须成功。
	if err := repo.Commit(ctx, biz.Batch{Fills: []biz.Fill{fill}}); err != nil {
		t.Fatal(err)
	}
	// 正常判重路径。
	seen, err := repo.HasFillExec(ctx, book, execID)
	if err != nil || !seen {
		t.Fatalf("HasFillExec = %v, %v", seen, err)
	}
	// PG 唯一约束兜底：绕过预检直接重复提交同 (book, exec_id)，
	// Must DoNothing——不报错、不二次入账（重复回报/修复脚本的安全带）。
	if err := repo.Commit(ctx, biz.Batch{Fills: []biz.Fill{fill}}); err != nil {
		t.Fatalf("unique backstop must silently skip replay: %v", err)
	}
	fills, err := repo.ListFills(ctx, book, 0)
	if err != nil || len(fills) != 1 || fills[0].ExecID != execID {
		t.Fatalf("fills after replay: %d %v err %v", len(fills), fills, err)
	}
	seen, err = repo.HasFillExec(ctx, book, "EX"+uuid.NewString()[:16])
	if err != nil || seen {
		t.Fatalf("HasFillExec false positive: %v, %v", seen, err)
	}

	// RecordDisposition：同 (event_id, action) 去重，第二笔 created=false。
	d := biz.Disposition{EventID: "broker-report:orphan_fill:EX" + uuid.NewString()[:8], TraceID: "t1", Action: "manual_review", Note: "孤儿成交", Actor: "trade/report"}
	created, err := repo.RecordDisposition(ctx, d)
	if err != nil || !created {
		t.Fatalf("first disposition: %v, %v", created, err)
	}
	again, err := repo.RecordDisposition(ctx, d)
	if err != nil || again {
		t.Fatalf("duplicate disposition must dedupe: %v, %v", again, err)
	}
}

func TestTradeFillWithoutExecIDUnaffectedByUniqueIndex(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)
	book := "rp_" + uuid.NewString()[:8]
	t.Cleanup(func() {
		_, _ = c.TradeFill.Delete().Where(tradefill.BookEQ(book)).Exec(ctx)
	})
	// SIM 撮合成交没有 exec_id：PG 唯一索引对 NULL 天然放开，多笔并立。
	for _, q := range []int{100, 200} {
		err := repo.Commit(ctx, biz.Batch{Fills: []biz.Fill{{
			ClientOrderID: "sim_" + uuid.NewString()[:8], Book: book, Symbol: "600000.SH", Side: "buy",
			Qty: q, Price: 10, Amount: float64(q) * 10, Fee: 5,
		}}})
		if err != nil {
			t.Fatal(err)
		}
	}
	fills, err := repo.ListFills(ctx, book, 0)
	if err != nil || len(fills) != 2 {
		t.Fatalf("SIM fills blocked by unique index: %d err %v", len(fills), err)
	}
}
