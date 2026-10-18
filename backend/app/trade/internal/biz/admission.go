package biz

import (
	"context"
	"fmt"
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
