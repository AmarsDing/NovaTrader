package kbstore

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"server/conf"
	"server/pkg/llm"
)

func TestNewEmbedderConfig(t *testing.T) {
	e, err := NewEmbedder(&conf.Kb{})
	if err != nil || e == nil {
		t.Fatalf("empty provider = %v, %v", e, err)
	}
	if _, err := e.EmbedQuery(context.Background(), "x"); err == nil {
		t.Fatal("empty provider should stay off")
	}
	if _, err := NewEmbedder(&conf.Kb{EmbedProvider: "hash", EmbedDims: 768}); err == nil {
		t.Fatal("dims mismatch must fail")
	}
	if _, err := NewEmbedder(&conf.Kb{EmbedProvider: "openai"}); err == nil {
		t.Fatal("openai without endpoint must fail")
	}
	if _, err := NewEmbedder(&conf.Kb{EmbedProvider: "chroma"}); err == nil {
		t.Fatal("unknown provider must fail")
	}
}

func TestOpenAIEmbedder(t *testing.T) {
	var inputs []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/embeddings" || r.Header.Get("Authorization") != "Bearer k" {
			http.Error(w, "bad request", http.StatusBadRequest)
			return
		}
		var req embedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		inputs = append(inputs, req.Input...)
		type item struct {
			Index     int       `json:"index"`
			Embedding []float32 `json:"embedding"`
		}
		var data []item
		for i := len(req.Input) - 1; i >= 0; i-- {
			v := make([]float32, Dims)
			v[i] = 3
			v[i+1] = 4
			data = append(data, item{Index: i, Embedding: v})
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
	}))
	defer srv.Close()

	e, err := NewEmbedder(&conf.Kb{EmbedProvider: "openai", EmbedEndpoint: srv.URL + "/", EmbedModel: "bge", EmbedApiKey: "k"})
	if err != nil {
		t.Fatal(err)
	}
	vecs, err := e.EmbedDocuments(context.Background(), []string{"甲", "乙"})
	if err != nil {
		t.Fatal(err)
	}
	if len(vecs) != 2 || math.Abs(float64(vecs[0][0])-0.6) > 1e-6 || math.Abs(float64(vecs[1][2])-0.8) > 1e-6 {
		t.Fatalf("vectors not ordered or normalized: %v %v", vecs[0][:3], vecs[1][:3])
	}
	if _, err := e.EmbedQuery(context.Background(), "弱转强"); err != nil {
		t.Fatal(err)
	}
	if last := inputs[len(inputs)-1]; !strings.HasPrefix(last, bgeQueryInstruction) {
		t.Fatalf("query missing instruction: %q", last)
	}
	if inputs[0] != "甲" {
		t.Fatalf("documents must not carry instruction: %q", inputs[0])
	}
}

func TestOpenAIEmbedderRejectsWrongDims(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"data":[{"index":0,"embedding":[1,2,3]}]}`))
	}))
	defer srv.Close()
	e, _ := NewEmbedder(&conf.Kb{EmbedProvider: "openai", EmbedEndpoint: srv.URL + "/v1", EmbedModel: "m"})
	if _, err := e.EmbedDocuments(context.Background(), []string{"x"}); err == nil {
		t.Fatal("wrong dims must fail")
	}
}

func TestHashEmbedderDeterministic(t *testing.T) {
	h := hashEmbedder{}
	a, _ := h.EmbedQuery(context.Background(), "弱转强")
	b, _ := h.EmbedQuery(context.Background(), "弱转强")
	if len(a) != Dims {
		t.Fatalf("dims = %d", len(a))
	}
	for i := range a {
		if a[i] != b[i] {
			t.Fatal("hash embedding must be deterministic")
		}
	}
}

// TestEmbedderResolvesAPIKey 钉测试（M13-T06b）：embed 客户端复用 pkg/llm.ResolveAPIKey——
// env: 前缀运行期取真值鉴权；env 缺变量显式报错不发请求；enc: 未配置密钥文件报错且不回显密文原文。
func TestEmbedderResolvesAPIKey(t *testing.T) {
	t.Setenv("NT_KB_TEST_KEY", "sk-env-real")
	var hits atomic.Int32
	var auth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		auth = r.Header.Get("Authorization")
		v := make([]float32, Dims)
		v[0] = 1
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": v}}})
	}))
	defer srv.Close()

	embed := func(t *testing.T, key string) error {
		t.Helper()
		e, err := NewEmbedder(&conf.Kb{
			EmbedProvider: "openai", EmbedEndpoint: srv.URL + "/v1", EmbedModel: "m", EmbedApiKey: key,
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = e.EmbedDocuments(context.Background(), []string{"x"})
		return err
	}

	// env: 前缀：真实密钥从运行期环境变量取，鉴权头带真值。
	if err := embed(t, "env:NT_KB_TEST_KEY"); err != nil {
		t.Fatal(err)
	}
	if auth != "Bearer sk-env-real" {
		t.Fatalf("auth = %q", auth)
	}
	// env: 缺变量：显式报错（错误只带变量名），且不发出任何 HTTP 请求。
	if err := embed(t, "env:NT_KB_MISSING"); !errors.Is(err, llm.ErrUnavailable) || !strings.Contains(err.Error(), "NT_KB_MISSING") {
		t.Fatalf("want unavailable+变量名, got %v", err)
	}
	// enc: 密文未接解密器：显式报错，错误绝不回显密文原文（会进错误留痕）。
	if err := embed(t, "enc:Y2lwaGVydGV4dA=="); !errors.Is(err, llm.ErrUnavailable) || !strings.Contains(err.Error(), "未配置密钥文件") {
		t.Fatalf("want 解密器未配置, got %v", err)
	} else if strings.Contains(err.Error(), "Y2lwaGVydGV4dA==") {
		t.Fatalf("error must not echo the ciphertext: %v", err)
	}
	if n := hits.Load(); n != 1 {
		t.Fatalf("解析失败的密钥不得发送到服务端, hits = %d", n)
	}
}
