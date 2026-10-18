package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"server/app/trade/internal/biz"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/executionintent"
	"server/ent/outbox"
	"server/ent/paperrunsample"
	"server/ent/sourcehealth"
	"server/ent/tradeorder"
	"server/pkg/accountstate"
	"server/pkg/admission"
	"server/pkg/ashare"
	"server/pkg/market"
	"server/pkg/params"
	"server/pkg/tradecal"
)

var admissionInstanceID = uuid.NewString()

func (r *Repo) AdmissionSlotExists(ctx context.Context, slot time.Time) (bool, error) {
	return r.clientFor(ctx).PaperRunSample.Query().Where(paperrunsample.BookEQ("paper"), paperrunsample.AccountIDEQ("default"), paperrunsample.SlotStartEQ(slot), paperrunsample.InstanceIDEQ(admissionInstanceID)).Exist(ctx)
}

func (r *Repo) CollectAdmissionFacts(ctx context.Context, now time.Time) (biz.AdmissionFacts, error) {
	s := admission.Sample{Version: admission.Version, Book: "paper", AccountID: "default", InstanceID: admissionInstanceID, ClockSource: "server_wall_clock", ObservedAt: now, MarketSources: map[string]int{}, CollectionErrors: []string{}}
	f := biz.AdmissionFacts{Sample: s}
	c := r.clientFor(ctx)
	row, err := c.AccountSnapshot.Query().Where(accountsnapshot.BookEQ("paper")).Order(ent.Desc(accountsnapshot.FieldID)).First(ctx)
	if err != nil {
		f.Sample.CollectionErrors = append(f.Sample.CollectionErrors, "account_snapshot_unavailable")
	} else {
		a, err := accountstate.Decode(row.State)
		if err != nil {
			f.Sample.CollectionErrors = append(f.Sample.CollectionErrors, "account_snapshot_invalid")
		} else {
			f.Account = a
			f.Sample.AccountSnapshotID = a.SnapshotID
			f.Sample.AccountGeneration = a.Generation
			at := a.AsOf
			f.Sample.AccountAsOf = &at
			f.Sample.AccountTradingDay = a.TradingDay
			known := a.DayStartKnown
			f.Sample.DayStartKnown = &known
			f.Sample.Reconciliation = a.Reconciliation.Status
			reconciledAt := a.Reconciliation.AsOf
			f.Sample.ReconciliationAsOf = &reconciledAt
		}
	}
	unresolved, err := c.ExecutionIntent.Query().Where(executionintent.BookEQ("paper"), executionintent.StateIn("claimed", "unknown")).Count(ctx)
	if err != nil {
		return f, err
	}
	f.Sample.UnresolvedIntents = &unresolved
	unknownOrders, err := c.TradeOrder.Query().Where(tradeorder.BookEQ("paper"), tradeorder.StatusIn("unknown", "submitting", "cancel_pending")).Count(ctx)
	if err != nil {
		return f, err
	}
	f.Sample.UnknownOrders = &unknownOrders
	dead, err := c.Outbox.Query().Where(outbox.SourceEQ("trade"), outbox.DeadLetterEQ(true)).Count(ctx)
	if err != nil {
		return f, err
	}
	f.Sample.OutboxDead = &dead
	oldest, err := c.Outbox.Query().Where(outbox.SourceEQ("trade"), outbox.PublishedAtIsNil(), outbox.DeadLetterEQ(false)).Order(outbox.ByCreatedAt()).First(ctx)
	if err != nil && !ent.IsNotFound(err) {
		return f, err
	}
	if err == nil {
		at := oldest.CreatedAt
		f.Sample.OutboxOldest = &at
	}
	f.Sample.OutboxKnown = true
	config, err := params.Read(ctx, c)
	if err != nil {
		f.Sample.CollectionErrors = append(f.Sample.CollectionErrors, "config_head_unavailable")
	} else {
		v := config.Revision
		f.Sample.StoredConfigRevision = &v
	}
	if err = r.collectAdmissionMarket(ctx, now, &f.Sample); err != nil {
		f.Sample.CollectionErrors = append(f.Sample.CollectionErrors, "market_dependency_unavailable")
	}
	return f, nil
}

func (r *Repo) collectAdmissionMarket(ctx context.Context, now time.Time, s *admission.Sample) error {
	if r.rdb == nil {
		return fmt.Errorf("admission: redis unavailable")
	}
	day := now.In(tradecal.Shanghai()).Format("2006-01-02")
	securities, err := r.clientFor(ctx).StockBasic.Query().All(ctx)
	if err != nil {
		return err
	}
	universe := map[string]bool{}
	unknown := false
	for _, r := range securities {
		if !ashare.IsAStock(r.StockCode) || r.SuspendFlag {
			continue
		}
		if r.ListDate == nil || r.UpdateTime.In(tradecal.Shanghai()).Format("2006-01-02") != day {
			unknown = true
			continue
		}
		if r.ListDate.In(tradecal.Shanghai()).Format("2006-01-02") > day || r.DelistDate != nil && r.DelistDate.In(tradecal.Shanghai()).Format("2006-01-02") <= day {
			continue
		}
		universe[r.StockCode] = true
	}
	health, err := r.clientFor(ctx).SourceHealth.Query().Where(sourcehealth.DomainEQ("security"), sourcehealth.EnabledEQ(true), sourcehealth.StateEQ("closed"), sourcehealth.LastSuccessAtNotNil()).All(ctx)
	if err != nil {
		return err
	}
	sourceKnown := false
	for _, h := range health {
		if h.LastSuccessAt != nil && h.LastSuccessAt.In(tradecal.Shanghai()).Format("2006-01-02") == day && !h.LastSuccessAt.After(now.Add(time.Second)) {
			sourceKnown = true
		}
	}
	count := len(universe)
	s.UniverseCount = &count
	s.UniverseKnown = !unknown && sourceKnown && count >= 3000
	s.UniverseBasis = "current-day stock_basic listed/non-suspended A shares + successful security source observation"
	values, err := r.rdb.HGetAll(ctx, market.SnapshotKey).Result()
	if err != nil {
		return err
	}
	observed, freshRows, closingRows := 0, 0, 0
	for symbol := range universe {
		raw, ok := values[symbol]
		if !ok {
			continue
		}
		q, err := market.ParseSnapshot([]byte(raw))
		if err != nil || q.Symbol != symbol || q.Source == "" || q.Last <= 0 || q.Time.After(now.Add(time.Second)) || q.AsOf.After(now.Add(time.Second)) {
			continue
		}
		observed++
		s.MarketSources[q.Source]++
		if !q.Expired(now, 10*time.Second) {
			freshRows++
			at := q.FreshnessTime()
			if s.MarketOldestSourceTime == nil || at.Before(*s.MarketOldestSourceTime) {
				s.MarketOldestSourceTime = &at
			}
		}
		at := q.FreshnessTime().In(tradecal.Shanghai())
		if !q.Stale && at.Format("2006-01-02") == day && (at.Hour() > 14 || at.Hour() == 14 && at.Minute() >= 55) {
			closingRows++
		}
	}
	s.MarketRows = &observed
	s.FreshRows = &freshRows
	s.ClosingRows = &closingRows
	// Persisted source health is flushed every minute. It is supporting evidence;
	// every actual quote still has the stricter ten-second source-time check.
	sources, err := r.clientFor(ctx).SourceHealth.Query().Where(sourcehealth.DomainEQ("snapshot"), sourcehealth.EnabledEQ(true), sourcehealth.StateEQ("closed"), sourcehealth.LastSuccessAtNotNil()).All(ctx)
	if err != nil {
		return err
	}
	knownSources := map[string]bool{}
	session, err := tradecal.Default.SessionAt(now)
	if err != nil {
		return err
	}
	closing := session.Phase == tradecal.PostClose
	for _, h := range sources {
		if h.LastSuccessAt != nil && admissionSourceObserved(*h.LastSuccessAt, now, closing) {
			knownSources[h.Source] = true
		}
	}
	allHealthy := len(s.MarketSources) > 0
	for source := range s.MarketSources {
		allHealthy = allHealthy && knownSources[source]
	}
	s.MarketSourceHealthy = &allHealthy
	return nil
}

// Snapshot collection intentionally stops after the quote session. Closing
// evidence therefore requires a successful source observation near this day's
// close, while intraday operation still requires the two-minute health heartbeat.
func admissionSourceObserved(success, now time.Time, closing bool) bool {
	if success.IsZero() || success.After(now.Add(time.Second)) {
		return false
	}
	if !closing {
		return now.Sub(success) <= 120*time.Second
	}
	local := now.In(tradecal.Shanghai())
	cutoff := time.Date(local.Year(), local.Month(), local.Day(), 14, 55, 0, 0, tradecal.Shanghai())
	return !success.Before(cutoff)
}

func (r *Repo) SaveAdmissionSample(ctx context.Context, s admission.Sample) error {
	if err := s.Verify(); err != nil {
		return err
	}
	now := time.Now()
	if s.ObservedAt.After(now.Add(time.Second)) || now.Sub(s.ObservedAt) > 10*time.Second {
		return fmt.Errorf("admission: historical/future writes are prohibited")
	}
	p, err := json.Marshal(s)
	if err != nil {
		return err
	}
	err = r.clientFor(ctx).PaperRunSample.Create().SetBook(s.Book).SetAccountID(s.AccountID).SetTradingDay(s.TradingDay).SetSlotStart(s.SlotStart).SetInstanceID(s.InstanceID).SetObservedAt(s.ObservedAt).SetObservation(p).SetEvidenceHash(s.EvidenceHash).OnConflictColumns(paperrunsample.FieldBook, paperrunsample.FieldAccountID, paperrunsample.FieldSlotStart, paperrunsample.FieldInstanceID).DoNothing().Exec(ctx)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}

func (r *Repo) ReadAdmissionSamples(ctx context.Context, from time.Time) ([]admission.Sample, error) {
	rows, err := r.clientFor(ctx).PaperRunSample.Query().Where(paperrunsample.BookEQ("paper"), paperrunsample.AccountIDEQ("default"), paperrunsample.ObservedAtGTE(from)).Limit(150001).All(ctx)
	if err != nil {
		return nil, err
	}
	if len(rows) > 150000 {
		return nil, fmt.Errorf("admission: evidence query exceeds safe bound; no partial admission result")
	}
	out := make([]admission.Sample, 0, len(rows))
	for _, r := range rows {
		var s admission.Sample
		if err = json.Unmarshal(r.Observation, &s); err != nil {
			return nil, err
		}
		if err = s.Verify(); err != nil {
			return nil, err
		}
		if s.EvidenceHash != r.EvidenceHash || s.TradingDay != r.TradingDay || s.InstanceID != r.InstanceID || !s.SlotStart.Equal(r.SlotStart) || !s.ObservedAt.Equal(r.ObservedAt) {
			return nil, fmt.Errorf("admission: stored evidence identity mismatch")
		}
		out = append(out, s)
	}
	return out, nil
}
