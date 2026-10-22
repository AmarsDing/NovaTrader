package data

import (
	"context"
	"strings"
	"testing"

	"server/app/backtest/internal/biz"
	"server/ent/auditlog"
)

// TestRecordApprovalPersistsAuditRow 验证 RecordApproval 真写 audit_log 表，
// 且长字段按 UTF-8 字节裁剪到列上限内（ent MaxLen 按字节校验，不能切碎汉字）。
// 跑在隔离 PG（NT_TEST_PG_PORT）。
func TestRecordApprovalPersistsAuditRow(t *testing.T) {
	client := datasetClient(t)
	ctx := context.Background()
	// 专用测试库跨轮持久：先清动作域，避免上轮行混进本轮断言。
	if _, err := client.AuditLog.Delete().Where(auditlog.ActionHasPrefix("backtest.proposal.")).Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(client)

	longActor := strings.Repeat("用", 80) // 80 个汉字 > actor 上限 64
	if err := repo.RecordApproval(ctx, biz.ApprovalAudit{
		Actor: longActor, Role: "owner", Action: biz.AuditActionApprove,
		Target: "param_proposal/12", Detail: "strategy=fb proposal=12 version=7", OK: true,
	}); err != nil {
		t.Fatal(err)
	}
	rows, err := client.AuditLog.Query().Where(auditlog.ActionEQ(biz.AuditActionApprove)).All(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != 1 {
		t.Fatalf("rows = %d, want 1", len(rows))
	}
	row := rows[0]
	if !row.Ok || row.Role != "owner" || row.Target != "param_proposal/12" || row.Detail == "" {
		t.Fatalf("row = %+v", row)
	}
	if n := len([]rune(row.Actor)); n != 21 || len(row.Actor) != 63 {
		// 80 个三字节汉字 → 按 64 字节裁并顺 UTF-8 边界退到 63 字节 = 21 字。
		t.Fatalf("actor = %d 字/%d 字节, want 21 字/63 字节（UTF-8 字节级裁剪）", n, len(row.Actor))
	}
}
