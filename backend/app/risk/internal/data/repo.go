package data

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"
	"time"

	"server/app/risk/internal/biz"
	"server/ent"
	"server/ent/marketdata"
	"server/ent/riskcheck"
	"server/ent/riskevent"
	"server/ent/strategyconfig"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/params"
	"server/pkg/risk"
	"server/pkg/tradecal"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
)

const (
	kindKill    = "killswitch"
	kindMode    = "mode"
	kindBreaker = "breaker"
	kindParam   = "param"
	kindReject  = "reject_rate"
)

// Repo 实现 biz.Repo。
type Repo struct {
	client *ent.Client
	log    *log.Helper
}

func NewRepo(client *ent.Client, logger log.Logger) *Repo {
	return &Repo{client: client, log: log.NewHelper(log.With(logger, "module", "risk/data"))}
}

func (r *Repo) LoadParams(ctx context.Context) (map[string]string, error) {
	rows, err := r.client.StrategyConfig.Query().
		Where(strategyconfig.ConfigKeyHasPrefix(risk.KeyPrefix)).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]string, len(rows))
	for _, row := range rows {
		out[row.ConfigKey] = row.ConfigValue
	}
	return out, nil
}

func (r *Repo) SaveParam(ctx context.Context, key, value, old, operator string) error {
	if err := params.Put(ctx, r.client, key, value, valueType(value), "M08 风控参数"); err != nil {
		return err
	}
	return r.client.RiskEvent.Create().
		SetKind(kindParam).
		SetOperator(operator).
		SetReason(key).
		SetDetail(map[string]any{"key": key, "old": old, "new": value}).
		Exec(ctx)
}

func valueType(v string) string {
	if v == "true" || v == "false" {
		return "bool"
	}
	if _, err := strconv.ParseInt(v, 10, 64); err == nil {
		return "int"
	}
	if _, err := strconv.ParseFloat(v, 64); err == nil {
		return "float"
	}
	if _, err := time.ParseDuration(v); err == nil {
		return "duration"
	}
	return "string"
}

func latest(q *ent.RiskEventQuery) *ent.RiskEventQuery {
	return q.Order(riskevent.ByCreatedAt(entsql.OrderDesc()), riskevent.ByID(entsql.OrderDesc()))
}

func (r *Repo) LastKill(ctx context.Context) (risk.KillState, error) {
	row, err := latest(r.client.RiskEvent.Query().Where(riskevent.KindEQ(kindKill))).First(ctx)
	if ent.IsNotFound(err) {
		return risk.KillState{}, nil
	}
	if err != nil {
		return risk.KillState{}, err
	}
	if !row.Active {
		return risk.KillState{}, nil
	}
	return risk.KillState{Active: true, Source: row.Source, Reason: row.Reason, Operator: row.Operator, At: row.CreatedAt}, nil
}

func (r *Repo) LastMode(ctx context.Context) (risk.Mode, bool, error) {
	row, err := latest(r.client.RiskEvent.Query().Where(
		riskevent.KindIn(kindKill, kindMode),
		riskevent.ModeNEQ(""),
	)).First(ctx)
	if ent.IsNotFound(err) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	m, err := risk.ParseMode(row.Mode)
	if err != nil {
		return 0, false, fmt.Errorf("risk_event %d: %w", row.ID, err)
	}
	return m, true, nil
}

// SaveKill 在一个事务里写 risk_event 和 outbox（sys.killswitch、risk.alert）。
func (r *Repo) SaveKill(ctx context.Context, k risk.KillState, mode risk.Mode) error {
	at := k.At
	if at.IsZero() {
		at = time.Now()
	}
	msg := "Kill Switch 已触发：" + k.Reason
	level := "critical"
	if !k.Active {
		msg = "Kill Switch 已由 " + k.Operator + " 恢复：" + k.Reason
		level = "warning"
	}
	return r.inTx(ctx, func(tx *ent.Tx) error {
		if err := tx.RiskEvent.Create().
			SetKind(kindKill).SetActive(k.Active).SetMode(mode.String()).
			SetSource(k.Source).SetReason(k.Reason).SetOperator(k.Operator).
			SetCreatedAt(at).
			Exec(ctx); err != nil {
			return err
		}
		if err := insert(ctx, tx, events.SubjectKillSwitch, biz.KillSwitchPayload{
			Active: k.Active, Source: k.Source, Reason: k.Reason, Operator: k.Operator, Mode: mode.String(), At: at,
		}); err != nil {
			return err
		}
		return insert(ctx, tx, events.SubjectRiskAlert, biz.AlertPayload{Level: level, Kind: kindKill, Message: msg, At: at})
	})
}

func (r *Repo) SaveMode(ctx context.Context, mode risk.Mode, operator, reason string) error {
	return r.client.RiskEvent.Create().
		SetKind(kindMode).SetMode(mode.String()).SetOperator(operator).SetReason(reason).
		Exec(ctx)
}

func (r *Repo) SaveBreaker(ctx context.Context, account risk.AccountType, active bool, reason string) error {
	now := time.Now()
	msg := reason
	if account != "" {
		msg = string(account) + " " + reason
	}
	if !active {
		msg = "市场熔断解除"
	}
	return r.inTx(ctx, func(tx *ent.Tx) error {
		if err := tx.RiskEvent.Create().
			SetKind(kindBreaker).SetActive(active).SetSource(string(account)).SetReason(reason).
			SetCreatedAt(now).
			Exec(ctx); err != nil {
			return err
		}
		return insert(ctx, tx, events.SubjectRiskAlert, biz.AlertPayload{
			Level: "warning", Kind: kindBreaker, AccountType: account, Message: msg, At: now,
		})
	})
}

func (r *Repo) SaveAlert(ctx context.Context, message string) error {
	now := time.Now()
	return r.inTx(ctx, func(tx *ent.Tx) error {
		if err := tx.RiskEvent.Create().
			SetKind(kindReject).SetActive(true).SetReason(message).SetCreatedAt(now).
			Exec(ctx); err != nil {
			return err
		}
		return insert(ctx, tx, events.SubjectRiskAlert, biz.AlertPayload{
			Level: "warning", Kind: kindReject, Message: message, At: now,
		})
	})
}

func (r *Repo) Report(ctx context.Context, from, to time.Time) (*biz.Report, error) {
	rows, err := r.client.RiskCheck.Query().
		Where(riskcheck.CreatedAtGTE(from), riskcheck.CreatedAtLT(to)).
		Select(riskcheck.FieldApproved, riskcheck.FieldVolumeReq, riskcheck.FieldVolumeFinal,
			riskcheck.FieldRuleID, riskcheck.FieldLatencyUs).
		All(ctx)
	if err != nil {
		return nil, err
	}
	rep := &biz.Report{Rejects: map[string]int{}}
	lat := make([]int64, 0, len(rows))
	for _, row := range rows {
		rep.Checks++
		lat = append(lat, row.LatencyUs)
		switch {
		case !row.Approved:
			rep.Rejected++
			rep.Rejects[row.RuleID]++
		case row.VolumeFinal < row.VolumeReq:
			rep.Approved++
			rep.Adjusted++
		default:
			rep.Approved++
		}
	}
	if len(lat) > 0 {
		sort.Slice(lat, func(i, j int) bool { return lat[i] < lat[j] })
		idx := (len(lat)*99+99)/100 - 1
		rep.P99Latency = time.Duration(lat[idx]) * time.Microsecond
	}
	evs, err := r.client.RiskEvent.Query().
		Where(riskevent.CreatedAtGTE(from), riskevent.CreatedAtLT(to), riskevent.KindNEQ(kindParam)).
		Order(riskevent.ByCreatedAt()).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, e := range evs {
		rep.Events = append(rep.Events, biz.Event{
			Kind: e.Kind, Mode: e.Mode, Source: e.Source, Reason: e.Reason,
			Operator: e.Operator, Active: e.Active, At: e.CreatedAt,
		})
	}
	return rep, nil
}

func (r *Repo) RestoreDay(ctx context.Context, from time.Time) (biz.DayState, error) {
	st := biz.DayState{Breakers: map[risk.AccountType]string{}, Buys: map[risk.AccountType]int{}}
	evs, err := r.client.RiskEvent.Query().
		Where(riskevent.KindEQ(kindBreaker), riskevent.ActiveEQ(true), riskevent.SourceNEQ(""), riskevent.CreatedAtGTE(from)).
		Order(riskevent.ByCreatedAt()).
		All(ctx)
	if err != nil {
		return st, err
	}
	for _, e := range evs {
		st.Breakers[risk.AccountType(e.Source)] = e.Reason
	}
	var counts []struct {
		AccountType string `json:"account_type"`
		Count       int    `json:"count"`
	}
	if err := r.client.RiskCheck.Query().
		Where(riskcheck.CreatedAtGTE(from), riskcheck.ApprovedEQ(true), riskcheck.SideEQ(string(risk.Buy))).
		GroupBy(riskcheck.FieldAccountType).
		Aggregate(ent.Count()).
		Scan(ctx, &counts); err != nil {
		return st, err
	}
	for _, c := range counts {
		st.Buys[risk.AccountType(c.AccountType)] = c.Count
	}
	return st, nil
}

// RefData 读 stock_basic 全表和 day 那天的日线成交额。
func (r *Repo) RefData(ctx context.Context, day time.Time) (map[string]biz.RefInfo, error) {
	rows, err := r.client.StockBasic.Query().All(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]biz.RefInfo, len(rows))
	for _, row := range rows {
		info := biz.RefInfo{ST: row.StFlag, Suspended: row.SuspendFlag}
		if row.ListDate != nil {
			info.ListDate = *row.ListDate
		}
		if row.Industry != nil {
			info.Industry = strings.TrimSpace(*row.Industry)
		}
		out[row.StockCode] = info
	}
	d := day.In(tradecal.Shanghai())
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	bars, err := r.client.MarketData.Query().
		Where(
			marketdata.FreqEQ("1d"),
			marketdata.BarTimeGTE(start),
			marketdata.BarTimeLT(start.AddDate(0, 0, 1)),
		).
		Select(marketdata.FieldSymbol, marketdata.FieldAmount).
		All(ctx)
	if err != nil {
		return nil, err
	}
	for _, b := range bars {
		if b.Amount == nil {
			continue
		}
		info := out[b.Symbol]
		info.PrevAmount = *b.Amount
		out[b.Symbol] = info
	}
	return out, nil
}

func (r *Repo) inTx(ctx context.Context, fn func(tx *ent.Tx) error) error {
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

func insert(ctx context.Context, tx *ent.Tx, subject string, payload any) error {
	env, err := events.New(Source, subject, events.TraceID(ctx), payload)
	if err != nil {
		return err
	}
	return outbox.Insert(ctx, tx, env)
}
