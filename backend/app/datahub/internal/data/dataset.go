package data

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"server/ent"
	"server/ent/datasetrecord"
	"server/pkg/dataset"
)

// captureDataset is part of the same transaction as the current-view upsert and outbox.
// Only insertion is allowed: repeated collection is a no-op; corrections create another revision.
func captureDataset(ctx context.Context, tx *ent.Tx, domain, key, source string, event time.Time, a dataset.Availability, payload any) error {
	if source == "" {
		source = "unknown"
	}
	observed := time.Now()
	if a.ObservedAt != nil {
		observed = *a.ObservedAt
	}
	r, err := dataset.NewRecord(domain, key, source, event, observed, a, payload)
	if err != nil {
		return err
	}
	err = tx.DatasetRecord.Create().SetDomain(r.Domain).SetBusinessKey(r.BusinessKey).SetSource(r.Source).
		SetEventTime(r.EventTime).SetNillableAvailableAt(r.AvailableAt).SetAvailability(r.Availability).
		SetAvailabilityEvidence(r.Evidence).SetObservedAt(r.ObservedAt).SetRawHash(r.RawHash).
		SetRevisionHash(r.RevisionHash).SetPayload(r.Payload).
		OnConflictColumns(datasetrecord.FieldRevisionHash).DoNothing().Exec(ctx)
	// PostgreSQL DO NOTHING returns no ID; ent's single-row create reports sql.ErrNoRows.
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	return err
}
