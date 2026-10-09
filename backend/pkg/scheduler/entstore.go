package scheduler

import (
	"context"
	"time"

	"server/ent"
	"server/ent/taskrun"
	"server/pkg/tradecal"
)

// EntStore 把执行记录写到 task_run。
type EntStore struct {
	Client *ent.Client
}

func (s EntStore) SucceededOn(ctx context.Context, name string, day time.Time) (bool, error) {
	d := day.In(tradecal.Shanghai())
	start := time.Date(d.Year(), d.Month(), d.Day(), 0, 0, 0, 0, tradecal.Shanghai())
	n, err := s.Client.TaskRun.Query().Where(
		taskrun.TaskNameEQ(name),
		taskrun.StatusEQ("success"),
		taskrun.SlotGTE(start),
		taskrun.SlotLT(start.Add(24*time.Hour)),
	).Count(ctx)
	return n > 0, err
}

func (s EntStore) Start(ctx context.Context, name string, slot time.Time, attempt int) error {
	return s.Client.TaskRun.Create().
		SetTaskName(name).
		SetSlot(slot).
		SetAttempt(attempt).
		SetStatus("running").
		Exec(ctx)
}

func (s EntStore) Finish(ctx context.Context, name string, slot time.Time, attempt int, runErr error) error {
	row, err := s.Client.TaskRun.Query().Where(
		taskrun.TaskNameEQ(name),
		taskrun.SlotEQ(slot),
		taskrun.AttemptEQ(attempt),
		taskrun.StatusEQ("running"),
	).Only(ctx)
	if err != nil {
		return err
	}
	upd := s.Client.TaskRun.UpdateOneID(row.ID).SetFinishedAt(time.Now())
	if runErr != nil {
		upd.SetStatus("failed").SetError(runErr.Error())
	} else {
		upd.SetStatus("success").ClearError()
	}
	return upd.Exec(ctx)
}
