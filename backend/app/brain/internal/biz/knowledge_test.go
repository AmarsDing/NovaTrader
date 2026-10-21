package biz

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// knowledge_test.go：M13-T09 事实包注入相似案例。双路径必测：命中注入 / degraded 显式缺失。

type fakeKB struct {
	reply *KbCases
	err   error
	calls int
	lastQ KbQuery
}

func (f *fakeKB) SimilarCases(_ context.Context, q KbQuery) (*KbCases, error) {
	f.calls++
	f.lastQ = q
	return f.reply, f.err
}

func newKBEnv(kb KnowledgeClient) *Usecase {
	return NewUsecase(NewSettings(nil), nil, &fakeMarket{}, nil, nil, nil, stubLlmCalls{}, nil, kb, log.DefaultLogger)
}

func kbPack() FactPack {
	return FactPack{
		Symbol: "600519.SH", Name: "贵州茅台", Phase: "HOT",
		AsOf:  time.Date(2026, 10, 8, 15, 0, 0, 0, tradecal.Shanghai()),
		Facts: []Fact{{ID: "F:close", Label: "收盘价", Value: 1680, Dim: Technical}},
	}
}

func TestAttachKBCasesHit(t *testing.T) {
	pnl := 3.2
	kb := &fakeKB{reply: &KbCases{Cases: []KbCase{
		{SignalID: 7, StockCode: "000858.SZ", StockName: "五粮液", Pattern: "首板弱转强", Outcome: "win", Attribution: "题材发酵", PnLPct: &pnl},
		{SignalID: 9, StockCode: "600111.SH", Pattern: "二板分歧", Outcome: "loss"},
	}}}
	uc := newKBEnv(kb)
	pack := uc.attachKBCases(context.Background(), kbPack())
	if kb.calls != 1 {
		t.Fatalf("calls = %d", kb.calls)
	}
	if kb.lastQ.TopK != kbTopK || kb.lastQ.StockCode != "600519.SH" || kb.lastQ.EmotionPhase != "HOT" || !strings.Contains(kb.lastQ.Situation, "贵州茅台") {
		t.Fatalf("query = %+v", kb.lastQ)
	}
	if pack.Meta != nil {
		t.Fatalf("meta = %v, 命中不应标 kb_missing", pack.Meta)
	}
	if len(pack.Facts) != 3 {
		t.Fatalf("facts = %d, 期望原 1 条 + 2 案例", len(pack.Facts))
	}
	got := pack.Facts[1]
	if got.ID != "C:[kb_case]1" || got.Dim != External {
		t.Fatalf("fact = %+v", got)
	}
	if !strings.Contains(got.Text, "pattern=首板弱转强") || !strings.Contains(got.Text, "outcome=win") || !strings.Contains(got.Text, "pnl=+3.20%") {
		t.Fatalf("text = %q", got.Text)
	}
	// 收益没取到的案例：pnl 段整体省略，不落 0。
	if strings.Contains(pack.Facts[2].Text, "pnl=") {
		t.Fatalf("无 pnl 的案例不应输出 pnl 段: %q", pack.Facts[2].Text)
	}
}

func TestAttachKBCasesDegraded(t *testing.T) {
	for name, kb := range map[string]*fakeKB{
		"degraded": {reply: &KbCases{Degraded: true, Cases: []KbCase{{SignalID: 1, Outcome: "win"}}}},
		"error":    {err: errors.New("connection refused")},
	} {
		uc := newKBEnv(kb)
		pack := uc.attachKBCases(context.Background(), kbPack())
		if pack.Meta["kb_missing"] != true {
			t.Fatalf("%s: meta = %v, 检索不可用必须显式 kb_missing", name, pack.Meta)
		}
		if len(pack.Facts) != 1 {
			t.Fatalf("%s: facts = %d, 不命中不应注入案例", name, len(pack.Facts))
		}
	}
}

func TestAttachKBCasesNoClient(t *testing.T) {
	uc := newKBEnv(nil) // 未配置 intel_addr
	pack := uc.attachKBCases(context.Background(), kbPack())
	if pack.Meta["kb_missing"] != true {
		t.Fatalf("客户端未配置必须显式 kb_missing, meta = %v", pack.Meta)
	}
}

func TestAttachKBCasesDisabled(t *testing.T) {
	kb := &fakeKB{reply: &KbCases{Cases: []KbCase{{SignalID: 1}}}}
	cfg := NewSettings(nil)
	cfg.KBEnabled = false
	uc := NewUsecase(cfg, nil, &fakeMarket{}, nil, nil, nil, stubLlmCalls{}, nil, kb, log.DefaultLogger)
	pack := uc.attachKBCases(context.Background(), kbPack())
	if kb.calls != 0 {
		t.Fatalf("开关关闭不应发起检索, calls = %d", kb.calls)
	}
	if pack.Meta != nil || len(pack.Facts) != 1 {
		t.Fatalf("开关关闭应直通: meta = %v facts = %d", pack.Meta, len(pack.Facts))
	}
}
