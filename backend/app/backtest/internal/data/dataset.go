package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"server/ent"
	"server/ent/datasetmanifest"
	"server/ent/datasetrecord"
	"server/ent/marketdata"
	"server/pkg/backtest"
	"server/pkg/dataset"
	"server/pkg/tradecal"
)

const maxManifestRecords = 250000

// FreezeDataset captures inputs before queueing. No later mutable table reads occur during execution.
func (s *Source) FreezeDataset(ctx context.Context, cfg backtest.Config, cutoff time.Time) (*dataset.Manifest, error) {
	start, end, err := cfg.Normalize()
	if err != nil {
		return nil, err
	}
	from := start.AddDate(0, 0, -(cfg.HistoryBars*3/2 + 10))
	if err = s.backfillLegacyBars(ctx, from, end); err != nil {
		return nil, err
	}
	if err = s.backfillLegacyMetadata(ctx); err != nil {
		return nil, err
	}
	days, err := s.TradingDays(ctx, start, end)
	if err != nil {
		return nil, err
	}
	rows, err := s.client.DatasetRecord.Query().Where(datasetrecord.EventTimeLTE(end.AddDate(0, 0, 1)), datasetrecord.Or(datasetrecord.DomainNEQ("bar"), datasetrecord.EventTimeGTE(from))).Limit(maxManifestRecords + 1).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > maxManifestRecords {
		return nil, fmt.Errorf("dataset: 超过 %d 条冻结记录上限，请缩小区间或离线分片，未截断输入", maxManifestRecords)
	}
	records := make([]dataset.Record, 0, len(rows))
	for _, r := range rows {
		if r.Domain == "bar" && r.EventTime.Before(from) {
			continue
		}
		records = append(records, recordOf(r))
	}
	m, err := dataset.Freeze(dataset.Request{From: from, To: end.AddDate(0, 0, 1).Add(-time.Nanosecond), Cutoff: cutoff, Freq: cfg.Freq}, days, records)
	if err != nil {
		return nil, err
	}
	_, excluded, err := buildReplaySource(m)
	if err != nil {
		return nil, err
	}
	if err = m.WithReplayExclusions(excluded); err != nil {
		return nil, err
	}
	p, err := json.Marshal(m)
	if err != nil {
		return nil, err
	}
	err = s.client.DatasetManifest.Create().SetManifestID(m.ManifestID).SetHash(m.Hash).SetSchemaVersion(m.SchemaVersion).SetCutoff(m.Cutoff).SetContent(p).
		OnConflictColumns(datasetmanifest.FieldManifestID).DoNothing().Exec(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return m, err
}

func recordOf(r *ent.DatasetRecord) dataset.Record {
	return dataset.Record{Domain: r.Domain, BusinessKey: r.BusinessKey, Source: r.Source, EventTime: r.EventTime, AvailableAt: r.AvailableAt, Availability: r.Availability, Evidence: r.AvailabilityEvidence, ObservedAt: r.ObservedAt, RawHash: r.RawHash, RevisionHash: r.RevisionHash, Payload: r.Payload}
}

func (s *Source) GetDataset(ctx context.Context, id string) (*dataset.Manifest, error) {
	r, err := s.client.DatasetManifest.Query().Where(datasetmanifest.ManifestIDEQ(id)).Only(ctx)
	if err != nil {
		return nil, err
	}
	var m dataset.Manifest
	if err = json.Unmarshal(r.Content, &m); err != nil {
		return nil, err
	}
	if err = m.Verify(); err != nil {
		return nil, err
	}
	if r.Hash != m.Hash || r.ManifestID != m.ManifestID {
		return nil, fmt.Errorf("dataset: stored manifest identity mismatch")
	}
	return &m, nil
}

// Legacy rows have no vintage evidence. Backfill is reentrant and explicitly unknown, never now-as-publication.
func (s *Source) backfillLegacyBars(ctx context.Context, from, to time.Time) error {
	rows, err := s.client.MarketData.Query().Where(marketdata.BarTimeGTE(from), marketdata.BarTimeLT(to.AddDate(0, 0, 1))).Limit(maxManifestRecords + 1).All(ctx)
	if err != nil {
		return err
	}
	if len(rows) > maxManifestRecords {
		return fmt.Errorf("dataset: legacy backfill exceeds safe bound; request smaller range")
	}
	for _, b := range rows {
		if b.Open == nil || b.High == nil || b.Low == nil || b.Close == nil || b.Volume == nil || b.Amount == nil {
			continue
		}
		key := b.Symbol + "/" + b.Freq + "/" + b.BarTime.UTC().Format(time.RFC3339Nano)
		// Live collection already captured its own source record. Do not add an unknown twin every freeze.
		exists, err := s.client.DatasetRecord.Query().Where(datasetrecord.DomainEQ("bar"), datasetrecord.BusinessKeyEQ(key), datasetrecord.SourceEQ(b.Source)).Exist(ctx)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		source := b.Source
		if source == "" {
			source = "legacy_unknown"
		}
		r, err := dataset.NewRecord("bar", key, source, b.BarTime, time.Now(), dataset.Availability{}, dataset.Bar{Symbol: b.Symbol, Time: b.BarTime, Freq: b.Freq, Open: *b.Open, High: *b.High, Low: *b.Low, Close: *b.Close, PreClose: b.PreClose, Volume: *b.Volume, Amount: *b.Amount})
		if err != nil {
			return err
		}
		if err = s.client.DatasetRecord.Create().SetDomain(r.Domain).SetBusinessKey(r.BusinessKey).SetSource(r.Source).SetEventTime(r.EventTime).SetAvailability(r.Availability).SetAvailabilityEvidence(r.Evidence).SetObservedAt(r.ObservedAt).SetRawHash(r.RawHash).SetRevisionHash(r.RevisionHash).SetPayload(r.Payload).OnConflictColumns(datasetrecord.FieldRevisionHash).DoNothing().Exec(ctx); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
	}
	return nil
}

func (s *Source) OpenDataset(ctx context.Context, id string) (backtest.Source, *dataset.Manifest, error) {
	m, err := s.GetDataset(ctx, id)
	if err != nil {
		return nil, nil, err
	}
	src, err := sourceFromManifest(m)
	return src, m, err
}

// sourceFromManifest is deliberately conservative: a raw bar's first-complete vintage and
// pre-open security/factor evidence are required. Late revisions cannot leak into earlier decisions.
func sourceFromManifest(m *dataset.Manifest) (*backtest.Memory, error) {
	out, _, err := buildReplaySource(m)
	return out, err
}

func buildReplaySource(m *dataset.Manifest) (*backtest.Memory, []dataset.Exclusion, error) {
	if err := m.Verify(); err != nil {
		return nil, nil, err
	}
	excluded := []dataset.Exclusion{}
	exclude := func(r dataset.Record, reason string) {
		excluded = append(excluded, dataset.Exclusion{Domain: r.Domain, BusinessKey: r.BusinessKey, RevisionHash: r.RevisionHash, Reason: reason})
	}
	out := &backtest.Memory{Days: m.Calendar, Daily: map[string][]backtest.DayBar{}, Minute: map[string]map[string][]backtest.MinBar{}, NameMap: map[string][]backtest.NameSpan{}, Lists: map[string]time.Time{}}
	bars := map[string]dataset.Record{}
	for _, r := range m.Records {
		if r.Domain != "bar" {
			continue
		}
		var b dataset.Bar
		if err := json.Unmarshal(r.Payload, &b); err != nil {
			return nil, nil, err
		}
		completed := barCompleted(b)
		if r.AvailableAt.After(completed) {
			exclude(r, "revision_not_available_at_bar_completion")
			continue
		}
		old, ok := bars[r.BusinessKey]
		if !ok || old.AvailableAt.Before(*r.AvailableAt) || (old.AvailableAt.Equal(*r.AvailableAt) && old.RevisionHash < r.RevisionHash) {
			bars[r.BusinessKey] = r
		}
	}
	byDay := map[string]map[string]dataset.Record{}
	nameSeen := map[string]bool{}
	keys := make([]string, 0, len(bars))
	for k := range bars {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		r := bars[k]
		var b dataset.Bar
		if err := json.Unmarshal(r.Payload, &b); err != nil {
			return nil, nil, err
		}
		if b.Open <= 0 || b.Close <= 0 || b.Low <= 0 || b.High < b.Low || b.Volume < 0 || b.Amount < 0 {
			exclude(r, "invalid_bar")
			continue
		}
		d := backtest.Day(b.Time)
		preOpen := d.Add(9*time.Hour + 25*time.Minute)
		dayKey := d.Format("2006-01-02")
		if byDay[dayKey] == nil {
			byDay[dayKey] = map[string]dataset.Record{}
			for _, p := range m.At(preOpen) {
				byDay[dayKey][p.Domain+"/"+p.BusinessKey] = p
			}
		}
		var security *dataset.Security
		var factor *dataset.AdjFactor
		if p, ok := byDay[dayKey]["security/"+b.Symbol]; ok && !p.EventTime.After(preOpen) {
			var v dataset.Security
			if err := json.Unmarshal(p.Payload, &v); err != nil {
				return nil, nil, err
			}
			security = &v
		}
		if p, ok := byDay[dayKey]["adj_factor/"+b.Symbol+"/"+dayKey]; ok {
			var v dataset.AdjFactor
			if err := json.Unmarshal(p.Payload, &v); err != nil {
				return nil, nil, err
			}
			factor = &v
		}
		// Indices are benchmark-only and need no equity security/factor contract.
		index := b.Symbol == "000300.SH" || b.Symbol == "000001.SH" || b.Symbol == "399001.SZ"
		if !index {
			reason := ""
			switch {
			case security == nil:
				reason = "preopen_security_unknown"
			case security.ListDate == nil:
				reason = "listing_date_unknown"
			case security.Suspended:
				reason = "suspended"
			case security.ListDate.After(d):
				reason = "not_listed"
			case security.DelistDate != nil && !d.Before(*security.DelistDate):
				reason = "delisted"
			case factor == nil || factor.Factor <= 0:
				reason = "preopen_adjustment_factor_unknown"
			}
			if reason != "" {
				exclude(r, reason)
				continue
			}
		}
		adj := 1.0
		if factor != nil {
			adj = factor.Factor
		}
		if b.Freq == backtest.Freq1d {
			out.Daily[b.Symbol] = append(out.Daily[b.Symbol], backtest.DayBar{Day: d, Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume, Amount: b.Amount, Adj: adj, PreClose: val(b.PreClose)})
		}
		if b.Freq == backtest.Freq1m {
			key := d.Format("2006-01-02")
			if out.Minute[key] == nil {
				out.Minute[key] = map[string][]backtest.MinBar{}
			}
			out.Minute[key][b.Symbol] = append(out.Minute[key][b.Symbol], backtest.MinBar{Time: b.Time.In(tradecal.Shanghai()), Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume, Amount: b.Amount})
		}
		if security != nil && !nameSeen[b.Symbol+dayKey] {
			name := security.Name
			if security.ST {
				name = "ST " + name
			}
			out.NameMap[b.Symbol] = append(out.NameMap[b.Symbol], backtest.NameSpan{Name: name, Start: d, End: d})
			out.Lists[b.Symbol] = backtest.Day(*security.ListDate)
			nameSeen[b.Symbol+dayKey] = true
		}
	}
	for _, b := range out.Daily {
		sort.Slice(b, func(i, j int) bool { return b[i].Day.Before(b[j].Day) })
	}
	for _, syms := range out.Minute {
		for _, b := range syms {
			sort.Slice(b, func(i, j int) bool { return b[i].Time.Before(b[j].Time) })
		}
	}
	for _, sp := range out.NameMap {
		sort.Slice(sp, func(i, j int) bool { return sp[i].Start.Before(sp[j].Start) })
	}
	return out, excluded, nil
}

func barCompleted(b dataset.Bar) time.Time {
	if b.Freq == backtest.Freq1d {
		return backtest.Day(b.Time).Add(15 * time.Hour)
	}
	return b.Time.Add(time.Minute)
}
