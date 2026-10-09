package biz

import (
	"context"
	"time"

	"server/pkg/llm"
)

type Dim string

const (
	Technical Dim = "technical"
	News      Dim = "news"
	Capital   Dim = "capital"
	External  Dim = "external"
)

var Dims = []Dim{Technical, News, Capital, External}

var dimNames = map[Dim]string{Technical: "技术面", News: "消息面", Capital: "资金面", External: "外围/情绪"}

// Fact 是事实包里的一条因子。Text 为空时是数值因子。
type Fact struct {
	ID    string  `json:"id"`
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Unit  string  `json:"unit,omitempty"`
	Text  string  `json:"text,omitempty"`
	Dim   Dim     `json:"dim"`
}

type Intel struct {
	ID         string    `json:"id"`
	Title      string    `json:"title"`
	Body       string    `json:"body"`
	Source     string    `json:"source,omitempty"`
	Time       time.Time `json:"time"`
	Sentiment  float64   `json:"sentiment"`
	Importance int       `json:"importance"`
	Symbol     string    `json:"symbol,omitempty"`
}

type FactPack struct {
	Symbol string    `json:"symbol"`
	Name   string    `json:"name"`
	AsOf   time.Time `json:"as_of"`
	Phase  string    `json:"phase,omitempty"`
	Facts  []Fact    `json:"facts"`
	Intel  []Intel   `json:"intel"`
}

func (p *FactPack) ids() map[string]bool {
	out := make(map[string]bool, len(p.Facts)+len(p.Intel))
	for _, f := range p.Facts {
		out[f.ID] = true
	}
	for _, n := range p.Intel {
		out[n.ID] = true
	}
	return out
}

type Reason struct {
	Text     string   `json:"text"`
	Evidence []string `json:"evidence"`
}

type DimScore struct {
	Dim     Dim      `json:"dim"`
	Score   int      `json:"score"`
	Reasons []Reason `json:"reasons"`
	Risks   []string `json:"risks"`
	Source  string   `json:"source"` // model / rule / failed
	Model   string   `json:"model,omitempty"`
}

type Analysis struct {
	DecisionID      int        `json:"decision_id"`
	TraceID         string     `json:"trace_id"`
	Symbol          string     `json:"symbol"`
	Dims            []DimScore `json:"dims"`
	Composite       float64    `json:"composite"`
	Weighted        float64    `json:"weighted"`
	Phase           string     `json:"phase"`
	PhaseCoef       float64    `json:"phase_coef"`
	Summary         string     `json:"summary"`
	SummaryEvidence []string   `json:"summary_evidence"`
	Degraded        bool       `json:"ai_degraded"`
	Discarded       bool       `json:"discarded"`
	Reason          string     `json:"reason,omitempty"`
	LatencyMS       int64      `json:"latency_ms"`
	RuleScore       float64    `json:"rule_score"`
}

type AnalyzeInput struct {
	Symbol    string
	Name      string
	TraceID   string
	Priority  llm.Priority
	RuleScore float64
	Phase     string
	Facts     []Fact
	Intel     []Intel
	AsOf      time.Time
}

type WatchItem struct {
	Symbol   string   `json:"symbol"`
	Reason   string   `json:"reason"`
	Evidence []string `json:"evidence"`
}

type PositionView struct {
	Symbol   string   `json:"symbol"`
	View     string   `json:"view"`
	Evidence []string `json:"evidence"`
}

type NewsLine struct {
	ID         string `json:"id"`
	Title      string `json:"title"`
	Importance int    `json:"importance"`
	Symbol     string `json:"symbol,omitempty"`
}

type Briefing struct {
	Date       time.Time      `json:"date"`
	Degraded   bool           `json:"degraded"`
	Headline   string         `json:"headline"`
	MarketView string         `json:"market_view"`
	Risks      []string       `json:"risks"`
	Watchlist  []WatchItem    `json:"watchlist"`
	Positions  []PositionView `json:"positions"`
	News       []NewsLine     `json:"news"`
	DecisionID int            `json:"decision_id"`
	CreatedAt  time.Time      `json:"created_at"`
	Reason     string         `json:"reason,omitempty"`
}

type ExplainInput struct {
	Symbol  string
	Event   string
	Holding bool
	TraceID string
	Facts   []Fact
}

type Explanation struct {
	DecisionID int      `json:"decision_id"`
	Summary    string   `json:"summary"`
	Impact     string   `json:"impact"`
	Risks      []string `json:"risks"`
	Evidence   []string `json:"evidence"`
	Degraded   bool     `json:"ai_degraded"`
	Discarded  bool     `json:"discarded"`
	Reason     string   `json:"reason,omitempty"`
}

type AskReply struct {
	DecisionID int      `json:"decision_id"`
	Answer     string   `json:"answer"`
	Evidence   []string `json:"evidence"`
	Degraded   bool     `json:"ai_degraded"`
	Discarded  bool     `json:"discarded"`
	Reason     string   `json:"reason,omitempty"`
}

// ProgressEvent 发到 brain.progress，只给在线客户端看（FR-05-10）。
type ProgressEvent struct {
	TraceID string `json:"trace_id"`
	Symbol  string `json:"symbol"`
	Stage   string `json:"stage"`
	Dim     Dim    `json:"dim,omitempty"`
	OK      bool   `json:"ok"`
}

// LLM 是 pkg/llm.Gateway 在 biz 层看到的样子。
type LLM interface {
	Chat(ctx context.Context, req llm.Request) (llm.Response, error)
	Stats(ctx context.Context) []llm.TierStats
}
