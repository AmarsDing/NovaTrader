package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"server/app/datahub/internal/biz"
	"server/ent"
	"server/ent/macroseries"
	"server/ent/marketdata"
	box "server/ent/outbox"
	"server/pkg/dataset"
	"server/pkg/events"

	entsql "entgo.io/ent/dialect/sql"
)

func supplementIdentity(domain, source string, records []dataset.Record) string {
	hashes := make([]string, 0, len(records))
	for _, record := range records {
		hashes = append(hashes, record.RevisionHash)
	}
	sort.Strings(hashes)
	return "sb:" + sourceHash([]byte(domain + "\n" + source + "\n" + strings.Join(hashes, "\n")))[:60]
}

func supplementRecord(domain, key, source string, event time.Time, a dataset.Availability, payload any) (dataset.Record, error) {
	if a.ObservedAt == nil || a.Provenance == nil || a.AvailableAt != nil || !validSourceHash(a.Provenance.ProviderRawSHA256) || !validSourceHash(a.Provenance.RecordsSHA256) {
		return dataset.Record{}, fmt.Errorf("supplement provenance or unknown availability contract missing")
	}
	if age := time.Since(*a.ObservedAt); age < -5*time.Second || age > time.Minute {
		return dataset.Record{}, fmt.Errorf("supplement observed clock outside freshness window")
	}
	return dataset.NewRecord(domain, key, source, event, *a.ObservedAt, a, payload)
}

// Reserving the existing unique outbox identity first serializes identical batches
// across processes. Revision/current writes and this reservation commit together.
func (r *Repo) supplementTransaction(ctx context.Context, source string, rd biz.Ready, records []dataset.Record, write func(*ent.Tx) error) (int, error) {
	if len(records) == 0 || len(records) > 10000 {
		return 0, fmt.Errorf("supplement record count outside bound")
	}
	id := supplementIdentity(rd.Domain, source, records)
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return 0, err
	}
	defer tx.Rollback()
	payload := map[string]any{"domain": rd.Domain, "source": source, "trade_date": rd.TradeDate, "count": len(records), "count_basis": "accepted_provider_records_not_current_view_overwrites", "coverage_known": false, "training_ready": false, "current_view_policy": "fill_missing_or_same_supplement_only"}
	env, err := events.NewWithContext(ctx, "datahub", events.SubjectMDReady(rd.Domain), payload)
	if err != nil {
		return 0, err
	}
	env.EventID = id
	raw, err := env.Marshal()
	if err != nil {
		return 0, err
	}
	var metadata map[string]any
	if json.Unmarshal(raw, &metadata) != nil {
		return 0, fmt.Errorf("invalid supplemental event metadata")
	}
	delete(metadata, "payload")
	delete(metadata, "published_at")
	err = tx.Outbox.Create().SetEventID(id).SetTraceID(env.TraceID).SetSubject(env.Subject).SetSource(env.Source).SetCreatedAt(env.Time).SetPayload(env.Payload).SetMetadata(metadata).OnConflictColumns(box.FieldEventID).DoNothing().Exec(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, nil
	}
	if err != nil {
		return 0, err
	}
	for _, rec := range records {
		// captureDataset preserves the worker observation clock independently of the stable content identity.
		a := dataset.Availability{AvailableAt: rec.AvailableAt, Evidence: rec.Evidence, ObservedAt: &rec.ObservedAt}
		if err := captureDataset(ctx, tx, rec.Domain, rec.BusinessKey, rec.Source, rec.EventTime, a, rec.Payload); err != nil {
			return 0, err
		}
	}
	if err := write(tx); err != nil {
		return 0, err
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(records), nil
}

func (r *Repo) upsertSupplementBars(ctx context.Context, bars []biz.Bar, source string, rd biz.Ready) (int, error) {
	if rd.Domain != biz.DomainDailyBar || rd.Source != source {
		return 0, fmt.Errorf("supplement is daily_bar only")
	}
	records := make([]dataset.Record, 0, len(bars))
	for _, b := range bars {
		if b.Freq != "1d" || b.Provenance == nil || b.Provenance.Provider != "tdx.com.cn" || b.Provenance.ProviderRawKind != "provider_http_zip_bytes" {
			return 0, fmt.Errorf("supplement bar provenance/domain mismatch")
		}
		key := b.Symbol + "/1d/" + b.Time.UTC().Format(time.RFC3339Nano)
		rec, err := supplementRecord("bar", key, source, b.Time, b.Availability, dataset.Bar{Provenance: b.Provenance, Symbol: b.Symbol, Time: b.Time, Freq: b.Freq, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, PreClose: b.PreClose, Volume: b.Volume, Amount: b.Amount})
		if err != nil {
			return 0, err
		}
		records = append(records, rec)
	}
	return r.supplementTransaction(ctx, source, rd, records, func(tx *ent.Tx) error {
		return chunks(bars, func(part []biz.Bar) error {
			bulk := make([]*ent.MarketDataCreate, 0, len(part))
			for _, b := range part {
				bulk = append(bulk, tx.MarketData.Create().SetSymbol(b.Symbol).SetBarTime(b.Time).SetFreq(b.Freq).SetOpen(b.Open).SetHigh(b.High).SetLow(b.Low).SetClose(b.Close).SetVolume(b.Volume).SetAmount(b.Amount).SetNillablePreClose(b.PreClose).SetSource(source))
			}
			return tx.MarketData.CreateBulk(bulk...).OnConflict(entsql.ConflictColumns(marketdata.FieldSymbol, marketdata.FieldBarTime, marketdata.FieldFreq), entsql.UpdateWhere(entsql.EQ(marketdata.FieldSource, source))).Update(func(u *ent.MarketDataUpsert) {
				u.UpdateOpen()
				u.UpdateHigh()
				u.UpdateLow()
				u.UpdateClose()
				u.UpdateVolume()
				u.UpdateAmount()
				u.UpdatePreClose()
			}).Exec(ctx)
		})
	})
}

func (r *Repo) upsertSupplementMacro(ctx context.Context, items []biz.MacroPoint, source string, rd biz.Ready) (int, error) {
	if rd.Domain != biz.DomainMacro || rd.Source != source {
		return 0, fmt.Errorf("supplement is macro only")
	}
	records := make([]dataset.Record, 0, len(items))
	for _, m := range items {
		if m.Provenance == nil || m.Provenance.Provider != "fred.stlouisfed.org" || m.Provenance.ProviderRawKind != "sdk_normalized_result_json" {
			return 0, fmt.Errorf("macro provenance mismatch")
		}
		rec, err := supplementRecord("macro", m.Code+"/"+day(m.Date).Format("2006-01-02"), source, day(m.Date), m.Availability, m)
		if err != nil {
			return 0, err
		}
		records = append(records, rec)
	}
	return r.supplementTransaction(ctx, source, rd, records, func(tx *ent.Tx) error {
		return chunks(items, func(part []biz.MacroPoint) error {
			bulk := make([]*ent.MacroSeriesCreate, 0, len(part))
			for _, m := range part {
				unit := m.Unit
				if len(unit) > 16 {
					unit = ""
				}
				bulk = append(bulk, tx.MacroSeries.Create().SetCode(m.Code).SetName(truncate(m.Name, 64)).SetObsDate(day(m.Date)).SetValue(m.Value).SetUnit(unit).SetSource(source).SetAsOf(*m.ObservedAt))
			}
			return tx.MacroSeries.CreateBulk(bulk...).OnConflict(entsql.ConflictColumns(macroseries.FieldCode, macroseries.FieldObsDate), entsql.UpdateWhere(entsql.EQ(macroseries.FieldSource, source))).Update(func(u *ent.MacroSeriesUpsert) { u.UpdateName(); u.UpdateValue(); u.UpdateUnit(); u.UpdateAsOf() }).Exec(ctx)
		})
	})
}
