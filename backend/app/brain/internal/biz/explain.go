package biz

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	"server/pkg/llm"
)

// ErrNotFound 表示请求的晨报或研判记录不存在。
var ErrNotFound = errors.New("not found")

const (
	maxEventRunes    = 300
	maxQuestionRunes = 500
)

// Explain 解读持仓或候选的异动（FR-05-07）。持仓走最高优先级。
func (uc *Usecase) Explain(ctx context.Context, in ExplainInput) (*Explanation, error) {
	sym, err := normalizeSymbol(in.Symbol)
	if err != nil {
		return nil, err
	}
	event := strings.TrimSpace(in.Event)
	if event == "" || utf8.RuneCountInString(event) > maxEventRunes {
		return nil, badRequest("event 不能为空，且不超过 %d 字", maxEventRunes)
	}
	facts, err := checkCallerFacts(in.Facts, nil)
	if err != nil {
		return nil, badRequest("%v", err)
	}
	trace := newTrace(in.TraceID)
	ctx, cancel := context.WithTimeout(ctx, uc.cfg.AnalyzeTimeout)
	defer cancel()
	id, err := uc.decisions.Begin(ctx, "explain", sym, trace)
	if err != nil {
		return nil, fmt.Errorf("brain: begin decision: %w", err)
	}
	start := uc.now()
	res := &Explanation{DecisionID: id}
	pack, perr := uc.loadPack(ctx, sym, uc.now(), AnalyzeInput{Facts: facts})
	prio := llm.Intraday
	if in.Holding {
		prio = llm.PositionRisk
	}
	var model string
	switch {
	case perr != nil:
		res.Degraded, res.Reason = true, "读取事实包失败："+perr.Error()
	case len(pack.Facts) == 0:
		res.Degraded, res.Reason = true, "事实包里没有因子，未调用模型"
	default:
		user := renderPack(&pack) + "[异动]\n<data id=\"event\">\n" + escapeData(event) + "\n</data>\n"
		ids := pack.ids()
		nums := newNumberSet(pack.Facts, explainPrompt.System, user)
		out, resp, err := callJSON(ctx, uc, callSpec{
			task: "explain", prompt: explainPrompt, prio: prio, trace: trace, decision: id, user: user,
		}, func(o *explainOut) error { return o.check(ids, nums) })
		model = resp.Model
		switch {
		case err == nil:
			res.Summary, res.Impact, res.Risks, res.Evidence = out.Summary, out.Impact, out.Risks, out.Evidence
		case errors.Is(err, llm.ErrInvalid):
			res.Discarded, res.Reason = true, "模型输出两次不合格："+invalidReason(err)
		default:
			res.Degraded, res.Reason = true, "模型不可用："+err.Error()
		}
	}
	pctx, pcancel := persistCtx(ctx)
	defer pcancel()
	if err := uc.decisions.Finish(pctx, id, DecisionRecord{
		Kind: "explain", Symbol: sym, TraceID: trace,
		Content:       toMap(map[string]any{"pack": pack, "event": event, "result": res}),
		PromptVersion: explainPrompt.Tag(), Model: model,
		Degraded: res.Degraded, Discarded: res.Discarded, LatencyMS: int(uc.now().Sub(start).Milliseconds()),
	}); err != nil {
		return nil, fmt.Errorf("brain: finish decision: %w", err)
	}
	return res, nil
}

// Ask 针对某次研判追问（FR-05-11）。回答里的数字要能在原事实包或此前结论里找到。
func (uc *Usecase) Ask(ctx context.Context, decisionID int, question string) (*AskReply, error) {
	question = strings.TrimSpace(question)
	if question == "" || utf8.RuneCountInString(question) > maxQuestionRunes {
		return nil, badRequest("question 不能为空，且不超过 %d 字", maxQuestionRunes)
	}
	if decisionID <= 0 {
		return nil, badRequest("decision_id 无效")
	}
	parent, err := uc.decisions.Get(ctx, decisionID)
	if err != nil {
		return nil, err
	}
	var pack FactPack
	if err := fromMap(parent.Content["pack"], &pack); err != nil || (len(pack.Facts) == 0 && len(pack.Intel) == 0) {
		return nil, badRequest("记录 %d 没有事实包，不能追问", decisionID)
	}
	prior, _ := json.Marshal(parent.Content["result"])

	ctx, cancel := context.WithTimeout(ctx, uc.cfg.AnalyzeTimeout)
	defer cancel()
	id, err := uc.decisions.Begin(ctx, "ask", parent.Symbol, parent.TraceID)
	if err != nil {
		return nil, fmt.Errorf("brain: begin decision: %w", err)
	}
	start := uc.now()
	user := renderPack(&pack) + "[此前结论]\n" + escapeData(string(prior)) + "\n[问题]\n" + escapeData(question) + "\n"
	ids := pack.ids()
	nums := newNumberSet(pack.Facts, askPrompt.System, user)
	out, resp, err := callJSON(ctx, uc, callSpec{
		task: "ask", prompt: askPrompt, prio: llm.Intraday, trace: parent.TraceID, decision: id, user: user,
	}, func(o *askOut) error { return o.check(ids, nums) })
	res := &AskReply{DecisionID: id}
	switch {
	case err == nil:
		res.Answer, res.Evidence = out.Answer, out.Evidence
	case errors.Is(err, llm.ErrInvalid):
		res.Discarded, res.Reason = true, "模型输出两次不合格："+invalidReason(err)
	default:
		res.Degraded, res.Reason = true, "模型不可用："+err.Error()
	}
	pctx, pcancel := persistCtx(ctx)
	defer pcancel()
	if err := uc.decisions.Finish(pctx, id, DecisionRecord{
		Kind: "ask", Symbol: parent.Symbol, TraceID: parent.TraceID,
		Content:       toMap(map[string]any{"parent_id": decisionID, "question": question, "result": res}),
		PromptVersion: askPrompt.Tag(), Model: resp.Model,
		Degraded: res.Degraded, Discarded: res.Discarded, LatencyMS: int(uc.now().Sub(start).Milliseconds()),
	}); err != nil {
		return nil, fmt.Errorf("brain: finish decision: %w", err)
	}
	return res, nil
}

// toMap 把结构体转成 agent_decisions.content 需要的 map。
func toMap(v any) map[string]any {
	b, err := json.Marshal(v)
	if err != nil {
		return map[string]any{"marshal_error": err.Error()}
	}
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

func fromMap(v any, out any) error {
	if v == nil {
		return errors.New("empty")
	}
	b, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return json.Unmarshal(b, out)
}
