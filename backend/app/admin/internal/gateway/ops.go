package gateway

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/go-redis/redis/v8"
)

func (g *Gate) ops(w http.ResponseWriter, r *http.Request) {
	if _, ok := g.principal(r); !ok {
		writeErr(w, http.StatusUnauthorized, "未登录")
		return
	}
	ctx, cancel := context.WithTimeout(context.WithoutCancel(r.Context()), 3*time.Second)
	defer cancel()
	client := g.httpClient()
	writeJSON(w, http.StatusOK, map[string]any{
		"services":  g.services(ctx, client),
		"sources":   g.sources(ctx, client),
		"models":    g.models(ctx, client),
		"resources": hostResources(ctx),
	})
}

func (g *Gate) httpClient() *http.Client {
	return &http.Client{Timeout: 800 * time.Millisecond}
}

func (g *Gate) services(ctx context.Context, client *http.Client) []map[string]string {
	nodes := []struct {
		name string
		key  string
	}{
		{"admin", ""},
		{"datahub", "datahub"},
		{"market", "market"},
		{"intel", "intel"},
		{"brain", "brain"},
		{"strategy", "strategy"},
		{"backtest", "backtest"},
		{"risk", "risk"},
		{"trade", "trade"},
		{"notify", "notify"},
	}
	out := make([]map[string]string, 0, len(nodes)+3)
	for _, node := range nodes {
		if node.key == "" {
			out = append(out, nodeStatus(node.name, "ok", "本进程"))
			continue
		}
		base := g.Settings.Upstreams[node.key]
		code, err := getStatus(ctx, client, base+"/healthz")
		if err != nil || code != http.StatusOK {
			detail := "无响应"
			if base == "" {
				detail = "未配置"
			}
			out = append(out, nodeStatus(node.name, "down", detail))
			continue
		}
		out = append(out, nodeStatus(node.name, "ok", base))
	}
	if g.PingDB == nil {
		out = append(out, nodeStatus("postgres", "down", "未配置"))
	} else if err := g.PingDB(ctx); err != nil {
		out = append(out, nodeStatus("postgres", "down", "连不上"))
	} else {
		out = append(out, nodeStatus("postgres", "ok", "novatrader"))
	}
	out = append(out, g.redisStatus(ctx))
	if g.NATSUp != nil && g.NATSUp() {
		out = append(out, nodeStatus("nats", "ok", "已连接"))
	} else {
		out = append(out, nodeStatus("nats", "down", "未连接"))
	}
	return out
}

func (g *Gate) redisStatus(ctx context.Context) map[string]string {
	if g.Settings.RedisAddr == "" {
		return nodeStatus("redis", "down", "未配置")
	}
	client := redis.NewClient(&redis.Options{Addr: g.Settings.RedisAddr, DialTimeout: 500 * time.Millisecond})
	defer client.Close()
	if err := client.Ping(ctx).Err(); err != nil {
		return nodeStatus("redis", "down", "连不上")
	}
	return nodeStatus("redis", "ok", g.Settings.RedisAddr)
}

func (g *Gate) sources(ctx context.Context, client *http.Client) []map[string]string {
	base := g.Settings.Upstreams["datahub"]
	body, code, err := getJSON(ctx, client, base+"/v1/datahub/sources")
	if err != nil || code != http.StatusOK {
		return []map[string]string{nodeStatus("datahub", "down", "源列表取不到")}
	}
	var parsed struct {
		Sources []map[string]any `json:"sources"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Sources) == 0 {
		return []map[string]string{nodeStatus("datahub", "ok", "没有数据源")}
	}
	out := make([]map[string]string, 0, len(parsed.Sources))
	for _, src := range parsed.Sources {
		name := firstString(src, "source", "domain")
		state := firstString(src, "state", "status")
		if state == "" {
			state = "unknown"
		}
		status := "ok"
		if state == "unavailable" || state == "disabled" || state == "open" {
			status = "down"
		}
		out = append(out, nodeStatus(name, status, state))
	}
	return out
}

func (g *Gate) models(ctx context.Context, client *http.Client) []map[string]string {
	base := g.Settings.Upstreams["brain"]
	body, code, err := getJSON(ctx, client, base+"/v1/brain/stats")
	if err != nil || code != http.StatusOK {
		return []map[string]string{nodeStatus("vLLM", "down", "脑服务无响应")}
	}
	var parsed struct {
		Tiers []map[string]any `json:"tiers"`
	}
	if json.Unmarshal(body, &parsed) != nil || len(parsed.Tiers) == 0 {
		return []map[string]string{nodeStatus("vLLM", "down", "还没有模型档")}
	}
	out := make([]map[string]string, 0, len(parsed.Tiers))
	for _, tier := range parsed.Tiers {
		name := firstString(tier, "model", "tier")
		if name == "" {
			name = "model"
		}
		status := "ok"
		detail := "已配置"
		if configured, ok := tier["configured"].(bool); ok && !configured {
			status = "down"
			detail = "未配置"
		}
		if open, ok := tier["circuitOpen"].(bool); ok && open {
			status = "down"
			detail = "熔断"
		}
		if open, ok := tier["circuit_open"].(bool); ok && open {
			status = "down"
			detail = "熔断"
		}
		latency := firstString(tier, "p95Ms", "p95_ms")
		row := nodeStatus(name, status, detail)
		if latency != "" {
			row["latency"] = latency
		}
		out = append(out, row)
	}
	return out
}

func nodeStatus(name, status, detail string) map[string]string {
	return map[string]string{"name": name, "status": status, "detail": detail}
}

func firstString(m map[string]any, keys ...string) string {
	for _, key := range keys {
		switch v := m[key].(type) {
		case string:
			if v != "" {
				return v
			}
		case float64:
			return jsonNumber(v)
		}
	}
	return ""
}

func jsonNumber(v float64) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func getStatus(ctx context.Context, client *http.Client, raw string) (int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer res.Body.Close()
	_, _ = io.Copy(io.Discard, res.Body)
	return res.StatusCode, nil
}

func getJSON(ctx context.Context, client *http.Client, raw string) ([]byte, int, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, raw, nil)
	if err != nil {
		return nil, 0, err
	}
	res, err := client.Do(req)
	if err != nil {
		return nil, 0, err
	}
	defer res.Body.Close()
	body, err := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	return body, res.StatusCode, err
}
