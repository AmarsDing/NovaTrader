package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"server/app/trade/internal/biz"
	"server/ent/auditlog"
	"server/pkg/params"
)

// LiveGrant 持久载体（M13-T05）：strategy_config 键 app.trade.live.enabled。
// 写口复用 params 版本化 CAS——键在 = 放行，键无 = 收权，每次写口折叠进
// strategy_version 快照留审计链；该键刻意不进 pkg/params 参数白名单，
// 配置页泛化 PATCH/回滚遇到它一律以「未登记的参数键」拒绝（先收权再回滚）。
// 本文件是该键的唯一数据层读写点；运行期翻转经 biz.GrantLive/RevokeLive，
// 启动恢复经 data.NewSettings 映射（读不到/损坏一律保持关闭 fail-closed）。

func (r *Repo) LoadLiveGrant(ctx context.Context) (biz.LiveGrant, bool, error) {
	snap, err := params.Read(ctx, r.clientFor(ctx))
	if err != nil {
		return biz.LiveGrant{}, false, err
	}
	raw, ok := snap.Values[biz.LiveGrantParamKey]
	if !ok || strings.TrimSpace(raw) == "" {
		return biz.LiveGrant{}, false, nil
	}
	g, err := biz.ParseLiveGrant(raw)
	if err != nil {
		return biz.LiveGrant{}, false, err
	}
	return g, true, nil
}

func (r *Repo) SaveLiveGrant(ctx context.Context, g biz.LiveGrant) error {
	raw, err := biz.MarshalLiveGrant(g)
	if err != nil {
		return err
	}
	c := r.clientFor(ctx)
	base, err := params.CurrentRevision(ctx, c)
	if err != nil {
		return err
	}
	if _, err = params.SaveCAS(ctx, c, base, map[string]string{biz.LiveGrantParamKey: raw}, "trade live grant"); errors.Is(err, params.ErrConflict) {
		return biz.ErrConflict
	}
	return err
}

func (r *Repo) ClearLiveGrant(ctx context.Context) error {
	c := r.clientFor(ctx)
	base, err := params.CurrentRevision(ctx, c)
	if err != nil {
		return err
	}
	if _, err = params.SaveCAS(ctx, c, base, map[string]string{biz.LiveGrantParamKey: ""}, "trade live revoke"); errors.Is(err, params.ErrConflict) {
		return biz.ErrConflict
	}
	return err
}

// RecentLiveGateAudit 核验「服务上层（admin 网关）已先写 audit_log」：
// 只认 action+actor 完全匹配、成功的最近留痕。审计不可读/查不到一律视为未留痕，
// 绝不静默放行（与「先写 audit_log 后生效」同口径）。
func (r *Repo) RecentLiveGateAudit(ctx context.Context, action, actor string, within time.Duration) (bool, error) {
	if within <= 0 {
		within = 2 * time.Minute
	}
	return r.clientFor(ctx).AuditLog.Query().Where(
		auditlog.ActionEQ(action), auditlog.ActorEQ(actor), auditlog.Ok(true),
		auditlog.CreatedAtGTE(time.Now().UTC().Add(-within)),
	).Exist(ctx)
}
