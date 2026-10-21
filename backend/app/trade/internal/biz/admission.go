package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"server/pkg/accountstate"
	"server/pkg/admission"
	"server/pkg/decision"
	"server/pkg/tradecal"
)

type AdmissionFacts struct {
	Sample  admission.Sample
	Account accountstate.Snapshot
}
type AdmissionStore interface {
	AdmissionSlotExists(context.Context, time.Time) (bool, error)
	CollectAdmissionFacts(context.Context, time.Time) (AdmissionFacts, error)
	SaveAdmissionSample(context.Context, admission.Sample) error
	ReadAdmissionSamples(context.Context, time.Time) ([]admission.Sample, error)
}
type AdmissionRiskSource interface {
	ReadAdmissionRisk(context.Context) (admission.Sample, error)
}

// RecordPaperRunSample uses the real server clock independently of Engine.now test clocks.
// No service/API accepts a timestamp, health status or day credit from a client.
func (e *Engine) RecordPaperRunSample(ctx context.Context) error {
	_, err := e.RecordPaperRunSampleObserved(ctx)
	return err
}

// The bool denotes an actual collection/store call, not unique rows or day admission.
func (e *Engine) RecordPaperRunSampleObserved(ctx context.Context) (bool, error) {
	now := time.Now().UTC()
	session, err := tradecal.Default.SessionAt(now)
	if err != nil {
		return false, err
	}
	local := now.In(tradecal.Shanghai())
	closing := session.Phase == tradecal.PostClose && local.Hour() == 15 && local.Minute() < 10
	if !session.Trading && !closing {
		return false, nil
	}
	store, ok := e.store.(AdmissionStore)
	if !ok {
		return false, fmt.Errorf("admission: production evidence repository unavailable")
	}
	known, err := store.AdmissionSlotExists(ctx, now.Truncate(admission.SlotWidth))
	if err != nil || known {
		return false, err
	}
	facts, err := store.CollectAdmissionFacts(ctx, now)
	if err != nil {
		return false, err
	}
	s := facts.Sample
	if client, ok := e.risk.(AdmissionRiskSource); ok {
		risk, err := client.ReadAdmissionRisk(ctx)
		if err != nil {
			s.CollectionErrors = append(s.CollectionErrors, "risk_state_unavailable")
		} else {
			s.RiskReady = risk.RiskReady
			s.RiskMode = risk.RiskMode
			s.KillActive = risk.KillActive
			s.ConfigReady = risk.ConfigReady
			s.RiskConfigRevision = risk.RiskConfigRevision
			s.RiskAccountAsOf = risk.RiskAccountAsOf
		}
	} else {
		s.CollectionErrors = append(s.CollectionErrors, "risk_observation_port_unavailable")
	}
	e.mu.Lock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		_, _, actual, err := e.accountSnapshot(c, BookPaper)
		if err != nil {
			s.CollectionErrors = append(s.CollectionErrors, "ledger_read_unavailable")
		} else {
			matched := facts.Account.Generation > 0 && decision.AccountHash(actual) == decision.AccountHash(facts.Account)
			s.LedgerMatched = &matched
		}
		s.EndedAt = time.Now().UTC()
		s.Seal()
		return store.SaveAdmissionSample(c, s)
	})
	e.mu.Unlock()
	return err == nil, err
}

func (e *Engine) PaperAdmission(ctx context.Context, account string, limit int) (admission.Report, error) {
	book, err := bookOf(account)
	if err != nil || book != BookPaper {
		return admission.Report{}, fmt.Errorf("%w: 运行证据只支持SIM/default", ErrInvalid)
	}
	store, ok := e.store.(AdmissionStore)
	if !ok {
		return admission.Report{}, fmt.Errorf("admission: evidence repository unavailable")
	}
	if limit <= 0 || limit > 120 {
		limit = 40
	}
	now := time.Now().UTC()
	samples, err := store.ReadAdmissionSamples(ctx, now.AddDate(0, 0, -(limit*2+30)))
	if err != nil {
		return admission.Report{}, err
	}
	return admission.Evaluate(tradecal.Default, now, samples, e.set.PaperDaysRequired, limit)
}

// ---------------------------------------------------------------------------
// M13-T05 LIVE 放行总闸
//
// 持久载体：strategy_config 键 app.trade.live.enabled（值 JSON 含
// granted_by/granted_at/admission_evidence），唯一写口是 GrantLive/RevokeLive
// （不管 conf、不管 appcfg、不进 pkg/params 参数白名单——配置页泛化 PATCH
// 以「未登记的参数键」拒绝，配置回滚遇到该键同样被拒）。
// 双闸语义（与 M08-R01 互锁，M09 决策3）：本闸 ∧ 风控侧 risk.live_admitted
// 同时为 true 才真放行实盘委托；place() 另行内联重算运行证据准入。
// 读口两个：运行期 Grant/Revoke 翻转（先留痕后生效）+ 启动时 data.NewSettings
// 持久映射恢复（读不到/损坏一律保持关闭 fail-closed）。
const LiveGrantParamKey = "app.trade.live.enabled"

// LiveGateAuditGrant/Revoke 是 admin 网关 audit_log 的动作名（网关 gate.go
// auditAction 同源登记）：Grant/Revoke 生效前必须查到同名同操作人的最近留痕，
// 任何绕过网关审计的直达调用一律拒绝。
const (
	LiveGateAuditGrant  = "trade.grant_live"
	LiveGateAuditRevoke = "trade.revoke_live"
)

// liveGateAuditWindow 放宽只够覆盖「网关写审计→转发」的时钟漂移与排队，
// 事后补审计不能伪造成放行前的留痕。
const liveGateAuditWindow = 2 * time.Minute

// LiveGrantEvidence 是放行当下准入评估的留痕引用（重启恢复门状态的依据）；
// 证据本身只读留痕，实时准入判定仍以 place() 内的 PaperAdmission 重算为准。
type LiveGrantEvidence struct {
	ConsecutiveDays int    `json:"consecutive_days"`
	RequiredDays    int    `json:"required_days"`
	LatestClosedDay string `json:"latest_closed_trading_day"`
	Policy          string `json:"policy"`
}

// LiveGrant 是 LIVE 总闸的留痕记录：键在 = 放行，键无 = 收权。
// 历史折叠进 params 版本快照（审计链），当前值始终代表最新生效门状态。
type LiveGrant struct {
	GrantedBy string            `json:"granted_by"`
	GrantedAt time.Time         `json:"granted_at"`
	Evidence  LiveGrantEvidence `json:"admission_evidence"`
}

func MarshalLiveGrant(g LiveGrant) (string, error) {
	p, err := json.Marshal(g)
	return string(p), err
}

// ParseLiveGrant 严格解码持久值：任何损坏/未知字段都按错误返回，由调用方
// fail-closed 处理——「没取到/解不开≠放行」。
func ParseLiveGrant(raw string) (LiveGrant, error) {
	var g LiveGrant
	d := json.NewDecoder(strings.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&g); err != nil {
		return LiveGrant{}, err
	}
	var trailing any
	if err := d.Decode(&trailing); err != io.EOF {
		return LiveGrant{}, errors.New("live grant must be a single JSON object")
	}
	if strings.TrimSpace(g.GrantedBy) == "" || g.GrantedAt.IsZero() {
		return LiveGrant{}, errors.New("live grant: granted_by/granted_at missing")
	}
	return g, nil
}

// LiveGrantStore 是 grant 留痕与审计核验的数据端口（data 层以 params CAS +
// audit_log 表实现；配置版本快照天然保留每次写口折叠历史）。
type LiveGrantStore interface {
	LoadLiveGrant(context.Context) (LiveGrant, bool, error)
	SaveLiveGrant(context.Context, LiveGrant) error
	ClearLiveGrant(context.Context) error
	RecentLiveGateAudit(context.Context, string, string, time.Duration) (bool, error)
}

// LiveGateFromGrant 是「持久留痕 → 门状态」的唯一判定：取到且合法解码即开，
// 其余全关。生产启动路径（data.NewSettings 映射）与任何重灌路径必须共用本判定。
func LiveGateFromGrant(g LiveGrant, known bool, err error) bool {
	return err == nil && known && strings.TrimSpace(g.GrantedBy) != ""
}

// SetLiveEnabled/LiveEnabled 是 LIVE 总闸的运行期读写口（e.mu 串行，与 place()
// 内闸读取同锁）；只能由 GrantLive/RevokeLive 翻转，无任何 conf/界面旁路。
func (e *Engine) SetLiveEnabled(v bool) { e.mu.Lock(); e.set.LiveEnabled = v; e.mu.Unlock() }

func (e *Engine) LiveEnabled() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set.LiveEnabled
}

// SetBrokerConnected/BrokerConnected 是实盘通道闸的运行期读写口：只由 runner 的
// broker-gw 健康探测置位/复位，探测失败立即回落 false（fail-closed，T05）。
func (e *Engine) SetBrokerConnected(v bool) { e.mu.Lock(); e.set.BrokerConnected = v; e.mu.Unlock() }

func (e *Engine) BrokerConnected() bool {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.set.BrokerConnected
}

// GrantLive 打开 LIVE 总闸（M13-T05 唯一开闸路径）：
//  1. 批准人非空；
//  2. 服务上层（admin 网关）已就本动作写过 audit_log（先审计后生效，绕过即拒）；
//  3. 运行证据准入就绪（RunEvidenceReady ∧ 连续≥20 真实交易日，M09 决策3 门槛）；
//  4. 先持久留痕（strategy_config 键）后翻闸门——留痕写不进去绝不生效。
func (e *Engine) GrantLive(ctx context.Context, operator string) (admission.Report, error) {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return admission.Report{}, fmt.Errorf("%w: 放行必须记录批准人", ErrInvalid)
	}
	store, ok := e.store.(LiveGrantStore)
	if !ok {
		return admission.Report{}, fmt.Errorf("live gate repository unavailable")
	}
	audited, err := store.RecentLiveGateAudit(ctx, LiveGateAuditGrant, operator, liveGateAuditWindow)
	if err != nil {
		return admission.Report{}, err
	}
	if !audited {
		return admission.Report{}, fmt.Errorf("%w: 准入放行必须先经 admin 网关审计留痕", ErrInvalid)
	}
	report, err := e.PaperAdmission(ctx, "SIM", 40)
	if err != nil {
		return admission.Report{}, err
	}
	if !report.RunEvidenceReady || report.ConsecutiveDays < admission.MinRealTradingDays || report.RequiredDays < admission.MinRealTradingDays {
		return admission.Report{}, fmt.Errorf("%w: 运行证据准入未通过（连续合格交易日不足%d）", ErrInvalid, admission.MinRealTradingDays)
	}
	grant := LiveGrant{
		GrantedBy: operator, GrantedAt: e.clock().UTC(),
		Evidence: LiveGrantEvidence{
			ConsecutiveDays: report.ConsecutiveDays, RequiredDays: report.RequiredDays,
			LatestClosedDay: report.LatestClosedTradingDay, Policy: report.Policy,
		},
	}
	if err := store.SaveLiveGrant(ctx, grant); err != nil {
		return admission.Report{}, err
	}
	e.SetLiveEnabled(true)
	report.LiveEnabled = true
	return report, nil
}

// RevokeLive 收权：同样要求已审计留痕且记录操作人；先清除持久留痕后关闸门
// （清除失败门保持现状并如实报错——不假装已关，也不在未留痕状态下静默放行）。
func (e *Engine) RevokeLive(ctx context.Context, operator string) error {
	operator = strings.TrimSpace(operator)
	if operator == "" {
		return fmt.Errorf("%w: 收权必须记录操作人", ErrInvalid)
	}
	store, ok := e.store.(LiveGrantStore)
	if !ok {
		return fmt.Errorf("live gate repository unavailable")
	}
	audited, err := store.RecentLiveGateAudit(ctx, LiveGateAuditRevoke, operator, liveGateAuditWindow)
	if err != nil {
		return err
	}
	if !audited {
		return fmt.Errorf("%w: 准入收权必须先经 admin 网关审计留痕", ErrInvalid)
	}
	if err := store.ClearLiveGrant(ctx); err != nil {
		return err
	}
	e.SetLiveEnabled(false)
	return nil
}
