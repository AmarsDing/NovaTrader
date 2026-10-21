package biz

// =============================================================================
// M13-T03 实盘文件单出口链（trade/biz ← pkg/brokerif/remote.Client）
// 契约（对齐 M13 设计 T03 与 M09 文件单 / UNKNOWN 纪律）：
//   - 三道闸（BrokerConnected / LiveEnabled / 运行证据准入）仍在 place() 拦截；
//     闸后委托进 pending_confirm，30 秒人工确认窗不变。
//   - 出口只发生在确认与撤单两条链，按「先落本地意图、后写文件、再把传输回执
//     入账」组织：confirm 先把 submitted 落库（第一笔账本事务），网络调用在账本
//     事务之外，回执再入第二笔事务——发布永远发生在意图已持久化之后。
//   - 回执只有两类语义：FILE_PUBLISHED（文件已发布，受理真值等柜台回报）与
//     UNKNOWN（发布结局未知）。UNKNOWN 纪律：订单保持 submitted + order_trail
//     记 transport_unknown + notify critical，绝不自动重发；同一 SID 的重交由
//     网关传输占用保证返回原回执，但本层不发起第二次调用（残余留人核）。
//   - 明确错误（网关 4xx 语义拒绝、本地非法载荷）意味文件确定未发布：
//     委托 → rejected（出口失败）+ notify critical，与状态机终态语义闭合并由
//     T04 归并器保持终态不回退。
//   - 撤单对称：active 实盘委托的 Cancel 只发撤单文件，本地不预支终态——柜台
//     撤单回报经 T04 归并器落定 cancelled；撤单 UNKNOWN 同样不重发。
//   - 崩溃间隙（submitted 已落、回执未入账）不算决议：order_trail 缺发布记录
//     本身就是人核锚点，处置只许按原 SID 只读查询。
// =============================================================================

import (
	"context"
	"fmt"
	"math"
	"strconv"

	"server/pkg/brokerif/remote"
	"server/pkg/events"
	"server/pkg/symbol"
)

// FileBroker 是东财文件单出口端口。biz 只见提交/撤单意图与传输回执；实现由
// core 装配（remote.Client）。回执不是柜台受理，受理真值只来自柜台回报链。
type FileBroker interface {
	Submit(ctx context.Context, order remote.Order) (remote.Receipt, error)
	Cancel(ctx context.Context, requestID, sid string) (remote.Receipt, error)
}

// AttachFileBroker 把文件单出口挂进引擎。装配期调用（Runner 创建时）；双闸
// （LiveEnabled/BrokerConnected）仍是任何实盘出口的必经门禁，本接线不放行。
func (e *Engine) AttachFileBroker(b FileBroker) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.fileBroker = b
}

const (
	trailExportPublished  = "文件单已发布，等待柜台回报"
	trailCancelPublished  = "撤单文件已发布，等待柜台撤单回报"
	trailTransportUnknown = "transport_unknown"
	notifyExportUnknown   = "实盘文件单传输回执未知"
	notifyExportFailed    = "实盘文件单出口失败"
	notifyCancelUnknown   = "实盘撤单传输回执未知"
	notifyCancelFailed    = "实盘撤单出口失败"
)

// liveBrokerOrder 把本地委托翻成文件单载荷。价格必须是合法的 0.01 最小变动
// 价位——传输层契约不许代为入舍，入不了位的本地直接判明确错误。
func liveBrokerOrder(order Order) (remote.Order, error) {
	parsed, err := symbol.Parse(order.Symbol)
	if err != nil {
		return remote.Order{}, fmt.Errorf("证券代码非法：%v", err)
	}
	if order.Price <= 0 || math.Abs(round2(order.Price)-order.Price) > 1e-9 {
		return remote.Order{}, fmt.Errorf("价格 %.4f 不是 0.01 最小变动价位", order.Price)
	}
	return remote.Order{
		SID:    order.ClientOrderID,
		Symbol: parsed.EastMoney(),
		Side:   order.Side,
		Price:  strconv.FormatFloat(order.Price, 'f', 2, 64),
		Volume: order.Volume,
	}, nil
}

func cancelRequestID(clientOrderID string) string {
	return clientOrderID + "#cancel"
}

func clipRunes(s string, n int) string {
	if len([]rune(s)) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// settleLiveSubmit 在账本事务之外发文件，再把传输回执入第二笔事务。
func (e *Engine) settleLiveSubmit(ctx context.Context, order Order) (Order, error) {
	cmd, buildErr := liveBrokerOrder(order)
	var receipt remote.Receipt
	var serr error
	if buildErr != nil {
		serr = buildErr
	} else {
		receipt, serr = e.fileBroker.Submit(ctx, cmd)
	}
	err := e.withLedger(ctx, "global", func(c context.Context) error {
		var err error
		order, err = e.recordSubmitReceipt(c, order, receipt, serr)
		return err
	})
	return order, err
}

// recordSubmitReceipt 按 UNKNOWN 纪律把提交回执入账（第二笔账本事务内）。
// 三类归属只按传输证据裁决：发布、结局未知、明确失败。
func (e *Engine) recordSubmitReceipt(c context.Context, order Order, receipt remote.Receipt, serr error) (Order, error) {
	if serr == nil && receipt.State == "FILE_PUBLISHED" {
		batch := Batch{Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: trailExportPublished}}}
		if err := e.store.Commit(c, batch); err != nil {
			return Order{}, err
		}
		return order, nil
	}
	if serr == nil || remote.IsTransportUnknown(serr) {
		cause := receipt.Reason
		if serr != nil {
			cause = serr.Error()
		}
		batch := Batch{
			Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: trailTransportUnknown}},
			Events: []Event{notifyCritical("trade:live-export-unknown:"+order.ClientOrderID, notifyExportUnknown,
				fmt.Sprintf("委托 %s %s %s %d股 @%.2f 的文件发布结局未知（%s）。严禁重发同一 SID；请上机核对成交回报，只读查询原 SID。",
					order.ClientOrderID, order.Symbol, order.Side, order.Volume, order.Price, clipRunes(cause, 120)))},
		}
		if err := e.store.Commit(c, batch); err != nil {
			return Order{}, err
		}
		return order, nil
	}
	order.Status = StatusRejected
	order.Reason = clipRunes("出口失败："+serr.Error(), 180)
	batch := Batch{
		Orders: []Order{order},
		Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: order.Reason}},
		Events: []Event{
			{Subject: events.SubjectTradeOrder, Payload: orderPayload(order)},
			notifyCritical("trade:live-export-failed:"+order.ClientOrderID, notifyExportFailed,
				fmt.Sprintf("委托 %s 被文件单出口明确拒绝：%s。文件未发布；请人工核对重新下单。", order.ClientOrderID, order.Reason)),
		},
	}
	if err := e.store.Commit(c, batch); err != nil {
		return Order{}, err
	}
	return order, nil
}

// settleLiveCancel 发撤单文件并把回执入账：本地不预支终态，柜台撤单回报经
// T04 归并器落定 cancelled。cause 只进人核文案，不改语义。
func (e *Engine) settleLiveCancel(ctx context.Context, order Order, cause string) (Order, error) {
	receipt, serr := e.fileBroker.Cancel(ctx, cancelRequestID(order.ClientOrderID), order.ClientOrderID)
	err := e.withLedger(ctx, "global", func(c context.Context) error {
		fresh, ok, err := e.store.LoadOrder(c, order.ClientOrderID)
		if err != nil {
			return err
		}
		if !ok {
			return ErrNotFound
		}
		order, err = e.recordCancelReceipt(c, fresh, receipt, serr, cause)
		return err
	})
	return order, err
}

// recordCancelReceipt 把撤单回执入账（第二笔账本事务内）。撤单文件发布后本地
// 保持 working：真值由柜台撤单回报落定。明确错误意味撤单文件确定未发布——
// 委托仍有效，按人核路径处理。
func (e *Engine) recordCancelReceipt(c context.Context, order Order, receipt remote.Receipt, serr error, cause string) (Order, error) {
	if serr == nil && receipt.State == "FILE_PUBLISHED" {
		batch := Batch{Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: trailCancelPublished}}}
		if err := e.store.Commit(c, batch); err != nil {
			return Order{}, err
		}
		return order, nil
	}
	if serr == nil || remote.IsTransportUnknown(serr) {
		detail := receipt.Reason
		if serr != nil {
			detail = serr.Error()
		}
		batch := Batch{
			Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: trailTransportUnknown}},
			Events: []Event{notifyCritical("trade:live-cancel-unknown:"+order.ClientOrderID, notifyCancelUnknown,
				fmt.Sprintf("委托 %s 的撤单（%s）文件发布结局未知（%s）。严禁重发撤单；请上机核对委托回报。",
					order.ClientOrderID, cause, clipRunes(detail, 120)))},
		}
		if err := e.store.Commit(c, batch); err != nil {
			return Order{}, err
		}
		return order, nil
	}
	note := clipRunes("撤单出口失败："+serr.Error(), 180)
	batch := Batch{
		Trails: []Trail{{ClientOrderID: order.ClientOrderID, Status: order.Status, Note: note}},
		Events: []Event{notifyCritical("trade:live-cancel-failed:"+order.ClientOrderID, notifyCancelFailed,
			fmt.Sprintf("委托 %s 的撤单（%s）被文件单出口明确拒绝：%s。撤单文件未发布，委托仍在柜台有效；请人工处置。",
				order.ClientOrderID, cause, note))},
	}
	if err := e.store.Commit(c, batch); err != nil {
		return Order{}, err
	}
	return order, nil
}

// notifyCritical 组装 notify.request 事件载荷（level=critical 与 notify 分类器、
// category=trade 与告警分级口径对齐；dedup_key 按委托归并防告警风暴）。
func notifyCritical(dedup, title, body string) Event {
	return Event{Subject: events.SubjectNotifyRequest, Payload: map[string]any{
		"level": "critical", "category": "trade", "title": title, "body": body, "dedup_key": dedup,
	}}
}
