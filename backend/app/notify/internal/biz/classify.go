package biz

import (
	"encoding/json"
	"strconv"
	"strings"
	"unicode/utf8"

	"server/pkg/events"
)

// Classify 把一条事件变成通知。第二返回值为 false 时忽略，不落库。
func Classify(env events.Envelope) (Message, bool) {
	m := object(env.Payload)
	var msg Message
	var ok bool
	switch env.Subject {
	case events.SubjectSignal:
		msg, ok = fromSignal(m)
	case events.SubjectRiskAlert:
		msg, ok = fromRisk(m)
	case events.SubjectIntelAlert:
		msg, ok = fromIntel(m)
	case events.SubjectTradeFill:
		msg, ok = fromFill(m)
	case events.SubjectBriefing:
		msg, ok = fromBriefing(m)
	case events.SubjectStrategyReview:
		msg, ok = fromReview(m)
	case events.SubjectNotifyRequest:
		msg, ok = fromRequest(m)
	default:
		return Message{}, false
	}
	if !ok {
		return Message{}, false
	}
	msg.EventID = env.EventID
	msg.TraceID = clip(env.TraceID, 64)
	msg.Source = clip(env.Source, 64)
	msg.Subject = clip(env.Subject, 128)
	msg.Category = clip(msg.Category, 32)
	msg.Title = clip(msg.Title, 200)
	msg.Body = clip(msg.Body, 4000)
	if msg.DedupKey == "" {
		msg.DedupKey = msg.EventID
	}
	msg.DedupKey = clip(msg.DedupKey, 256)
	msg.Status = StatusPending
	return msg, true
}

func fromSignal(m map[string]any) (Message, bool) {
	if str(m, "status") != "pending" {
		return Message{}, false
	}
	id := str(m, "signal_id")
	code := str(m, "stock_code", "symbol")
	name := str(m, "stock_name")
	title := name
	if title == "" {
		title = code
	}
	if title == "" {
		title = "交易信号"
	}
	side := str(m, "signal_type", "side")
	body := str(m, "reasoning", "body")
	return Message{
		Category: "signal", Priority: PriCritical, Title: title, Body: body,
		DedupKey: "signal:" + id,
		Fields:   map[string]string{"symbol": code, "side": side, "strategy": str(m, "strategy")},
	}, true
}

func fromRisk(m map[string]any) (Message, bool) {
	kind := str(m, "kind")
	title := kind
	if title == "" {
		title = "风险告警"
	}
	acct := str(m, "account_type")
	return Message{
		Category: "risk", Priority: PriCritical, Title: title, Body: str(m, "message", "body"),
		DedupKey: "risk:" + kind + ":" + acct,
		Fields:   map[string]string{"kind": kind, "account": acct},
	}, true
}

func fromIntel(m map[string]any) (Message, bool) {
	level := strings.ToLower(str(m, "level"))
	pri := PriHigh
	if level == "critical" || level == "error" {
		pri = PriCritical
	}
	cluster := str(m, "cluster_id")
	key := "intel:" + cluster
	if cluster == "" || cluster == "0" {
		key = "intel:" + str(m, "title")
	}
	return Message{
		Category: "intel", Priority: pri, Title: str(m, "title"), Body: str(m, "reason", "body"),
		DedupKey: key,
		Fields:   map[string]string{"level": level, "symbol": firstStock(m)},
	}, true
}

func fromFill(m map[string]any) (Message, bool) {
	qty := num(m, "Qty", "qty")
	if qty == "" || qty == "0" {
		return Message{}, false
	}
	sym := str(m, "Symbol", "symbol")
	side := str(m, "Side", "side")
	price := num(m, "Price", "price")
	oid := str(m, "ClientOrderID", "client_order_id")
	title := "成交 " + sym
	body := strings.TrimSpace(side + " " + qty + "股 @ " + price)
	return Message{
		Category: "fill", Priority: PriHigh, Title: title, Body: body,
		DedupKey: "fill:" + oid + ":" + sym + ":" + side + ":" + qty + ":" + price,
		Fields:   map[string]string{"symbol": sym, "side": side, "qty": qty, "price": price},
	}, true
}

func fromBriefing(m map[string]any) (Message, bool) {
	date := str(m, "date")
	return Message{
		Category: "briefing", Priority: PriHigh, Title: strings.TrimSpace("晨报 " + date),
		Body: str(m, "headline", "body"), DedupKey: "briefing:" + date,
		Fields: map[string]string{"date": date},
	}, true
}

func fromReview(m map[string]any) (Message, bool) {
	date := str(m, "date")
	title := str(m, "title")
	if title == "" {
		title = strings.TrimSpace("复盘 " + date)
	}
	return Message{
		Category: "review", Priority: PriMedium, Title: title,
		Body: str(m, "body", "summary", "headline"), DedupKey: "review:" + date,
		Fields: map[string]string{"date": date},
	}, true
}

func fromRequest(m map[string]any) (Message, bool) {
	cat := str(m, "category")
	if cat == "" {
		cat = "system"
	}
	level := strings.ToLower(str(m, "level"))
	title := str(m, "title")
	key := str(m, "dedup_key")
	if key == "" {
		key = cat + ":" + title
	}
	return Message{
		Category: cat, Priority: priorityForRequest(cat, level),
		Title: title, Body: str(m, "body"), DedupKey: key,
		Fields: map[string]string{"level": level},
	}, title != "" || str(m, "body") != ""
}

func priorityForRequest(cat, level string) string {
	switch cat {
	case "signal", "risk":
		return PriCritical
	case "intel":
		if level == "critical" || level == "error" {
			return PriCritical
		}
		return PriHigh
	case "briefing", "fill":
		return PriHigh
	case "review":
		return PriMedium
	}
	switch level {
	case "info", "medium":
		return PriMedium
	default:
		return PriCritical
	}
}

func firstStock(m map[string]any) string {
	switch v := m["stocks"].(type) {
	case []any:
		if len(v) == 0 {
			return ""
		}
		s, _ := v[0].(string)
		return s
	default:
		return ""
	}
}

func object(raw json.RawMessage) map[string]any {
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}

func str(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		case float64:
			if v != 0 {
				return strconv.FormatInt(int64(v), 10)
			}
		}
	}
	return ""
}

func num(m map[string]any, keys ...string) string {
	for _, k := range keys {
		switch v := m[k].(type) {
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case string:
			if s := strings.TrimSpace(v); s != "" {
				return s
			}
		}
	}
	return ""
}

func clip(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n])
}
