package data

import (
	"context"
	"errors"
	"time"

	"github.com/google/wire"
	"server/app/backtest/internal/biz"
	"server/ent"
	"server/ent/researchjob"
	"server/pkg/events"
	"server/pkg/outbox"
)

var ResearchProviderSet = wire.NewSet(NewResearchRepo, NewResearchExecutor, wire.Bind(new(biz.ResearchRepo), new(*ResearchRepo)), wire.Bind(new(biz.ResearchExecutor), new(*ResearchExecutor)))

type ResearchRepo struct {
	client     *ent.Client
	dispatcher *outbox.Dispatcher
}

func NewResearchRepo(client *ent.Client, bus *events.Bus) *ResearchRepo {
	return &ResearchRepo{client: client, dispatcher: outbox.NewDispatcher(client, bus)}
}
func researchOf(r *ent.ResearchJob) *biz.ResearchJob {
	return &biz.ResearchJob{RunID: r.RunID, Kind: r.Kind, ManifestID: r.ManifestID, ManifestHash: r.ManifestHash, AsOf: r.AsOf, InputJSON: r.InputJSON, OptionsJSON: r.OptionsJSON, InputHash: r.InputHash, Seed: r.Seed, TimeoutSeconds: r.TimeoutSeconds, CreatedBy: r.CreatedBy, Status: r.Status, Reason: r.Reason, CancelRequested: r.CancelRequested, Owner: r.Owner, LeaseUntil: r.LeaseUntil, Revision: r.Revision, ResultJSON: r.ResultJSON, ArtifactHash: r.ArtifactHash, CreatedAt: r.CreatedAt, StartedAt: r.StartedAt, FinishedAt: r.FinishedAt}
}
func researchEvent(ctx context.Context, tx *ent.Tx, r *ent.ResearchJob) error {
	e, err := events.NewWithContext(ctx, "backtest", biz.ResearchSubject, map[string]any{"run_id": r.RunID, "kind": r.Kind, "status": r.Status, "reason": r.Reason, "revision": r.Revision, "manifest_id": r.ManifestID, "input_hash": r.InputHash, "artifact_hash": r.ArtifactHash, "cancel_requested": r.CancelRequested})
	if err != nil {
		return err
	}
	e.EntityID = r.RunID
	e.EntityVersion = r.Revision
	e.CorrelationID = r.RunID
	return outbox.Insert(ctx, tx, e)
}
func (r *ResearchRepo) CreateResearch(ctx context.Context, j *biz.ResearchJob) (*biz.ResearchJob, error) {
	tx, err := r.client.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row, err := tx.ResearchJob.Create().SetRunID(j.RunID).SetKind(j.Kind).SetManifestID(j.ManifestID).SetManifestHash(j.ManifestHash).SetAsOf(j.AsOf).SetInputJSON(j.InputJSON).SetOptionsJSON(j.OptionsJSON).SetInputHash(j.InputHash).SetSeed(j.Seed).SetTimeoutSeconds(j.TimeoutSeconds).SetCreatedBy(j.CreatedBy).Save(ctx)
	if err != nil {
		return nil, err
	}
	if err = researchEvent(ctx, tx, row); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return researchOf(row), nil
}
func (r *ResearchRepo) GetResearch(ctx context.Context, id string) (*biz.ResearchJob, error) {
	row, err := r.client.ResearchJob.Query().Where(researchjob.RunIDEQ(id)).Only(ctx)
	if err != nil {
		return nil, missing(err)
	}
	return researchOf(row), nil
}
func (r *ResearchRepo) ListResearch(ctx context.Context, status string, limit, offset int) ([]*biz.ResearchJob, error) {
	q := r.client.ResearchJob.Query()
	if status != "" {
		q.Where(researchjob.StatusEQ(status))
	}
	rows, err := q.Order(ent.Desc(researchjob.FieldID)).Limit(limit).Offset(offset).All(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]*biz.ResearchJob, 0, len(rows))
	for _, row := range rows {
		out = append(out, researchOf(row))
	}
	return out, nil
}

var errResearchRace = errors.New("research compare-and-swap conflict")

// Each transition and its outbox event commit together. Revision CAS keeps a
// cancellation racing completion authoritative and prevents two claimants.
func (r *ResearchRepo) mutateResearch(ctx context.Context, id string, change func(*ent.ResearchJob, *ent.ResearchJobUpdate) (bool, error)) (*biz.ResearchJob, error) {
	for attempt := 0; attempt < 5; attempt++ {
		tx, err := r.client.Tx(ctx)
		if err != nil {
			return nil, err
		}
		row, err := tx.ResearchJob.Query().Where(researchjob.RunIDEQ(id)).Only(ctx)
		if err != nil {
			tx.Rollback()
			return nil, missing(err)
		}
		u := tx.ResearchJob.Update().Where(researchjob.IDEQ(row.ID), researchjob.RevisionEQ(row.Revision)).AddRevision(1)
		changed, err := change(row, u)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if !changed {
			tx.Rollback()
			return researchOf(row), nil
		}
		n, err := u.Save(ctx)
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if n != 1 {
			tx.Rollback()
			continue
		}
		row, err = tx.ResearchJob.Get(ctx, row.ID)
		if err == nil {
			err = researchEvent(ctx, tx, row)
		}
		if err != nil {
			tx.Rollback()
			return nil, err
		}
		if err = tx.Commit(); err != nil {
			return nil, err
		}
		return researchOf(row), nil
	}
	return nil, errResearchRace
}
func (r *ResearchRepo) ClaimResearch(ctx context.Context, owner string, now time.Time) (*biz.ResearchJob, error) {
	rows, err := r.client.ResearchJob.Query().Where(researchjob.StatusEQ("queued")).Order(ent.Asc(researchjob.FieldID)).Limit(10).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, row := range rows {
		claimed := false
		got, err := r.mutateResearch(ctx, row.RunID, func(current *ent.ResearchJob, u *ent.ResearchJobUpdate) (bool, error) {
			claimed = false
			if current.Status != "queued" {
				return false, nil
			}
			claimed = true
			u.SetStatus("running").SetOwner(owner).SetStartedAt(now).SetLeaseUntil(now.Add(time.Duration(current.TimeoutSeconds+30) * time.Second))
			return true, nil
		})
		if err != nil {
			return nil, err
		}
		if claimed && got.Owner == owner && got.Status == "running" {
			return got, nil
		}
	}
	return nil, nil
}
func terminalResearch(status string) bool { return status != "queued" && status != "running" }
func (r *ResearchRepo) CancelResearch(ctx context.Context, id string) (*biz.ResearchJob, error) {
	return r.mutateResearch(ctx, id, func(row *ent.ResearchJob, u *ent.ResearchJobUpdate) (bool, error) {
		if terminalResearch(row.Status) || row.CancelRequested {
			return false, nil
		}
		u.SetCancelRequested(true).SetReason("cancel_requested")
		if row.Status == "queued" {
			u.SetStatus("cancelled").SetFinishedAt(time.Now())
		}
		return true, nil
	})
}
func (r *ResearchRepo) FinishResearch(ctx context.Context, id, owner string, result biz.ResearchResult) error {
	_, err := r.mutateResearch(ctx, id, func(row *ent.ResearchJob, u *ent.ResearchJobUpdate) (bool, error) {
		if row.Status != "running" || row.Owner != owner || row.LeaseUntil == nil || row.LeaseUntil.Before(time.Now()) {
			return false, nil
		}
		status, reason := result.Status, result.Reason
		switch status {
		case "succeeded", "failed", "unavailable", "rejected", "timed_out", "cancelled":
		default:
			status = "failed"
			reason = "invalid_worker_status"
		}
		if row.CancelRequested {
			status = "cancelled"
			reason = "cancel_requested"
			result.JSON = ""
			result.ArtifactHash = ""
		}
		if len(reason) > 256 {
			reason = reason[:256]
		}
		u.SetStatus(status).SetReason(reason).SetResultJSON(result.JSON).SetArtifactHash(result.ArtifactHash).SetFinishedAt(time.Now()).ClearLeaseUntil()
		return true, nil
	})
	return err
}
func (r *ResearchRepo) RecoverResearch(ctx context.Context, now time.Time) error {
	rows, err := r.client.ResearchJob.Query().Where(researchjob.StatusEQ("running"), researchjob.LeaseUntilLT(now)).Limit(100).All(ctx)
	if err != nil {
		return err
	}
	for _, row := range rows {
		_, err = r.mutateResearch(ctx, row.RunID, func(current *ent.ResearchJob, u *ent.ResearchJobUpdate) (bool, error) {
			if current.Status != "running" || current.LeaseUntil == nil || !current.LeaseUntil.Before(now) {
				return false, nil
			}
			status := "failed"
			if current.CancelRequested {
				status = "cancelled"
			}
			u.SetStatus(status).SetReason("execution_lease_expired_no_automatic_replay").SetFinishedAt(now).ClearLeaseUntil()
			return true, nil
		})
		if err != nil {
			return err
		}
	}
	return nil
}
func (r *ResearchRepo) DispatchResearch(ctx context.Context) error {
	_, err := r.dispatcher.Dispatch(ctx, 20)
	return err
}
