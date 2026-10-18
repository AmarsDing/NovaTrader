package gateway

import (
	"context"
	"net/http"
)

// SourceCoverage 是一个生产者的因果形状统计（N0-02/N2-02 全域因果覆盖口径）。
// total = with_trace + without_trace；children 统计带因果父的事件（按理 ⊆ with_trace，
// 但「有父却丢链号」的畸形行会同时计入 children 与 without_trace——如实计数，不掩饰）。
// orphan_parents 表示因果父未在同一窗口持久历史中重现：父事件可能本来就不落盘
// （market.* 走内存），或在窗口之外，不等于链路断裂。
type SourceCoverage struct {
	Source        string `json:"source,omitempty"`
	Total         int    `json:"total"`
	WithTrace     int    `json:"with_trace"`
	WithoutTrace  int    `json:"without_trace"`
	Children      int    `json:"children"`
	OrphanParents int    `json:"orphan_parents"`
}

// TraceCoverage 是持久域事件（outbox）的因果覆盖快照，回答「全域因果现在到底续上了多少」：
// 根事件=有链号无因果父，续链=有因果父，无链=链号为空。只覆盖落盘域事件；
// 行情快照与 brain.progress 按设计不走 JetStream，不在此口径内。
type TraceCoverage struct {
	SchemaVersion  int              `json:"schema_version"`
	Available      bool             `json:"available"`
	Scope          string           `json:"scope"`
	AsOf           string           `json:"as_of"`
	Note           string           `json:"note"`
	WindowLimit    int              `json:"window_limit"`
	WindowRows     int              `json:"window_rows"`
	OldestInWindow string           `json:"oldest_in_window,omitempty"`
	Totals         SourceCoverage   `json:"totals"`
	BySource       []SourceCoverage `json:"by_source"`
}

// TraceCoverageReader 由 data 层实现；断言式接入，不扩大 Auditor 合同。
type TraceCoverageReader interface {
	ReadTraceCoverage(ctx context.Context, limit int) (*TraceCoverage, error)
}

const traceCoverageWindowLimit = 2000

// traceCoverage 全域因果覆盖口径：GET /api/ops/trace-coverage。只读，登录即可（与观测同权）。
// 它度量因果缺口本身，不制造因果：生产端续链改造以这里的数字为准绳逐项推进。
func (g *Gate) traceCoverage(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if r.Method != http.MethodGet {
		w.Header().Set("Allow", http.MethodGet)
		writeErr(w, http.StatusMethodNotAllowed, "因果覆盖只读")
		return
	}
	reader, ok := g.Audit.(TraceCoverageReader)
	if !ok || reader == nil {
		writeErr(w, http.StatusServiceUnavailable, "因果覆盖未启用")
		return
	}
	coverage, err := reader.ReadTraceCoverage(r.Context(), traceCoverageWindowLimit)
	if err != nil {
		writeErr(w, http.StatusServiceUnavailable, "因果覆盖读取失败")
		return
	}
	writeJSON(w, http.StatusOK, coverage)
}
