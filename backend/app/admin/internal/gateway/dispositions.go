package gateway

import (
	"encoding/json"
	"io"
	"net/http"
	"strconv"
	"strings"
	"context"
)

// Disposition 是一条事件处置留痕（N2-02 处置回执）：对持久 outbox 事件的人工标注。
// 留痕只标注，不改投递状态、不回放事件、不可删改；死信仍是死信。
type Disposition struct {
	ID        int    `json:"id"`
	EventID   string `json:"event_id"`
	TraceID   string `json:"trace_id,omitempty"`
	Action    string `json:"action"`
	Note      string `json:"note,omitempty"`
	Actor     string `json:"actor"`
	CreatedAt string `json:"created_at"`
}

// DispositionStore 由 data 层实现；断言式接入，不扩大 Auditor 合同。
type DispositionStore interface {
	ListDispositions(ctx context.Context, eventID, traceID string, limit int) ([]Disposition, error)
	WriteDisposition(ctx context.Context, d Disposition) error
}

// 处置动作封闭集合：只含标注动词，不含 replay/restart/delete 等后果性动作。
var dispositionActions = map[string]bool{
	"acknowledged": true, "investigating": true, "resolved": true, "ignored": true, "escalated": true,
}

const dispositionNoteMax = 280

// dispositions 事件处置留痕：GET 需登录，POST 需 Owner 且审计必须可写。
// 两个方向都不读 raw payload，不接受任意长文本。
func (g *Gate) dispositions(w http.ResponseWriter, r *http.Request) {
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	store, ok := g.Audit.(DispositionStore)
	if !ok || store == nil {
		writeErr(w, http.StatusServiceUnavailable, "处置留痕未启用")
		return
	}
	switch r.Method {
	case http.MethodGet:
		eventID := strings.TrimSpace(r.URL.Query().Get("event_id"))
		traceID := strings.TrimSpace(r.URL.Query().Get("trace_id"))
		if (eventID != "" && !observationID.MatchString(eventID)) || (traceID != "" && !observationID.MatchString(traceID)) {
			writeErr(w, http.StatusBadRequest, "event_id/trace_id 需为有限标识")
			return
		}
		if eventID == "" && traceID == "" {
			writeErr(w, http.StatusBadRequest, "需要 event_id 或 trace_id")
			return
		}
		limit, err := strconv.Atoi(strings.TrimSpace(r.URL.Query().Get("limit")))
		if err != nil || limit <= 0 || limit > 100 {
			limit = 100
		}
		rows, err := store.ListDispositions(r.Context(), eventID, traceID, limit)
		if err != nil {
			writeErr(w, http.StatusServiceUnavailable, "处置留痕读取失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"items": rows, "note": "处置留痕只标注，不改投递状态、不回放事件"})
	case http.MethodPost:
		if !isOwner(principal.Role) {
			writeErr(w, http.StatusForbidden, "需要 Owner")
			return
		}
		// 留痕必须可审计：审计未启用时前置拒绝，与配置保存同口径，先不写任何行。
		if g.Audit == nil || !g.Audit.Enabled() {
			writeErr(w, http.StatusServiceUnavailable, "审计未启用")
			return
		}
		body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
		if err != nil {
			writeErr(w, http.StatusBadRequest, "请求读不了")
			return
		}
		var req struct {
			EventID string `json:"event_id"`
			TraceID string `json:"trace_id"`
			Action  string `json:"action"`
			Note    string `json:"note"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			writeErr(w, http.StatusBadRequest, "请求不是 JSON")
			return
		}
		req.EventID = strings.TrimSpace(req.EventID)
		req.TraceID = strings.TrimSpace(req.TraceID)
		req.Action = strings.TrimSpace(req.Action)
		req.Note = strings.TrimSpace(req.Note)
		if !observationID.MatchString(req.EventID) || (req.TraceID != "" && !observationID.MatchString(req.TraceID)) {
			writeErr(w, http.StatusBadRequest, "event_id/trace_id 需为有限标识")
			return
		}
		if !dispositionActions[req.Action] {
			writeErr(w, http.StatusBadRequest, "处置动作不在允许集合")
			return
		}
		if len(req.Note) > dispositionNoteMax {
			writeErr(w, http.StatusBadRequest, "备注过长")
			return
		}
		row := Disposition{EventID: req.EventID, TraceID: req.TraceID, Action: req.Action, Note: req.Note, Actor: principal.Name}
		if err := store.WriteDisposition(r.Context(), row); err != nil {
			_ = g.audit(r.Context(), Entry{
				Actor: principal.Name, Role: principal.Role, Action: "ops.disposition",
				Target: clip(req.EventID, 128), OK: false, Detail: clip(err.Error(), 256),
			})
			writeErr(w, http.StatusServiceUnavailable, "处置留痕写入失败")
			return
		}
		if err := g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: "ops.disposition",
			Target: clip(req.EventID, 128)+" "+req.Action, OK: true, Detail: clip(req.Note, 256),
		}); err != nil {
			writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
			return
		}
		writeJSON(w, http.StatusOK, map[string]any{"ok": true})
	default:
		writeErr(w, http.StatusMethodNotAllowed, "只支持 GET/POST")
	}
}
