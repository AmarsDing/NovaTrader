package gateway

import "strings"

// FileConfig 是 config.yaml 里 admin 下、尚未写进 conf.proto 的网关配置。
type FileConfig struct {
	TokenSecret string            `json:"token_secret"`
	Users       []User            `json:"users"`
	Upstreams   map[string]string `json:"upstreams"`
}

// Settings 是网关运行时要用的账号、密钥和上游地址。
type Settings struct {
	Secret    string
	Users     []User
	Upstreams map[string]string
	RedisAddr string
}

// DefaultUpstreams 是各服务在本机的 HTTP 地址。
func DefaultUpstreams() map[string]string {
	return map[string]string{
		"datahub":  "http://127.0.0.1:2011",
		"market":   "http://127.0.0.1:2021",
		"intel":    "http://127.0.0.1:2031",
		"brain":    "http://127.0.0.1:2051",
		"strategy": "http://127.0.0.1:2061",
		"backtest": "http://127.0.0.1:2071",
		"risk":     "http://127.0.0.1:2081",
		"trade":    "http://127.0.0.1:2091",
		"notify":   "http://127.0.0.1:2101",
	}
}

// BuildSettings 合并默认上游、回测地址和配置文件里的账号。
func BuildSettings(file FileConfig, backtestUpstream, secret string) Settings {
	up := DefaultUpstreams()
	if backtestUpstream != "" {
		up["backtest"] = strings.TrimRight(backtestUpstream, "/")
	}
	for name, raw := range file.Upstreams {
		if raw = strings.TrimRight(strings.TrimSpace(raw), "/"); raw != "" {
			up[name] = raw
		}
	}
	if file.TokenSecret != "" {
		secret = file.TokenSecret
	}
	return Settings{
		Secret:    secret,
		Users:     normalizeUsers(file.Users),
		Upstreams: up,
	}
}

func normalizeUsers(in []User) []User {
	seen := map[string]struct{}{}
	var out []User
	for _, user := range in {
		user.Name = strings.TrimSpace(user.Name)
		user.Role = strings.ToLower(strings.TrimSpace(user.Role))
		user.DisplayName = strings.TrimSpace(user.DisplayName)
		user.PasswordHash = strings.TrimSpace(user.PasswordHash)
		if user.Name == "" || user.PasswordHash == "" {
			continue
		}
		if user.Role != "owner" && user.Role != "viewer" {
			continue
		}
		if _, ok := seen[user.Name]; ok {
			continue
		}
		seen[user.Name] = struct{}{}
		out = append(out, user)
	}
	return out
}
