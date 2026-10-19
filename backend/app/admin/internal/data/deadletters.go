package data

import (
	"context"
	"fmt"

	"server/app/admin/internal/gateway"
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

// RequeueDeadLetters（N2-02 批量重放）：逐条复用条件单行重排，各条事务相互独立、
// 一条失败如实记 Err 并继续（不拖垮同批其余）；输入顺序保留——同一 event_id 重复出现时，
// 第二次的条件检查已被第一次改掉，自然如实返回 found=false 不重复改动。
func (s *AuditStore) RequeueDeadLetters(ctx context.Context, eventIDs []string) []gateway.DeadLetterRequeueOutcome {
	out := make([]gateway.DeadLetterRequeueOutcome, 0, len(eventIDs))
	for _, id := range eventIDs {
		found, err := s.RequeueDeadLetter(ctx, id)
		out = append(out, gateway.DeadLetterRequeueOutcome{EventID: id, Requeued: found, Err: err})
	}
	return out
}
