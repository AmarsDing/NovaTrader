package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
)

// DeadLetterStore 死信重排施写口（N2-02 死信重放审批）：由 data 层实现。
// 断言式接入，不扩大 Auditor 合同；与 DispositionStore 同型。
type DeadLetterStore interface {
	// RequeueDeadLetter 对「未投递且当前是死信」的行做条件单行重排。
	// found=false 表示不存在可重排行（已重排、已投递或从未死信）。
	RequeueDeadLetter(ctx context.Context, eventID string) (found bool, err error)
}

// replayReasonMax 重放理由上限：审批理由必须给出但不可任意长。
const replayReasonMax = 280

// replayDeadLetter 死信重放审批：POST only、Owner-only、审计必写。
// 只做条件单行重排（dead_letter→false、attempts 归零、清租约与下次尝试时刻），
// 实际投递由 outbox 调度器下一拍执行；重排不等于投递成功，失败仍会按退避重新起死信。
// 监控只读口径不变：此口不挂监控面板，仅 API 直连。
func (g *Gate) replayDeadLetter(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeErr(w, http.StatusMethodNotAllowed, "只支持 POST")
		return
	}
	principal, ok := g.principal(r)
	if !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	if !isOwner(principal.Role) {
		writeErr(w, http.StatusForbidden, "需要 Owner")
		return
	}
	// 重放是后果性动作：审计未启用时前置拒绝，一行都不改。
	if g.Audit == nil || !g.Audit.Enabled() {
		writeErr(w, http.StatusServiceUnavailable, "审计未启用")
		return
	}
	store, ok := g.Audit.(DeadLetterStore)
	if !ok || store == nil {
		writeErr(w, http.StatusServiceUnavailable, "死信重排未启用")
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<16))
	if err != nil {
		writeErr(w, http.StatusBadRequest, "请求读不了")
		return
	}
	var req struct {
		EventID string `json:"event_id"`
		Reason  string `json:"reason"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求不是 JSON")
		return
	}
	req.EventID = strings.TrimSpace(req.EventID)
	req.Reason = strings.TrimSpace(req.Reason)
	if !observationID.MatchString(req.EventID) {
		writeErr(w, http.StatusBadRequest, "event_id 需为有限标识")
		return
	}
	if req.Reason == "" {
		writeErr(w, http.StatusBadRequest, "重放必须给出理由")
		return
	}
	if len(req.Reason) > replayReasonMax {
		writeErr(w, http.StatusBadRequest, "理由过长")
		return
	}
	found, err := store.RequeueDeadLetter(r.Context(), req.EventID)
	if err != nil {
		_ = g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
			Target: clip(req.EventID, 128), OK: false, Detail: clip(err.Error(), 256),
		})
		writeErr(w, http.StatusServiceUnavailable, "死信重排失败")
		return
	}
	if !found {
		_ = g.audit(r.Context(), Entry{
			Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
			Target: clip(req.EventID, 128), OK: false, Detail: clip("无可重排死信: "+req.Reason, 256),
		})
		writeErr(w, http.StatusConflict, "不存在未投递死信《"+req.EventID+"》，可能已重排、已投递或从未死信")
		return
	}
	if err := g.audit(r.Context(), Entry{
		Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
		Target: clip(req.EventID, 128), OK: true, Detail: clip(req.Reason, 256),
	}); err != nil {
		writeErr(w, http.StatusServiceUnavailable, "审计写入失败")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":   true,
		"note": "已重排回投递队列，实际投递由 outbox 调度器下一拍执行；重排不等于投递成功，再次失败仍会按退避退为死信",
	})
}
