package service

import (
	"context"
	"strings"
	"testing"

	v1 "server/api/backtest/v1"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	"github.com/go-kratos/kratos/v2/transport"
)

type stubHeader map[string]string

func (h stubHeader) Get(key string) string {
	for k, v := range h {
		if strings.EqualFold(k, key) {
			return v
		}
	}
	return ""
}
func (h stubHeader) Set(key, value string) { h[key] = value }
func (h stubHeader) Add(key, value string) { h[key] = value }
func (h stubHeader) Values(key string) []string {
	if v := h.Get(key); v != "" {
		return []string{v}
	}
	return nil
}
func (h stubHeader) Keys() []string {
	ks := make([]string, 0, len(h))
	for k := range h {
		ks = append(ks, k)
	}
	return ks
}

type stubTransport struct{ h stubHeader }

func (t stubTransport) Kind() transport.Kind            { return transport.KindHTTP }
func (t stubTransport) Endpoint() string                { return "" }
func (t stubTransport) Operation() string               { return "" }
func (t stubTransport) RequestHeader() transport.Header { return t.h }
func (t stubTransport) ReplyHeader() transport.Header   { return stubHeader{} }

func ctxWithRole(role string) context.Context {
	h := stubHeader{}
	if role != "" {
		h.Set("X-User-Role", role)
	}
	return transport.NewServerContext(context.Background(), stubTransport{h: h})
}

func TestRequireOwner(t *testing.T) {
	if err := requireOwner(context.Background()); err == nil {
		t.Fatal("无 transport 上下文必须拒绝（直连无角色注入）")
	}
	for _, role := range []string{"", "viewer", "admin", "owners", "owner "} {
		if err := requireOwner(ctxWithRole(role)); err == nil {
			t.Fatalf("角色 %q 必须拒绝", role)
		}
	}
	for _, role := range []string{"owner", "Owner", "OWNER"} {
		if err := requireOwner(ctxWithRole(role)); err != nil {
			t.Fatalf("角色 %q 应放行: %v", role, err)
		}
	}
}

// 审批三件套在角色缺失时必须在进入 biz 前拒绝（nil usecase 也不会被触达），
// 且错误语义为 403 FORBIDDEN。
func TestApprovalRPCsDenyWithoutOwner(t *testing.T) {
	s := &BacktestService{}
	denies := []func(context.Context) error{
		func(ctx context.Context) error {
			_, err := s.ApproveProposal(ctx, &v1.DecideProposalRequest{Id: 1})
			return err
		},
		func(ctx context.Context) error {
			_, err := s.RejectProposal(ctx, &v1.DecideProposalRequest{Id: 1})
			return err
		},
		func(ctx context.Context) error {
			_, err := s.RollbackStrategy(ctx, &v1.RollbackStrategyRequest{Name: "x"})
			return err
		},
	}
	for i, call := range denies {
		err := call(transport.NewServerContext(context.Background(), stubTransport{h: stubHeader{}}))
		if kerrors.Code(err) != 403 {
			t.Fatalf("case %d: 期望 403，实得 %v", i, err)
		}
	}
}
