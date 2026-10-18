package data

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"entgo.io/ent/dialect/sql"

	"server/app/admin/internal/gateway"
	box "server/ent/outbox"
	"server/pkg/events"
)

// ReadTraceCoverage 统计最近 limit 条持久域事件的因果形状（N0-02/N2-02 全域因果覆盖口径）。
// 窗口有界：样本量随结果返回，最老一条的时间标注读者在看哪一段；
// 只解码合同元数据副本（metadata 列的信封），不读 raw business payload。
func (s *AuditStore) ReadTraceCoverage(ctx context.Context, limit int) (*gateway.TraceCoverage, error) {
	if !s.Enabled() {
		return nil, fmt.Errorf("database unavailable")
	}
	if limit <= 0 || limit > 5000 {
		limit = 5000
	}
	rows, err := s.client.Outbox.Query().
		Order(box.ByCreatedAt(sql.OrderDesc()), box.ByID(sql.OrderDesc())).
		Limit(limit).
		All(ctx)
	if err != nil {
		return nil, err
	}
	out := &gateway.TraceCoverage{
		SchemaVersion: 1,
		Available:     true,
		Scope:         "durable_outbox_history",
		AsOf:          time.Now().UTC().Format(time.RFC3339Nano),
		WindowLimit:   limit,
		WindowRows:    len(rows),
		BySource:      []gateway.SourceCoverage{},
		Note: "持久域事件的因果形状分布：with_trace=有链号，children=带因果父（续链），without_trace=链号为空。" +
			"orphan_parents 指父事件未在同一窗口持久历史中重现——行情等内存域父事件本来就不落盘，也可能在窗口外，不等于链路断裂。" +
			"只覆盖落盘域事件；market.* 与 brain.progress 按设计走内存，不在此口径。",
	}
	if len(rows) > 0 {
		oldest := rows[len(rows)-1].CreatedAt
		out.OldestInWindow = stamp(&oldest)
	}
	ids := make(map[string]bool, len(rows))
	for _, r := range rows {
		ids[r.EventID] = true
	}
	bySource := map[string]*gateway.SourceCoverage{}
	for _, r := range rows {
		source := r.Source
		if source == "" {
			source = "(unknown)" // 信封本来就不允许空 source；防御性占位，便于认出异常行
		}
		sc, ok := bySource[source]
		if !ok {
			sc = &gateway.SourceCoverage{Source: source}
			bySource[source] = sc
		}
		causation := causationOf(r.Metadata)
		for _, bucket := range []*gateway.SourceCoverage{sc, &out.Totals} {
			bucket.Total++
			if r.TraceID == "" {
				bucket.WithoutTrace++
			} else {
				bucket.WithTrace++
			}
			if causation != "" {
				bucket.Children++
				if !ids[causation] {
					bucket.OrphanParents++
				}
			}
		}
	}
	ordered := make([]gateway.SourceCoverage, 0, len(bySource))
	for _, sc := range bySource {
		ordered = append(ordered, *sc)
	}
	sort.Slice(ordered, func(i, j int) bool { return ordered[i].Source < ordered[j].Source })
	out.BySource = ordered
	return out, nil
}

// causationOf 只从合同元数据副本取因果父 ID；副本缺失或畸形时按无父如实计数。
func causationOf(metadata map[string]any) string {
	b, err := json.Marshal(metadata)
	if err != nil {
		return ""
	}
	var meta events.Envelope
	if err := json.Unmarshal(b, &meta); err != nil {
		return ""
	}
	return meta.CausationID
}
