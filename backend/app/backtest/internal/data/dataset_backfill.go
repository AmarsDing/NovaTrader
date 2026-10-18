package data

import (
	"context"
	"fmt"
	"time"

	"server/ent"
	"server/ent/datasetrecord"
	"server/pkg/dataset"
)

// backfillLegacyMetadata only records what is actually present. It never supplies a release time
// from report periods, current names, listing dates, membership in_date or collection timestamps.
func (s *Source) backfillLegacyMetadata(ctx context.Context) error {
	var records []dataset.Record
	add := func(domain, key string, event time.Time, p any) error {
		r, e := dataset.NewRecord(domain, key, "legacy_unknown", event, time.Now(), dataset.Availability{}, p)
		if e == nil {
			records = append(records, r)
		}
		return e
	}
	securities, e := s.client.StockBasic.Query().Limit(maxManifestRecords + 1).All(ctx)
	if e != nil {
		return e
	}
	if len(securities) > maxManifestRecords {
		return fmt.Errorf("dataset: security backfill exceeds bound")
	}
	for _, r := range securities {
		if e = add("security", r.StockCode, r.UpdateTime, dataset.Security{Symbol: r.StockCode, Name: r.StockName, ListDate: r.ListDate, DelistDate: r.DelistDate, ST: r.StFlag, Suspended: r.SuspendFlag}); e != nil {
			return e
		}
	}
	names, e := s.client.StockNameHistory.Query().Limit(maxManifestRecords + 1).All(ctx)
	if e != nil {
		return e
	}
	if len(names) > maxManifestRecords {
		return fmt.Errorf("dataset: name history backfill exceeds bound")
	}
	for _, r := range names {
		if e = add("name", r.Symbol+"/"+r.StartDate.Format("2006-01-02"), r.StartDate, dataset.Name{Symbol: r.Symbol, Name: r.Name, Start: r.StartDate, End: r.EndDate}); e != nil {
			return e
		}
	}
	finance, e := s.client.FinanceReport.Query().Limit(maxManifestRecords + 1).All(ctx)
	if e != nil {
		return e
	}
	if len(finance) > maxManifestRecords {
		return fmt.Errorf("dataset: finance backfill exceeds bound")
	}
	for _, r := range finance {
		if e = add("finance", r.Symbol+"/"+r.EndDate.Format("2006-01-02")+"/"+r.Kind, r.EndDate, map[string]any{"symbol": r.Symbol, "end_date": r.EndDate, "kind": r.Kind, "ann_date": r.AnnDate, "summary": r.Summary, "data": r.Data, "net_profit": r.NetProfit}); e != nil {
			return e
		}
	}
	macro, e := s.client.MacroSeries.Query().Limit(maxManifestRecords + 1).All(ctx)
	if e != nil {
		return e
	}
	if len(macro) > maxManifestRecords {
		return fmt.Errorf("dataset: macro backfill exceeds bound")
	}
	for _, r := range macro {
		if e = add("macro", r.Code+"/"+r.ObsDate.Format("2006-01-02"), r.ObsDate, map[string]any{"code": r.Code, "date": r.ObsDate, "value": r.Value, "unit": r.Unit}); e != nil {
			return e
		}
	}
	members, e := s.client.SectorMember.Query().Limit(maxManifestRecords + 1).All(ctx)
	if e != nil {
		return e
	}
	if len(members) > maxManifestRecords {
		return fmt.Errorf("dataset: membership backfill exceeds bound")
	}
	for _, r := range members {
		if e = add("sector_member", r.SectorCode+"/"+r.Symbol+"/"+r.InDate.Format("2006-01-02"), r.InDate, map[string]any{"sector_code": r.SectorCode, "symbol": r.Symbol, "in_date": r.InDate, "out_date": r.OutDate}); e != nil {
			return e
		}
	}
	for i := 0; i < len(records); i += 1000 {
		end := min(i+1000, len(records))
		bulk := make([]*ent.DatasetRecordCreate, 0, end-i)
		for _, r := range records[i:end] {
			bulk = append(bulk, s.client.DatasetRecord.Create().SetDomain(r.Domain).SetBusinessKey(r.BusinessKey).SetSource(r.Source).SetEventTime(r.EventTime).SetAvailability("unknown").SetObservedAt(r.ObservedAt).SetRawHash(r.RawHash).SetRevisionHash(r.RevisionHash).SetPayload(r.Payload))
		}
		if e = s.client.DatasetRecord.CreateBulk(bulk...).OnConflictColumns(datasetrecord.FieldRevisionHash).DoNothing().Exec(ctx); e != nil {
			return e
		}
	}
	return nil
}
