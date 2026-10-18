package data

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"

	box "server/ent/outbox"
)

// 死信重放审批持久往返（隔离PG，独立库名避开处置留痕测试的 outbox 空断言）：
// 条件单行重排 → 字段逐一核验 → 回到调度器取单谓词 → 双重重排/已投递守卫如实拒绝。
func TestRequeueDeadLetterOnIsolatedPostgres(t *testing.T) {
	client := adminContractClient(t, "novatrader_admin_replay_test")
	s := &AuditStore{client: client}
	ctx := context.Background()

	// 不存在的 event_id：如实 false，不报错
	if found, err := s.RequeueDeadLetter(ctx, "missing-"+uuid.NewString()[:8]); err != nil || found {
		t.Fatalf("missing row = %v %v", found, err)
	}

	tag := uuid.NewString()[:8]
	eventID := "evt-dead-" + tag
	future := time.Now().UTC().Add(time.Hour)
	lastErr := "publisher unavailable"
	created, err := client.Outbox.Create().
		SetEventID(eventID).
		SetTraceID("tr-" + tag).
		SetSubject("novatrader.test.replay").
		SetSource("admin-test").
		SetPayload([]byte(`{"k":1}`)).
		SetMetadata(map[string]any{"kind": "test"}).
		SetAttempts(12).
		SetDeadLetter(true).
		SetNextAttemptAt(future).
		SetLeaseUntil(future).
		SetLeaseOwner("w1").
		SetLastError(lastErr).
		Save(ctx)
	if err != nil {
		t.Fatal(err)
	}

	// 重排前：不在调度器取单谓词内（dead_letter=true 被排除）
	claimableBefore, err := client.Outbox.Query().Where(
		box.EventID(eventID),
		box.PublishedAtIsNil(), box.DeadLetter(false),
		box.Or(box.NextAttemptAtIsNil(), box.NextAttemptAtLTE(time.Now().UTC())),
		box.Or(box.LeaseUntilIsNil(), box.LeaseUntilLTE(time.Now().UTC())),
	).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimableBefore != 0 {
		t.Fatal("dead row must be invisible to the dispatcher claim predicate")
	}

	found, err := s.RequeueDeadLetter(ctx, eventID)
	if err != nil || !found {
		t.Fatalf("requeue = %v %v", found, err)
	}

	row, err := client.Outbox.Get(ctx, created.ID)
	if err != nil {
		t.Fatal(err)
	}
	if row.DeadLetter || row.Attempts != 0 || row.NextAttemptAt != nil || row.LeaseUntil != nil || row.LeaseOwner != "" {
		t.Fatalf("requeued shape wrong: dead=%v attempts=%d next=%v lease=%v owner=%q",
			row.DeadLetter, row.Attempts, row.NextAttemptAt, row.LeaseUntil, row.LeaseOwner)
	}
	// last_error 是案底：重排不抹掉；trace/subject/payload 不许动
	if row.LastError == nil || *row.LastError != lastErr {
		t.Fatalf("last_error must be preserved, got %+v", row.LastError)
	}
	if row.TraceID != "tr-"+tag || row.Subject != "novatrader.test.replay" || row.EventID != eventID {
		t.Fatalf("identity fields changed: %+v", row)
	}

	// 重排后：落回调度器取单谓词 → 下一拍即可被取单投递
	claimableAfter, err := client.Outbox.Query().Where(
		box.EventID(eventID),
		box.PublishedAtIsNil(), box.DeadLetter(false),
		box.Or(box.NextAttemptAtIsNil(), box.NextAttemptAtLTE(time.Now().UTC())),
		box.Or(box.LeaseUntilIsNil(), box.LeaseUntilLTE(time.Now().UTC())),
	).Count(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if claimableAfter != 1 {
		t.Fatal("requeued row must re-enter the dispatcher claim set")
	}

	// 双重重排：第二次条件不再匹配 → false，不报错、不重复改动
	if found, err := s.RequeueDeadLetter(ctx, eventID); err != nil || found {
		t.Fatalf("second requeue = %v %v", found, err)
	}

	// 已投递的行（即使被标注 dead）不许被重排复活
	pubID := "evt-pub-" + tag
	pubAt := time.Now().UTC()
	if _, err := client.Outbox.Create().
		SetEventID(pubID).
		SetSubject("novatrader.test.replay").
		SetSource("admin-test").
		SetPayload([]byte(`{}`)).
		SetPublishedAt(pubAt).
		SetDeadLetter(true).
		Save(ctx); err != nil {
		t.Fatal(err)
	}
	if found, err := s.RequeueDeadLetter(ctx, pubID); err != nil || found {
		t.Fatalf("published row requeue = %v %v", found, err)
	}
	pubRow, err := client.Outbox.Query().Where(box.EventID(pubID)).Only(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if pubRow.PublishedAt == nil || !pubRow.DeadLetter {
		t.Fatal("published row must be untouched by replay attempts")
	}
}
