package data

import (
	"context"
	"testing"
	"time"

	"server/app/trade/internal/biz"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
)

// TestLiveGrantRoundTrip 是 M13-T05 留痕载体的数据层合同（隔离 PG，缺库 skip）：
// 键无 = 收权（known=false）；Save 后 Load 原样回放（批准人/证据/时间完整）；
// Clear 后回到收权态；审计核验只认 action+actor 同匹配、成功的最近留痕。
func TestLiveGrantRoundTrip(t *testing.T) {
	c := accountTestClient(t)
	ctx := context.Background()
	repo := NewRepo(c, log.DefaultLogger)

	// 测试库可能已有历史留痕：先清一次，钉死「键无 = 收权」的读出口径。
	if err := repo.ClearLiveGrant(ctx); err != nil {
		t.Fatal(err)
	}
	if _, known, err := repo.LoadLiveGrant(ctx); err != nil || known {
		t.Fatalf("cleared key must read back as revoked: known=%v err=%v", known, err)
	}

	grant := biz.LiveGrant{
		GrantedBy: "owner", GrantedAt: time.Now().UTC().Truncate(time.Second),
		Evidence: biz.LiveGrantEvidence{
			ConsecutiveDays: 20, RequiredDays: 20, LatestClosedDay: "2026-10-21", Policy: "real-v1",
		},
	}
	if err := repo.SaveLiveGrant(ctx, grant); err != nil {
		t.Fatal(err)
	}
	loaded, known, err := repo.LoadLiveGrant(ctx)
	if err != nil || !known {
		t.Fatalf("saved grant must load: known=%v err=%v", known, err)
	}
	if loaded.GrantedBy != grant.GrantedBy || loaded.Evidence != grant.Evidence || !loaded.GrantedAt.Equal(grant.GrantedAt) {
		t.Fatalf("round trip must preserve the grant: got %+v want %+v", loaded, grant)
	}
	if !biz.LiveGateFromGrant(loaded, known, err) {
		t.Fatal("loaded grant must reopen the gate through the shared predicate")
	}

	if err := repo.ClearLiveGrant(ctx); err != nil {
		t.Fatal(err)
	}
	if _, known, err := repo.LoadLiveGrant(ctx); err != nil || known {
		t.Fatalf("clear must revoke: known=%v err=%v", known, err)
	}

	// 审计核验：无留痕 → false；成功留痕在窗口内且动作人一致 → true；动作人错位 → false。
	// actor 用本档唯一值——共享测试库留历史，任何撞名旧行都可能把「无留痕」读成有。
	actor := "owner-" + uuid.NewString()
	ok, err := repo.RecentLiveGateAudit(ctx, biz.LiveGateAuditGrant, actor, 5*time.Minute)
	if err != nil || ok {
		t.Fatalf("absent audit must read as not audited: ok=%v err=%v", ok, err)
	}
	if _, err := c.AuditLog.Create().
		SetActor(actor).SetRole("owner").SetAction(biz.LiveGateAuditGrant).
		SetTarget("/api/trade/admission/live/grant").SetDetail("POST").SetOk(true).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	ok, err = repo.RecentLiveGateAudit(ctx, biz.LiveGateAuditGrant, actor, 5*time.Minute)
	if err != nil || !ok {
		t.Fatalf("recent successful audit must be seen: ok=%v err=%v", ok, err)
	}
	ok, err = repo.RecentLiveGateAudit(ctx, biz.LiveGateAuditGrant, "other", 5*time.Minute)
	if err != nil || ok {
		t.Fatalf("audit of another operator must not match: ok=%v err=%v", ok, err)
	}
}
