// Package biz 是 backtest 的业务编排：任务、影子、自省、进化提案、周报月报、准入检查。
// 计算在 pkg/backtest，这里只管读写和流程。见 doc/开发文档/M07-回测与自进化/设计文档.md。
package biz

import (
	"context"
	"encoding/json"
	"errors"
	"runtime/debug"
	"sync"
	"time"

	"server/conf"
	"server/pkg/backtest"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"

	_ "server/pkg/rules/refstrat"
)

var ProviderSet = wire.NewSet(NewSettings, NewUsecase)

const SubjectProgress = "backtest.progress"

const (
	KindSingle      = "single"
	KindOptimize    = "optimize"
	KindWalkForward = "walkforward"
	KindEvolve      = "evolve"

	StatusQueued    = "queued"
	StatusRunning   = "running"
	StatusSucceeded = "succeeded"
	StatusFailed    = "failed"
	StatusCancelled = "cancelled"

	ProposalPending    = "pending"
	ProposalApproved   = "approved"
	ProposalRejected   = "rejected"
	ProposalRolledBack = "rolled_back"
)

var (
	ErrNotFound   = errors.New("not found")
	ErrBadRequest = errors.New("bad request")
	ErrConflict   = errors.New("conflict")
)

// Spec 是任务 config 列的内容：引擎配置加上各类任务自己的参数。
type Spec struct {
	Config         backtest.Config        `json:"config"`
	Search         *backtest.SearchConfig `json:"search,omitempty"`
	Evolve         *EvolveSpec            `json:"evolve,omitempty"`
	MonteCarloRuns int                    `json:"monte_carlo_runs"`
}

// EvolveSpec 是 evolve 任务的输入：有界候选和样本内外交易日数。
type EvolveSpec struct {
	Candidates      []map[string]float64 `json:"candidates"`
	ISDays          int                  `json:"is_days"`
	OOSDays         int                  `json:"oos_days"`
	IntrospectionID int                  `json:"introspection_id"`
}

type Summary struct {
	TotalReturn   float64
	AnnualReturn  float64
	MaxDrawdown   float64
	Sharpe        float64
	WinRate       float64
	ProfitFactor  float64
	Rounds        int
	LookaheadPass *bool
	OOSDecay      bool
	FillsHash     string
}

type Task struct {
	ID            int
	Kind          string
	Name          string
	Strategy      string
	Freq          string
	Start         time.Time
	End           time.Time
	Spec          Spec
	Status        string
	Progress      float64
	Message       string
	Error         string
	ParamsHash    string
	EngineVersion string
	BuildVersion  string
	Seed          int64
	DataHash      string
	DataCutoff    *time.Time
	RerunOf       *int
	CreatedBy     string
	CreatedAt     time.Time
	StartedAt     *time.Time
	FinishedAt    *time.Time
	Summary       *Summary
}

type TaskFilter struct {
	Status, Strategy, Kind string
	Limit, Offset          int
}

type TradeFilter struct {
	TaskID        int
	Symbol        string
	Limit, Offset int
}

type Shadow struct {
	ID            int
	DecisionID    int
	Symbol        string
	Model         string
	PromptVersion string
	DecisionType  string
	Score         *float64
	DecidedAt     time.Time
	RefDate       *time.Time
	RefPrice      *float64
	RetT1         *float64
	RetT3         *float64
	RetT5         *float64
	Status        string
}

type ShadowFilter struct {
	Model, PromptVersion string
	From, To             time.Time
}

// Candidate 是 M06 strategy_candidates 的一行和它关联信号的执行情况。
type Candidate struct {
	TradeDate  time.Time
	Strategy   string
	Symbol     string
	Stage      string
	MissReason string
	SignalID   *int
	Ret        map[int]*float64
	// 信号字段，没关联信号时为零值
	SignalStatus string
	ExitKind     string
	NewsDim      float64
}

type Feedback struct {
	TradeDate     time.Time
	Symbol        string
	Strategy      string
	SignalID      *int
	Actual        float64
	Correct       *bool
	FalseNegative bool
	Attribution   string
	Analysis      string
}

type Introspection struct {
	ID            int
	TradeDate     time.Time
	Book          string
	WindowDays    int
	SampleCount   int
	WinRate       float64
	AvgReturn     float64
	MaxDrawdown   float64
	MissedCount   int
	Attribution   map[string]int
	Triggered     bool
	TriggerReason string
	TaskID        *int
	Note          string
	CreatedAt     time.Time
}

type Proposal struct {
	ID            int
	Strategy      string
	BaseParams    map[string]float64
	Params        map[string]float64
	Reason        string
	TaskID        *int
	BaseMetrics   json.RawMessage
	NewMetrics    json.RawMessage
	Status        string
	VersionID     *int
	PrevVersionID *int
	DecidedBy     string
	CreatedAt     time.Time
	DecidedAt     *time.Time
}

type ProposalFilter struct {
	Status, Strategy string
	Limit            int
}

// Repo 是 biz 需要的全部读写。实现见 data.Repo。
type Repo interface {
	CreateTask(ctx context.Context, t *Task) (*Task, error)
	GetTask(ctx context.Context, id int) (*Task, error)
	ListTasks(ctx context.Context, f TaskFilter) ([]*Task, int, error)
	ClaimTask(ctx context.Context) (*Task, error)
	ProgressTask(ctx context.Context, id int, progress float64, msg string) error
	CancelQueued(ctx context.Context, id int) (bool, error)
	FinishTask(ctx context.Context, id int, status, msg, errText string) error
	SaveResult(ctx context.Context, id int, rep *backtest.Report, trades []backtest.Trade, sum Summary) error
	FailRunning(ctx context.Context, msg string) (int, error)
	ActiveEvolve(ctx context.Context, strategy string) (bool, error)
	Report(ctx context.Context, taskID int) (json.RawMessage, error)
	Trades(ctx context.Context, f TradeFilter) ([]backtest.Trade, int, error)
	LatestSucceeded(ctx context.Context, strategy string, kinds ...string) (*Task, error)
	TasksFinished(ctx context.Context, from, to time.Time) ([]*Task, error)

	MaxShadowDecision(ctx context.Context) (int, error)
	NewDecisions(ctx context.Context, afterID, limit int) ([]Shadow, error)
	InsertShadows(ctx context.Context, rows []Shadow) error
	OpenShadows(ctx context.Context) ([]Shadow, error)
	UpdateShadow(ctx context.Context, s Shadow) error
	Shadows(ctx context.Context, f ShadowFilter) ([]Shadow, error)
	DayBars(ctx context.Context, symbol string, from, to time.Time) ([]backtest.DayBar, error)

	Candidates(ctx context.Context, from, to time.Time) ([]Candidate, error)
	PhaseOn(ctx context.Context, day time.Time) (string, error)
	SaveFeedback(ctx context.Context, rows []Feedback) error
	Equity(ctx context.Context, book string, from, to time.Time) ([]backtest.EquityPoint, error)
	SaveIntrospection(ctx context.Context, in *Introspection) (*Introspection, error)
	Introspections(ctx context.Context, book string, from, to time.Time, limit int) ([]*Introspection, error)

	ConfigValues(ctx context.Context, keys []string) (map[string]string, error)
	CreateProposal(ctx context.Context, p *Proposal) (*Proposal, error)
	GetProposal(ctx context.Context, id int) (*Proposal, error)
	ListProposals(ctx context.Context, f ProposalFilter) ([]*Proposal, error)
	ProposalsBetween(ctx context.Context, from, to time.Time) ([]*Proposal, error)
	HasPendingProposal(ctx context.Context, strategy string) (bool, error)
	ApproveProposal(ctx context.Context, id int, values map[string]string, user string) (*Proposal, error)
	RejectProposal(ctx context.Context, id int, user, note string) (*Proposal, error)
	RollbackProposal(ctx context.Context, strategy, user string) (*Proposal, error)
}

// ProgressPayload 是 backtest.progress 的内容。
type ProgressPayload struct {
	TaskID   int     `json:"task_id"`
	Status   string  `json:"status"`
	Progress float64 `json:"progress"`
	Message  string  `json:"message"`
}

type Publisher interface {
	Progress(ctx context.Context, p ProgressPayload)
}

// Settings 是 conf.Backtest 补齐默认值后的结果。
type Settings struct {
	Workers          int
	SearchWorkers    int
	EvolveStrategies []string
	Book             string

	WindowDays     int
	Horizon        int
	TriggerDays    int
	WinRateBelow   float64
	MinSamples     int
	MissedReturn   float64
	EvolveISDays   int
	EvolveOOSDays  int
	EvolveCands    int
	EvolveFreq     string
	MonteCarloRuns int

	MinTrades        int
	MaxDrawdown      float64
	MinAnnual        float64
	MinProfitFactor  float64
	MinSharpe        float64
	PaperDays        int
	PaperMaxDrawdown float64
}

func orInt(v int32, d int) int {
	if v > 0 {
		return int(v)
	}
	return d
}

func orFloat(v, d float64) float64 {
	if v > 0 {
		return v
	}
	return d
}

func NewSettings(c *conf.Backtest) Settings {
	s := Settings{
		Workers:          orInt(c.GetWorkers(), 1),
		SearchWorkers:    orInt(c.GetSearchWorkers(), 2),
		EvolveStrategies: c.GetEvolveStrategies(),
		Book:             c.GetIntrospectBook(),
		MonteCarloRuns:   1000,
	}
	if s.Book == "" {
		s.Book = "paper"
	}
	in := c.GetIntrospect()
	s.WindowDays = orInt(in.GetWindowDays(), 30)
	s.Horizon = orInt(in.GetHorizon(), 3)
	if s.Horizon != 1 && s.Horizon != 3 && s.Horizon != 5 {
		s.Horizon = 3
	}
	s.TriggerDays = orInt(in.GetTriggerDays(), 5)
	s.WinRateBelow = orFloat(in.GetWinRateBelow(), 0.4)
	s.MinSamples = orInt(in.GetMinSamples(), 10)
	s.MissedReturn = orFloat(in.GetMissedReturn(), 0.05)
	s.EvolveISDays = orInt(in.GetEvolveIsDays(), 120)
	s.EvolveOOSDays = orInt(in.GetEvolveOosDays(), 60)
	s.EvolveCands = orInt(in.GetEvolveCandidates(), 20)
	s.EvolveFreq = in.GetEvolveFreq()
	if s.EvolveFreq == "" {
		s.EvolveFreq = backtest.Freq1d
	}
	a := c.GetAdmission()
	s.MinTrades = orInt(a.GetMinTrades(), 30)
	s.MaxDrawdown = orFloat(a.GetMaxDrawdown(), 0.20)
	s.MinAnnual = a.GetMinAnnual()
	s.MinProfitFactor = orFloat(a.GetMinProfitFactor(), 1.2)
	s.MinSharpe = orFloat(a.GetMinSharpe(), 0.5)
	s.PaperDays = orInt(a.GetPaperDays(), 20)
	s.PaperMaxDrawdown = orFloat(a.GetPaperMaxDrawdown(), 0.10)
	return s
}

type Usecase struct {
	repo Repo
	src  backtest.Source
	pub  Publisher
	cfg  Settings
	log  *log.Helper
	now  func() time.Time

	build string

	mu      sync.Mutex
	running map[int]context.CancelFunc
}

func NewUsecase(repo Repo, src backtest.Source, pub Publisher, cfg Settings, logger log.Logger) *Usecase {
	return &Usecase{
		repo: repo, src: src, pub: pub, cfg: cfg,
		log:     log.NewHelper(log.With(logger, "module", "backtest/biz")),
		now:     time.Now,
		build:   buildVersion(),
		running: map[int]context.CancelFunc{},
	}
}

func (uc *Usecase) Settings() Settings { return uc.cfg }

func buildVersion() string {
	info, ok := debug.ReadBuildInfo()
	if !ok {
		return "unknown"
	}
	rev, dirty := "", false
	for _, s := range info.Settings {
		switch s.Key {
		case "vcs.revision":
			rev = s.Value
		case "vcs.modified":
			dirty = s.Value == "true"
		}
	}
	if rev == "" {
		return info.Main.Version
	}
	if len(rev) > 12 {
		rev = rev[:12]
	}
	if dirty {
		rev += "-dirty"
	}
	return rev
}
