package data

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"server/ent"
	"server/ent/accountsnapshot"
	"server/ent/datasetmanifest"
	"server/ent/tradefill"
	"server/ent/tradeorder"
	"server/pkg/accountnav"
	"server/pkg/dataset"
	"server/pkg/tradecal"
	"time"
)

func (r *Repo) ReadNAVEvidence(ctx context.Context, q accountnav.Query) (accountnav.Evidence, error) {
	e := accountnav.Evidence{Snapshots: []accountnav.SnapshotRow{}, Fills: []accountnav.Fill{}}
	tx, err := r.client.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelRepeatableRead, ReadOnly: true})
	if err != nil {
		return e, err
	}
	defer tx.Rollback()
	if len(q.ExpectedDates) > 370 {
		return e, fmt.Errorf("NAV daily query exceeds supported bound")
	}
	sourceBytes := 0
	for _, key := range q.ExpectedDates {
		d, err := time.ParseInLocation("2006-01-02", key, tradecal.Shanghai())
		if err != nil {
			return e, err
		}
		start := d.Add(15 * time.Hour)
		row, err := tx.AccountSnapshot.Query().Where(accountsnapshot.Book("paper"), accountsnapshot.SnapshotTimeGTE(start), accountsnapshot.SnapshotTimeLT(start.Add(10*time.Minute)), accountsnapshot.SnapshotTimeLTE(q.AsOf)).Order(ent.Desc(accountsnapshot.FieldSnapshotTime), ent.Desc(accountsnapshot.FieldID)).First(ctx)
		if ent.IsNotFound(err) {
			continue
		}
		if err != nil {
			return e, err
		}
		sourceBytes += len(row.State)
		if sourceBytes > 16<<20 {
			return e, fmt.Errorf("NAV selected full-state evidence exceeds 16 MiB; shorten requested range")
		}
		e.Snapshots = append(e.Snapshots, accountnav.SnapshotRow{ID: row.ID, Book: row.Book, At: row.SnapshotTime, State: append(json.RawMessage(nil), row.State...)})
	}
	fills, err := tx.TradeFill.Query().Where(tradefill.Book("paper"), tradefill.CreatedAtGTE(q.From), tradefill.CreatedAtLTE(q.To)).Order(tradefill.ByCreatedAt(), tradefill.ByID()).Limit(100001).All(ctx)
	if err != nil {
		return e, err
	}
	if len(fills) > 100000 {
		return e, fmt.Errorf("NAV cash reconciliation exceeds 100000 fills; shorten requested range")
	}
	ids := []string{}
	seen := map[string]bool{}
	for _, f := range fills {
		if !seen[f.ClientOrderID] {
			seen[f.ClientOrderID] = true
			ids = append(ids, f.ClientOrderID)
		}
	}
	if len(ids) > 10000 {
		return e, fmt.Errorf("NAV original orders exceed 10000; shorten requested range")
	}
	orders := map[string]*ent.TradeOrder{}
	filledTotals := map[string]int{}
	for _, f := range fills {
		filledTotals[f.ClientOrderID] += f.Qty
	}
	if len(ids) > 0 {
		rows, err := tx.TradeOrder.Query().Where(tradeorder.Book("paper"), tradeorder.ClientOrderIDIn(ids...)).All(ctx)
		if err != nil {
			return e, err
		}
		for _, o := range rows {
			orders[o.ClientOrderID] = o
		}
	}
	for _, f := range fills {
		o := orders[f.ClientOrderID]
		known := o != nil && o.Symbol == f.Symbol && o.Side == f.Side && o.Filled <= o.Volume && o.Filled >= filledTotals[f.ClientOrderID]
		e.Fills = append(e.Fills, accountnav.Fill{ID: f.ID, Book: f.Book, ClientOrderID: f.ClientOrderID, Symbol: f.Symbol, Side: f.Side, Qty: f.Qty, Price: f.Price, Amount: f.Amount, Fee: f.Fee, At: f.CreatedAt, OriginalOrderKnown: known})
	}
	if q.ManifestID != "" {
		row, err := tx.DatasetManifest.Query().Where(datasetmanifest.ManifestID(q.ManifestID)).Only(ctx)
		if ent.IsNotFound(err) {
			e.ManifestError = "manifest_unavailable"
		} else if err != nil {
			return e, err
		} else {
			var m dataset.Manifest
			if err = json.Unmarshal(row.Content, &m); err != nil || m.ManifestID != row.ManifestID || m.Hash != row.Hash || !m.Cutoff.Equal(row.Cutoff) {
				e.ManifestError = "manifest_identity_invalid"
			} else {
				e.Manifest = &m
			}
		}
	}
	// Read-only transaction: rollback releases one consistent MVCC view; no
	// account, dataset, admission or outbox row is created by an export.
	return e, nil
}
