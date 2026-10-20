package data

import (
	"context"
	"fmt"

	"server/app/risk/internal/biz"
	"server/ent"
	"server/ent/alertrule"
	"server/pkg/alert"
	"server/pkg/events"
	"server/pkg/redact"

	"github.com/go-kratos/kratos/v2/log"
)

// AlertRuleRepo 是 biz.AlertStore 的 ent 实现：规则落 alert_rule 表，
// 触发行更新与 notify.request 在同事务（outbox 模式，同 SaveKill）。
type AlertRuleRepo struct {
	client *ent.Client
	log    *log.Helper
}

func NewAlertRuleRepo(client *ent.Client, logger log.Logger) *AlertRuleRepo {
	return &AlertRuleRepo{client: client, log: log.NewHelper(log.With(logger, "module", "risk/alertdata"))}
}

// toRule 把行解码为 pkg 规则：condition/status 不在三态两态内就报错带主键与
// rule_id（运维可直接定位行），绝不静默兜底。
func toRule(row *ent.AlertRule) (alert.Rule, error) {
	cond, err := alert.ParseCondition(row.Condition)
	if err != nil {
		return alert.Rule{}, fmt.Errorf("alert_rule %d %s: %w", row.ID, row.RuleID, err)
	}
	r := alert.Rule{
		ID: row.RuleID, Symbol: row.Symbol, Condition: cond, TargetPrice: row.TargetPrice,
		Note: row.Note, LastPrice: row.LastPrice, TriggeredAt: row.TriggeredAt,
		CreatedAt: row.CreatedAt, ArmedAt: row.ArmedAt, UpdatedAt: row.UpdatedAt,
	}
	switch alert.Status(row.Status) {
	case alert.StatusActive:
		if row.TriggeredAt != nil {
			return alert.Rule{}, fmt.Errorf("alert_rule %d %s: active 行带 triggered_at", row.ID, row.RuleID)
		}
		r.Status = alert.StatusActive
	case alert.StatusTriggered:
		if row.TriggeredAt == nil {
			return alert.Rule{}, fmt.Errorf("alert_rule %d %s: triggered 行缺 triggered_at", row.ID, row.RuleID)
		}
		r.Status = alert.StatusTriggered
	default:
		return alert.Rule{}, fmt.Errorf("alert_rule %d %s: unknown status %q", row.ID, row.RuleID, row.Status)
	}
	return r, nil
}

func alertRows(rs []*ent.AlertRule) ([]alert.Rule, error) {
	out := make([]alert.Rule, 0, len(rs))
	for _, row := range rs {
		r, err := toRule(row)
		if err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, nil
}

func (r *AlertRuleRepo) ListAlertRules(ctx context.Context) ([]alert.Rule, error) {
	rows, err := r.client.AlertRule.Query().Order(alertrule.ByCreatedAt()).All(ctx)
	if err != nil {
		return nil, err
	}
	return alertRows(rows)
}

func (r *AlertRuleRepo) ActiveAlertRules(ctx context.Context) ([]alert.Rule, error) {
	rows, err := r.client.AlertRule.Query().
		Where(alertrule.StatusEQ(string(alert.StatusActive))).
		Order(alertrule.ByCreatedAt()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return alertRows(rows)
}

func (r *AlertRuleRepo) GetAlertRule(ctx context.Context, id string) (alert.Rule, error) {
	row, err := r.client.AlertRule.Query().Where(alertrule.RuleIDEQ(id)).Only(ctx)
	if ent.IsNotFound(err) {
		return alert.Rule{}, biz.ErrAlertNotFound
	}
	if err != nil {
		return alert.Rule{}, err
	}
	return toRule(row)
}

func (r *AlertRuleRepo) CreateAlertRule(ctx context.Context, rule alert.Rule, operator string) error {
	return r.client.AlertRule.Create().
		SetRuleID(rule.ID).
		SetSymbol(rule.Symbol).
		SetCondition(string(rule.Condition)).
		SetTargetPrice(rule.TargetPrice).
		SetNote(rule.Note).
		SetStatus(string(rule.Status)).
		SetArmedAt(rule.ArmedAt).
		SetOperator(operator).
		Exec(ctx)
}

// ReplaceAlertRule 整行白名单替换：触发态字段一并按重建结果落（Edit/Rearm 会清成零值）。
func (r *AlertRuleRepo) ReplaceAlertRule(ctx context.Context, rule alert.Rule, operator string) error {
	n, err := r.client.AlertRule.Update().
		Where(alertrule.RuleIDEQ(rule.ID)).
		SetSymbol(rule.Symbol).
		SetCondition(string(rule.Condition)).
		SetTargetPrice(rule.TargetPrice).
		SetNote(rule.Note).
		SetStatus(string(rule.Status)).
		ClearLastPrice().
		ClearTriggeredAt().
		SetArmedAt(rule.ArmedAt).
		SetOperator(operator).
		Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return biz.ErrAlertNotFound
	}
	return nil
}

func (r *AlertRuleRepo) DeleteAlertRule(ctx context.Context, id, operator string) error {
	n, err := r.client.AlertRule.Delete().Where(alertrule.RuleIDEQ(id)).Exec(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return biz.ErrAlertNotFound
	}
	r.log.Infof("alert rule %s deleted by %s", id, operator)
	return nil
}

// ApplyAlertStep 落一步评估结果：以 (rule_id, armed_at, status=active, 无 triggered_at)
// 为乐观前置 CAS 应用——评估期间的 Edit/Rearm（前移 armed_at）或并发节拍先触发，
// 都会让过期评估整体作废（applied=false）：不写库、不投 outbox，等下拍按最新读面再评；
// 规则不存在返回 ErrAlertNotFound。触发时 notify.request 与触发行同事务（outbox 模式，
// 同 SaveKill），触发后重复节拍由本 CAS 与 notify 侧 dedup_key 双重兜底。
// 节拍落库不覆写 operator（那是 CRUD 归因列），触发/未触发的不变式在这里断言而非空指针裸奔。
func (r *AlertRuleRepo) ApplyAlertStep(ctx context.Context, next alert.Rule, fire *biz.AlertFire) (bool, error) {
	if fire != nil {
		if next.Status != alert.StatusTriggered || next.TriggeredAt == nil {
			return false, fmt.Errorf("alert_rule %s: fire 步需要 triggered 态且带 triggered_at（status=%s）", next.ID, next.Status)
		}
	} else if next.Status != alert.StatusActive {
		return false, fmt.Errorf("alert_rule %s: 无触发步只落 active 基线（status=%s）", next.ID, next.Status)
	}
	applied := false
	err := r.inTx(ctx, func(tx *ent.Tx) error {
		up := tx.AlertRule.Update().
			Where(
				alertrule.RuleIDEQ(next.ID),
				alertrule.ArmedAtEQ(next.ArmedAt),
				alertrule.StatusEQ(string(alert.StatusActive)),
				alertrule.TriggeredAtIsNil(),
			)
		if next.LastPrice != nil {
			up.SetLastPrice(*next.LastPrice)
		} else {
			up.ClearLastPrice()
		}
		if fire != nil {
			up.SetStatus(string(next.Status)).SetTriggeredAt(*next.TriggeredAt)
		}
		n, err := up.Save(ctx)
		if err != nil {
			return err
		}
		if n == 0 {
			// 区分「规则不存在」与「评估已过期」（重挂/编辑动了 armed_at，或并发节拍先触发）。
			_, qerr := tx.AlertRule.Query().Where(alertrule.RuleIDEQ(next.ID)).Only(ctx)
			if ent.IsNotFound(qerr) {
				return biz.ErrAlertNotFound
			}
			return qerr // qerr==nil：过期节拍，空提交返回 applied=false
		}
		if fire == nil {
			applied = true
			return nil
		}
		if err := insert(ctx, tx, events.SubjectNotifyRequest, fireNotice(fire)); err != nil {
			return err
		}
		applied = true
		return nil
	})
	if err != nil {
		// 任何失败（更新/insert/提交）都不报 applied：整步未生效。
		return false, err
	}
	return applied, nil
}

// fireNotice 是 M10 fromRequest 的通用载荷：notify 服务按 dedup_key 合并重复。
func fireNotice(fire *biz.AlertFire) map[string]any {
	desc := alert.Description(fire.Rule)
	return map[string]any{
		"level":    "warning",
		"category": "alert-rule",
		"title":    desc + " 已触发",
		"body": redact.Line(fmt.Sprintf("%s 触发（现价 %.2f，%s）；触发后需编辑或重新布防才会再响。",
			desc, fire.Price, fire.At.Format("15:04:05"))),
		"dedup_key": fmt.Sprintf("alert-rule:%s:%d", fire.Rule.ID, fire.At.Unix()),
	}
}

func (r *AlertRuleRepo) inTx(ctx context.Context, fn func(tx *ent.Tx) error) error {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
