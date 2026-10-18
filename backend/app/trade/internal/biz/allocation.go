package biz

import (
	"context"
	"fmt"
	"regexp"

	"server/pkg/accountstate"
	"server/pkg/allocation"
	"server/pkg/decision"
	"server/pkg/market"
)

type AllocationFacts struct {
	Research       allocation.ResearchRecord
	Account        accountstate.Snapshot
	Quotes         map[string]market.Snapshot
	Assets         map[string]allocation.Asset
	Policy         allocation.Policy
	ConfigRevision int
}
type AllocationStore interface {
	ReadAllocationFacts(context.Context, string) (AllocationFacts, error)
}

var allocationRunID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func (e *Engine) PreviewPortfolio(ctx context.Context, runID, account string) (out allocation.Preview, err error) {
	if account != "SIM" || !allocationRunID.MatchString(runID) {
		return out, fmt.Errorf("%w: 仅接受SIM/default和有效research_run_id", ErrInvalid)
	}
	store, ok := e.store.(AllocationStore)
	if !ok {
		return out, fmt.Errorf("%w: 权威组合预览仓储未接入", ErrInvalid)
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		facts, err := store.ReadAllocationFacts(c, runID)
		if err != nil {
			return err
		}
		portfolio, err := allocation.DecodePortfolio(facts.Research)
		if err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		if err := allocation.ValidateAccountBounds(facts.Account); err != nil {
			return fmt.Errorf("%w: %v", ErrInvalid, err)
		}
		_, _, current, err := e.accountSnapshot(c, BookPaper)
		if err != nil {
			return err
		}
		out = allocation.Calculate(allocation.Input{Portfolio: portfolio, Account: facts.Account, AccountMatchesLedger: decision.AccountHash(current) == decision.AccountHash(facts.Account), Quotes: facts.Quotes, Assets: facts.Assets, Policy: facts.Policy, ConfigRevision: facts.ConfigRevision, Fee: e.set.Fee, Now: e.clock()})
		return nil
	})
	return
}
