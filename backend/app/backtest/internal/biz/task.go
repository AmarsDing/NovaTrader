package biz

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"server/pkg/backtest"
)

// CreateRequest 是客户端建任务的输入。Config 未给的字段取 backtest.DefaultConfig。
type CreateRequest struct {
	Kind      string
	Name      string
	Config    json.RawMessage
	Search    json.RawMessage
	CreatedBy string
}

// CreateTask 校验配置后写一条 queued 任务。
func (uc *Usecase) CreateTask(ctx context.Context, req CreateRequest) (*Task, error) {
	if req.Kind == "" {
		req.Kind = KindSingle
	}
	if req.Kind != KindSingle && req.Kind != KindOptimize && req.Kind != KindWalkForward {
		return nil, fmt.Errorf("%w: kind 只能是 single / optimize / walkforward", ErrBadRequest)
	}
	cfg := backtest.DefaultConfig()
	cfg.LookaheadCheck = req.Kind == KindSingle
	if len(req.Config) > 0 {
		if err := json.Unmarshal(req.Config, &cfg); err != nil {
			return nil, fmt.Errorf("%w: config: %v", ErrBadRequest, err)
		}
	}
	spec := Spec{Config: cfg, MonteCarloRuns: uc.cfg.MonteCarloRuns}
	if req.Kind != KindSingle {
		sc := backtest.SearchConfig{Workers: uc.cfg.SearchWorkers}
		if len(req.Search) > 0 {
			if err := json.Unmarshal(req.Search, &sc); err != nil {
				return nil, fmt.Errorf("%w: search: %v", ErrBadRequest, err)
			}
		}
		if sc.Workers <= 0 || sc.Workers > uc.cfg.SearchWorkers {
			sc.Workers = uc.cfg.SearchWorkers
		}
		spec.Search = &sc
	}
	return uc.enqueue(ctx, req.Kind, req.Name, spec, req.CreatedBy, nil)
}

func (uc *Usecase) enqueue(ctx context.Context, kind, name string, spec Spec, by string, rerunOf *int) (*Task, error) {
	start, end, err := spec.Config.Normalize()
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrBadRequest, err)
	}
	if !end.Before(backtest.Day(uc.now()).AddDate(0, 0, 1)) {
		return nil, fmt.Errorf("%w: 结束日 %s 不能晚于今天", ErrBadRequest, spec.Config.End)
	}
	if name == "" {
		name = fmt.Sprintf("%s %s %s~%s", spec.Config.Strategy, kind, spec.Config.Start, spec.Config.End)
	}
	if by == "" {
		by = "local"
	}
	return uc.repo.CreateTask(ctx, &Task{
		Kind: kind, Name: name, Strategy: spec.Config.Strategy, Freq: spec.Config.Freq,
		Start: start, End: end, Spec: spec, Status: StatusQueued,
		ParamsHash: paramsHash(spec), EngineVersion: backtest.EngineVersion, BuildVersion: uc.build,
		Seed: spec.Config.Seed, RerunOf: rerunOf, CreatedBy: by,
	})
}

// paramsHash 是规范化后任务配置的指纹。encoding/json 对 map 键排序，同一份配置得到同一个值。
func paramsHash(spec Spec) string {
	b, _ := json.Marshal(spec)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func (uc *Usecase) GetTask(ctx context.Context, id int) (*Task, error) {
	return uc.repo.GetTask(ctx, id)
}

func (uc *Usecase) ListTasks(ctx context.Context, f TaskFilter) ([]*Task, int, error) {
	if f.Limit <= 0 || f.Limit > 200 {
		f.Limit = 50
	}
	return uc.repo.ListTasks(ctx, f)
}

func (uc *Usecase) Report(ctx context.Context, id int) (json.RawMessage, error) {
	return uc.repo.Report(ctx, id)
}

func (uc *Usecase) Trades(ctx context.Context, f TradeFilter) ([]backtest.Trade, int, error) {
	if f.Limit <= 0 || f.Limit > 1000 {
		f.Limit = 200
	}
	return uc.repo.Trades(ctx, f)
}

// CancelTask 排队中直接取消；运行中通过 context 取消，引擎在下一个交易日检查。
func (uc *Usecase) CancelTask(ctx context.Context, id int) (*Task, error) {
	t, err := uc.repo.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	switch t.Status {
	case StatusQueued:
		ok, err := uc.repo.CancelQueued(ctx, id)
		if err != nil {
			return nil, err
		}
		if !ok {
			return uc.CancelTask(ctx, id)
		}
	case StatusRunning:
		uc.mu.Lock()
		cancel := uc.running[id]
		uc.mu.Unlock()
		if cancel == nil {
			return nil, fmt.Errorf("%w: 任务 %d 不在本进程运行", ErrConflict, id)
		}
		cancel()
	default:
		return nil, fmt.Errorf("%w: 任务已%s", ErrConflict, t.Status)
	}
	return uc.repo.GetTask(ctx, id)
}

// RerunTask 复制配置新建任务，结束后与原任务比对哈希。
func (uc *Usecase) RerunTask(ctx context.Context, id int, by string) (*Task, error) {
	t, err := uc.repo.GetTask(ctx, id)
	if err != nil {
		return nil, err
	}
	if t.Kind == KindEvolve {
		return nil, fmt.Errorf("%w: 进化任务由自省生成，不能重跑", ErrBadRequest)
	}
	origin := t.ID
	if t.RerunOf != nil {
		origin = *t.RerunOf
	}
	return uc.enqueue(ctx, t.Kind, t.Name+" 重跑", t.Spec, by, &origin)
}

// Recover 把上次进程遗留的 running 任务置为失败。启动时调用一次。
func (uc *Usecase) Recover(ctx context.Context) error {
	n, err := uc.repo.FailRunning(ctx, "服务重启中断")
	if n > 0 {
		uc.log.Warnf("marked %d running tasks failed after restart", n)
	}
	return err
}

// RunNext 认领一条排队任务并执行。没有任务时返回 false。
func (uc *Usecase) RunNext(ctx context.Context) (bool, error) {
	t, err := uc.repo.ClaimTask(ctx)
	if err != nil || t == nil {
		return false, err
	}
	runCtx, cancel := context.WithCancel(ctx)
	uc.mu.Lock()
	uc.running[t.ID] = cancel
	uc.mu.Unlock()
	defer func() {
		cancel()
		uc.mu.Lock()
		delete(uc.running, t.ID)
		uc.mu.Unlock()
	}()
	uc.publish(ctx, t.ID, StatusRunning, 0, "开始")
	err = uc.execute(runCtx, t)
	switch {
	case err == nil:
		uc.publish(ctx, t.ID, StatusSucceeded, 1, "完成")
	case errors.Is(err, context.Canceled) && ctx.Err() == nil:
		if ferr := uc.repo.FinishTask(ctx, t.ID, StatusCancelled, "已取消", ""); ferr != nil {
			return true, ferr
		}
		uc.publish(ctx, t.ID, StatusCancelled, 0, "已取消")
	default:
		fctx := ctx
		if ctx.Err() != nil {
			var c context.CancelFunc
			fctx, c = context.WithTimeout(context.Background(), 5*time.Second)
			defer c()
		}
		uc.log.Errorf("task %d failed: %v", t.ID, err)
		if ferr := uc.repo.FinishTask(fctx, t.ID, StatusFailed, "失败", err.Error()); ferr != nil {
			return true, ferr
		}
		uc.publish(fctx, t.ID, StatusFailed, 0, err.Error())
	}
	return true, nil
}

func (uc *Usecase) publish(ctx context.Context, id int, status string, p float64, msg string) {
	if uc.pub != nil {
		uc.pub.Progress(ctx, ProgressPayload{TaskID: id, Status: status, Progress: p, Message: msg})
	}
}

// progress 每秒最多写一次库并发一条进度。stage 是当前阶段在全部阶段里的区间。
type progress struct {
	uc   *Usecase
	ctx  context.Context
	id   int
	mu   sync.Mutex
	last time.Time
	lo   float64
	hi   float64
	msg  string
}

func (p *progress) stage(lo, hi float64, msg string) {
	p.mu.Lock()
	p.lo, p.hi, p.msg = lo, hi, msg
	p.mu.Unlock()
	p.report(0, 1)
}

func (p *progress) report(done, total int) {
	p.mu.Lock()
	now := time.Now()
	if total <= 0 || (now.Sub(p.last) < time.Second && done < total) {
		p.mu.Unlock()
		return
	}
	p.last = now
	v := p.lo + (p.hi-p.lo)*float64(done)/float64(total)
	msg := fmt.Sprintf("%s %d/%d", p.msg, done, total)
	p.mu.Unlock()
	if err := p.uc.repo.ProgressTask(p.ctx, p.id, v, msg); err != nil && p.ctx.Err() == nil {
		p.uc.log.Warnf("task %d progress: %v", p.id, err)
	}
	p.uc.publish(p.ctx, p.id, StatusRunning, v, msg)
}

func (uc *Usecase) execute(ctx context.Context, t *Task) error {
	pg := &progress{uc: uc, ctx: ctx, id: t.ID}
	cfg := t.Spec.Config
	var (
		rep    *backtest.Report
		trades []backtest.Trade
	)
	switch t.Kind {
	case KindSingle:
		hi := 1.0
		if cfg.LookaheadCheck {
			hi = 0.5
		}
		pg.stage(0, hi, "回测")
		res, err := backtest.Run(ctx, cfg, uc.src, pg.report)
		if err != nil {
			return err
		}
		rep = backtest.BuildReport(t.Kind, res)
		if cfg.LookaheadCheck {
			pg.stage(0.5, 1, "截断自检")
			if rep.Lookahead, err = backtest.LookaheadCheck(ctx, cfg, uc.src, res); err != nil {
				return err
			}
		}
		rep.MonteCarlo = backtest.MonteCarlo(cfg.InitialCash, res.Equity, t.Spec.MonteCarloRuns, 5, cfg.Seed, uc.cfg.MaxDrawdown)
		trades = res.Trades
	case KindOptimize:
		pg.stage(0, 1, "参数搜索")
		out, oos, err := backtest.Optimize(ctx, cfg, *t.Spec.Search, uc.src, pg.report)
		if err != nil {
			return err
		}
		rep = reportOf(t.Kind, cfg, oos)
		rep.Optimize = out
		if oos != nil {
			trades = oos.Trades
		}
	case KindWalkForward:
		pg.stage(0, 1, "滚动前推")
		wf, comb, err := backtest.WalkForward(ctx, cfg, *t.Spec.Search, uc.src, pg.report)
		if err != nil {
			return err
		}
		rep = reportOf(t.Kind, cfg, comb)
		rep.WalkForward = wf
		trades = comb.Trades
	case KindEvolve:
		pg.stage(0, 1, "参数进化")
		ev := t.Spec.Evolve
		sc := backtest.SearchConfig{Workers: uc.cfg.SearchWorkers}
		if t.Spec.Search != nil {
			sc = *t.Spec.Search
		}
		out, err := backtest.Evolve(ctx, cfg, sc, ev.Candidates, ev.ISDays, ev.OOSDays, uc.src)
		if err != nil {
			return err
		}
		rep = &backtest.Report{Kind: t.Kind, Config: cfg, Evolve: out, Assumptions: backtest.Assumptions(cfg)}
		rep.Metrics = out.BestOOS
		if err := uc.propose(ctx, t, out); err != nil {
			return err
		}
	default:
		return fmt.Errorf("unknown task kind %q", t.Kind)
	}
	if t.RerunOf != nil {
		rep.Reproduce = uc.reproduce(ctx, *t.RerunOf, rep)
	}
	return uc.repo.SaveResult(ctx, t.ID, rep, trades, summarize(rep))
}

func reportOf(kind string, cfg backtest.Config, res *backtest.Result) *backtest.Report {
	if res == nil {
		return &backtest.Report{Kind: kind, Config: cfg, Assumptions: backtest.Assumptions(cfg)}
	}
	rep := backtest.BuildReport(kind, res)
	rep.Config = cfg
	return rep
}

func summarize(rep *backtest.Report) Summary {
	m := rep.Metrics
	s := Summary{
		TotalReturn: m.TotalReturn, AnnualReturn: m.AnnualReturn, MaxDrawdown: m.MaxDrawdown,
		Sharpe: m.Sharpe, WinRate: m.WinRate, ProfitFactor: m.ProfitFactor, Rounds: m.Rounds,
		FillsHash: rep.FillsHash,
	}
	if rep.Lookahead != nil {
		pass := rep.Lookahead.Pass
		s.LookaheadPass = &pass
	}
	for _, f := range flagsOf(rep) {
		if f == "样本外衰减" {
			s.OOSDecay = true
		}
	}
	return s
}

func flagsOf(rep *backtest.Report) []string {
	switch {
	case rep.Optimize != nil:
		return rep.Optimize.Flags
	case rep.WalkForward != nil:
		return rep.WalkForward.Flags
	}
	return nil
}

// reproduce 比对重跑与原任务：数据哈希不同说明数据变了；数据相同而成交哈希不同说明不可复现。
func (uc *Usecase) reproduce(ctx context.Context, origin int, rep *backtest.Report) *backtest.Reproduce {
	out := &backtest.Reproduce{OriginTaskID: origin}
	o, err := uc.repo.GetTask(ctx, origin)
	if err != nil || o.Summary == nil {
		out.Verdict = "原任务没有报告，无法比对"
		return out
	}
	out.SameData = o.DataHash == rep.DataHash
	out.SameFills = o.Summary.FillsHash == rep.FillsHash
	switch {
	case o.EngineVersion != backtest.EngineVersion:
		out.Verdict = "引擎版本不同（" + o.EngineVersion + " → " + backtest.EngineVersion + "），不做比对"
	case !out.SameData:
		out.Verdict = "数据已变"
	case out.SameFills:
		out.Verdict = "复现一致"
	default:
		out.Verdict = "不一致"
	}
	return out
}
