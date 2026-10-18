package alert

import (
	"encoding/json"
	"fmt"
)

// CorruptError 表示存储里的规则清单读不出来。G3 的关键纪律：坏存储必须显式报错，
// 绝不能静默当成空清单——否则面板会骗用户"你没有预警"。
type CorruptError struct {
	Reason string
}

func (e *CorruptError) Error() string {
	return "alert: stored rules unreadable: " + e.Reason
}

// Encode 序列化规则清单（持久化形态为 JSON 数组；DB 行存储时一行一规则同样可复用）。
func Encode(rules []Rule) ([]byte, error) {
	return json.Marshal(rules)
}

// Decode 反序列化规则清单。空内容（空白）视为合法的空清单；
// 非数组、坏 JSON、缺字段的元素都返回 *CorruptError，一条规则都不静默丢弃。
func Decode(blob []byte) ([]Rule, error) {
	if len(trimSpace(blob)) == 0 {
		return nil, nil
	}
	var raw []json.RawMessage
	if err := json.Unmarshal(blob, &raw); err != nil {
		return nil, &CorruptError{Reason: err.Error()}
	}
	rules := make([]Rule, 0, len(raw))
	for i, item := range raw {
		var r Rule
		if err := json.Unmarshal(item, &r); err != nil {
			return nil, &CorruptError{Reason: fmt.Sprintf("rule #%d: %v", i, err)}
		}
		if err := validate(r); err != nil {
			return nil, &CorruptError{Reason: fmt.Sprintf("rule #%d: %v", i, err)}
		}
		rules = append(rules, r)
	}
	return rules, nil
}

func validate(r Rule) error {
	if r.ID == "" || r.Symbol == "" {
		return fmt.Errorf("id and symbol are required")
	}
	switch r.Condition {
	case Above, Below, Crosses:
	default:
		return fmt.Errorf("unknown condition %q", r.Condition)
	}
	if r.TargetPrice <= 0 {
		return fmt.Errorf("target price must be positive")
	}
	switch r.Status {
	case StatusActive, StatusTriggered:
	default:
		return fmt.Errorf("unknown status %q", r.Status)
	}
	return nil
}

func trimSpace(b []byte) []byte {
	start := 0
	for start < len(b) && isSpace(b[start]) {
		start++
	}
	end := len(b)
	for end > start && isSpace(b[end-1]) {
		end--
	}
	return b[start:end]
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r'
}
