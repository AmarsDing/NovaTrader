package llm

import "strings"

// ExtractJSON 去掉模型偶尔加的 ```json 围栏和前后说明，返回第一个 { 到最后一个 } 之间的内容。
func ExtractJSON(s string) string {
	s = strings.TrimSpace(s)
	start := strings.IndexByte(s, '{')
	end := strings.LastIndexByte(s, '}')
	if start < 0 || end < start {
		return s
	}
	return s[start : end+1]
}
