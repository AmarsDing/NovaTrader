package data

import (
	"context"
	"errors"
	"strings"
	"time"

	"server/app/notify/internal/biz"
	"server/ent"
	"server/ent/notifymessage"

	entsql "entgo.io/ent/dialect/sql"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/jackc/pgx/v5/pgconn"
)

// Repo 读写 notify_message。
type Repo struct {
	db  *ent.Client
	log *log.Helper
}

func NewRepo(db *ent.Client, logger log.Logger) *Repo {
	return &Repo{db: db, log: log.NewHelper(log.With(logger, "module", "notify/data"))}
}

func (r *Repo) Insert(ctx context.Context, msg biz.Message) (biz.Message, bool, error) {
	fields := msg.Fields
	if fields == nil {
		fields = map[string]string{}
	}
	row, err := r.db.NotifyMessage.Create().
		SetEventID(msg.EventID).
		SetTraceID(msg.TraceID).
		SetSource(msg.Source).
		SetSubject(msg.Subject).
		SetCategory(msg.Category).
		SetPriority(msg.Priority).
		SetTitle(msg.Title).
		SetBody(msg.Body).
		SetFields(fields).
		SetDedupKey(msg.DedupKey).
		SetStatus(biz.StatusPending).
		SetCreatedAt(nonZero(msg.CreatedAt)).
		Save(ctx)
	if isUnique(err) {
		old, gerr := r.db.NotifyMessage.Query().Where(notifymessage.EventIDEQ(msg.EventID)).Only(ctx)
		if gerr != nil {
			return biz.Message{}, false, gerr
		}
		return toBiz(old), true, nil
	}
	if err != nil {
		return biz.Message{}, false, err
	}
	return toBiz(row), false, nil
}

func (r *Repo) Recent(ctx context.Context, key string, since time.Time, excludeID int) (*biz.Message, error) {
	row, err := r.db.NotifyMessage.Query().
		Where(
			notifymessage.DedupKeyEQ(key),
			notifymessage.IDNEQ(excludeID),
			notifymessage.StatusIn(biz.StatusSent, biz.StatusPartial, biz.StatusHeld, biz.StatusPending),
			notifymessage.CreatedAtGTE(since),
		).
		Order(notifymessage.ByCreatedAt(entsql.OrderDesc()), notifymessage.ByID(entsql.OrderDesc())).
		First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := toBiz(row)
	return &out, nil
}

func (r *Repo) MarkMerged(ctx context.Context, id, anchorID int, latest biz.Message) error {
	tx, err := r.db.Tx(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := tx.NotifyMessage.UpdateOneID(id).
		SetStatus(biz.StatusMerged).
		SetMergedInto(anchorID).
		Exec(ctx); err != nil {
		return err
	}
	anchor, err := tx.NotifyMessage.Get(ctx, anchorID)
	if err != nil {
		return err
	}
	u := tx.NotifyMessage.UpdateOneID(anchorID).AddMergeCount(1)
	if anchor.Status == biz.StatusHeld || anchor.Status == biz.StatusPending {
		fields := latest.Fields
		if fields == nil {
			fields = map[string]string{}
		}
		u.SetTitle(latest.Title).SetBody(latest.Body).SetFields(fields)
	}
	if err := u.Exec(ctx); err != nil {
		return err
	}
	return tx.Commit()
}

func (r *Repo) MarkHeld(ctx context.Context, id int) error {
	return r.db.NotifyMessage.UpdateOneID(id).SetStatus(biz.StatusHeld).Exec(ctx)
}

func (r *Repo) SaveDelivery(ctx context.Context, msg biz.Message) error {
	u := r.db.NotifyMessage.UpdateOneID(msg.ID).
		SetStatus(msg.Status).
		SetRendered(msg.Rendered).
		SetDesktopStatus(msg.DesktopStatus).
		SetDesktopAttempts(msg.DesktopAttempts).
		SetDesktopError(msg.DesktopError).
		SetFeishuStatus(msg.FeishuStatus).
		SetFeishuAttempts(msg.FeishuAttempts).
		SetFeishuError(msg.FeishuError)
	if !msg.SentAt.IsZero() {
		u.SetSentAt(msg.SentAt)
	}
	return u.Exec(ctx)
}

func (r *Repo) Get(ctx context.Context, id int) (biz.Message, error) {
	row, err := r.db.NotifyMessage.Get(ctx, id)
	if ent.IsNotFound(err) {
		return biz.Message{}, biz.ErrNotFound
	}
	if err != nil {
		return biz.Message{}, err
	}
	return toBiz(row), nil
}

func (r *Repo) List(ctx context.Context, limit int) ([]biz.Message, error) {
	rows, err := r.db.NotifyMessage.Query().
		Order(notifymessage.ByID(entsql.OrderDesc())).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toBizList(rows), nil
}

func (r *Repo) ListStatus(ctx context.Context, status string) ([]biz.Message, error) {
	rows, err := r.db.NotifyMessage.Query().
		Where(notifymessage.StatusEQ(status)).
		Order(notifymessage.ByID()).
		Limit(500).
		All(ctx)
	if err != nil {
		return nil, err
	}
	return toBizList(rows), nil
}

func toBizList(rows []*ent.NotifyMessage) []biz.Message {
	out := make([]biz.Message, 0, len(rows))
	for _, row := range rows {
		out = append(out, toBiz(row))
	}
	return out
}

func toBiz(row *ent.NotifyMessage) biz.Message {
	msg := biz.Message{
		ID: row.ID, EventID: row.EventID, TraceID: row.TraceID, Source: row.Source, Subject: row.Subject,
		Category: row.Category, Priority: row.Priority, Title: row.Title, Body: row.Body, Fields: row.Fields,
		DedupKey: row.DedupKey, MergeCount: row.MergeCount, Status: row.Status, Rendered: row.Rendered,
		DesktopStatus: row.DesktopStatus, DesktopAttempts: row.DesktopAttempts, DesktopError: row.DesktopError,
		FeishuStatus: row.FeishuStatus, FeishuAttempts: row.FeishuAttempts, FeishuError: row.FeishuError,
		CreatedAt: row.CreatedAt,
	}
	if row.MergedInto != nil {
		msg.MergedInto = *row.MergedInto
	}
	if row.SentAt != nil {
		msg.SentAt = *row.SentAt
	}
	if msg.Fields == nil {
		msg.Fields = map[string]string{}
	}
	return msg
}

// isUnique 识别 event_id 冲突。本机 PostgreSQL 的报错是中文，ent 只匹配英文 "violates unique constraint"。
func isUnique(err error) bool {
	if err == nil {
		return false
	}
	if ent.IsConstraintError(err) {
		return true
	}
	var pg *pgconn.PgError
	if errors.As(err, &pg) && pg.Code == "23505" {
		return true
	}
	msg := err.Error()
	return strings.Contains(msg, "SQLSTATE 23505") || strings.Contains(msg, "violates unique constraint")
}

func nonZero(t time.Time) time.Time {
	if t.IsZero() {
		return time.Now()
	}
	return t
}
