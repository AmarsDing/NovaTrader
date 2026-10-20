package biz

// 本文件是 M06 信号 done→closed 闭仓归属的纯函数规划器（设计合同：
// doc/开发文档/实施记录/M06-闭仓归属-2026-10-21.md §1–§4）。按 trade_fill
// 全序账本重放 (book, symbol) 的零交叉窗口，窗口内买入按 signal_id 归集成
// FIFO 批次、卖出按时间序向批队头分摊，过四道门后产出 ExitPrice（毛口径）与
// PnlPct（净口径扣双边实费）。零 IO：时间源全部来自 FillFact.CreatedAt。

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"time"
)

// FillFact 是 trade_fill 的一行账本事实，按 ent 自增 id 全序重放。
type FillFact struct {
	ID       int
	SignalID string // 空串或指向非本窗口候选信号 = 外来批（占库存、参与 FIFO、永不产出闭仓结果）
	Side     string
	Qty      int
	Price    float64
	Amount   float64
	Fee      float64 // trade_fill.fee 持久化值（净口径就取它，不复刻费率常数）

	// CreatedAt 是 trade 进程提交入库的进程墙钟，closed_at 的确定性来源
	// （承重属性是重放恒定，不是物理真实成交时刻，见合同 §1.4）。
	CreatedAt time.Time
}

// ClosureFacts 是一个 (book, symbol) pair 一次重放的全部输入。
type ClosureFacts struct {
	Signals       []Signal     // 该 pair 全部候选形态信号行（done ∧ buy ∧ ExecutedVolume 非空且 > 0）
	Fills         []FillFact   // 全序账本，匿名 fill 一体计入库存
	PendingIntent map[int]bool // Gate-D：存在未决执行意图的信号编号
}

// ClosePlan 是一条 done→closed 的归属结论，交给 Usecase.Transition 落库。
// holding_days 一律由 closePatch 按交易日历自算，planner 不出此字段。
type ClosePlan struct {
	SignalID  int
	Qty       int       // 窗口内该信号买入 Σqty（账本自证的应闭量）
	ExitPrice float64   // 毛口径：归属卖出 Σamount/Σqty，round4
	PnlPct    float64   // 净口径扣双边实费，百分比，符号按未舍入值判定，round4
	At        time.Time // 最后一笔归属卖 fill 的 CreatedAt
	Reason    string    // 「闭仓归并」前缀 + §1.5 证据 JSON

	Window       [2]int  // [窗口首 fillID, 窗口末 fillID（零交叉 fill）]
	Buys         []int   // 归属本信号的窗口内买 fill id（FIFO 序）
	Sells        []int   // 分摊到本信号的卖 fill id（时间序）
	BuyAmount    float64 // 窗口内该信号买入 Σamount（未舍入聚合，恒等式复核口径）
	BuyFee       float64 // 窗口内该信号买入 Σfee
	SellAmount   float64 // 分摊到本信号的卖出 Σamount
	SellFee      float64 // 分摊到本信号的卖出 Σfee
	ClosedFillID int     // 归属本信号的最后一笔卖 fill id（与 At 同行）
	ReplayHead   int     // 本次重放消费到的最大 fill id（决策时刻账本尾部见证）
}

// ClosureResult 是一次 pair 重放的归属结论。
type ClosureResult struct {
	Plans []ClosePlan // 可闭仓信号，按窗口序 × 窗口内 FIFO 批次序
	// Over 是 Gate-B 防御分支（Σ名下买入 > ExecutedVolume）跳过的信号编号，
	// 由 core 做 Warnf 计数与持续超 2 分钟的 Errorf 升级（合同 §4.1）。
	Over []int
}

const (
	closureReasonVersion = 1
	closureReasonLimit   = 4096 // 证据 JSON 超 4KB 时 buys/sells 降级为区间摘要，键一个不少
	closureReasonPrefix  = "闭仓归并 "
)

// closureEvidence 是 §1.5 冻结的证据 JSON 形态，键一个不少（值可降级）。
type closureEvidence struct {
	V            int     `json:"v"`
	Window       [2]int  `json:"window"`
	SignalID     int     `json:"signal_id"`
	Qty          int     `json:"qty"`
	BuyAmount    float64 `json:"buy_amount"`
	BuyFee       float64 `json:"buy_fee"`
	SellAmount   float64 `json:"sell_amount"`
	SellFee      float64 `json:"sell_fee"`
	ExitPrice    float64 `json:"exit_price"`
	PnlPct       float64 `json:"pnl_pct"`
	ClosedFillID int     `json:"closed_fill_id"`
	ReplayHead   int     `json:"replay_head"`
	Buys         any     `json:"buys"`  // ≤4KB 全量 fill id 列表，超限降级为 closureIDRange
	Sells        any     `json:"sells"` // 同上
}

// closureIDRange 是 fill id 列表超 4KB 时的降级值（区间摘要，值降级而非键消失）。
type closureIDRange struct {
	First int `json:"first"`
	Last  int `json:"last"`
	Count int `json:"count"`
}

// closeBatch 是窗口内同一 signal_id 归集成的 FIFO 批次；外来批同样占位参与分摊。
type closeBatch struct {
	key        string // fill.SignalID 原文；命中候选键才可归属，否则外来批
	qty        int    // 窗口内买入 Σqty
	remaining  int    // 尚未被卖出分摊的余量
	allocated  int    // 已被卖出分摊的量
	buyAmount  float64
	buyFee     float64
	sellAmount float64
	sellFee    float64
	buys       []int
	sells      []int
	lastSellID int
	lastSellAt time.Time
}

func round4(v float64) float64 { return math.Round(v*1e4) / 1e4 }

// validClosureFill 校验账本行。账本矛盾不可重放，属库存恒等门（整 pair loud + 冷却）。
func validClosureFill(f FillFact) error {
	if f.Side != "buy" && f.Side != "sell" {
		return fmt.Errorf("%w: closure fill %d unknown side %q", ErrBadRequest, f.ID, f.Side)
	}
	if f.ID <= 0 || f.Qty <= 0 {
		return fmt.Errorf("%w: closure fill %d bad identity or qty %d", ErrBadRequest, f.ID, f.Qty)
	}
	if f.Price <= 0 || math.IsNaN(f.Price) || math.IsInf(f.Price, 0) {
		return fmt.Errorf("%w: closure fill %d bad price %v", ErrBadRequest, f.ID, f.Price)
	}
	if f.Amount <= 0 || math.IsNaN(f.Amount) || math.IsInf(f.Amount, 0) || math.Abs(f.Amount-f.Price*float64(f.Qty)) > 0.02 {
		return fmt.Errorf("%w: closure fill %d bad amount %v", ErrBadRequest, f.ID, f.Amount)
	}
	if f.Fee < 0 || math.IsNaN(f.Fee) || math.IsInf(f.Fee, 0) {
		return fmt.Errorf("%w: closure fill %d bad fee %v", ErrBadRequest, f.ID, f.Fee)
	}
	return nil
}

// PlanClosure 是纯函数：窗口界定 → FIFO 归属 → 四道门 → 双口径 → reason JSON。
// 库存恒等门任一矛盾（账本行脏 / 卖超 / 窗口内 Σ卖≠Σ买）返回 error，
// 整 pair loud Errorf + 冷却由 core 兜底；Gate-B 滞后与 Gate-D 跳过不视为错误。
func PlanClosure(facts *ClosureFacts) (ClosureResult, error) {
	out := ClosureResult{}
	if facts == nil || len(facts.Fills) == 0 {
		return out, nil
	}
	for _, f := range facts.Fills {
		if err := validClosureFill(f); err != nil {
			return out, err
		}
	}
	// Gate-B 归属证据门：对比基准 = 信号全部名下 buy fill，不限窗口。
	// signal_id 解析失败或指向非候选信号的买入不会命中任何候选键，自然按外来批处理。
	namedBuy := map[string]int{}
	for _, f := range facts.Fills {
		if f.Side == "buy" {
			namedBuy[f.SignalID] += f.Qty
		}
	}
	eligible := map[string]bool{}
	for i := range facts.Signals {
		s := &facts.Signals[i]
		if s.ExecutedVolume == nil || *s.ExecutedVolume <= 0 {
			continue // 投影事实缺失：宁缺毋伪造（候选谓语已挡，此处防御复挡）
		}
		if facts.PendingIntent[s.ID] {
			continue // Gate-D 残余意图门：本轮跳过，不冷却
		}
		sum := namedBuy[strconv.Itoa(s.ID)]
		switch {
		case sum < *s.ExecutedVolume:
			continue // Gate-B：投影滞后/在途，本轮静默跳过（下轮自愈）
		case sum > *s.ExecutedVolume:
			out.Over = append(out.Over, s.ID) // 结构不可达防御分支：跳过并报超配
			continue
		}
		eligible[strconv.Itoa(s.ID)] = true
	}

	var (
		batches   []*closeBatch // 本窗口 FIFO 批队（窗口闭合后整队判定）
		open      = map[string]*closeBatch{}
		inventory int // 累计库存 Q：买入加、卖出减，自 >0 归至 ==0 划出窗口
		winFirst  = facts.Fills[0].ID
		winBought int
		winSold   int
		queueHead int // 本窗口 FIFO 队头游标：批次摊满即前移，整窗 O(n)
	)
	replayHead := facts.Fills[len(facts.Fills)-1].ID // 决策时刻账本尾部见证（本次重放消费到的最大 fill id）
	flush := func(winLast int) error {
		if winBought != winSold {
			// 窗口内 Σ卖≠Σ买：零交叉结构性成立，此处是防御性复核。
			return fmt.Errorf("%w: closure window buy/sell mismatch %d/%d", ErrBadRequest, winBought, winSold)
		}
		for _, b := range batches {
			if !eligible[b.key] {
				continue // 外来批：占库存、参与 FIFO 排序、永不产出闭仓结果
			}
			if b.allocated != b.qty || b.allocated == 0 {
				return fmt.Errorf("%w: closure batch allocation %d/%d at zero crossing", ErrBadRequest, b.allocated, b.qty)
			}
			plan, err := closePlanOf(b, winFirst, winLast, replayHead)
			if err != nil {
				return err
			}
			out.Plans = append(out.Plans, plan)
		}
		return nil
	}
	for _, f := range facts.Fills {
		if len(batches) == 0 && winBought == 0 && winSold == 0 {
			winFirst = f.ID // 窗口 = 上一零交叉（不含）起的首笔 fill
		}
		switch f.Side {
		case "buy":
			inventory += f.Qty
			winBought += f.Qty
			b := open[f.SignalID]
			if b == nil {
				b = &closeBatch{key: f.SignalID}
				open[f.SignalID] = b
				batches = append(batches, b)
			}
			b.qty += f.Qty
			b.remaining += f.Qty
			b.buyAmount += f.Amount
			b.buyFee += f.Fee
			b.buys = append(b.buys, f.ID)
		case "sell":
			if f.Qty > inventory {
				return out, fmt.Errorf("%w: closure ledger oversold at fill %d", ErrBadRequest, f.ID) // 卖超：库存恒等门
			}
			inventory -= f.Qty
			winSold += f.Qty
			need := f.Qty
			amountLeft, feeLeft := f.Amount, f.Fee
			for need > 0 && queueHead < len(batches) {
				b := batches[queueHead]
				if b.remaining == 0 {
					queueHead++
					continue
				}
				take := b.remaining
				if take > need {
					take = need
				}
				var amount, fee float64
				if take == need {
					// 本卖 fill 的最后一份取余：分摊量按序归集后恒等式精确守恒。
					amount, fee = amountLeft, feeLeft
				} else {
					amount = f.Amount * float64(take) / float64(f.Qty)
					fee = f.Fee * float64(take) / float64(f.Qty)
					amountLeft -= amount
					feeLeft -= fee
				}
				b.remaining -= take
				b.allocated += take
				b.sellAmount += amount
				b.sellFee += fee
				if n := len(b.sells); n == 0 || b.sells[n-1] != f.ID {
					b.sells = append(b.sells, f.ID)
				}
				b.lastSellID = f.ID
				b.lastSellAt = f.CreatedAt
				need -= take
			}
			if need > 0 {
				// 队空仍有卖出未分摊：卖超防御性兜底（库存恒等门）。
				return out, fmt.Errorf("%w: closure ledger oversold at fill %d", ErrBadRequest, f.ID)
			}
		}
		if inventory == 0 {
			if err := flush(f.ID); err != nil {
				return out, err
			}
			batches, open = nil, map[string]*closeBatch{}
			winBought, winSold, queueHead = 0, 0, 0
		}
	}
	// 窗口结束时批队头有残余（尾段未零交叉）：残余库存滚入下一窗口，
	// 本窗口不闭，下次重放自账本头重算，无需在此产出。
	return out, nil
}

// closePlanOf 把窗口内已摊满的候选批次折算成闭仓结论与证据 JSON。
func closePlanOf(b *closeBatch, winFirst, winLast, replayHead int) (ClosePlan, error) {
	id, err := strconv.Atoi(b.key)
	if err != nil || id <= 0 {
		return ClosePlan{}, fmt.Errorf("%w: closure batch signal identity %q", ErrBadRequest, b.key)
	}
	plan := ClosePlan{
		SignalID: id, Qty: b.qty, Window: [2]int{winFirst, winLast},
		Buys: b.buys, Sells: b.sells,
		BuyAmount: b.buyAmount, BuyFee: b.buyFee, SellAmount: b.sellAmount, SellFee: b.sellFee,
		ClosedFillID: b.lastSellID, ReplayHead: replayHead, At: b.lastSellAt,
	}
	// exit_price 毛口径（对称 executed_price）：归属卖出 Σamount/Σqty，round4，numeric(12,4)。
	exit := round4(b.sellAmount / float64(b.qty))
	// exit 显式拒 +Inf/NaN/<=0：closePatch 只挡 NaN 不挡 +Inf，此洞在 planner 侧堵死，
	// 必须显式断言，不依赖间接不变量（合同 §1.3 CAS 状态门）。
	if exit <= 0 || math.IsInf(exit, 0) || math.IsNaN(exit) {
		return ClosePlan{}, fmt.Errorf("%w: closure exit price %v rejected", ErrBadRequest, exit)
	}
	// pnl_pct 净口径扣双边实费：(Σ卖(amount−fee) − Σ买(amount+fee)) / Σ买(amount+fee) × 100。
	buyCost := b.buyAmount + b.buyFee
	raw := ((b.sellAmount - b.sellFee) - buyCost) / buyCost * 100
	if math.IsInf(raw, 0) || math.IsNaN(raw) {
		return ClosePlan{}, fmt.Errorf("%w: closure pnl %v rejected", ErrBadRequest, raw)
	}
	pnl := round4(raw)
	if pnl == 0 && raw != 0 {
		// 符号按未舍入值判定（保护 M04 win/loss/flat 不被舍入翻转），再 round 4 位。
		pnl = math.Copysign(0.0001, raw)
	}
	plan.ExitPrice, plan.PnlPct = exit, pnl
	plan.Reason, err = closureReason(plan)
	if err != nil {
		return ClosePlan{}, err
	}
	return plan, nil
}

// closureReason 产出 §1.5 证据：中文前缀「闭仓归并」+ 紧凑 JSON，键一个不少。
func closureReason(plan ClosePlan) (string, error) {
	ev := closureEvidence{
		V: closureReasonVersion, Window: plan.Window, SignalID: plan.SignalID, Qty: plan.Qty,
		BuyAmount: round4(plan.BuyAmount), BuyFee: round4(plan.BuyFee),
		SellAmount: round4(plan.SellAmount), SellFee: round4(plan.SellFee),
		ExitPrice: plan.ExitPrice, PnlPct: plan.PnlPct,
		ClosedFillID: plan.ClosedFillID, ReplayHead: plan.ReplayHead,
		Buys: plan.Buys, Sells: plan.Sells,
	}
	body, err := json.Marshal(ev)
	if err != nil {
		return "", fmt.Errorf("%w: closure evidence: %v", ErrBadRequest, err)
	}
	if len(body) > closureReasonLimit {
		ev.Buys = shrinkIDs(plan.Buys)
		ev.Sells = shrinkIDs(plan.Sells)
		if body, err = json.Marshal(ev); err != nil {
			return "", fmt.Errorf("%w: closure evidence: %v", ErrBadRequest, err)
		}
	}
	return closureReasonPrefix + string(body), nil
}

func shrinkIDs(ids []int) closureIDRange {
	r := closureIDRange{Count: len(ids)}
	if len(ids) > 0 {
		r.First, r.Last = ids[0], ids[len(ids)-1]
	}
	return r
}

// ClosureProjection 以类型断言扩展模式取闭仓归并的只读仓储（wire 零变动，
// 与 execution.go 的 ExecutionProjectionRepo 同款）。
func (uc *Usecase) ClosureProjection() (ClosureProjectionRepo, bool) {
	repo, ok := uc.signals.(ClosureProjectionRepo)
	return repo, ok
}

// ClosurePairOf 返回候选信号归属的 (book, symbol)：页内 pair 去重与评估以它为键。
func (uc *Usecase) ClosurePairOf(ctx context.Context, id int) (book, symbol string, err error) {
	s, err := uc.signals.Get(ctx, id)
	if err != nil {
		return "", "", err
	}
	return s.Book, s.Symbol, nil
}
