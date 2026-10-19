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
	// RequeueDeadLetters 批量条件重排：逐条复用单行语义，各条事务相互独立、
	// 一条失败如实记 Err 并继续，不拖垮同批其余；输入顺序与重复保留。
	RequeueDeadLetters(ctx context.Context, eventIDs []string) []DeadLetterRequeueOutcome
}

// DeadLetterRequeueOutcome 是批量重排的逐条结果：如实区分重排/无可重排/施写失败，
// 绝不含糊汇成一个总数就回去。
type DeadLetterRequeueOutcome struct {
	EventID  string
	Requeued bool
	Err      error
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

// replayBatchMax 批量重放单据的事件上限：超上限的审批拆多张单据走，
// 同时把审计行数限在有界范围。
const replayBatchMax = 64

// replayDeadLetterBatch 死信批量重放审批（N2-02「批量另议」的落地；重放后自动升级
// 策略仍另议并在 M11 开发进度留痕）。前置守卫、审计口径与单行版相同：
// 逐条条件重排（error→Err 不拖垮整批），每条各写一条 ops.dead_letter_replay 审计，
// 结果数组如实区分 requeued / skipped / error，绝不只回一个汇总成功。
func (g *Gate) replayDeadLetterBatch(w http.ResponseWriter, r *http.Request) {
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
		EventIDs []string `json:"event_ids"`
		Reason   string   `json:"reason"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		writeErr(w, http.StatusBadRequest, "请求不是 JSON")
		return
	}
	req.Reason = strings.TrimSpace(req.Reason)
	if req.Reason == "" {
		writeErr(w, http.StatusBadRequest, "重放必须给出理由")
		return
	}
	if len(req.Reason) > replayReasonMax {
		writeErr(w, http.StatusBadRequest, "理由过长")
		return
	}
	if len(req.EventIDs) == 0 {
		writeErr(w, http.StatusBadRequest, "event_ids 不能为空")
		return
	}
	// 同一审批单据上同一事件出现两次没有意义，第二次既不重排也不产生第二条
	// 审计（如实现=每事件一条真实动作）；去重保序，其余形状错误一律拒。
	seen := map[string]bool{}
	ids := make([]string, 0, len(req.EventIDs))
	for _, raw := range req.EventIDs {
		id := strings.TrimSpace(raw)
		if !observationID.MatchString(id) {
			writeErr(w, http.StatusBadRequest, "event_id 需为有限标识: "+clip(id, 64))
			return
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		ids = append(ids, id)
	}
	if len(ids) > replayBatchMax {
		writeErr(w, http.StatusBadRequest, "单次批量超出上限，分批走审批")
		return
	}

	outcomes := store.RequeueDeadLetters(r.Context(), ids)
	type outcomeRow struct {
		EventID string `json:"event_id"`
		Outcome string `json:"outcome"`
		Detail  string `json:"detail,omitempty"`
	}
	results := make([]outcomeRow, 0, len(outcomes))
	var requeued, skipped, errors int
	for _, o := range outcomes {
		switch {
		case o.Err != nil:
			errors++
			results = append(results, outcomeRow{EventID: o.EventID, Outcome: "error", Detail: clip(o.Err.Error(), 256)})
			_ = g.audit(r.Context(), Entry{
				Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
				Target: clip(o.EventID, 128), OK: false, Detail: clip(o.Err.Error(), 256),
			})
		case o.Requeued:
			if err := g.audit(r.Context(), Entry{
				Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
				Target: clip(o.EventID, 128), OK: true, Detail: clip(req.Reason, 256),
			}); err != nil {
				// 单行口径：成功重排但审计写不进去，如实报失败不装成功
				errors++
				results = append(results, outcomeRow{EventID: o.EventID, Outcome: "error", Detail: "审计写入失败"})
				break
			}
			requeued++
			results = append(results, outcomeRow{EventID: o.EventID, Outcome: "requeued"})
		default:
			skipped++
			results = append(results, outcomeRow{EventID: o.EventID, Outcome: "skipped"})
			_ = g.audit(r.Context(), Entry{
				Actor: principal.Name, Role: principal.Role, Action: "ops.dead_letter_replay",
				Target: clip(o.EventID, 128), OK: false, Detail: clip("无可重排死信: "+req.Reason, 256),
			})
		}
	}

	status := http.StatusOK
	switch {
	case errors > 0:
		status = http.StatusServiceUnavailable
	case requeued == 0:
		status = http.StatusConflict
	}
	writeJSON(w, status, map[string]any{
		"ok":       errors == 0 && requeued > 0,
		"requeued": requeued,
		"skipped":  skipped,
		"errors":   errors,
		"results":  results,
		"note":     "已按条件逐条重排，实际投递由 outbox 调度器下一拍执行；重排不等于投递成功，再次失败仍会按退避退为死信；重复 event_id 只计首次",
	})
}
