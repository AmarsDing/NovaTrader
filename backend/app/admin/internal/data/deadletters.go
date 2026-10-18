package data

import (
	"context"
	"fmt"

	"server/ent"
	box "server/ent/outbox"
)

// RequeueDeadLetter（N2-02 死信重放审批）：条件单行重排。
// 只接受「未投递(published_at 为空)且当前是死信」的行，行锁串行化并发审批：
// 第二名审批者等待后条件不再匹配，如实返回 found=false，不会双重重排。
// attempts 归零给满退避预算；清 next_attempt_at 使调度器下一拍即可取单；
// 清 lease_until/lease_owner 防陈旧租约；last_error 保留为案底，不在重排时抹掉。
func (s *AuditStore) RequeueDeadLetter(ctx context.Context, eventID string) (bool, error) {
	if !s.Enabled() {
		return false, fmt.Errorf("database unavailable")
	}
	tx, err := s.client.Tx(ctx)
	if err != nil {
		return false, err
	}
	defer func() { _ = tx.Rollback() }()
	row, err := tx.Outbox.Query().
		Where(box.EventID(eventID), box.DeadLetter(true), box.PublishedAtIsNil()).
		ForUpdate().Only(ctx)
	if ent.IsNotFound(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if err := tx.Outbox.UpdateOne(row).
		SetDeadLetter(false).
		SetAttempts(0).
		ClearNextAttemptAt().
		ClearLeaseUntil().
		SetLeaseOwner("").
		Exec(ctx); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}
