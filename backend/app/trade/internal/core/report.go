// M13-T04 实盘回报轮询回路：交易时段 2s / 非交易时段 30s 拉取 broker-gw
// 快照，交给 biz.ApplyBrokerReport 归并入账。
//
// 纪律（对齐 M13 设计 T04 与包级 live_channel 注释）：
//   - broker 未连接（健康链 Tracker 停在 idle/down）时明示休眠——不发起调用、
//     不算失败，只按既定节拍等健康探测把它拉回 connected；
//   - 快照解析失败 = 数据纪律异常：记日志跳过整帧，绝不把坏帧当空帧入账；
//   - 归并结果（入账/重放/孤儿/对账）只走 biz.ReportOutcome 落日志与观测，
//     每条链路同一根 trace，事件走 outbox（同事务）。
package core

import (
	"context"
	"encoding/json"
	"strconv"
	"time"

	"server/app/trade/internal/biz"
	"server/pkg/brokerif"
	"server/pkg/brokerif/remote"
	"server/pkg/events"
	"server/pkg/observe"
	"server/pkg/symbol"
	"server/pkg/tradecal"
)

// ReportSource 是实盘回报的只读来源：remote.Client 天然实现，
// 测试注入 broker-gw simulator 语义的回放器（同一协议载荷，不经网络）。
type ReportSource interface {
	Snapshot(context.Context) (json.RawMessage, error)
}

const (
	reportTickTrading    = 2 * time.Second
	reportTickNonTrading = 30 * time.Second
)

// reportSource 选择回报来源：测试注入优先，否则用装配好的 emfile 客户端。
func (r *Runner) reportSource() ReportSource {
	if r.reportSrc != nil {
		return r.reportSrc
	}
	if r.live != nil && r.live.Client != nil {
		return r.live.Client
	}
	return nil
}

// reportLoop 与 loop/liveProbeLoop 同一派发纪律：Start 内统一 wg.Add+go，
// ctx 取消即退出。通道未装配（nil 客户端）时如实缺席不占循环。
func (r *Runner) reportLoop(ctx context.Context) {
	if r.reportSource() == nil {
		return
	}
	next := time.Duration(0)
	for {
		timer := time.NewTimer(next)
		select {
		case <-ctx.Done():
			timer.Stop()
			return
		case <-timer.C:
		}
		next = r.reportInterval(ctx)
		c, cancel := context.WithTimeout(ctx, 2*remoteCallBudget())
		tick := r.reportOnce(c)
		cancel()
		if tick.err != nil && ctx.Err() == nil {
			r.log.Warnf("broker report: %v", tick.err)
			continue
		}
		if tick.dormant {
			r.log.Debugf("broker report dormant: %s", tick.reason)
			continue
		}
		if tick.merged || len(tick.out.SectionsSkipped) > 0 {
			r.log.Infof("broker report %s: fills=%d replay=%d orders=%d unknown=%d orphans=%d skipped=%v halting=%v",
				tick.out.Account, tick.out.FillsApplied, tick.out.FillsReplayed, tick.out.OrdersAdvanced,
				tick.out.OrdersUnknown, len(tick.out.Orphans), tick.out.SectionsSkipped, tick.halted())
		}
	}
}

func remoteCallBudget() time.Duration { return 10 * time.Second }

// reportInterval 按上海日历选节拍：交易时段 2s，其余 30s；日历不识别的年份
// 按非交易时段处理（tradecal 包级纪律：不自己猜盘中）。
func (r *Runner) reportInterval(ctx context.Context) time.Duration {
	if ctx.Err() != nil {
		return reportTickNonTrading
	}
	now := time.Now()
	if r.now != nil {
		now = r.now()
	}
	session, err := tradecal.Default.SessionAt(now)
	if err == nil && session.Trading {
		return reportTickTrading
	}
	return reportTickNonTrading
}

type reportTick struct {
	dormant bool
	reason  string
	merged  bool
	out     biz.ReportOutcome
	err     error
}

func (t reportTick) halted() bool {
	return t.out.Reconciled != nil && !*t.out.Reconciled
}

// reportOnce 执行一拍：休眠判定 → 快照 → 解析 → 归并。错误全部显式返回，
// 「没取到」不写成「没有」。
func (r *Runner) reportOnce(ctx context.Context) (tick reportTick) {
	src := r.reportSource()
	if src == nil {
		return reportTick{dormant: true, reason: "emfile 通道未装配"}
	}
	if tr := r.liveTracker(); tr != nil {
		status, _ := tr.Current()
		if status == brokerif.Idle || status == brokerif.Down {
			return reportTick{dormant: true, reason: "broker 未连接（" + string(status) + "），回报轮询休眠"}
		}
	}
	done := observe.ForService("trade").Op("broker_report").Begin()
	raw, err := src.Snapshot(ctx)
	if err != nil {
		done(observe.Failed)
		tick.err = err
		return tick
	}
	rep, err := parseBrokerReport(raw, r.reportAccount())
	if err != nil {
		done(observe.Rejected)
		tick.err = err
		return tick
	}
	ctx = events.OpenTrace(ctx)
	out, err := r.e.ApplyBrokerReport(ctx, rep)
	if err != nil {
		observe.FinishError(done, err)
		tick.err = err
		return tick
	}
	done(observe.Succeeded)
	tick.out, tick.merged = out, true
	return tick
}

func (r *Runner) liveTracker() *brokerif.Tracker {
	if r.live == nil {
		return nil
	}
	return r.live.Tracker
}

func (r *Runner) reportAccount() string {
	if r.live != nil && r.live.Client != nil {
		return r.live.Client.Account()
	}
	return r.reportAcct
}

// parseBrokerReport 把协议快照翻成 biz 归并输入：各节按自身新鲜度独立置位，
// 数据纪律的「没取到≠没有」由节级 Ready 显式承载。
func parseBrokerReport(raw json.RawMessage, account string) (biz.BrokerReport, error) {
	snap, err := remote.ParseSnapshot(raw, account)
	if err != nil {
		return biz.BrokerReport{}, err
	}
	rep := biz.BrokerReport{Account: snap.AccountID, AsOf: snap.Observed}
	rep.Orders.Ready = snap.Orders.Ready()
	rep.Orders.Reason = sectionReason("order_status.csv", snap.Orders.Available, snap.Orders.Fresh, snap.Orders.Reason)
	for _, row := range snap.Orders.Records {
		side, err := row.Side()
		if err != nil {
			return biz.BrokerReport{}, err
		}
		rep.Orders.Records = append(rep.Orders.Records, biz.BrokerOrderReport{
			SID: row.SID, Symbol: row.Symbol, Side: side, State: row.State,
			Price: priceOf(row.Price), Volume: row.Volume, Filled: row.FilledVolume,
			RejectDetail: row.RejectDetail,
		})
	}
	rep.Executions.Ready = snap.Executions.Ready()
	rep.Executions.Reason = sectionReason("execution_report.csv", snap.Executions.Available, snap.Executions.Fresh, snap.Executions.Reason)
	for _, row := range snap.Executions.Records {
		report := biz.BrokerExecution{SID: row.SID, ExecID: row.ExecID, Symbol: row.Symbol, Kind: row.Kind,
			Qty: row.Volume, Price: priceOf(row.Price), Amount: priceOf(row.Amount)}
		if side, err := row.Side(); err == nil {
			report.Side = side
		}
		rep.Executions.Records = append(rep.Executions.Records, report)
	}
	rep.Positions.Ready = snap.Positions.Ready()
	rep.Positions.Reason = sectionReason("position.csv", snap.Positions.Available, snap.Positions.Fresh, snap.Positions.Reason)
	for _, row := range snap.Positions.Records {
		parsed, err := symbol.Parse(row.Symbol)
		if err != nil {
			return biz.BrokerReport{}, err
		}
		rep.Positions.Records = append(rep.Positions.Records, biz.RemotePosition{
			Symbol: parsed.Tongdaxin(), Quantity: row.Volume, Available: row.AvailableNow,
		})
	}
	rep.Cash.Ready = snap.Cash.Ready()
	rep.Cash.Reason = sectionReason("cash.csv", snap.Cash.Available, snap.Cash.Fresh, snap.Cash.Reason)
	for _, row := range snap.Cash.Records {
		rep.Cash.Records = append(rep.Cash.Records, biz.BrokerCash{
			Balance: priceOf(row.Balance), Available: priceOf(row.Available), Frozen: priceOf(row.Frozen),
		})
	}
	return rep, nil
}

func sectionReason(name string, available bool, fresh *bool, reason string) string {
	if reason != "" {
		return name + ":" + reason
	}
	if !available {
		return name + ":REPORT_UNAVAILABLE"
	}
	if fresh != nil && !*fresh {
		return name + ":STALE"
	}
	return name + ":NOT_READY"
}

func priceOf(raw string) float64 {
	value, _ := strconv.ParseFloat(raw, 64)
	return value
}
