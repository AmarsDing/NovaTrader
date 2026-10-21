// Package llm 是本地 vLLM 的统一网关（M05 FR-05-01）。
// 模型分 large、small 两档；每档一个优先级队列，排队满、超时、熔断都返回 ErrUnavailable，
// 调用方据此退回规则分。每次调用都交给 Recorder 留痕（FR-05-08）。
package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

type Tier string

const (
	Large Tier = "large"
	Small Tier = "small"
)

// Priority 数值越小越先执行。
type Priority int

const (
	PositionRisk Priority = iota
	Intraday
	Briefing
	NewsBatch
	Review
)

var (
	// ErrUnavailable 表示模型此刻不可用：未配置、排队满、超时、连接失败、熔断中。
	ErrUnavailable = errors.New("llm: unavailable")
	// ErrInvalid 表示模型有回复，但没通过调用方的校验。
	ErrInvalid = errors.New("llm: invalid output")
)

const (
	statusOK          = "ok"
	statusInvalid     = "invalid"
	statusUnavailable = "unavailable"
	statusError       = "error"

	excerptRunes     = 2000
	breakerThreshold = 5
	breakerCooldown  = 30 * time.Second
	recordTimeout    = 5 * time.Second
)

type ModelConfig struct {
	BaseURL        string // 例如 http://dgx:8000/v1
	Model          string
	APIKey         string
	Timeout        time.Duration
	MaxConcurrency int
	MaxQueue       int
	Temperature    float64
	MaxTokens      int
	// DisableJSONMode 为 true 时请求体不带 response_format，适配不支持 json_object
	// 的模型或第三方网关（M13-T06）。默认 false 保持请求 response_format=json_object。
	DisableJSONMode bool
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Task          string
	Tier          Tier
	Priority      Priority
	Messages      []Message
	TraceID       string
	PromptVersion string
	PromptHash    string
	Attempt       int
	DecisionID    *int
	// Validate 非空时检查回复正文。返回错误则本次记为 invalid，Chat 返回包裹 ErrInvalid 的错误和回复。
	Validate func(content string) error
}

type Response struct {
	Content          string
	Model            string
	PromptTokens     int
	CompletionTokens int
	Latency          time.Duration
	QueueWait        time.Duration
}

// CallRecord 对应 llm_call_log 的一行。
type CallRecord struct {
	TraceID          string
	Task             string
	Tier             Tier
	Model            string
	PromptVersion    string
	PromptHash       string
	InputDigest      string
	InputExcerpt     string
	Output           string
	Status           string
	Error            string
	LatencyMS        int
	QueueMS          int
	PromptTokens     int
	CompletionTokens int
	Attempt          int
	DecisionID       *int
}

type Recorder interface {
	Record(ctx context.Context, rec CallRecord) error
}

type tier struct {
	cfg   ModelConfig
	queue *limiter
	stats *tierStats

	mu        sync.Mutex
	failures  int
	openUntil time.Time
}

// Overlay 是配置页写进参数表后，某一档模型要盖过启动配置的部分。空字段保持原值。
type Overlay struct {
	BaseURL string
	Model   string
	APIKey  string
}

type Gateway struct {
	tiers map[Tier]*tier
	rec   Recorder
	http  *http.Client
	now   func() time.Time

	liveMu sync.RWMutex
	live   func(Tier) Overlay
}

// New 建立网关。BaseURL 为空的档位视为未配置，调用直接返回 ErrUnavailable。rec 可为 nil。
func New(models map[Tier]ModelConfig, rec Recorder) *Gateway {
	g := &Gateway{tiers: map[Tier]*tier{}, rec: rec, http: &http.Client{}, now: time.Now}
	for name, cfg := range models {
		if cfg.MaxConcurrency <= 0 {
			cfg.MaxConcurrency = 1
		}
		if cfg.MaxQueue <= 0 {
			cfg.MaxQueue = 64
		}
		if cfg.Timeout <= 0 {
			cfg.Timeout = 60 * time.Second
		}
		if cfg.MaxTokens <= 0 {
			cfg.MaxTokens = 1024
		}
		cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")
		g.tiers[name] = &tier{cfg: cfg, queue: newLimiter(cfg.MaxConcurrency, cfg.MaxQueue), stats: newTierStats()}
	}
	return g
}

// SetLive 在每次调用前读取配置页里的地址、模型名和密钥。
func (g *Gateway) SetLive(fn func(Tier) Overlay) {
	if g == nil {
		return
	}
	g.liveMu.Lock()
	g.live = fn
	g.liveMu.Unlock()
}

func (g *Gateway) overlay(t Tier, cfg ModelConfig) ModelConfig {
	g.liveMu.RLock()
	fn := g.live
	g.liveMu.RUnlock()
	if fn == nil {
		return cfg
	}
	over := fn(t)
	if over.BaseURL != "" {
		cfg.BaseURL = strings.TrimRight(strings.TrimSpace(over.BaseURL), "/")
	}
	if over.Model != "" {
		cfg.Model = strings.TrimSpace(over.Model)
	}
	if over.APIKey != "" {
		cfg.APIKey = over.APIKey
	}
	return cfg
}

// Configured 表示该档位填了地址。
func (g *Gateway) Configured(t Tier) bool {
	tr, ok := g.tiers[t]
	return ok && tr.cfg.BaseURL != ""
}

// Chat 排队后调用一次模型，不做重试。重试由调用方带上错误原因再发。
func (g *Gateway) Chat(ctx context.Context, req Request) (Response, error) {
	if req.Attempt <= 0 {
		req.Attempt = 1
	}
	rec := CallRecord{
		TraceID: req.TraceID, Task: req.Task, Tier: req.Tier,
		PromptVersion: req.PromptVersion, PromptHash: req.PromptHash,
		Attempt: req.Attempt, DecisionID: req.DecisionID,
	}
	rec.InputDigest, rec.InputExcerpt = digest(req.Messages)

	tr, ok := g.tiers[req.Tier]
	cfg := ModelConfig{}
	if ok {
		cfg = g.overlay(req.Tier, tr.cfg)
	}
	if !ok || cfg.BaseURL == "" {
		return Response{}, g.fail(ctx, &rec, statusUnavailable, fmt.Errorf("%w: tier %q not configured", ErrUnavailable, req.Tier))
	}
	rec.Model = cfg.Model
	if tr.breakerOpen(g.now()) {
		return Response{}, g.fail(ctx, &rec, statusUnavailable, fmt.Errorf("%w: circuit open", ErrUnavailable))
	}

	queued := g.now()
	if err := tr.queue.acquire(ctx, req.Priority); err != nil {
		rec.QueueMS = int(g.now().Sub(queued).Milliseconds())
		return Response{}, g.fail(ctx, &rec, statusUnavailable, fmt.Errorf("%w: %v", ErrUnavailable, err))
	}
	wait := g.now().Sub(queued)
	rec.QueueMS = int(wait.Milliseconds())
	started := g.now()
	resp, err := g.post(ctx, cfg, req.Messages)
	tr.queue.release()
	resp.QueueWait = wait
	resp.Latency = g.now().Sub(started)
	rec.LatencyMS = int(resp.Latency.Milliseconds())
	rec.Output = resp.Content
	rec.PromptTokens, rec.CompletionTokens = resp.PromptTokens, resp.CompletionTokens
	if resp.Model != "" {
		rec.Model = resp.Model
	}
	if err != nil {
		status := statusError
		if errors.Is(err, ErrUnavailable) {
			status = statusUnavailable
		}
		tr.stats.observe(resp.Latency, false)
		if ctx.Err() == nil {
			tr.fail(g.now())
		}
		return resp, g.fail(ctx, &rec, status, err)
	}
	tr.stats.observe(resp.Latency, true)
	tr.succeed()
	if req.Validate != nil {
		if verr := req.Validate(resp.Content); verr != nil {
			err := fmt.Errorf("%w: %v", ErrInvalid, verr)
			g.record(ctx, &rec, statusInvalid, err)
			return resp, err
		}
	}
	g.record(ctx, &rec, statusOK, nil)
	return resp, nil
}

func (g *Gateway) fail(ctx context.Context, rec *CallRecord, status string, err error) error {
	g.record(ctx, rec, status, err)
	return err
}

func (g *Gateway) record(ctx context.Context, rec *CallRecord, status string, err error) {
	rec.Status = status
	if err != nil {
		rec.Error = err.Error()
	}
	if g.rec == nil {
		return
	}
	rctx, cancel := context.WithTimeout(context.WithoutCancel(ctx), recordTimeout)
	defer cancel()
	_ = g.rec.Record(rctx, *rec)
}

func (t *tier) breakerOpen(now time.Time) bool {
	t.mu.Lock()
	defer t.mu.Unlock()
	return now.Before(t.openUntil)
}

func (t *tier) fail(now time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.failures++
	if t.failures >= breakerThreshold {
		t.openUntil = now.Add(breakerCooldown)
		t.failures = 0
	}
}

func (t *tier) succeed() {
	t.mu.Lock()
	t.failures = 0
	t.mu.Unlock()
}

type chatBody struct {
	Model          string            `json:"model"`
	Messages       []Message         `json:"messages"`
	Temperature    float64           `json:"temperature"`
	MaxTokens      int               `json:"max_tokens"`
	ResponseFormat map[string]string `json:"response_format,omitempty"`
}

type chatReply struct {
	Model   string `json:"model"`
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
	} `json:"choices"`
	Usage struct {
		PromptTokens     int `json:"prompt_tokens"`
		CompletionTokens int `json:"completion_tokens"`
	} `json:"usage"`
}

func (g *Gateway) post(ctx context.Context, cfg ModelConfig, msgs []Message) (Response, error) {
	apiKey, err := ResolveAPIKey(cfg.APIKey)
	if err != nil {
		return Response{}, err
	}
	payload := chatBody{
		Model: cfg.Model, Messages: msgs, Temperature: cfg.Temperature, MaxTokens: cfg.MaxTokens,
	}
	if !cfg.DisableJSONMode {
		payload.ResponseFormat = map[string]string{"type": "json_object"}
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Response{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, cfg.Timeout)
	defer cancel()
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.BaseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		return Response{}, err
	}
	hreq.Header.Set("Content-Type", "application/json")
	if apiKey != "" {
		hreq.Header.Set("Authorization", "Bearer "+apiKey)
	}
	hresp, err := g.http.Do(hreq)
	if err != nil {
		return Response{}, fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer hresp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(hresp.Body, 4<<20))
	if err != nil {
		return Response{}, fmt.Errorf("%w: read: %v", ErrUnavailable, err)
	}
	if hresp.StatusCode >= 500 || hresp.StatusCode == http.StatusTooManyRequests {
		return Response{}, fmt.Errorf("%w: http %d: %s", ErrUnavailable, hresp.StatusCode, clip(string(raw), 300))
	}
	if hresp.StatusCode != http.StatusOK {
		return Response{}, fmt.Errorf("llm: http %d: %s", hresp.StatusCode, clip(string(raw), 300))
	}
	var rep chatReply
	if err := json.Unmarshal(raw, &rep); err != nil {
		return Response{}, fmt.Errorf("llm: decode reply: %w", err)
	}
	if len(rep.Choices) == 0 {
		return Response{Model: rep.Model}, fmt.Errorf("llm: reply has no choices")
	}
	return Response{
		Content:          rep.Choices[0].Message.Content,
		Model:            rep.Model,
		PromptTokens:     rep.Usage.PromptTokens,
		CompletionTokens: rep.Usage.CompletionTokens,
	}, nil
}

// ResolveAPIKey 把配置里的 api_key 解析成真实密钥（M13-T06）。
//
//	""           → ""，调用方不加鉴权头（本地 vLLM 现状）。
//	"env:NAME"   → 读环境变量 NAME；未设置或为空属配置缺失，显式报错（数据纪律：缺失≠空密钥）。
//	"enc:..."    → 预留密文形态；本迭代未接解密器，明确报错，绝不静默当明文发送。
//	其他         → 原样返回（明文密钥，保持现状）。
//
// 解析失败统一按 ErrUnavailable 上报：在配置修复前该档模型不可用，调用方照常退回规则分。
// 错误文案只带环境变量名，绝不带密钥值（会进 llm_call_log）。
func ResolveAPIKey(raw string) (string, error) {
	switch {
	case raw == "":
		return "", nil
	case strings.HasPrefix(raw, "env:"):
		name := strings.TrimSpace(strings.TrimPrefix(raw, "env:"))
		if name == "" {
			return "", fmt.Errorf("%w: api_key env: 前缀后缺少环境变量名", ErrUnavailable)
		}
		v, ok := os.LookupEnv(name)
		if !ok || v == "" {
			return "", fmt.Errorf("%w: api_key 引用的环境变量 %s 未设置或为空", ErrUnavailable, name)
		}
		return v, nil
	case strings.HasPrefix(raw, "enc:"):
		return "", fmt.Errorf("%w: api_key 为 enc: 密文但解密器未配置（M13 预留）", ErrUnavailable)
	default:
		return raw, nil
	}
}

func digest(msgs []Message) (sum, excerpt string) {
	h := sha256.New()
	var b strings.Builder
	for _, m := range msgs {
		h.Write([]byte(m.Role))
		h.Write([]byte{0})
		h.Write([]byte(m.Content))
		h.Write([]byte{0})
		if m.Role != "system" {
			b.WriteString(m.Content)
			b.WriteByte('\n')
		}
	}
	return hex.EncodeToString(h.Sum(nil)), clip(b.String(), excerptRunes)
}

func clip(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// Hash 返回文本 sha256 的前 12 位，用来给提示词模板打指纹。
func Hash(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])[:12]
}
