package core

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"server/app/trade/internal/biz"
	"server/conf"
	"server/pkg/appcfg"
	"server/pkg/brokerif"
	"server/pkg/events"
	"server/pkg/market"
	"server/pkg/observe"
	"server/pkg/outbox"
	"server/pkg/registry"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/uuid"
	"github.com/google/wire"
	"github.com/nats-io/nats.go"
)

var ProviderSet = wire.NewSet(NewRunner, NewChannelTracker, NewLiveChannel)

// NewChannelTracker 建 sim 执行通道的状态机单例（G7）：Runner 记心跳，service 读状态面。
func NewChannelTracker() *brokerif.Tracker { return brokerif.NewTracker(time.Now()) }

// Runner 每 10 秒记一次模拟账户快照，投递 outbox，并用分钟线撮合未完成的模拟委托。
// 快照回路同时充当 sim 执行通道的心跳（G7）：连续失败按 brokerif.Observer 口径
// 折算为通道降级/停机，迁移发布到 trade.channel.status。
type Runner struct {
	now     func() time.Time
	e       *biz.Engine
	bus     *events.Bus
	publish func(ctx context.Context, env events.Envelope) error
	box     *outbox.Dispatcher
	track   *brokerif.Tracker
	obs     *brokerif.Observer
	live    *LiveChannel
	// reportSrc 可选注入（测试用 broker-gw simulator 语义的假来源）；
	// 生产由 live.Client 解析——接口注入，回报回路不经测试网络。
	reportSrc ReportSource
	// reportAcct 是假来源的回声账户（仅测试装配用）：ParseSnapshot 的账户
	// 回声校验在两条路径都走同一份校验，测试不豁免身份门槛。
	reportAcct string
	// liveUnknownBaseline 是 transport_unknown 告警的边沿基线：只记探测循环一根
	// goroutine，无需加锁。归零重武装、上升沿才打屏（告警转净纪律，同 U6 族）。
	liveUnknownBaseline int
	beater              *registry.Beater
	addr                string
	id                  string
	interval            time.Duration
	log                 *log.Helper
	sub                 *nats.Subscription
	exitSub             *nats.Subscription
	sigSub              *nats.Subscription
	cancel              context.CancelFunc
	watchOwner          string
	lifecycle           sync.Mutex
	runCtx              context.Context
	started             bool
	stopping            bool
	done                chan struct{}
	wg                  sync.WaitGroup
}

func NewRunner(c *conf.Trade, e *biz.Engine, ch *brokerif.Tracker, live *LiveChannel, beat *registry.Beater, bus *events.Bus, box *outbox.Dispatcher, logger log.Logger) *Runner {
	every := 10 * time.Second
	if c != nil && c.GetSnapshotInterval() != nil && c.GetSnapshotInterval().AsDuration() > 0 {
		every = c.GetSnapshotInterval().AsDuration()
	}
	if ch == nil {
		ch = NewChannelTracker()
	}
	id, _ := os.Hostname()
	addr := ":2091"
	if h := c.GetHttp(); h != nil && h.GetAddr() != "" {
		addr = h.GetAddr()
	}
	r := &Runner{e: e, bus: bus, box: box, interval: every, log: log.NewHelper(logger), watchOwner: "trade." + uuid.NewString(), beater: beat, addr: addr, id: id}
	if bus != nil {
		r.publish = bus.Publish
	}
	r.track = ch
	r.obs = brokerif.NewObserver(ch, time.Now)
	r.track.Subscribe(func(from, to brokerif.Status, at time.Time) {
		r.publishChannel("sim", from, to, at)
	})
	// emfile 通道（G7 服务端接线）：未配置时 live 为 nil、状态面如实缺席；
	// 起不来的半件实例已在装配期如实停在 down（装配早于这里的订阅，那一笔不补发事件，
	// 读面第一次查询即如实返回 down，与「没取到≠0」同口径：如实表达，不回填假事件）。
	if live != nil && live.Tracker != nil {
		r.live = live
		live.Tracker.Subscribe(func(from, to brokerif.Status, at time.Time) {
			r.publishChannel("emfile", from, to, at)
		})
	}
	// M13-T03：emfile 客户端可用时把文件单出口挂进引擎（confirm/cancel 两段式
	// 出口链）。双闸（LiveEnabled 经 data.Settings、BrokerConnected 经核验链置位）
	// 仍是必经门禁——本接线只提供通道，不放行任何实盘委托。
	if live != nil && live.Client != nil {
		e.AttachFileBroker(live.Client)
	}
	return r
}

// Channel 暴露 sim 执行通道的状态机，供状态读面与测试订阅（G7）。
func (r *Runner) Channel() *brokerif.Tracker { return r.track }

// channelOK/channelFail 是 nil 安全的心跳记账；测试里按字面量拼的 Runner 没有状态机。
func (r *Runner) channelOK() {
	if r.obs != nil {
		r.obs.Ok()
	}
}

func (r *Runner) channelFail() {
	if r.obs != nil {
		r.obs.Fail()
	}
}

// publishChannel 把每次状态迁移投递到 trade.channel.status（随 trade.> 走 JetStream）。
// 回调由 Tracker 在锁外同步触发，发布带 5s 上限，不会反压状态机。
// 链纪律（决策 C）：Tracker 回调结构上不带因果父，每次迁移即一条根链——OpenTrace
// 幂等开新链、不编 causation_id；若未来 Tracker 透传上游链，OpenTrace 尊重不另开。
// channel 参数区分 sim（快照心跳折算）与 emfile（broker-gw 健康探测折算）两条通道。
func (r *Runner) publishChannel(channel string, from, to brokerif.Status, at time.Time) {
	if r.publish == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = events.OpenTrace(ctx)
	env, err := events.NewWithContext(ctx, "trade", events.SubjectTradeChannel, map[string]any{
		"channel": channel, "from": string(from), "to": string(to),
		"at": at.UTC().Format(time.RFC3339),
	})
	if err != nil {
		r.log.Warnf("channel event: %v", err)
		return
	}
	if err := r.publish(ctx, env); err != nil {
		r.log.Warnf("publish channel %s→%s: %v", from, to, err)
	}
}

func (r *Runner) Start(ctx context.Context) error {
	r.lifecycle.Lock()
	if r.stopping {
		r.lifecycle.Unlock()
		return fmt.Errorf("trade runner is stopped; create a new runner to restart")
	}
	if r.started {
		r.lifecycle.Unlock()
		return nil
	}
	r.started = true
	r.runCtx, r.cancel = context.WithCancel(ctx)
	r.done = make(chan struct{})
	ctx = r.runCtx
	// Stop also waits for startup, so it cannot miss a subscription being installed.
	r.wg.Add(1)
	r.lifecycle.Unlock()
	defer r.wg.Done()
	startupCtx, startupCancel := context.WithTimeout(ctx, 10*time.Second)
	defer startupCancel()
	if err := observedTradeCall("snapshot", func() error { _, err := r.e.Snapshot(startupCtx, "SIM"); return err }); err != nil {
		r.beginStop()
		return err
	}
	if err := observedTradeCall("intent_recovery", func() error { return r.e.RecoverIntents(startupCtx) }); err != nil && startupCtx.Err() == nil {
		r.log.Warnf("recover intents: %v", err)
	}
	if err := startupCtx.Err(); err != nil {
		r.beginStop()
		return err
	}
	sub, err := r.bus.Conn().Subscribe(events.SubjectMarketBar, r.onBars)
	if err != nil {
		r.log.Warnf("subscribe bars: %v", err)
	} else {
		r.keepSubscription(sub, false)
	}
	if sub, err = r.subscribeExit(startupCtx); err != nil {
		r.log.Warnf("subscribe exit: %v", err)
	} else {
		r.keepSubscription(sub, true)
	}
	if sub, err = r.subscribeSignal(startupCtx); err != nil {
		r.log.Warnf("subscribe signals: %v", err)
	} else {
		r.keepSignalSubscription(sub)
	}
	r.lifecycle.Lock()
	if r.stopping || ctx.Err() != nil {
		r.lifecycle.Unlock()
		r.beginStop()
		return context.Canceled
	}
	for _, run := range []func(context.Context){r.loop, r.evidenceLoop, r.watchLoop, r.liveProbeLoop, r.reportLoop, r.beatLoop} {
		r.wg.Add(1)
		go func(run func(context.Context)) {
			defer r.wg.Done()
			if ctx.Err() == nil {
				run(ctx)
			}
		}(run)
	}
	r.lifecycle.Unlock()
	// 引擎装配（首帧快照）已是 Start 前置条件；走到这里 sim 通道视为连上。
	r.channelOK()
	return nil
}

// liveProbeLoop 是 emfile 通道的健康心跳：按 LiveChannel.Interval 节拍探测 broker-gw，
// 成败由 remote.Client 按 Observer 口径折算进 LiveChannel.Tracker（与 sim 同一份状态机、
// 同一份 publishChannel 纪律）。未配置（nil）或配置不合法的半件实例（Client 为 nil）
// 直接返回不占循环——前者状态面如实缺席，后者已在装配期如实停在 down。
func (r *Runner) liveProbeLoop(ctx context.Context) {
	if r.live == nil || r.live.Client == nil {
		return
	}
	ticker := time.NewTicker(r.live.Interval())
	defer ticker.Stop()
	r.probeLive(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			r.probeLive(ctx)
		}
	}
}

// probeLive 做一次健康探测并折算 LIVE 通道闸（M13-T05）：探测成功置位
// BrokerConnected，任何失败立即回落 false（fail-closed）——闸只由这条核验链
// 驱动，无配置/界面旁路；引擎未装配（测试裸 Runner）时探测记账照旧、不翻闸。
func (r *Runner) probeLive(ctx context.Context) {
	if ctx.Err() != nil || r.live == nil || r.live.Client == nil {
		return
	}
	c, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	done := observe.ForService("trade").Op("live_probe").Begin()
	health, err := r.live.Probe(c)
	if err != nil {
		observe.FinishError(done, err)
		if r.e != nil {
			r.e.SetBrokerConnected(false)
		}
		if c.Err() == nil {
			r.log.Warnf("emfile probe: %v", err)
		}
		return
	}
	done(observe.Succeeded)
	if r.e != nil {
		r.e.SetBrokerConnected(true)
	}
	r.noteLiveUnknown(health.TransportUnknown)
}

// noteLiveUnknown 是 transport_unknown 的告警边沿纪律（D7）：回执滞留 PREPARED/UNKNOWN
// 意味文件单到量化终端的结局未知，按决策 3 只能人工上机核对、严禁重发同一 SID。
// 计数上升沿才告警（M10 再按 dedup_key 合并同义重复），计数归零重新武装——同源
// 滞留数不变不重复打屏，恢复后新发能再次提醒。
func (r *Runner) noteLiveUnknown(unknown int) {
	if unknown <= 0 {
		r.liveUnknownBaseline = 0
		return
	}
	if unknown <= r.liveUnknownBaseline {
		return
	}
	r.liveUnknownBaseline = unknown
	r.publishLiveUnknown(unknown)
}

// publishLiveUnknown 把 transport_unknown 上升沿投递到 notify.request（L2=warn，
// category=trade 与 alert-levels.ts levelOf 对齐）。与 publishChannel 同一链路纪律：
// OpenTrace 幂等开根链、5s 发布上限、失败只记日志不反压探测循环。
func (r *Runner) publishLiveUnknown(unknown int) {
	if r.publish == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = events.OpenTrace(ctx)
	env, err := events.NewWithContext(ctx, "trade", events.SubjectNotifyRequest, map[string]any{
		"level":    "warn",
		"category": "trade",
		"title":    "东财文件单传输回执未知",
		"body": fmt.Sprintf("broker-gw 报告 %d 笔委托/撤单回执处于 PREPARED/UNKNOWN：文件是否被量化终端执行结局未知。"+
			"严禁重发同一 SID；先上机核对成交回报，再按原 SID 只读查询。", unknown),
		"dedup_key": "trade:live-transport-unknown",
	})
	if err != nil {
		r.log.Warnf("live unknown event: %v", err)
		return
	}
	if err := r.publish(ctx, env); err != nil {
		r.log.Warnf("publish live transport unknown: %v", err)
	}
}

const beatEvery = 15 * time.Second

// beat 是 serviceinstance 心跳记录纪律：失败只在父 ctx 未取消时记日志，Beat 本身
// 为 nil 接收者返回错误而不 panic，便于测试里按字面量拼 Runner 时省略 beater。
func (r *Runner) beat(ctx context.Context) {
	if err := r.beater.Beat(ctx, "trade", r.id, r.addr); err != nil && ctx.Err() == nil {
		r.log.Errorf("heartbeat: %v", err)
	}
}

// beatLoop 与 loop/evidenceLoop/watchLoop/liveProbeLoop 同一派发纪律：Start 内统一
// wg.Add(1)+go run(ctx)，defer wg.Done 由外层包装负责。beater 为 nil（测试装配）
// 时直接返回，不占循环。
func (r *Runner) beatLoop(ctx context.Context) {
	if r.beater == nil {
		return
	}
	t := time.NewTicker(beatEvery)
	defer t.Stop()
	r.beat(ctx)
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			r.beat(ctx)
		}
	}
}

const exitConsumer = "trade_risk_exit"

func (r *Runner) subscribeExit(ctx context.Context) (*nats.Subscription, error) {
	js := r.bus.JetStream()
	info, err := js.ConsumerInfo(events.StreamName, exitConsumer, nats.Context(ctx))
	if errors.Is(err, nats.ErrConsumerNotFound) {
		info, err = js.AddConsumer(events.StreamName, &nats.ConsumerConfig{
			Durable: exitConsumer, FilterSubject: events.SubjectRiskExit, DeliverSubject: nats.NewInbox(),
			DeliverPolicy: nats.DeliverAllPolicy, AckPolicy: nats.AckExplicitPolicy,
			AckWait: 30 * time.Second, MaxAckPending: 64,
		}, nats.Context(ctx))
	}
	if err != nil {
		return nil, err
	}
	if info.Config.FilterSubject != events.SubjectRiskExit || info.Config.AckPolicy != nats.AckExplicitPolicy || info.Config.DeliverSubject == "" || info.Config.DeliverGroup != "" {
		return nil, fmt.Errorf("risk exit durable has incompatible configuration")
	}
	// Explicitly bind a durable owned by the server. Unsubscribe on shutdown then
	// stops callbacks without deleting its acknowledgement/recovery position.
	return js.Subscribe(events.SubjectRiskExit, r.onExit, nats.Bind(events.StreamName, exitConsumer), nats.ManualAck())
}

// signalConsumer 是 M13-T02 自动消费者的 durable 名：重启/重挂续用同一
// ack 位置，完成消费的信号事件不被重放（重放也只会命中 execution_intent 唯一
// 约束而跳过并计数）。ack 纪律与 risk.exit 消费者一致：显式 ack、30s 窗。
const signalConsumer = "trade_signal_auto"

func (r *Runner) subscribeSignal(ctx context.Context) (*nats.Subscription, error) {
	js := r.bus.JetStream()
	info, err := js.ConsumerInfo(events.StreamName, signalConsumer, nats.Context(ctx))
	if errors.Is(err, nats.ErrConsumerNotFound) {
		info, err = js.AddConsumer(events.StreamName, &nats.ConsumerConfig{
			Durable: signalConsumer, FilterSubject: events.SubjectSignal, DeliverSubject: nats.NewInbox(),
			DeliverPolicy: nats.DeliverAllPolicy, AckPolicy: nats.AckExplicitPolicy,
			AckWait: 30 * time.Second, MaxAckPending: 64,
		}, nats.Context(ctx))
	}
	if err != nil {
		return nil, err
	}
	if info.Config.FilterSubject != events.SubjectSignal || info.Config.AckPolicy != nats.AckExplicitPolicy || info.Config.DeliverSubject == "" || info.Config.DeliverGroup != "" {
		return nil, fmt.Errorf("signal auto durable has incompatible configuration")
	}
	return js.Subscribe(events.SubjectSignal, r.onSignal, nats.Bind(events.StreamName, signalConsumer), nats.ManualAck())
}

// onSignal 解开 strategy.signal.updated 的信封，过滤链与消费动作收口到
// biz.AutoConsume（终审仍在 place() 内联风控，契约见 automation.go 头注）。
// ack 纪律：信封/载荷不可信即 ack 丢弃；过滤或去重跳过即 ack；处理成功才 ack；
// 消费失败先投 notify（critical）再 nak 让 durable 重投——残余意图已占位，
// 重放只会命中唯一约束跳过并计数，绝不产生第二意图。
func (r *Runner) onSignal(msg *nats.Msg) {
	op := observe.ForService("trade").Op("signal_auto")
	op.Received()
	env, err := events.Unmarshal(msg.Data)
	var observed func(observe.Outcome)
	if err == nil {
		observed = op.BeginFrom(env.SourceOrZero(), env.Time)
	} else {
		observed = op.Begin()
	}
	outcome := observe.Rejected
	defer func() { observed(outcome) }()
	ctx, finish, active := r.beginCallback()
	if !active {
		outcome = observe.Cancelled
		return // Leave unacknowledged for the next active instance.
	}
	defer finish()
	if err != nil || env.Subject != events.SubjectSignal || env.Source != "strategy" || msg.Subject != events.SubjectSignal {
		r.log.Warnf("signal envelope: %v", err)
		_ = msg.Ack()
		return
	}
	var body struct {
		SignalID   int     `json:"signal_id"`
		Book       string  `json:"book"`
		Status     string  `json:"status"`
		StockCode  string  `json:"stock_code"`
		Strategy   string  `json:"strategy"`
		SignalType string  `json:"signal_type"`
		FinalScore float64 `json:"final_score"`
		AIDegraded bool    `json:"ai_degraded"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil || body.SignalID <= 0 {
		r.log.Warnf("signal payload: %v", err)
		_ = msg.Ack()
		return
	}
	if ctx.Err() != nil {
		outcome = observe.Cancelled
		return
	}
	out, err := r.e.AutoConsume(events.ContextWithEnvelope(ctx, env), biz.AutoSignal{
		SignalID: body.SignalID, Book: body.Book, Status: body.Status, Symbol: body.StockCode,
		Strategy: body.Strategy, SignalType: body.SignalType, FinalScore: body.FinalScore, AIDegraded: body.AIDegraded,
	}, r.autoGates(ctx))
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = observe.Cancelled
		} else {
			outcome = observe.Failed
		}
		r.log.Warnf("signal auto %d %s: %v", body.SignalID, body.StockCode, err)
		if ctx.Err() == nil {
			r.publishAutoFailure(body.SignalID, body.StockCode, err)
			_ = msg.Nak()
		}
		return
	}
	if out.Skipped != "" {
		outcome = observe.Skipped
		r.log.Debugf("signal auto %d skipped: %s", body.SignalID, out.Skipped)
	} else {
		outcome = observe.Succeeded
		r.log.Infof("signal auto %d: plan %s authorized", body.SignalID, out.Plan.Plan.PlanID)
	}
	_ = msg.Ack()
}

// autoGates 是 M13-T02 闸门三键的运行期读取（appcfg 5s 缓存，策略化文本市
// app.trade.auto.score_min/.templates/.allow_degraded，白名单在 pkg/params）。
// 键缺省/非法一律回落出厂闸门（默认两模板、阈值 80、AI 降级不开新仓），
// 非法值被当成缺省表达而不是被当成 0/空集——「没取到≠0」。
func (r *Runner) autoGates(ctx context.Context) biz.AutoGates {
	g := biz.DefaultAutoGates()
	if v := strings.TrimSpace(appcfg.Get(ctx, "app.trade.auto.score_min")); v != "" {
		if n, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && n >= 0 && n <= 100 {
			g.ScoreMin = n
		}
	}
	if v := strings.TrimSpace(appcfg.Get(ctx, "app.trade.auto.templates")); v != "" {
		var names []string
		if err := json.NewDecoder(strings.NewReader(v)).Decode(&names); err == nil {
			set := map[string]bool{}
			for _, n := range names {
				if s := strings.TrimSpace(n); s != "" {
					set[s] = true
				}
			}
			if len(set) > 0 {
				g.Templates = set
			}
		}
	}
	if v := strings.TrimSpace(appcfg.Get(ctx, "app.trade.auto.allow_degraded")); v != "" {
		g.AllowDegraded = v == "true"
	}
	return g
}

// publishAutoFailure 把自动消费失败投递到 notify.request（level=critical 与
// notify 分类器对齐，category=trade）。dedup_key 按信号归并：同一信号的失败
// 只留最新一条。与 publishChannel 同一链路纪律：OpenTrace 幂等开根链、5s
// 发布上限、失败只记日志不反压信号消费。
func (r *Runner) publishAutoFailure(signalID int, symbol string, cause error) {
	if r.publish == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	ctx = events.OpenTrace(ctx)
	env, err := events.NewWithContext(ctx, "trade", events.SubjectNotifyRequest, map[string]any{
		"level":    "critical",
		"category": "trade",
		"title":    "全自动条件计划生成失败",
		"body": fmt.Sprintf("信号 %d %s 的计划生成或授权失败：%v。"+
			"残余意图保留待人工核对；同一信号不会被自动重发。", signalID, symbol, cause),
		"dedup_key": fmt.Sprintf("trade:auto-plan:%d", signalID),
	})
	if err != nil {
		r.log.Warnf("auto failure event: %v", err)
		return
	}
	if err := r.publish(ctx, env); err != nil {
		r.log.Warnf("publish auto failure: %v", err)
	}
}

func (r *Runner) keepSubscription(sub *nats.Subscription, exit bool) {
	r.lifecycle.Lock()
	if !r.stopping && r.runCtx.Err() == nil {
		if exit {
			r.exitSub = sub
		} else {
			r.sub = sub
		}
		r.lifecycle.Unlock()
		return
	}
	r.lifecycle.Unlock()
	_ = sub.Unsubscribe()
}

// keepSignalSubscription 与 keepSubscription 同一份生命周期纪律：
// 停机可见前只允许登记，之后立即退订，绝不丢下一条还在回调的订阅。
func (r *Runner) keepSignalSubscription(sub *nats.Subscription) {
	r.lifecycle.Lock()
	if !r.stopping && r.runCtx.Err() == nil {
		r.sigSub = sub
		r.lifecycle.Unlock()
		return
	}
	r.lifecycle.Unlock()
	_ = sub.Unsubscribe()
}

// beginCallback and beginStop serialize WaitGroup admission. Once stopping is
// visible, even a queued callback cannot begin new engine work.
func (r *Runner) beginCallback() (context.Context, func(), bool) {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.stopping {
		return nil, nil, false
	}
	base := r.runCtx
	if base == nil { // Existing isolated core tests call handlers without Start.
		base = context.Background()
	}
	if base.Err() != nil {
		return nil, nil, false
	}
	r.wg.Add(1)
	ctx, cancel := context.WithTimeout(base, 10*time.Second)
	return ctx, func() { cancel(); r.wg.Done() }, true
}

// Evidence collection has its own bounded loop. A slow source or evidence DB
// cannot stall the order/outbox ticker, and no client-supplied clock is used.
func (r *Runner) evidenceLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	sample := func() {
		if ctx.Err() != nil {
			return
		}
		c, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		finish := observe.ForService("trade").Op("paper_evidence").Begin()
		stored, err := r.e.RecordPaperRunSampleObserved(c)
		if err == nil && !stored {
			finish(observe.Skipped)
		} else {
			observe.FinishError(finish, err)
		}
		if err != nil && ctx.Err() == nil {
			r.log.Warnf("paper run evidence: %v", err)
		}
	}
	sample()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			sample()
		}
	}
}

// onBars 解开 M02 的事件信封，再把分钟线交给模拟撮合；不接受无来源的裸载荷。
func (r *Runner) onBars(msg *nats.Msg) {
	notice := observe.ForService("trade").Op("minute_notice")
	notice.Received()
	noticeFinish := notice.Begin()
	noticeOutcome := observe.Rejected
	defer func() { noticeFinish(noticeOutcome) }()
	ctx, finish, active := r.beginCallback()
	if !active {
		noticeOutcome = observe.Cancelled
		return
	}
	defer finish()
	env, err := events.Unmarshal(msg.Data)
	if err != nil || env.Subject != events.SubjectMarketBar || env.Source != "market" {
		r.log.Warnf("bar envelope: subject=%s error=%v", env.Subject, err)
		return
	}
	var body struct {
		Bars []market.Bar `json:"bars"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil {
		r.log.Warnf("bar payload: %v", err)
		return
	}
	if len(body.Bars) > market.MaxTradeWatchSymbols {
		return
	}
	noticeOutcome = observe.Succeeded
	if len(body.Bars) == 0 {
		noticeOutcome = observe.Skipped
	}
	for _, b := range body.Bars {
		if ctx.Err() != nil {
			return
		}
		now := time.Now()
		if r.now != nil {
			now = r.now()
		}
		op := observe.ForService("trade").Op("minute_bar")
		op.Received()
		done := op.BeginFrom(b.Time, env.Time)
		if b.Time.IsZero() || now.Before(b.Time.Add(time.Minute)) || now.Sub(b.Time) > 2*time.Minute {
			done(observe.Rejected)
			continue
		}
		up, down, known := b.ExecutionPrices()
		if !known {
			done(observe.Rejected)
			continue
		}
		applied, err := r.e.ApplySourceBar(events.ContextWithEnvelope(ctx, env), "SIM", b.Symbol, biz.Quote{
			Time: b.Time,
			Open: b.Open, High: b.High, Low: b.Low, Close: b.Close, Volume: b.Volume,
			LimitUp: up, LimitDown: down,
		})
		if err != nil {
			observe.FinishError(done, err)
		} else if !applied.Consumed {
			done(observe.Skipped)
		} else {
			done(observe.Succeeded)
		}
		if err != nil {
			r.log.Warnf("apply %s: %v", b.Symbol, err)
		}
	}
}

func (r *Runner) onExit(msg *nats.Msg) {
	op := observe.ForService("trade").Op("risk_exit")
	op.Received()
	env, err := events.Unmarshal(msg.Data)
	var observed func(observe.Outcome)
	if err == nil {
		observed = op.BeginFrom(env.SourceOrZero(), env.Time)
	} else {
		observed = op.Begin()
	}
	outcome := observe.Rejected
	defer func() { observed(outcome) }()
	ctx, finish, active := r.beginCallback()
	if !active {
		outcome = observe.Cancelled
		return // Leave unacknowledged for the next active instance.
	}
	defer finish()
	if err != nil || env.Subject != events.SubjectRiskExit || env.Source != "risk" || msg.Subject != events.SubjectRiskExit {
		r.log.Warnf("exit envelope: %v", err)
		_ = msg.Ack()
		return
	}
	var body struct {
		ExitID  string  `json:"exit_id"`
		Symbol  string  `json:"symbol"`
		Account string  `json:"account_type"`
		Volume  int     `json:"volume"`
		Price   float64 `json:"price"`
		Trigger string  `json:"trigger"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil || body.ExitID == "" || body.Volume <= 0 || body.Price <= 0 || (body.Account != "SIM" && body.Account != "LIVE") {
		r.log.Warnf("exit payload: %v", err)
		_ = msg.Ack()
		return
	}
	if ctx.Err() != nil {
		outcome = observe.Cancelled
		return
	}
	order, err := r.e.Place(events.ContextWithEnvelope(ctx, env), biz.PlaceRequest{
		ClientOrderID: body.ExitID,
		Account:       body.Account,
		Symbol:        body.Symbol,
		Side:          "sell",
		Price:         body.Price,
		Volume:        body.Volume,
		Source:        "watch",
		Operator:      "risk",
		// A protection trigger such as "stop_loss" is not a strategy signal ID.
		// Its original event remains the causal source of this exit order.
	})
	if err != nil {
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			outcome = observe.Cancelled
		} else {
			outcome = observe.Failed
		}
		r.log.Warnf("exit place %s: %v", body.Symbol, err)
		if ctx.Err() == nil {
			_ = msg.Nak()
		}
		return
	}
	if order.Status == biz.StatusRejected {
		outcome = observe.Rejected
	} else {
		outcome = observe.Succeeded
	}
	r.log.Infof("exit order %s %s %s", order.ClientOrderID, order.Status, order.Reason)
	_ = msg.Ack()
}

func (r *Runner) beginStop() <-chan struct{} {
	r.lifecycle.Lock()
	defer r.lifecycle.Unlock()
	if r.done == nil {
		r.done = make(chan struct{})
	}
	if !r.stopping {
		r.stopping = true
		if r.cancel != nil {
			r.cancel()
		}
		bars, exits, signals := r.sub, r.exitSub, r.sigSub
		r.sub, r.exitSub, r.sigSub = nil, nil, nil
		go func() {
			if bars != nil {
				_ = bars.Unsubscribe()
			}
			if exits != nil {
				_ = exits.Unsubscribe()
			}
			if signals != nil {
				_ = signals.Unsubscribe()
			}
			r.wg.Wait()
			close(r.done)
		}()
	}
	return r.done
}

func (r *Runner) Stop(ctx context.Context) error {
	done := r.beginStop()
	select {
	case <-done:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (r *Runner) loop(ctx context.Context) {
	snap := time.NewTicker(r.interval)
	dispatch := time.NewTicker(500 * time.Millisecond)
	defer snap.Stop()
	defer dispatch.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-snap.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if c.Err() != nil {
				cancel()
				return
			}
			if err := observedTradeCall("intent_recovery", func() error { return r.e.RecoverIntents(c) }); err != nil && c.Err() == nil {
				r.log.Warnf("recover intents: %v", err)
			}
			if c.Err() == nil {
				if err := observedTradeCall("snapshot", func() error { _, err := r.e.Snapshot(c, "SIM"); return err }); err != nil && c.Err() == nil {
					r.log.Warnf("snapshot: %v", err)
					r.channelFail()
				} else if c.Err() == nil {
					r.channelOK()
				}
			}
			cancel()
		case <-dispatch.C:
			c, cancel := context.WithTimeout(ctx, 10*time.Second)
			if c.Err() != nil {
				cancel()
				return
			}
			if err := observedTradeCall("plan_tick", func() error { return r.e.TickPlans(c) }); err != nil && c.Err() == nil {
				r.log.Debugf("conditional plans: %v", err)
			}
			if c.Err() == nil {
				if err := observedTradeCall("confirm_expiry", func() error { return r.e.ExpireConfirms(c) }); err != nil && c.Err() == nil {
					r.log.Warnf("confirm: %v", err)
				}
			}
			if c.Err() == nil {
				if err := observedTradeCall("watch_sweep", func() error { return r.e.SweepWorking(c) }); err != nil && c.Err() == nil {
					r.log.Warnf("chase: %v", err)
				}
			}
			if c.Err() == nil {
				if err := observedTradeCall("outbox", func() error { _, err := r.box.Dispatch(c, 100); return err }); err != nil && c.Err() == nil {
					r.log.Warnf("outbox: %v", err)
				}
			}
			cancel()
		}
	}
}
