package outbox

import (
	"context"
	"errors"
	"fmt"
	"time"

	"entgo.io/ent/dialect/sql"
	"github.com/google/uuid"

	"server/ent"
	box "server/ent/outbox"
	"server/ent/predicate"
	"server/pkg/metrics"
)

const leaseDuration = 30 * time.Second
const maxAttempts = 12

// oldestPendingAge 是最老未见投递事件的年龄（秒），按域分维。每批 Dispatch 前观测一次。
var oldestPendingAge = metrics.GaugeVec("outbox_oldest_pending_age_seconds", "oldest undispatched outbox row age in seconds, per domain", "domain")

// pendingConds 与 claim 的领取条件一致：未投递、未死信、退回到期、租约过期。
func pendingConds(now time.Time) []predicate.Outbox {
	return []predicate.Outbox{
		box.PublishedAtIsNil(),
		box.DeadLetter(false),
		box.Or(box.NextAttemptAtIsNil(), box.NextAttemptAtLTE(now)),
		box.Or(box.LeaseUntilIsNil(), box.LeaseUntilLTE(now)),
	}
}

// pendingDomains 列出当前有可领事件的域。按域隔离租约：域间轮询，洪水域不饿死其他域。
func pendingDomains(ctx context.Context, c *ent.Client, now time.Time) ([]string, error) {
	rows, err := c.Outbox.Query().Where(pendingConds(now)...).GroupBy(box.FieldDomain).Strings(ctx)
	if err != nil {
		return nil, err
	}
	return rows, nil
}

// observePending 按域统计最老待发年龄并整体替换指标分维（消失的域清出）。
func observePending(ctx context.Context, c *ent.Client, now time.Time) {
	var aggs []struct {
		Domain string    `json:"domain"`
		Oldest time.Time `json:"oldest"`
	}
	err := c.Outbox.Query().Where(pendingConds(now)...).
		GroupBy(box.FieldDomain).
		Aggregate(func(s *sql.Selector) string {
			return sql.As(sql.Min(s.C(box.FieldCreatedAt)), "oldest")
		}).
		Scan(ctx, &aggs)
	if err != nil {
		return // 观测失败不挡投递；下一轮再试
	}
	ages := make(map[string]float64, len(aggs))
	for _, a := range aggs {
		ages[a.Domain] = now.Sub(a.Oldest).Seconds()
	}
	oldestPendingAge.Replace(ages)
}

func claim(ctx context.Context, c *ent.Client, owner, domain string, now time.Time) (*ent.Outbox, error) {
	tx, err := c.Tx(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback()
	row, err := tx.Outbox.Query().Where(append(pendingConds(now), box.DomainEQ(domain))...).Order(box.ByCreatedAt(sql.OrderAsc()), box.ByID()).ForUpdate(sql.WithLockAction(sql.SkipLocked)).First(ctx)
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

// dispatchLeased 域间轮询、每行一次有界投递。坏行带重试/死信态隔离，毒行不堵无关事件。
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
	now := time.Now().UTC()
	domains, err := pendingDomains(ctx, c, now)
	if err != nil {
		return 0, err
	}
	observePending(ctx, c, now)
	for i := 0; sent+len(errs) < limit && len(domains) > 0; i++ {
		domain := domains[i%len(domains)]
		now = time.Now().UTC()
		row, err := claim(ctx, c, owner, domain, now)
		if err != nil {
			return sent, errors.Join(append(errs, err)...)
		}
		if row == nil {
			domains = append(domains[:i%len(domains)], domains[i%len(domains)+1:]...)
			continue
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
