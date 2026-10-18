package biz

import (
	"context"
	"fmt"
	"server/pkg/params"
	"server/pkg/risk"
)

type configRepository interface {
	ConfigSnapshot(context.Context) (params.Snapshot, error)
	RecordConfig(context.Context, int, string) error
}

// ReloadConfig atomically installs a fully parsed version. Failed reads cannot
// restore readiness; the next successful poll resumes checks against this version.
func (e *Engine) ReloadConfig(ctx context.Context) error {
	repo, ok := e.repo.(configRepository)
	if !ok {
		return nil
	}
	e.configMu.Lock()
	defer e.configMu.Unlock()
	snap, err := repo.ConfigSnapshot(ctx)
	if err != nil {
		e.ready.Store(false)
		return fmt.Errorf("risk configuration unavailable")
	}
	p, errs := risk.FromConfig(snap.Values)
	if len(errs) > 0 {
		e.ready.Store(false)
		_ = repo.RecordConfig(ctx, snap.Revision, "failed")
		return fmt.Errorf("risk configuration parse failed")
	}
	e.mu.Lock()
	e.params = p
	e.configRevision = snap.Revision
	downgrade := e.mode >= risk.L2 && !p.LiveAdmitted
	if downgrade {
		e.mode = risk.L1
	}
	e.mu.Unlock()
	if downgrade {
		if err = e.repo.SaveMode(ctx, risk.L1, "configuration", "实盘准入关闭，自动降到L1"); err != nil {
			e.ready.Store(false)
			return err
		}
	}
	if err = repo.RecordConfig(ctx, snap.Revision, "applied"); err != nil {
		e.ready.Store(false)
		return fmt.Errorf("risk configuration receipt unavailable")
	}
	e.ready.Store(true)
	return nil
}
