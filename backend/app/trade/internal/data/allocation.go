package data

import (
	"context"
	"fmt"
	"sort"

	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/confighead"
	"server/ent/researchjob"
	"server/ent/stockbasic"
	"server/ent/strategyversion"
	"server/pkg/accountstate"
	"server/pkg/allocation"
	"server/pkg/market"
	"server/pkg/risk"
)

func (r *Repo) ReadAllocationFacts(ctx context.Context, id string) (out biz.AllocationFacts, err error) {
	if !r.InLedger(ctx) {
		return out, biz.ErrInvalid
	}
	c := r.clientFor(ctx)
	row, err := c.ResearchJob.Query().Where(researchjob.RunID(id)).ForShare().Only(ctx)
	if ent.IsNotFound(err) {
		return out, biz.ErrNotFound
	}
	if err != nil {
		return out, err
	}
	out.Research = allocation.ResearchRecord{RunID: row.RunID, Kind: row.Kind, Status: row.Status, ManifestID: row.ManifestID, ManifestHash: row.ManifestHash, InputHash: row.InputHash, ResultHash: row.ArtifactHash, ResultJSON: row.ResultJSON, InputJSON: row.InputJSON, OptionsJSON: row.OptionsJSON, CancelRequested: row.CancelRequested}
	p, err := allocation.DecodePortfolio(out.Research)
	if err != nil {
		return out, fmt.Errorf("%w: %v", biz.ErrInvalid, err)
	}
	account, err := c.AccountSnapshot.Query().Where(accountsnapshot.Book(biz.BookPaper)).Order(ent.Desc(accountsnapshot.FieldID)).First(ctx)
	if ent.IsNotFound(err) {
		return out, fmt.Errorf("%w: 完整SIM账户快照缺失", biz.ErrInvalid)
	}
	if err != nil {
		return out, err
	}
	out.Account, err = accountstate.Decode(account.State)
	if err != nil {
		return out, fmt.Errorf("%w: 完整账户快照不可核验", biz.ErrInvalid)
	}
	if _, found, err := r.LoadCash(ctx, biz.BookPaper); err != nil {
		return out, err
	} else if !found {
		return out, fmt.Errorf("%w: SIM资金账本缺失，不能用初始资金补齐预览", biz.ErrInvalid)
	}
	// Read-only: unlike params.Read, no ConfigHead or default value is created.
	head, err := c.ConfigHead.Query().Where(confighead.ID(1)).ForShare().Only(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return out, err
	}
	if err == nil && head.Revision > 0 {
		version, err := c.StrategyVersion.Query().Where(strategyversion.ID(head.Revision)).Only(ctx)
		if err != nil {
			return out, err
		}
		values := map[string]string{}
		valid := true
		for k, v := range version.ConfigSnapshot {
			if s, ok := v.(string); ok {
				values[k] = s
			} else {
				valid = false
			}
		}
		params, errs := risk.FromConfig(values)
		out.ConfigRevision = head.Revision
		out.Policy = allocation.Policy{Known: valid && len(errs) == 0, MaxSingle: params.MaxSinglePct, MaxTotal: params.MaxTotalPct, MaxSector: params.MaxSectorPct, AllowSTBuy: params.AllowSTBuy}
	} else if err == nil && head.Revision == 0 {
		// A revision-zero installation has no immutable active configuration.
		out.Policy.Known = false
	}
	set := map[string]bool{}
	for s := range p.Weights {
		set[s] = true
	}
	for _, h := range out.Account.Holdings {
		set[h.Symbol] = true
	}
	if len(set) > 200 {
		return out, fmt.Errorf("%w: 预览证券超过200只，拒绝截断集合", biz.ErrInvalid)
	}
	symbols := make([]string, 0, len(set))
	for s := range set {
		symbols = append(symbols, s)
	}
	sort.Strings(symbols)
	assets, err := c.StockBasic.Query().Where(stockbasic.StockCodeIn(symbols...)).All(ctx)
	if err != nil {
		return out, err
	}
	out.Assets = map[string]allocation.Asset{}
	for _, a := range assets {
		sector := ""
		if a.Industry != nil {
			sector = *a.Industry
		}
		out.Assets[a.StockCode] = allocation.Asset{Known: true, Suspended: a.SuspendFlag, ST: a.StFlag, Sector: sector}
	}
	out.Quotes = map[string]market.Snapshot{}
	if r.quote != nil {
		for _, s := range symbols {
			q, err := r.quote(ctx, s)
			if err == nil {
				out.Quotes[s] = q
			}
		}
	} else if r.rdb != nil && len(symbols) > 0 {
		// One bounded server-side read, no per-symbol network waterfall.
		values, err := r.rdb.HMGet(ctx, market.SnapshotKey, symbols...).Result()
		if err == nil {
			for i, value := range values {
				raw, ok := value.(string)
				if !ok {
					continue
				}
				q, err := market.ParseSnapshot([]byte(raw))
				if err == nil {
					out.Quotes[symbols[i]] = q
				}
			}
		}
	}
	return out, nil
}
