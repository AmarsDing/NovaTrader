package biz

import (
	"context"
	"errors"
	"strings"
	"testing"

	"server/pkg/rules"

	"github.com/go-kratos/kratos/v2/log"
)

// approvalFakeRepo 只实现审批链涉及的方法，其余接口方法用嵌入的 nil 接口占位（调到即 panic，
// 说明测试走错了路）。RecordApproval 正常记账或按 auditErr 模拟审计库写不进去。
type approvalFakeRepo struct {
	Repo
	proposal    *Proposal
	approveErr  error
	rejectErr   error
	rollbackErr error
	auditErr    error
	audits      []ApprovalAudit
}

func (f *approvalFakeRepo) GetProposal(context.Context, int) (*Proposal, error) {
	return f.proposal, nil
}

func (f *approvalFakeRepo) ApproveProposal(_ context.Context, id int, _ map[string]string, _ string) (*Proposal, error) {
	if f.approveErr != nil {
		return nil, f.approveErr
	}
	p := *f.proposal
	p.Status = ProposalApproved
	return &p, nil
}

func (f *approvalFakeRepo) RejectProposal(_ context.Context, id int, _, note string) (*Proposal, error) {
	if f.rejectErr != nil {
		return nil, f.rejectErr
	}
	p := *f.proposal
	p.Status = ProposalRejected
	if note != "" {
		p.Reason = note
	}
	return &p, nil
}

func (f *approvalFakeRepo) RollbackProposal(_ context.Context, strategy, _ string) (*Proposal, error) {
	if f.rollbackErr != nil {
		return nil, f.rollbackErr
	}
	p := *f.proposal
	p.Status = ProposalRolledBack
	p.Strategy = strategy
	return &p, nil
}

func (f *approvalFakeRepo) RecordApproval(_ context.Context, a ApprovalAudit) error {
	f.audits = append(f.audits, a)
	return f.auditErr
}

func auditUsecase(repo Repo) *Usecase {
	return NewUsecase(repo, nil, nil, Settings{}, nil, log.DefaultLogger)
}

func pendingProposal() *Proposal {
	vid := 7
	prev := 5
	return &Proposal{
		ID:            12,
		Strategy:      rules.NameFirstBoard,
		Status:        ProposalPending,
		VersionID:     &vid,
		PrevVersionID: &prev,
		Params:        map[string]float64{},
	}
}

// 批准成功：审计一行，actor/role/action/target 与 decided_by 同源，OK=true。
func TestApproveRecordsAudit(t *testing.T) {
	repo := &approvalFakeRepo{proposal: pendingProposal()}
	uc := auditUsecase(repo)
	p, err := uc.Approve(context.Background(), 12, "alice", "owner")
	if err != nil || p.Status != ProposalApproved {
		t.Fatalf("approve = %+v, %v", p, err)
	}
	if len(repo.audits) != 1 {
		t.Fatalf("audits = %d, want 1", len(repo.audits))
	}
	a := repo.audits[0]
	if a.Action != AuditActionApprove || !a.OK || a.Actor != "alice" || a.Role != "owner" {
		t.Fatalf("audit = %+v", a)
	}
	if a.Target != "param_proposal/12" || !strings.Contains(a.Detail, "strategy=") || !strings.Contains(a.Detail, "version=7") {
		t.Fatalf("audit target/detail = %q %q", a.Target, a.Detail)
	}
}

// 决定未生效（状态冲突）：原错照常上抛，审计记 OK=false 且附错误原文——失败不能当没发生过。
func TestApproveConflictStillAudited(t *testing.T) {
	repo := &approvalFakeRepo{proposal: pendingProposal(), approveErr: errors.New("提案已不是待处理")}
	uc := auditUsecase(repo)
	if _, err := uc.Approve(context.Background(), 12, "alice", "owner"); err == nil {
		t.Fatal("want conflict error")
	}
	if len(repo.audits) != 1 || repo.audits[0].OK {
		t.Fatalf("audits = %+v", repo.audits)
	}
	if !strings.Contains(repo.audits[0].Detail, "error: ") {
		t.Fatalf("audit detail = %q", repo.audits[0].Detail)
	}
}

// 批准已生效但审计写库失败：显式报错让操作员重查，不留无审计同行的成功审批。
func TestApproveAuditWriteFailureIsLoud(t *testing.T) {
	repo := &approvalFakeRepo{proposal: pendingProposal(), auditErr: errors.New("audit_log 连接断开")}
	uc := auditUsecase(repo)
	_, err := uc.Approve(context.Background(), 12, "alice", "owner")
	if err == nil || !strings.Contains(err.Error(), "审计落库失败") {
		t.Fatalf("err = %v, want 审计落库失败", err)
	}
}

// 驳回：note 进 detail；空 actor 走 repo 同款 userOr 兜底，保证审计与 decided_by 一致。
func TestRejectRecordsAuditWithNote(t *testing.T) {
	repo := &approvalFakeRepo{proposal: pendingProposal()}
	uc := auditUsecase(repo)
	if _, err := uc.Reject(context.Background(), 12, "", "viewer", "样本不足"); err != nil {
		t.Fatal(err)
	}
	a := repo.audits[0]
	if a.Action != AuditActionReject || a.Actor != "local" || a.Role != "viewer" || !strings.Contains(a.Detail, "样本不足") {
		t.Fatalf("audit = %+v", a)
	}
}

// 回滚：target 是策略名，detail 带版本走向。
func TestRollbackRecordsAudit(t *testing.T) {
	repo := &approvalFakeRepo{proposal: pendingProposal()}
	uc := auditUsecase(repo)
	if _, err := uc.Rollback(context.Background(), rules.NameFirstBoard, "alice", "owner"); err != nil {
		t.Fatal(err)
	}
	a := repo.audits[0]
	if a.Action != AuditActionRollback || a.Target != rules.NameFirstBoard || !strings.Contains(a.Detail, "version=5->7") {
		t.Fatalf("audit = %+v", a)
	}
}
