package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"server/pkg/backtest"
	"server/pkg/llm"
)

// fakeLLM 是 M13-T14 双路径单测用的网关打样：成功路径返回 Validate 通过的 JSON，
// 失败路径返回 ErrUnavailable 或 Validate 不过的垃圾串。
type fakeLLM struct {
	calls []llm.Request
	reply string
	err   error
}

func (f *fakeLLM) Chat(_ context.Context, req llm.Request) (llm.Response, error) {
	f.calls = append(f.calls, req)
	if f.err != nil {
		return llm.Response{}, f.err
	}
	if req.Validate != nil {
		if err := req.Validate(f.reply); err != nil {
			return llm.Response{Content: f.reply}, err
		}
	}
	return llm.Response{Content: f.reply, Model: "mock-small"}, nil
}

// introFakeRepo 只覆盖 Introspect 触到的方法；其余走嵌入 nil interface 的缺省实现。
type introFakeRepo struct {
	Repo
	cands    []Candidate
	feedback []Feedback
	saved    []*Introspection
}

func (r *introFakeRepo) Candidates(context.Context, time.Time, time.Time) ([]Candidate, error) {
	return r.cands, nil
}
func (r *introFakeRepo) PhaseOn(context.Context, time.Time) (string, error) { return "warm", nil }
func (r *introFakeRepo) SaveFeedback(_ context.Context, rows []Feedback) error {
	r.feedback = append([]Feedback(nil), rows...)
	return nil
}
func (r *introFakeRepo) Equity(context.Context, string, time.Time, time.Time) ([]backtest.EquityPoint, error) {
	return nil, nil
}
func (r *introFakeRepo) SaveIntrospection(_ context.Context, in *Introspection) (*Introspection, error) {
	clone := *in
	r.saved = append(r.saved, &clone)
	return &clone, nil
}
func (r *introFakeRepo) Introspections(context.Context, string, time.Time, time.Time, int) ([]*Introspection, error) {
	return nil, nil
}

// introFixture 搭一个 T+3 后验齐全的最小窗口：1 亏损入选样本 + 1 漏选样本。
// 覆盖规则归因（Attribution 枚举+规则串 Analysis）和 LLM 叙述两条写入路径。
func introFixture(day time.Time) (*introFakeRepo, *backtest.Memory) {
	repo := &introFakeRepo{}
	daysN := 5
	days := make([]time.Time, daysN)
	for i := range days {
		days[i] = day.AddDate(0, 0, -(daysN - 1 - i))
	}
	src := &backtest.Memory{Days: days}
	lose := -0.04
	miss := 0.08
	repo.cands = []Candidate{
		{TradeDate: day.AddDate(0, 0, -1), Strategy: "breakout_ref", Symbol: "600001.SH",
			Stage: "selected", Ret: map[int]*float64{3: &lose}, SignalStatus: "done"},
		{TradeDate: day.AddDate(0, 0, -1), Strategy: "breakout_ref", Symbol: "600002.SH",
			Stage: "missed", MissReason: "not_in_ai_top", Ret: map[int]*float64{3: &miss}},
	}
	return repo, src
}

func TestIntrospectLLMWritesNarrativeAndErrorAnalysis(t *testing.T) {
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)
	repo, src := introFixture(day)
	fake := &fakeLLM{reply: `{"narrative":"指标失效为主，漏选样本集中在放量首板","tags":["指标失效","放量漏选"]}`}
	cfg := NewSettings(nil)
	cfg.WindowDays = 5
	uc := NewUsecase(repo, src, nil, cfg, fake, log.DefaultLogger)

	in, err := uc.Introspect(context.Background(), day, "")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(fake.calls) != 1 {
		t.Fatalf("expect 1 llm call, got %d", len(fake.calls))
	}
	if fake.calls[0].Task != "introspect" || fake.calls[0].Tier != llm.Small {
		t.Fatalf("wrong tier/task: %+v", fake.calls[0])
	}
	if in.Narrative == "" || len(in.NarrativeTags) != 2 {
		t.Fatalf("narrative not persisted: %+v", in)
	}
	last := repo.saved[len(repo.saved)-1]
	if last.Narrative != in.Narrative || len(last.NarrativeTags) != len(in.NarrativeTags) {
		t.Fatalf("repo narrative mismatch: %+v vs %+v", last, in)
	}
	if len(repo.feedback) != 2 {
		t.Fatalf("expect 2 feedback rows, got %v", repo.feedback)
	}
	for _, fb := range repo.feedback {
		if !strings.Contains(fb.Analysis, "指标失效") {
			t.Fatalf("error_analysis 应带 LLM 叙述，实际 %q", fb.Analysis)
		}
	}
}

func TestIntrospectLLMFailureKeepsFieldsEmptyAndRulePathIntact(t *testing.T) {
	cases := []struct {
		name  string
		reply string
		err   error
	}{
		{"unavailable", "", llm.ErrUnavailable},
		{"invalid json", "not a json", nil},
		{"empty narrative", `{"narrative":"","tags":[]}`, nil},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)
			repo, src := introFixture(day)
			fake := &fakeLLM{reply: tc.reply, err: tc.err}
			cfg := NewSettings(nil)
			cfg.WindowDays = 5
			uc := NewUsecase(repo, src, nil, cfg, fake, log.DefaultLogger)

			in, err := uc.Introspect(context.Background(), day, "")
			if err != nil {
				t.Fatalf("Introspect 主流程不得因 LLM 不可用而炸: %v", err)
			}
			if in.Narrative != "" || len(in.NarrativeTags) != 0 {
				t.Fatalf("LLM 失败时两字段必须留空，无占位串：%+v", in)
			}
			last := repo.saved[len(repo.saved)-1]
			if last.Narrative != "" || len(last.NarrativeTags) != 0 {
				t.Fatalf("写库的叙述字段必须为空：%+v", last)
			}
			if len(repo.feedback) != 2 {
				t.Fatalf("expect 2 feedback rows, got %v", repo.feedback)
			}
			// 规则归因零回归：Attribution 枚举必须落库，Analysis 规则串必须保留。
			for _, fb := range repo.feedback {
				if fb.Attribution == "" {
					t.Fatalf("规则归因 Attribution 应保留：%+v", fb)
				}
				if fb.Analysis == "" {
					t.Fatalf("规则 Analysis 串应保留（LLM 失败不写占位即可）：%+v", fb)
				}
				if strings.Contains(fb.Analysis, "LLM") {
					t.Fatalf("不允许写『LLM 不可用』类占位串：%q", fb.Analysis)
				}
			}
		})
	}
}

func TestIntrospectNilLLMNoCallNoNarrative(t *testing.T) {
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)
	repo, src := introFixture(day)
	cfg := NewSettings(nil)
	cfg.WindowDays = 5
	uc := NewUsecase(repo, src, nil, cfg, nil, log.DefaultLogger)
	in, err := uc.Introspect(context.Background(), day, "")
	if err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if in.Narrative != "" || len(in.NarrativeTags) != 0 {
		t.Fatalf("无 LLM 时两字段必须留空：%+v", in)
	}
}

func TestIntrospectNoFailureSamplesSkipsLLM(t *testing.T) {
	day := time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local)
	repo := &introFakeRepo{}
	days := []time.Time{day.AddDate(0, 0, -1), day}
	src := &backtest.Memory{Days: days}
	win := 0.05
	repo.cands = []Candidate{
		{TradeDate: day.AddDate(0, 0, -1), Strategy: "breakout_ref", Symbol: "600001.SH",
			Stage: "selected", Ret: map[int]*float64{3: &win}, SignalStatus: "done"},
	}
	cfg := NewSettings(nil)
	cfg.WindowDays = 2
	fake := &fakeLLM{reply: `{"narrative":"x","tags":["y"]}`}
	uc := NewUsecase(repo, src, nil, cfg, fake, log.DefaultLogger)
	if _, err := uc.Introspect(context.Background(), day, ""); err != nil {
		t.Fatalf("Introspect: %v", err)
	}
	if len(fake.calls) != 0 {
		t.Fatalf("无亏损/漏选样本时不该调 LLM，实际调用 %d 次", len(fake.calls))
	}
}

// TestIntrospectPromptContract 钉输出合同：提示词须明确 narrative / tags 两键，不得教模型写占位串。
// Validate 的具体空 narrative 拒绝已由 failure table 的 "empty narrative" 用例覆盖。
func TestIntrospectPromptContract(t *testing.T) {
	if strings.Contains(introspectSystemPrompt, "LLM不可用") {
		t.Fatalf("prompt 不得教模型写占位串：%s", introspectSystemPrompt)
	}
	if !strings.Contains(introspectSystemPrompt, "narrative") || !strings.Contains(introspectSystemPrompt, "tags") {
		t.Fatalf("prompt 必须明确两键合同：%s", introspectSystemPrompt)
	}
	// buildIntrospectUserPrompt 送出的事实包必须含两样本组标签，否则合同输出失去输入上下文。
	in := &Introspection{TradeDate: time.Date(2026, 10, 21, 0, 0, 0, 0, time.Local), WindowDays: 5,
		SampleCount: 2, WinRate: 0.5, AvgReturn: 0.01, MissedCount: 1}
	ok := false
	lose := -0.04
	miss := 0.08
	losers := []Feedback{{TradeDate: in.TradeDate, Strategy: "s", Symbol: "600001.SH",
		Actual: lose, Correct: &ok, Attribution: AttrIndicator}}
	missed := []Feedback{{TradeDate: in.TradeDate, Strategy: "s", Symbol: "600002.SH",
		Actual: miss, FalseNegative: true, Attribution: AttrMissed}}
	out := buildIntrospectUserPrompt(in, losers, missed, 3)
	for _, want := range []string{"亏损入选", "漏选样本", "600001.SH", "600002.SH", AttrIndicator} {
		if !strings.Contains(out, want) {
			t.Fatalf("用户 prompt 缺事实行 %q：%s", want, out)
		}
	}
}
