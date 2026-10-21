package biz

// M13-T01 四上游端口 biz 降级路径 + T10s 信号载荷的测试。
// - SentimentSource 缺失/err：快照 Stage 留空、融合系数 1.0、报告标 sentiment_missing，不误认 WARM；
// - NewsSource 空集/缺失区分：空集=确无利空，缺失=没查成（利空出口抑制、报告标 negative_news_missing）；
// - 终态事件载荷必带 attribution/pattern/emotion_phase/missed 四键（intel kb 回填消费契约）。

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"server/pkg/rules"
)

// exitEnv 组一笔可卖持仓：last=11.45 在止损 10 与止盈 13 之间，只剩利空/情绪出口可触发。
func exitEnv(t *testing.T) *env {
	t.Helper()
	e := newEnv(t, nil, nil, firstBoard("600000.SH"))
	e.pos.list = []Holding{{Symbol: "600000.SH", Quantity: 1000, Available: 1000, AvgCost: 12}}
	e.signals.put(Signal{
		Book: "paper", Symbol: "600000.SH", Side: rules.SideBuy, Status: StatusDone, Strategy: rules.NameFirstBoard,
		StopLoss: 10, TakeProfit: 13, TradeDate: tradeDay(asOf.AddDate(0, 0, -1)),
	})
	return e
}

// 情绪阶段接到快照：HOT 系数 1.05 进规则分；取不到是显式缺失（sentiment_missing），
// 系数按 1.0——不是误认 WARM，断言用 HOT 与缺失的分差钉住。
func TestScanSentimentStage(t *testing.T) {
	t.Run("hot coef 1.05", func(t *testing.T) {
		e := newEnv(t, scoreAI(80), nil, firstBoard("600000.SH"))
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return rules.StageHot, true, nil }
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 1 || len(rep.Missing) != 0 {
			t.Fatalf("report %+v", rep)
		}
		sig, _ := e.signals.Get(ctx, rep.Signals[0])
		near(t, "final", sig.FinalScore, 0.4*sig.RuleScore*1.05+0.6*80, 1e-9)
	})
	t.Run("missing coef 1.0 not warm", func(t *testing.T) {
		e := newEnv(t, scoreAI(80), nil, firstBoard("600000.SH"))
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return "", false, nil }
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 1 || len(rep.Missing) != 1 || rep.Missing[0] != MissSentiment {
			t.Fatalf("report %+v", rep)
		}
		sig, _ := e.signals.Get(ctx, rep.Signals[0])
		near(t, "final", sig.FinalScore, 0.4*sig.RuleScore+0.6*80, 1e-9)
	})
	t.Run("fetch error same as missing", func(t *testing.T) {
		e := newEnv(t, scoreAI(80), nil, firstBoard("600000.SH"))
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return "", false, errors.New("market down") }
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 1 || len(rep.Missing) != 1 || rep.Missing[0] != MissSentiment {
			t.Fatalf("report %+v", rep)
		}
	})
	t.Run("port not wired", func(t *testing.T) {
		e := newEnv(t, scoreAI(80), nil, firstBoard("600000.SH"))
		e.uc.sentiment = nil
		rep := scan(t, e, ScanRequest{})
		if len(rep.Signals) != 1 || len(rep.Missing) != 1 || rep.Missing[0] != MissSentiment {
			t.Fatalf("report %+v", rep)
		}
	})
}

// 情绪退潮出口：FADE 触发；情绪缺失不等于 FADE，本轮不触发。
func TestScanExitsStageFade(t *testing.T) {
	t.Run("fade fires exit", func(t *testing.T) {
		e := exitEnv(t)
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return rules.StageFade, true, nil }
		ids, err := e.uc.ScanExits(ctx, asOf)
		if err != nil || len(ids) != 1 {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
		s, _ := e.signals.Get(ctx, ids[0])
		if s.ExitKind != rules.ExitSentiment {
			t.Fatalf("exit %+v", s)
		}
	})
	t.Run("missing is not fade", func(t *testing.T) {
		e := exitEnv(t)
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return "", false, nil }
		ids, err := e.uc.ScanExits(ctx, asOf)
		if err != nil || len(ids) != 0 {
			t.Fatalf("missing must suppress fade exit: ids=%v err=%v", ids, err)
		}
	})
}

// 利空出口：查成且有利空触发 bad_news；确无利空（空集）不触发；没查成（缺失）也抑制。
func TestScanExitsBadNewsGate(t *testing.T) {
	t.Run("bad news fires exit", func(t *testing.T) {
		e := exitEnv(t)
		e.news.fn = func(symbols []string, _ time.Time) (map[string]bool, map[string]bool, error) {
			return map[string]bool{"600000.SH": true}, map[string]bool{}, nil
		}
		ids, err := e.uc.ScanExits(ctx, asOf)
		if err != nil || len(ids) != 1 {
			t.Fatalf("ids=%v err=%v", ids, err)
		}
		s, _ := e.signals.Get(ctx, ids[0])
		if s.ExitKind != rules.ExitBadNews {
			t.Fatalf("exit %+v", s)
		}
	})
	t.Run("honest empty set means no bad news", func(t *testing.T) {
		e := exitEnv(t)
		e.news.fn = func(symbols []string, _ time.Time) (map[string]bool, map[string]bool, error) {
			return map[string]bool{}, map[string]bool{}, nil
		}
		if ids, err := e.uc.ScanExits(ctx, asOf); err != nil || len(ids) != 0 {
			t.Fatalf("empty set must not fire: ids=%v err=%v", ids, err)
		}
	})
	t.Run("missing suppresses bad news exit", func(t *testing.T) {
		e := exitEnv(t)
		e.news.fn = func(symbols []string, _ time.Time) (map[string]bool, map[string]bool, error) {
			return map[string]bool{}, map[string]bool{"600000.SH": true}, nil
		}
		if ids, err := e.uc.ScanExits(ctx, asOf); err != nil || len(ids) != 0 {
			t.Fatalf("missing must suppress: ids=%v err=%v", ids, err)
		}
	})
}

// attachNews 的缺失语义钉板：查成的按集合挂，没查成的保持 false 并进 Missing；
// 端口失败/未装配时全部代码按缺失处理，空集（确无利空）不进 Missing。
func TestAttachNewsMissingSemantics(t *testing.T) {
	snaps := []rules.Snapshot{{Symbol: "a"}, {Symbol: "b"}, {Symbol: "c"}}
	t.Run("set vs missing vs empty", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.news.fn = func(_ []string, _ time.Time) (map[string]bool, map[string]bool, error) {
			return map[string]bool{"a": true}, map[string]bool{"b": true}, nil
		}
		rep := &ScanReport{}
		cp := append([]rules.Snapshot(nil), snaps...)
		e.uc.attachNews(ctx, cp, []string{"a", "b", "c"}, asOf, rep)
		if !cp[0].NegativeNews || cp[1].NegativeNews || cp[2].NegativeNews {
			t.Fatalf("snaps %+v", cp)
		}
		if len(rep.Missing) != 1 || rep.Missing[0] != MissNegativeNews {
			t.Fatalf("missing %+v", rep.Missing)
		}
	})
	t.Run("port error all missing", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.news.fn = func(_ []string, _ time.Time) (map[string]bool, map[string]bool, error) {
			return nil, nil, errors.New("intel down")
		}
		rep := &ScanReport{}
		cp := append([]rules.Snapshot(nil), snaps...)
		e.uc.attachNews(ctx, cp, []string{"a", "b", "c"}, asOf, rep)
		for i := range cp {
			if cp[i].NegativeNews {
				t.Fatalf("error must not fabricate bad news: %+v", cp[i])
			}
		}
		if len(rep.Missing) != 1 || rep.Missing[0] != MissNegativeNews {
			t.Fatalf("missing %+v", rep.Missing)
		}
	})
	t.Run("port not wired all missing", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.uc.news = nil
		_, missing := e.uc.negativeNews(ctx, []string{"a", "b"}, asOf)
		if !missing["a"] || !missing["b"] {
			t.Fatalf("missing %+v", missing)
		}
	})
}

// 载荷断言工具：终态四键在（键存在性判断 map 在场），非终态四键缺省。
func payloadMap(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	m := map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func requireAttributionKeys(t *testing.T, m map[string]any) (attribution, pattern, phase string, missed bool) {
	t.Helper()
	a, ok := m["attribution"]
	if !ok {
		t.Fatalf("terminal payload must carry attribution: %v", m)
	}
	p, ok := m["pattern"]
	if !ok {
		t.Fatalf("terminal payload must carry pattern: %v", m)
	}
	ph, ok := m["emotion_phase"]
	if !ok {
		t.Fatalf("terminal payload must carry emotion_phase: %v", m)
	}
	mi, ok := m["missed"]
	if !ok {
		t.Fatalf("terminal payload must carry missed: %v", m)
	}
	return a.(string), p.(string), ph.(string), mi.(bool)
}

// M13-T10：终态事件载荷四键。归因取值严格对齐 intel kb 白名单（indicator_failed /
// news_misread / unfilled / emotion_mismatch / missed），盈利无失败归因记空串且键在场。
func TestSignalPayloadAttribution(t *testing.T) {
	ice := func(time.Time) (rules.Stage, bool, error) { return rules.StageIce, true, nil }
	warm := func(time.Time) (rules.Stage, bool, error) { return rules.StageWarm, true, nil }

	closeEvent := func(e *env, s Signal, exitPrice, pnl float64) map[string]any {
		t.Helper()
		s.Book, s.Symbol, s.Side, s.Status = "paper", "600000.SH", rules.SideBuy, StatusDone
		id := e.signals.put(s)
		at := asOf
		if _, err := e.uc.Transition(ctx, id, StatusClosed, "仓位归零", ActorTrade,
			&Close{ExitPrice: exitPrice, PnlPct: pnl, At: at}); err != nil {
			t.Fatal(err)
		}
		return payloadMap(t, e.signals.events[len(e.signals.events)-1].Payload)
	}

	t.Run("expired is unfilled", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = ice
		e.signals.put(Signal{Status: StatusPending, ValidUntil: asOf, Strategy: rules.NameFirstBoard})
		if n, err := e.uc.ExpireDue(ctx, asOf.Add(time.Second)); err != nil || n != 1 {
			t.Fatalf("n=%d err=%v", n, err)
		}
		m := payloadMap(t, e.signals.events[0].Payload)
		attr, pattern, phase, missed := requireAttributionKeys(t, m)
		if attr != AttrUnfilled || pattern != rules.NameFirstBoard || phase != string(rules.StageIce) || missed {
			t.Fatalf("attr=%q pattern=%q phase=%q missed=%v", attr, pattern, phase, missed)
		}
	})
	t.Run("loss in ice is emotion_mismatch", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = ice
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 11, -4)
		attr, pattern, phase, missed := requireAttributionKeys(t, m)
		if attr != AttrEmotionMismatch || pattern != rules.NameFirstBoard || phase != string(rules.StageIce) || missed {
			t.Fatalf("attr=%q pattern=%q phase=%q missed=%v", attr, pattern, phase, missed)
		}
	})
	t.Run("loss on bad news is news_misread", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = warm
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, ExitKind: rules.ExitBadNews,
			TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 11, -4)
		attr, _, phase, _ := requireAttributionKeys(t, m)
		if attr != AttrNewsMisread || phase != string(rules.StageWarm) {
			t.Fatalf("attr=%q phase=%q", attr, phase)
		}
	})
	t.Run("plain loss is indicator_failed", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = warm
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 11, -4)
		attr, _, _, missed := requireAttributionKeys(t, m)
		if attr != AttrIndicatorFailed || missed {
			t.Fatalf("attr=%q missed=%v", attr, missed)
		}
	})
	t.Run("news bail dim routes to news_misread", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = warm
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, Dims: map[string]float64{"news": 70},
			TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 11, -4)
		attr, _, _, _ := requireAttributionKeys(t, m)
		if attr != AttrNewsMisread {
			t.Fatalf("attr=%q", attr)
		}
	})
	t.Run("win carries empty attribution key", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = warm
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 12, 4.8)
		attr, pattern, _, missed := requireAttributionKeys(t, m)
		if attr != "" || pattern != rules.NameFirstBoard || missed {
			t.Fatalf("attr=%q pattern=%q missed=%v", attr, pattern, missed)
		}
	})
	t.Run("missing phase is empty key, not fabricated", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = func(time.Time) (rules.Stage, bool, error) { return "", false, nil }
		m := closeEvent(e, Signal{Strategy: rules.NameFirstBoard, TradeDate: tradeDay(asOf.AddDate(0, 0, -1))}, 11, -4)
		attr, _, phase, _ := requireAttributionKeys(t, m)
		// 情绪没取到 → emotion_mismatch 分支不命中，落指标失败；phase 键在但为空。
		if attr != AttrIndicatorFailed || phase != "" {
			t.Fatalf("attr=%q phase=%q", attr, phase)
		}
	})
	t.Run("non-terminal omits attribution keys", func(t *testing.T) {
		e := newEnv(t, nil, nil)
		e.sentiment.fn = ice
		id := e.signals.put(Signal{Book: "paper", Symbol: "600000.SH", Side: rules.SideBuy, Status: StatusPending})
		if _, err := e.uc.Transition(ctx, id, StatusRiskChecking, "", ActorRisk, nil); err != nil {
			t.Fatal(err)
		}
		m := payloadMap(t, e.signals.events[0].Payload)
		for _, k := range []string{"attribution", "pattern", "emotion_phase", "missed"} {
			if _, ok := m[k]; ok {
				t.Fatalf("non-terminal must omit %s: %v", k, m)
			}
		}
		if m["status"] != string(StatusRiskChecking) {
			t.Fatalf("status %v", m)
		}
	})
}

// 归因枚举与 intel kb 白名单一字不差，契约字面钉板。
func TestAttributionConstantsMatchIntelWhitelist(t *testing.T) {
	want := []string{"indicator_failed", "news_misread", "unfilled", "emotion_mismatch", "missed"}
	got := []string{AttrIndicatorFailed, AttrNewsMisread, AttrUnfilled, AttrEmotionMismatch, AttrMissed}
	for i, w := range want {
		if got[i] != w {
			t.Fatalf("attribution constants drift from intel whitelist: %v", got)
		}
	}
}

func TestSignalPayloadAttributionNilSentimentPort(t *testing.T) {
	e := newEnv(t, nil, nil)
	e.uc.sentiment = nil
	id := e.signals.put(Signal{Book: "paper", Symbol: "600000.SH", Side: rules.SideBuy, Status: StatusDone,
		Strategy: rules.NameFirstBoard, TradeDate: tradeDay(asOf.AddDate(0, 0, -1))})
	if _, err := e.uc.Transition(ctx, id, StatusClosed, "仓位归零", ActorTrade,
		&Close{ExitPrice: 11, PnlPct: -4, At: asOf}); err != nil {
		t.Fatal(err)
	}
	m := payloadMap(t, e.signals.events[0].Payload)
	attr, _, phase, _ := requireAttributionKeys(t, m)
	if attr != AttrIndicatorFailed || phase != "" {
		t.Fatalf("nil port: attr=%q phase=%q", attr, phase)
	}
}
