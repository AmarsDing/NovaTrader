// Package redact 在写日志前遮住令牌、密码和密钥。
package redact

import "strings"

var keys = map[string]struct{}{
	"password": {}, "passwd": {}, "token": {}, "secret": {},
	"api_key": {}, "apikey": {}, "authorization": {}, "dsn": {},
}

// Map 返回一份副本，敏感字段的值替换为 ***。
func Map(in map[string]any) map[string]any {
	out := make(map[string]any, len(in))
	for k, v := range in {
		if _, hide := keys[strings.ToLower(k)]; hide {
			out[k] = "***"
			continue
		}
		if child, ok := v.(map[string]any); ok {
			out[k] = Map(child)
			continue
		}
		out[k] = v
	}
	return out
}

// Line 遮住常见的 key=value 片段，避免整行日志带出密钥。
func Line(s string) string {
	for _, key := range []string{"password", "token", "secret", "api_key", "authorization"} {
		s = mask(s, key+"=")
		s = mask(s, key+":")
	}
	return s
}

func mask(s, prefix string) string {
	var b strings.Builder
	for {
		lower := strings.ToLower(s)
		i := strings.Index(lower, prefix)
		if i < 0 {
			b.WriteString(s)
			return b.String()
		}
		b.WriteString(s[:i+len(prefix)])
		b.WriteString("***")
		end := i + len(prefix)
		for end < len(s) && s[end] != ' ' && s[end] != ',' && s[end] != '&' && s[end] != '"' {
			end++
		}
		s = s[end:]
	}
}
