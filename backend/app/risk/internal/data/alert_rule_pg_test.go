package data

// AlertRuleRepo 的隔离 PG 集成验证（房规：NT_TEST_PG_PORT 指定 55432 这类专用实例才跑，
// 缺省 skip 不进 CI）。覆盖 CRUD 往返、触发行+notify.request outbox 同事务、
// Replace 白名单重建清触发态、坏行显式报错、唯一约束、乐观 CAS 作废（重挂/双触发）、
// outbox 失败整步回滚、节拍落库不改写 operator。

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"entgo.io/ent/dialect"
	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	_ "github.com/jackc/pgx/v5/stdlib"

	"server/app/risk/internal/biz"
	"server/ent"
	"server/ent/alertrule"
	"server/ent/hook"
	"server/ent/outbox"
	"server/pkg/alert"
	"server/pkg/dbinit"
	"server/pkg/events"
	"server/pkg/migrate"
)

func alertRulePGClient(t *testing.T) *ent.Client {
	t.Helper()
	raw := os.Getenv("NT_TEST_PG_PORT")
	if raw == "" {
		t.Skip("requires isolated NT_TEST_PG_PORT")
	}
	port, err := strconv.Atoi(raw)
	if err != nil || port < 1 || port > 65535 || port == 5432 {
		t.Fatal("isolated non-default PostgreSQL port required")
	}
	cfg := dbinit.Config{Host: "127.0.0.1", Port: int32(port), User: "postgres", Password: "postgres", Database: "novatrader_risk_alert_rule_test", SSLMode: "disable", Timeout: 3 * time.Second}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err = dbinit.EnsureDatabase(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	if err = dbinit.EnsureExtension(ctx, cfg, "vector"); err != nil {
		t.Fatal(err)
	}
	if err = migrate.Schema(ctx, cfg); err != nil {
		t.Fatal(err)
	}
	dsn, err := dbinit.BusinessDSN(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("pgx", dsn)
	if err != nil {
		t.Fatal(err)
	}
	db.SetMaxOpenConns(4)
	c := ent.NewClient(ent.Driver(entsql.OpenDB(dialect.Postgres, db)))
	t.Cleanup(func() { c.Close() })
	return c
}

func newPGRule(id, symbol string, cond alert.Condition, target float64, armed time.Time) alert.Rule {
	return alert.Rule{ID: id, Symbol: symbol, Condition: cond, TargetPrice: target, Status: alert.StatusActive, ArmedAt: armed}
}

func pf(v float64) *float64 { return &v }

func TestAlertRuleRepoPG(t *testing.T) {
	c := alertRulePGClient(t)
	repo := NewAlertRuleRepo(c, log.NewStdLogger(io.Discard))
	ctx := context.Background()
	armed := time.Date(2026, 10, 19, 9, 41, 0, 0, time.UTC)

	// 干净库起步：重复跑本测试前先清空上一轮的行（专用库，不碰别的 schema）。
	if _, err := c.AlertRule.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err := c.Outbox.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}

	t.Run("create list get roundtrip with unique rule_id", func(t *testing.T) {
		r1 := newPGRule("alr-pg-a", "600519.SH", alert.Above, 1500.5, armed)
		r1.Note = "茅台上破"
		r2 := newPGRule("alr-pg-b", "000001.SZ", alert.Below, 9.9, armed.Add(time.Minute))
		if err := repo.CreateAlertRule(ctx, r1, "tester"); err != nil {
			t.Fatalf("create r1: %v", err)
		}
		if err := repo.CreateAlertRule(ctx, r2, "tester"); err != nil {
			t.Fatalf("create r2: %v", err)
		}
		// 唯一约束由迁移兜底，不靠应用层记忆。
		if err := repo.CreateAlertRule(ctx, r1, "tester"); err == nil {
			t.Fatal("duplicate rule_id must be rejected by the unique constraint")
		}
		rules, err := repo.ListAlertRules(ctx)
		if err != nil {
			t.Fatalf("list: %v", err)
		}
		if len(rules) != 2 || rules[0].ID != "alr-pg-a" || rules[1].ID != "alr-pg-b" {
			t.Fatalf("list order/content mismatch: %+v", rules)
		}
		got, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		if got.Symbol != "600519.SH" || got.Condition != alert.Above || got.TargetPrice != 1500.5 ||
			got.Note != "茅台上破" || got.Status != alert.StatusActive || got.LastPrice != nil || got.TriggeredAt != nil {
			t.Fatalf("roundtrip fields mismatch: %+v", got)
		}
		if got.CreatedAt.IsZero() || got.UpdatedAt.IsZero() || !got.ArmedAt.Equal(armed) {
			t.Fatalf("bookkeeping times must roundtrip, got %+v", got)
		}
		if _, err := repo.GetAlertRule(ctx, "alr-pg-missing"); !errors.Is(err, biz.ErrAlertNotFound) {
			t.Fatalf("missing id must be ErrAlertNotFound, got %v", err)
		}
	})

	t.Run("apply step no-fire refreshes baseline only", func(t *testing.T) {
		next, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		p := 1499.8
		next.LastPrice = &p
		applied, err := repo.ApplyAlertStep(ctx, next, nil)
		if err != nil {
			t.Fatalf("apply no-fire: %v", err)
		}
		if !applied {
			t.Fatal("fresh read must apply")
		}
		got, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		if got.LastPrice == nil || *got.LastPrice != p {
			t.Fatalf("last_price must persist, got %+v", got.LastPrice)
		}
		if got.Status != alert.StatusActive || got.TriggeredAt != nil {
			t.Fatalf("no-fire step must not flip status, got %s at %+v", got.Status, got.TriggeredAt)
		}
	})

	t.Run("apply step fire persists row and notify outbox atomically", func(t *testing.T) {
		next, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		fireAt := armed.Add(2 * time.Hour)
		next.Status = alert.StatusTriggered
		next.TriggeredAt = &fireAt
		fire := &biz.AlertFire{Rule: next, Price: 1501.2, At: fireAt}
		applied, err := repo.ApplyAlertStep(ctx, next, fire)
		if err != nil {
			t.Fatalf("apply fire: %v", err)
		}
		if !applied {
			t.Fatal("fresh read must apply")
		}
		got, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		if got.Status != alert.StatusTriggered || got.TriggeredAt == nil || !got.TriggeredAt.Equal(fireAt) {
			t.Fatalf("fired row mismatch: %s at %+v", got.Status, got.TriggeredAt)
		}
		// 触发后的规则不再参与评估。
		active, err := repo.ActiveAlertRules(ctx)
		if err != nil {
			t.Fatalf("active: %v", err)
		}
		for _, r := range active {
			if r.ID == "alr-pg-a" {
				t.Fatalf("triggered rule must leave the active set: %+v", active)
			}
		}
		// notify.request outbox 与触发行同事务落地，投递链从这里接手。
		rows, err := c.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectNotifyRequest)).All(ctx)
		if err != nil {
			t.Fatalf("outbox query: %v", err)
		}
		if len(rows) != 1 {
			t.Fatalf("exactly one notify.request expected, got %d", len(rows))
		}
		var payload map[string]any
		if err := json.Unmarshal(rows[0].Payload, &payload); err != nil {
			t.Fatalf("payload json: %v", err)
		}
		if payload["category"] != "alert-rule" || payload["level"] != "warning" {
			t.Fatalf("notice payload mismatch: %v", payload)
		}
		key, _ := payload["dedup_key"].(string)
		if !strings.HasPrefix(key, "alert-rule:alr-pg-a:") {
			t.Fatalf("dedup_key must carry rule id, got %q", key)
		}
		title, _ := payload["title"].(string)
		if !strings.Contains(title, "已触发") || !strings.Contains(title, "600519") {
			t.Fatalf("title must describe the fired rule, got %q", title)
		}
	})

	t.Run("replace whitelists fields and clears trigger state", func(t *testing.T) {
		rearmed, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		// Edit/Rearm 的产物：触发态字段清零、参数全量白名单替换。
		rearmed.Symbol = "600519.SH"
		rearmed.Condition = alert.Crosses
		rearmed.TargetPrice = 1600
		rearmed.Note = "重挂穿越"
		rearmed.Status = alert.StatusActive
		rearmed.LastPrice = nil
		rearmed.TriggeredAt = nil
		rearmed.ArmedAt = armed.Add(3 * time.Hour)
		if err := repo.ReplaceAlertRule(ctx, rearmed, "tester"); err != nil {
			t.Fatalf("replace: %v", err)
		}
		got, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		if got.Condition != alert.Crosses || got.TargetPrice != 1600 || got.Note != "重挂穿越" ||
			got.Status != alert.StatusActive || got.LastPrice != nil || got.TriggeredAt != nil {
			t.Fatalf("rearm must clear trigger state and apply whitelist fields, got %+v", got)
		}
		missing := newPGRule("alr-pg-missing", "000001.SZ", alert.Above, 10, armed)
		if err := repo.ReplaceAlertRule(ctx, missing, "tester"); !errors.Is(err, biz.ErrAlertNotFound) {
			t.Fatalf("replace missing must be ErrAlertNotFound, got %v", err)
		}
		// 重挂后重新进入评估集合。
		active, err := repo.ActiveAlertRules(ctx)
		if err != nil {
			t.Fatalf("active: %v", err)
		}
		seen := false
		for _, r := range active {
			seen = seen || r.ID == "alr-pg-a"
		}
		if !seen {
			t.Fatalf("rearmed rule must rejoin the active set: %+v", active)
		}
	})

	t.Run("apply step rejects invariant violations before any write", func(t *testing.T) {
		next, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		fireAt := armed.Add(6 * time.Hour)
		// fire 非 nil 但行未呈 triggered 态。
		if _, err := repo.ApplyAlertStep(ctx, next, &biz.AlertFire{Rule: next, Price: 1, At: fireAt}); err == nil ||
			!strings.Contains(err.Error(), "triggered") {
			t.Fatalf("fire on an active step must be rejected, got %v", err)
		}
		// fire 非 nil、status 已是 triggered 却缺 triggered_at。
		bad := next
		bad.Status = alert.StatusTriggered
		if _, err := repo.ApplyAlertStep(ctx, bad, &biz.AlertFire{Rule: bad, Price: 1, At: fireAt}); err == nil {
			t.Fatal("fire without triggered_at must be rejected")
		}
		// fire 为 nil 但行已呈 triggered 态。
		bad2 := bad
		bad2.TriggeredAt = &fireAt
		if _, err := repo.ApplyAlertStep(ctx, bad2, nil); err == nil ||
			!strings.Contains(err.Error(), "active") {
			t.Fatalf("no-fire step on triggered state must be rejected, got %v", err)
		}
		// 三种拒绝都不落任何写。
		cnt, err := c.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectNotifyRequest)).Count(ctx)
		if err != nil || cnt != 1 {
			t.Fatalf("rejected steps must write nothing, outbox=%d err=%v", cnt, err)
		}
	})

	t.Run("stale fire after concurrent rearm is discarded whole", func(t *testing.T) {
		next, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		fireAt := next.ArmedAt.Add(time.Hour)
		next.LastPrice = pf(1601.0)
		next.Status = alert.StatusTriggered
		next.TriggeredAt = &fireAt
		fire := &biz.AlertFire{Rule: next, Price: 1601.0, At: fireAt}
		// 并发重挂：读面之后 Replace 前移 armed_at、清触发态与基线。
		if err := repo.ReplaceAlertRule(ctx, alert.Rule{
			ID: next.ID, Symbol: next.Symbol, Condition: next.Condition,
			TargetPrice: next.TargetPrice, Note: next.Note,
			Status: alert.StatusActive, ArmedAt: next.ArmedAt.Add(time.Minute),
		}, "op2"); err != nil {
			t.Fatalf("concurrent rearm: %v", err)
		}
		applied, err := repo.ApplyAlertStep(ctx, next, fire)
		if err != nil {
			t.Fatalf("stale fire must be a clean discard, not an error: %v", err)
		}
		if applied {
			t.Fatal("stale fire must not apply")
		}
		got, err := repo.GetAlertRule(ctx, "alr-pg-a")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		if got.Status != alert.StatusActive || got.TriggeredAt != nil || got.LastPrice != nil {
			t.Fatalf("discarded step must leave no trace: %+v", got)
		}
		cnt, err := c.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectNotifyRequest)).Count(ctx)
		if err != nil || cnt != 1 {
			t.Fatalf("discarded fire must not reach the outbox: count=%d err=%v", cnt, err)
		}
	})

	t.Run("dual fire keeps exactly one outbox", func(t *testing.T) {
		next, err := repo.GetAlertRule(ctx, "alr-pg-b")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		fireAt := next.ArmedAt.Add(2 * time.Minute)
		next.Status = alert.StatusTriggered
		next.TriggeredAt = &fireAt
		fire := &biz.AlertFire{Rule: next, Price: 9.8, At: fireAt}
		applied, err := repo.ApplyAlertStep(ctx, next, fire)
		if err != nil || !applied {
			t.Fatalf("first fire must apply, applied=%v err=%v", applied, err)
		}
		// 同一拍象的过期副本再来一次（先行已触发）：CAS 整步作废。
		applied, err = repo.ApplyAlertStep(ctx, next, fire)
		if err != nil {
			t.Fatalf("dual fire copy must be a clean discard: %v", err)
		}
		if applied {
			t.Fatal("stale fire copy must not apply")
		}
		cnt, err := c.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectNotifyRequest)).Count(ctx)
		if err != nil || cnt != 2 {
			t.Fatalf("dual fire must keep exactly one new notify.request, count=%d err=%v", cnt, err)
		}
	})

	t.Run("outbox insert failure rolls back the whole step", func(t *testing.T) {
		if err := repo.CreateAlertRule(ctx, newPGRule("alr-pg-c", "600519.SH", alert.Above, 10, armed), "creator"); err != nil {
			t.Fatalf("create: %v", err)
		}
		failing := true
		c.Outbox.Use(func(next2 ent.Mutator) ent.Mutator {
			return hook.OutboxFunc(func(ctx context.Context, m *ent.OutboxMutation) (ent.Value, error) {
				if failing {
					return nil, errors.New("outbox boom")
				}
				return next2.Mutate(ctx, m)
			})
		})
		defer func() { failing = false }()

		next, err := repo.GetAlertRule(ctx, "alr-pg-c")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		fireAt := next.ArmedAt.Add(time.Hour)
		next.LastPrice = pf(10.5)
		next.Status = alert.StatusTriggered
		next.TriggeredAt = &fireAt
		fire := &biz.AlertFire{Rule: next, Price: 10.5, At: fireAt}
		applied, err := repo.ApplyAlertStep(ctx, next, fire)
		if err == nil || !strings.Contains(err.Error(), "outbox boom") {
			t.Fatalf("outbox boom must surface, applied=%v err=%v", applied, err)
		}
		if applied {
			t.Fatal("failed step must not report applied")
		}
		// 行与 outbox 完好如初（同事务回滚）。
		got, err := repo.GetAlertRule(ctx, "alr-pg-c")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		if got.Status != alert.StatusActive || got.TriggeredAt != nil || got.LastPrice != nil {
			t.Fatalf("rolled-back step must leave no trace: %+v", got)
		}
		cnt, err := c.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectNotifyRequest)).Count(ctx)
		if err != nil || cnt != 2 {
			t.Fatalf("failed step must not write the outbox: count=%d err=%v", cnt, err)
		}
		// 故障解除后同一读面仍可正常落库（CAS 前置未被污染）。
		failing = false
		applied, err = repo.ApplyAlertStep(ctx, next, fire)
		if err != nil || !applied {
			t.Fatalf("step must apply once the outbox recovers, applied=%v err=%v", applied, err)
		}
	})

	t.Run("tick step leaves operator attribution untouched", func(t *testing.T) {
		if err := repo.CreateAlertRule(ctx, newPGRule("alr-pg-op", "600519.SH", alert.Above, 100, armed), "zhang"); err != nil {
			t.Fatalf("create: %v", err)
		}
		next, err := repo.GetAlertRule(ctx, "alr-pg-op")
		if err != nil {
			t.Fatalf("get: %v", err)
		}
		next.LastPrice = pf(99.5)
		applied, err := repo.ApplyAlertStep(ctx, next, nil)
		if err != nil || !applied {
			t.Fatalf("no-fire step: applied=%v err=%v", applied, err)
		}
		row, err := c.AlertRule.Query().Where(alertrule.RuleIDEQ("alr-pg-op")).Only(ctx)
		if err != nil {
			t.Fatalf("raw get: %v", err)
		}
		if row.Operator != "zhang" {
			t.Fatalf("no-fire step must not overwrite operator, got %q", row.Operator)
		}
		next2, err := repo.GetAlertRule(ctx, "alr-pg-op")
		if err != nil {
			t.Fatalf("re-get: %v", err)
		}
		fireAt := next2.ArmedAt.Add(time.Hour)
		next2.LastPrice = pf(100.5)
		next2.Status = alert.StatusTriggered
		next2.TriggeredAt = &fireAt
		applied, err = repo.ApplyAlertStep(ctx, next2, &biz.AlertFire{Rule: next2, Price: 100.5, At: fireAt})
		if err != nil || !applied {
			t.Fatalf("fire step: applied=%v err=%v", applied, err)
		}
		row2, err := c.AlertRule.Query().Where(alertrule.RuleIDEQ("alr-pg-op")).Only(ctx)
		if err != nil {
			t.Fatalf("raw re-get: %v", err)
		}
		if row2.Operator != "zhang" {
			t.Fatalf("fire step must not overwrite operator, got %q", row2.Operator)
		}
	})

	t.Run("delete semantics", func(t *testing.T) {
		if err := repo.DeleteAlertRule(ctx, "alr-pg-missing", "tester"); !errors.Is(err, biz.ErrAlertNotFound) {
			t.Fatalf("delete missing must be ErrAlertNotFound, got %v", err)
		}
		if err := repo.DeleteAlertRule(ctx, "alr-pg-b", "tester"); err != nil {
			t.Fatalf("delete: %v", err)
		}
		if _, err := repo.GetAlertRule(ctx, "alr-pg-b"); !errors.Is(err, biz.ErrAlertNotFound) {
			t.Fatalf("deleted rule must be gone, got %v", err)
		}
	})

	t.Run("corrupt rows surface explicit errors instead of silent fallback", func(t *testing.T) {
		bad1, err := c.AlertRule.Create().
			SetRuleID("alr-pg-bad1").SetSymbol("600519.SH").SetCondition("above").
			SetTargetPrice(1).SetStatus("active").SetArmedAt(armed).
			SetTriggeredAt(armed). // active 行带 triggered_at：状态机不允许
			Save(ctx)
		if err != nil {
			t.Fatalf("seed bad1: %v", err)
		}
		if _, err := repo.ListAlertRules(ctx); err == nil || !strings.Contains(err.Error(), "active 行带 triggered_at") {
			t.Fatalf("active+triggered row must surface, got %v", err)
		}
		_ = c.AlertRule.DeleteOne(bad1).Exec(ctx)

		bad2, err := c.AlertRule.Create().
			SetRuleID("alr-pg-bad2").SetSymbol("600519.SH").SetCondition("sideways").
			SetTargetPrice(1).SetStatus("active").SetArmedAt(armed).
			Save(ctx)
		if err != nil {
			t.Fatalf("seed bad2: %v", err)
		}
		if _, err := repo.ListAlertRules(ctx); err == nil || !strings.Contains(err.Error(), "alr-pg-bad2") || !strings.Contains(err.Error(), `"sideways"`) {
			t.Fatalf("bad condition must surface with the rule identity and raw value, got %v", err)
		}
		_ = c.AlertRule.DeleteOne(bad2).Exec(ctx)

		bad3, err := c.AlertRule.Create().
			SetRuleID("alr-pg-bad3").SetSymbol("600519.SH").SetCondition("above").
			SetTargetPrice(1).SetStatus("weird").SetArmedAt(armed).
			Save(ctx)
		if err != nil {
			t.Fatalf("seed bad3: %v", err)
		}
		if _, err := repo.ListAlertRules(ctx); err == nil || !strings.Contains(err.Error(), `unknown status "weird"`) {
			t.Fatalf("unknown status must surface with the raw value, got %v", err)
		}
		_ = c.AlertRule.DeleteOne(bad3).Exec(ctx)
	})
}
