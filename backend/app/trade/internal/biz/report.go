package biz

// =============================================================================
// M13-T04 实盘回报入账归并器（trade/biz）
// 契约（对齐 M13 设计 T04 节与 M09 回报/对账纪律）：
//   - 成交回报按 exec_id 幂等入账：HasFillExec 预检是正常路径，trade_fill
//     (book, exec_id) PG 唯一约束兜底；重放整帧/单条一律跳过并计数，
//     绝不二次入账、绝不二次改现金。
//   - 委托状态由柜台状态机映射回 trade_order；本地终态不回退，成交事实
//     只来自 execution_report——remote 的累计成交量不直接改写账本，只用于
//     校验（「没取到≠0」，缺失的成交回报不许伪造）。
//   - 现金/持仓只走既有账本入口（store.Commit 同一事务，T+1 手数/费用公式与
//     模拟撮合 fill() 完全同一式）；柜台资金快照不改写本地现金——对账沿用
//     现有 reconcile（不平→告警+停止开仓，现状不变）。
//   - 无本地委托的回报、冲突/越界回报、缺日初基线的入账，一律写
//     event_disposition 并发 notify.request（UNKNOWN 纪律：资产类异常必须
//     留人眼核对口，绝不自动裁决、绝不自动重发）。
// =============================================================================

import (
	"context"
	"fmt"
	"strings"
	"time"

	"server/pkg/ashare"
	"server/pkg/events"
	"server/pkg/symbol"
)

// ReportSection 是一类柜台回报：Ready=false 时 Records 不可用于入账/对账
// （追加流只判 available，全量流还要求 fresh——沿用 reader 语义）。
type ReportSection[T any] struct {
	Ready   bool
	Reason  string // Ready=false 的原因（reader 报的 name:REASON 形态）
	Records []T
}

// BrokerOrderReport 是 order_status 回报的一行（已规范：symbol 为 SHSE.600519、
// state 为柜台枚举、order_business 已译为 buy/sell）。
type BrokerOrderReport struct {
	SID, Symbol, Side string
	State             string // PENDING/ACKNOWLEDGED/PARTIAL/FILLED/CANCELLED/REJECTED/EXPIRED/UNKNOWN
	Price             float64
	Volume, Filled    int
	RejectDetail      string
}

// BrokerExecution 是 execution_report 回报的一行（Kind=FILL 才入账；
// CANCEL_REJECTED 只计数——撤单被拒已由本地撤单链相应处理，不是资产事实）。
type BrokerExecution struct {
	SID, ExecID, Symbol, Side, Kind string
	Qty                             int
	Price, Amount                   float64
}

// BrokerCash 是 cash.csv 的一行（柜台口径：balance/available/frozen）。
type BrokerCash struct{ Balance, Available, Frozen float64 }

// BrokerReport 是一帧快照里 trade 关心的部分。各节独立带 Ready：
// 回报不齐/不新鲜只跳过对应节，决不把「没取到」当成「没有」。
type BrokerReport struct {
	Account    string
	AsOf       time.Time
	Orders     ReportSection[BrokerOrderReport]
	Executions ReportSection[BrokerExecution]
	Positions  ReportSection[RemotePosition]
	Cash       ReportSection[BrokerCash]
}

// OrphanReport 是一条无本地委托对应或语义冲突的回报（必须人核）。
type OrphanReport struct {
	Kind   string // orphan_fill / orphan_order / order_conflict / book_mismatch / baseline_missing
	SID    string
	ExecID string
	Note   string
}

// ReportOutcome 是一帧归并的全部事实计数；runner 按它记账与日志。
type ReportOutcome struct {
	Account          string
	FillsApplied     int // 新入账成交笔数
	FillsReplayed    int // exec_id 重放跳过笔数
	OrdersAdvanced   int // 状态机迁移的委托数
	OrdersKept       int
	OrdersUnknown    int // 柜台 UNKNOWN 委托：不裁决（等人工/健康链告警）
	CancelRejections int
	Malformed        int
	Orphans          []OrphanReport
	SectionsSkipped  []string // 因不新鲜/不可用而跳过的节名:原因
	BaselineMissing  bool     // live 账本缺少资金基线（已按 0 基线记账并留人核）
	Reconciled       *bool    // 持仓节可用才执行；nil=未对账
	ReconcileDiffs   []string
	CashReported     *BrokerCash // 观测用；不改写账本
}

// FillExecStore 是成交幂等查询端口（PG 唯一约束之外的正常判重路径）。
type FillExecStore interface {
	HasFillExec(ctx context.Context, book, execID string) (bool, error)
}

// Disposition 是一条人工处置留痕（event_disposition；语义见 schema 头注）。
type Disposition struct {
	EventID string
	TraceID string
	Action  string
	Note    string
	Actor   string
}

// ReportAuditStore 是处置留痕写入端口：同 (event_id, action) 的去重由
// 存储在账本事务内完成（返回 created=false 表示此前已留痕，避免重复告警风暴）。
type ReportAuditStore interface {
	RecordDisposition(ctx context.Context, d Disposition) (bool, error)
}

func (e *Engine) reportStores() (FillExecStore, ReportAuditStore, error) {
	fills, ok := e.store.(FillExecStore)
	if !ok {
		return nil, nil, fmt.Errorf("%w: 成交幂等存储未接入", ErrInvalid)
	}
	audit, ok := e.store.(ReportAuditStore)
	if !ok {
		return nil, nil, fmt.Errorf("%w: 回报处置存储未接入", ErrInvalid)
	}
	return fills, audit, nil
}

// ApplyBrokerReport 把一帧柜台快照归并进 LIVE 账本。全部改动在一个全局
// 账本事务内落库（含成交/委托/现金/持仓/对账/处置/outbox 同事务提交）。
// 任何一条回报自己坏掉（解析/冲突/越界）只影响那一条——记入人核口，
// 不阻塞同帧其他真实成交入账。
func (e *Engine) ApplyBrokerReport(ctx context.Context, rep BrokerReport) (out ReportOutcome, err error) {
	out.Account = rep.Account
	if strings.TrimSpace(rep.Account) == "" {
		return out, fmt.Errorf("%w: 回报账户不能为空", ErrInvalid)
	}
	execStore, audit, err := e.reportStores()
	if err != nil {
		return out, err
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	err = e.withLedger(ctx, "global", func(c context.Context) error {
		batch := Batch{}
		orders := map[string]Order{}
		positions := map[string]Position{}
		// changed 只记本帧被成交/状态真实改写的委托：没变化的委托不进批，
		// 避免 2s 轮询把 ACK 保持单反复 upsert 并重复投 trade.order 事件。
		changed := map[string]bool{}
		cash, cashOK, err := e.store.LoadCash(c, BookLive)
		if err != nil {
			return err
		}
		cash.Book = BookLive
		if !cashOK {
			// LIVE 未接入时不虚构资金（M09）；成交是柜台事实照常入账，
			// 但从 0 基线起的现金必须留人核——与对账「沿用现状」同口径。
			out.BaselineMissing = true
			out.Orphans = append(out.Orphans, OrphanReport{Kind: "baseline_missing", Note: "实盘账本缺少资金基线，本帧回报按 0 基线入账；请人工核对日初权益"})
		}
		if len(rep.Cash.Records) > 0 && rep.Cash.Ready {
			cashRep := rep.Cash.Records[0]
			out.CashReported = &cashRep
		}
		load := func(sid string) (Order, bool, error) {
			if o, ok := orders[sid]; ok {
				return o, true, nil
			}
			o, ok, err := e.store.LoadOrder(c, sid)
			if err != nil || !ok {
				return Order{}, ok, err
			}
			orders[sid] = o
			return o, true, nil
		}
		// 1) 成交回报：exec_id 幂等入账。
		if rep.Executions.Ready {
			for _, ex := range rep.Executions.Records {
				if ex.Kind != "FILL" {
					// CANCEL_REJECTED 不是资产事实；本地撤单链已处理语义，只计数。
					out.CancelRejections++
					continue
				}
				if ex.ExecID == "" || ex.SID == "" || ex.Qty <= 0 || ex.Price <= 0 {
					out.Malformed++
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "orphan_fill", SID: ex.SID, ExecID: ex.ExecID, Note: "成交回报字段不完整"})
					continue
				}
				seen, err := execStore.HasFillExec(c, BookLive, ex.ExecID)
				if err != nil {
					return err
				}
				if seen {
					out.FillsReplayed++
					continue
				}
				order, ok, err := load(ex.SID)
				if err != nil {
					return err
				}
				if !ok {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "orphan_fill", SID: ex.SID, ExecID: ex.ExecID, Note: fmt.Sprintf("成交 %d股 @%.2f 无对应本地委托", ex.Qty, ex.Price)})
					continue
				}
				if order.Book != BookLive {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "book_mismatch", SID: ex.SID, ExecID: ex.ExecID, Note: "本地同号委托不在实盘账本"})
					continue
				}
				if note := verifyFillIdentity(order, ex); note != "" {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "order_conflict", SID: ex.SID, ExecID: ex.ExecID, Note: note})
					continue
				}
				if order.Filled+ex.Qty > order.Volume {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "order_conflict", SID: ex.SID, ExecID: ex.ExecID, Note: fmt.Sprintf("成交累计 %d 超出委托量 %d", order.Filled+ex.Qty, order.Volume)})
					continue
				}
				pos, ok, err := e.store.LoadPosition(c, BookLive, order.Symbol)
				if err != nil {
					return err
				}
				if p, exists := positions[order.Symbol]; exists {
					pos = p
				} else if !ok {
					pos = Position{}
				}
				pos.Book, pos.Symbol = BookLive, order.Symbol
				fee := e.creditBrokerFill(&cash, &pos, order.Side, ex.Qty, ex.Price)
				order.Filled += ex.Qty
				orders[order.ClientOrderID] = order
				changed[order.ClientOrderID] = true
				positions[order.Symbol] = pos
				fill := Fill{
					ClientOrderID: order.ClientOrderID, Book: BookLive, Symbol: order.Symbol, Side: order.Side,
					Qty: ex.Qty, Price: ex.Price, Amount: ex.Price * float64(ex.Qty), Fee: fee,
					SignalID: order.SignalID, StrategyVersion: order.StrategyVersion,
					ExecID: ex.ExecID, CreatedAt: e.clock(),
				}
				batch.Fills = append(batch.Fills, fill)
				batch.Trails = append(batch.Trails, Trail{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: fmt.Sprintf("柜台成交回报 exec=%s %d股@%.2f 入账", ex.ExecID, ex.Qty, ex.Price)})
				batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeFill, Payload: fill})
				out.FillsApplied++
			}
		} else {
			out.SectionsSkipped = append(out.SectionsSkipped, "executions:"+rep.Executions.Reason)
		}
		// 2) 委托状态映射（remote 状态机可读才执行）。
		if rep.Orders.Ready {
			for _, row := range rep.Orders.Records {
				order, ok, err := load(row.SID)
				if err != nil {
					return err
				}
				if !ok {
					// PENDING 是网关自己刚发布的占位？不——PENDING 只来自终端回报文件；
					// 本地无单而柜台有单，资产类必须人核（不落本地委托）。
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "orphan_order", SID: row.SID, Note: fmt.Sprintf("柜台有委托而本地无记录：状态 %s %d股", row.State, row.Volume)})
					continue
				}
				if order.Book != BookLive {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "book_mismatch", SID: row.SID, Note: "本地同号委托不在实盘账本"})
					continue
				}
				target, note, conflict := mapBrokerOrderState(order, row)
				if conflict != "" {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "order_conflict", SID: row.SID, Note: conflict})
					continue
				}
				if row.State == "UNKNOWN" {
					// UNKNOWN 纪律：不裁决、不重发；健康链的 order_unknown 告警负责提示。
					out.OrdersUnknown++
					continue
				}
				if target == "" || target == order.Status {
					out.OrdersKept++
					continue
				}
				order.Status, order.Reason = target, note
				orders[order.ClientOrderID] = order
				changed[order.ClientOrderID] = true
				batch.Trails = append(batch.Trails, Trail{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason})
				out.OrdersAdvanced++
			}
		} else {
			out.SectionsSkipped = append(out.SectionsSkipped, "orders:"+rep.Orders.Reason)
		}
		// 3) 成交驱动的终态收口（滞后于 execution_report 的柜台状态不改写成交事实）。
		for id, order := range orders {
			if order.Book != BookLive {
				continue
			}
			if order.Filled >= order.Volume && order.Volume > 0 && order.Status != StatusFilled {
				if isTerminal(order.Status) {
					out.Orphans = append(out.Orphans, OrphanReport{Kind: "order_conflict", SID: id, Note: fmt.Sprintf("成交足量但本地已是终态 %s", order.Status)})
					continue
				}
				order.Status, order.Reason = StatusFilled, "柜台回报成交足量"
				orders[id] = order
				changed[id] = true
				batch.Trails = append(batch.Trails, Trail{ClientOrderID: id, Status: order.Status, Note: order.Reason})
				out.OrdersAdvanced++
			} else if order.Filled > 0 && order.Filled < order.Volume && order.Status == StatusSubmitted {
				order.Status, order.Reason = StatusPartial, "柜台回报部分成交"
				orders[id] = order
				changed[id] = true
				batch.Trails = append(batch.Trails, Trail{ClientOrderID: id, Status: order.Status, Note: order.Reason})
				out.OrdersAdvanced++
			}
		}
		for id, order := range orders {
			if !changed[id] {
				continue
			}
			batch.Orders = append(batch.Orders, order)
			batch.Events = append(batch.Events, Event{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)})
		}
		for _, pos := range positions {
			batch.Positions = upsertPos(batch.Positions, pos)
		}
		if !cashOK || len(positions) > 0 || batch.Fills != nil {
			batch.Cash = &cash
		}
		if err := e.store.Commit(c, batch); err != nil {
			return err
		}
		// 4) 持仓对账：沿用现有 reconcile（不平→OpenHalted+对账事件，现状不变）。
		if rep.Positions.Ready {
			matched, diffs, err := e.reconcile(c, "LIVE", rep.Positions.Records)
			if err != nil {
				return err
			}
			out.Reconciled, out.ReconcileDiffs = &matched, diffs
		} else {
			out.SectionsSkipped = append(out.SectionsSkipped, "positions:"+rep.Positions.Reason)
		}
		// 5) 人核口留痕：存储内去重；只有新留痕才发 notify（防 2s 轮询告警风暴）。
		reportTrace := events.TraceID(c)
		var alerts []Event
		for _, orphan := range out.Orphans {
			created, err := audit.RecordDisposition(c, orphanDisposition(orphan, reportTrace))
			if err != nil {
				return err
			}
			if created {
				alerts = append(alerts, Event{Subject: events.SubjectNotifyRequest, Payload: map[string]any{
					"level": "critical", "category": "trade",
					"title": "实盘回报需人工核对",
					"body": fmt.Sprintf("账户 %s：%s（%s）。归并器只留痕不裁决；请核对终端与本地账本后处置。",
						out.Account, orphan.Note, orphanDispositionID(orphan)),
					"dedup_key": "trade:broker-report-orphan:" + orphan.Kind + ":" + firstKey(orphan),
				}})
			}
		}
		if len(alerts) > 0 {
			if err := e.store.Commit(c, Batch{Events: alerts}); err != nil {
				return err
			}
		}
		return nil
	})
	return out, err
}

// creditBrokerFill 按柜台成交记账（与模拟撮合 fill() 同一份资金/T+1 公式）：
// 买入现金减（金额+费用）、均价加权、当日买入不可卖；卖出现金加（金额-费用）、
// 计已实现盈亏、可用扣减不小于 0。返回本笔费用。
func (e *Engine) creditBrokerFill(cash *Cash, pos *Position, side string, qty int, price float64) float64 {
	amount := price * float64(qty)
	fee := e.set.Fee.Cost(amount, side == "sell")
	if side == "buy" {
		cash.Cash -= amount + fee
		prev := pos.Quantity
		pos.Quantity += qty
		if pos.Quantity > 0 {
			pos.AvgCost = (pos.AvgCost*float64(prev) + amount) / float64(pos.Quantity)
		}
		pos.Available = ashare.Sellable(pos.Quantity, pos.Quantity-pos.Available)
	} else {
		cash.Cash += amount - fee
		cash.RealizedPnL += (price-pos.AvgCost)*float64(qty) - fee
		pos.Quantity -= qty
		pos.Available -= qty
		if pos.Available < 0 {
			pos.Available = 0
		}
	}
	pos.LastPrice = price
	return fee
}

// verifyFillIdentity 校验成交回报与本地委托的同一性（证券/方向）；不一致
// 即冲突进人核口，绝不在错单上入账。
func verifyFillIdentity(order Order, ex BrokerExecution) string {
	parsed, err := symbol.Parse(ex.Symbol)
	if err != nil {
		return "成交回报证券代码非法"
	}
	local, err := symbol.Parse(order.Symbol)
	if err != nil || local != parsed {
		return "成交回报证券与本地委托不一致"
	}
	if ex.Side != "" && ex.Side != order.Side {
		return "成交回报方向与本地委托不一致"
	}
	return ""
}

func isTerminal(status string) bool {
	return status == StatusFilled || status == StatusCancelled || status == StatusRejected
}

// mapBrokerOrderState 把柜台状态机映射到本地状态机。target="" 表示本地不变
// （PENDING/ACKNOWLEDGED 不推进本地状态，滞后于 execution_report 是常态）；
// conflict 非空表示语义冲突进人核口；终态单永不回退。
func mapBrokerOrderState(order Order, row BrokerOrderReport) (target, note, conflict string) {
	if isTerminal(order.Status) {
		// 本地终态 vs 柜台非终态：柜台文件滞后常见，静默保持（不告警噪声）。
		return "", "", ""
	}
	switch row.State {
	case "PENDING", "ACKNOWLEDGED":
		if order.Status == StatusPending {
			return "", "", "柜台已受理但本地仍待确认，文件单与确认链不一致"
		}
		return "", "", ""
	case "PARTIAL":
		if order.Filled <= 0 {
			return "", "", "柜台报部分成交但本地无成交记录，成交回报缺失"
		}
		if order.Status == StatusSubmitted {
			return StatusPartial, "柜台部分成交", ""
		}
		return "", "", ""
	case "FILLED":
		if order.Filled < order.Volume {
			return "", "", fmt.Sprintf("柜台报全部成交但本地成交 %d/%d，成交回报缺失", order.Filled, order.Volume)
		}
		return StatusFilled, "柜台全部成交", ""
	case "CANCELLED":
		return StatusCancelled, "柜台已撤", ""
	case "REJECTED":
		if order.Filled > 0 {
			return "", "", "柜台拒单但本地已有成交，单据冲突"
		}
		note := strings.TrimSpace(row.RejectDetail)
		if note == "" {
			note = "柜台拒单"
		}
		return StatusRejected, note, ""
	case "EXPIRED":
		return StatusCancelled, "柜台过期", ""
	case "UNKNOWN":
		return "", "", ""
	default:
		return "", "", "未知柜台状态 " + row.State
	}
}

func orphanDispositionID(o OrphanReport) string {
	key := o.ExecID
	if key == "" {
		key = o.SID
	}
	if key == "" {
		key = "ledger"
	}
	return "broker-report:" + o.Kind + ":" + key
}

func firstKey(o OrphanReport) string {
	if o.ExecID != "" {
		return o.ExecID
	}
	if o.SID != "" {
		return o.SID
	}
	return "ledger"
}

func orphanDisposition(o OrphanReport, trace string) Disposition {
	note := strings.TrimSpace(o.Note)
	if len([]rune(note)) > 260 {
		note = string([]rune(note)[:260])
	}
	return Disposition{
		EventID: orphanDispositionID(o),
		TraceID: trace,
		Action:  "manual_review",
		Note:    note,
		Actor:   "trade/report",
	}
}
