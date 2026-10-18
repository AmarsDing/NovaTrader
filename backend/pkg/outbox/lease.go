package outbox

import (
	"context"
	"entgo.io/ent/dialect/sql"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"server/ent"
	box "server/ent/outbox"
	"time"
)

const leaseDuration = 30 * time.Second
const maxAttempts = 12

func claim(ctx context.Context, c *ent.Client, owner string, now time.Time) (*ent.Outbox, error) {
	tx, err := c.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row, err := tx.Outbox.Query().Where(box.PublishedAtIsNil(), box.DeadLetter(false), box.Or(box.NextAttemptAtIsNil(), box.NextAttemptAtLTE(now)), box.Or(box.LeaseUntilIsNil(), box.LeaseUntilLTE(now))).Order(box.ByCreatedAt(sql.OrderAsc()), box.ByID()).ForUpdate(sql.WithLockAction(sql.SkipLocked)).First(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if err = tx.Outbox.UpdateOne(row).SetLeaseOwner(owner).SetLeaseUntil(now.Add(leaseDuration)).Exec(ctx); err != nil {
		return nil, err
	}
	if err = tx.Commit(); err != nil {
		return nil, err
	}
	return row, nil
}
func dispatchLeased(ctx context.Context, c *ent.Client, pub Publisher, limit int) (int, error) {
	if limit <= 0 {
		limit = 100
	}
	if limit > 1000 {
		limit = 1000
	}
	owner := uuid.NewString()
	sent := 0
	var errs []error
	for i := 0; i < limit; i++ {
		now := time.Now().UTC()
		row, err := claim(ctx, c, owner, now)
		if err != nil {
			return sent, errors.Join(append(errs, err)...)
		}
		if row == nil {
			break
		}
		env, decodeErr := envelopeFromRow(row)
		err = decodeErr
		if err == nil {
			pctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			err = pub.Publish(pctx, env)
			cancel()
		}
		update := c.Outbox.Update().Where(box.ID(row.ID), box.LeaseOwner(owner), box.PublishedAtIsNil(), box.LeaseUntilGT(time.Now().UTC())).ClearLeaseUntil().SetLeaseOwner("")
		if err != nil {
			// Store an error class, not an upstream body that might contain credentials.
			message := "publisher unavailable"
			dead := decodeErr != nil || row.Attempts+1 >= maxAttempts
			if decodeErr != nil {
				message = "invalid envelope metadata"
			}
			wait := time.Second * time.Duration(1<<min(row.Attempts, 8))
			_, writeErr := update.AddAttempts(1).SetLastError(message).SetDeadLetter(dead).SetNextAttemptAt(time.Now().UTC().Add(wait)).Save(ctx)
			errs = append(errs, fmt.Errorf("outbox: %s: %w", message, err))
			if writeErr != nil {
				return sent, errors.Join(append(errs, writeErr)...)
			}
			continue
		}
		n, err := update.SetPublishedAt(time.Now().UTC()).ClearNextAttemptAt().ClearLastError().Save(ctx)
		if err != nil {
			return sent, errors.Join(append(errs, err)...)
		}
		if n != 1 {
			return sent, errors.Join(append(errs, errors.New("outbox: publication lease lost; original ID retained"))...)
		}
		sent++
	}
	return sent, errors.Join(errs...)
}
