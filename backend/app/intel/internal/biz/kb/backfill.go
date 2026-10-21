package kb

import (
	"context"
	"encoding/json"
	"time"
)

// M13-T10 kb_case 归因回填：strategy 的 SignalUpdated 载荷在 W2 起补
// attribution/pattern/emotion_phase/missed 四字段。回填任务扫描已投递落库
// （outbox）的终态信号事件，为缺 kb_case 的信号按同一 RecordCase 路径建案例，
// (book, signal_id) 唯一保证重跑幂等。四字段任一缺席时该信号跳过并计数：
// 字段是 W2 才有的，缺席说明载荷来自旧版本生产端，不归因、不猜值。

// SignalEventRow 是 outbox 里一条已投递事件的回填视图。
type SignalEventRow struct {
	EventID     string
	Payload     []byte
	PublishedAt time.Time
}

// SignalRef 用 (book, signal_id) 唯一定位一条信号，与 kb_case 的唯一约束一致。
type SignalRef struct {
	Book     string
	SignalID int64
}

// BackfillResult 是一轮回填的计数。Skipped 是载荷缺归因四字段被跳过的信号数，
// 显式计数表达缺失（数据纪律），不当作失败。
type BackfillResult struct {
	Scanned  int // 读到的持久事件行数（含解析失败的）
	Created  int // 新建案例数
	Existing int // 已存在案例去重数
	Skipped  int // 四字段缺席而跳过的信号数
	Failed   int // 载荷解析失败或 RecordCase 失败的条数
}

// signalPayload 是 strategy.signal.updated 载荷里回填关心的部分。
// 四个归因字段用指针表达缺席：W2 之前的载荷没有这些键。
type signalPayload struct {
	SignalID     int64      `json:"signal_id"`
	Book         string     `json:"book"`
	Status       string     `json:"status"`
	StockCode    string     `json:"stock_code"`
	StockName    string     `json:"stock_name"`
	Strategy     string     `json:"strategy"`
	SignalType   string     `json:"signal_type"`
	SignalTime   time.Time  `json:"signal_time"`
	ClosedAt     *time.Time `json:"closed_at"`
	EntryPrice   *float64   `json:"entry_price"`
	ExitPrice    *float64   `json:"exit_price"`
	PnlPct       *float64   `json:"pnl_pct"`
	HoldingDays  *int       `json:"holding_days"`
	Reasoning    string     `json:"reasoning"`
	Review       string     `json:"review"`
	Attribution  *string    `json:"attribution"`
	Pattern      *string    `json:"pattern"`
	EmotionPhase *string    `json:"emotion_phase"`
	Missed       *bool      `json:"missed"`
}

// hasAttributionBundle 判断载荷是否带齐 M13-T10 的四个归因字段。
func (p *signalPayload) hasAttributionBundle() bool {
	return p.Attribution != nil && p.Pattern != nil && p.EmotionPhase != nil && p.Missed != nil
}

func (p *signalPayload) ref() SignalRef { return SignalRef{Book: p.Book, SignalID: p.SignalID} }

func (p *signalPayload) toSignalCase() SignalCase {
	s := SignalCase{
		SignalID:     p.SignalID,
		Book:         p.Book,
		Status:       p.Status,
		StockCode:    p.StockCode,
		StockName:    p.StockName,
		Strategy:     p.Strategy,
		SignalType:   p.SignalType,
		SignalTime:   p.SignalTime,
		EntryPrice:   p.EntryPrice,
		ExitPrice:    p.ExitPrice,
		PnlPct:       p.PnlPct,
		HoldingDays:  p.HoldingDays,
		Pattern:      *p.Pattern,
		EmotionPhase: *p.EmotionPhase,
		Attribution:  *p.Attribution,
		Reasoning:    p.Reasoning,
		Review:       p.Review,
	}
	if p.ClosedAt != nil {
		s.ClosedAt = *p.ClosedAt
	}
	return s
}

// BackfillCases 扫描 subject 主题 since 以来的持久事件，给缺案例的终态信号补 kb_case。
// 单条失败不中止整轮；限 limit 条防护事件积压。
func (uc *KnowledgeUsecase) BackfillCases(ctx context.Context, subject string, since time.Time, limit int) (*BackfillResult, error) {
	if limit <= 0 {
		limit = 500
	}
	rows, err := uc.repo.ListPublishedSignalEvents(ctx, subject, since, limit)
	if err != nil {
		return nil, err
	}
	res := &BackfillResult{Scanned: len(rows)}
	// 同一信号可能多次投递终态事件，按 ref 去重，保留最后一条载荷。
	candidates := map[SignalRef]*signalPayload{}
	for _, row := range rows {
		var p signalPayload
		if err := json.Unmarshal(row.Payload, &p); err != nil {
			res.Failed++
			uc.log.WithContext(ctx).Warnf("backfill: bad payload of event %s: %v", row.EventID, err)
			continue
		}
		if !IsTerminal(p.Status) {
			continue
		}
		candidates[p.ref()] = &p
	}
	refs := make([]SignalRef, 0, len(candidates))
	for ref := range candidates {
		refs = append(refs, ref)
	}
	existing, err := uc.repo.ExistingSignalCases(ctx, refs)
	if err != nil {
		return nil, err
	}
	for ref, p := range candidates {
		if existing[ref] {
			res.Existing++
			continue
		}
		if !p.hasAttributionBundle() {
			res.Skipped++
			continue
		}
		out, err := uc.RecordCase(ctx, p.toSignalCase())
		if err != nil {
			res.Failed++
			uc.log.WithContext(ctx).Warnf("backfill: record case for %s signal %d: %v", ref.Book, ref.SignalID, err)
			continue
		}
		if out.Created {
			res.Created++
		} else {
			res.Existing++
		}
	}
	return res, nil
}
