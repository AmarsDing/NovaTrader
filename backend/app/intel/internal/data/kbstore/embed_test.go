package kbstore

import (
	"context"
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"server/conf"
)

func TestNewEmbedderConfig(t *testing.T) {
	if e, err := NewEmbedder(&conf.Kb{}); err != nil || e != nil {
		t.Fatalf("empty provider = %v, %v", e, err)
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
