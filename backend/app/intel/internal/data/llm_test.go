package data

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/conf"
	"server/pkg/events"
	"server/pkg/llm"
)

func TestNewModelDisabled(t *testing.T) {
	if NewModel(nil, nil) != nil {
		t.Fatal("nil config should not build a model")
	}
	if NewModel(&conf.Intel{Llm: &conf.IntelLLM{Enabled: false, BaseUrl: "http://127.0.0.1"}}, nil) != nil {
		t.Fatal("disabled llm should not build a model")
	}
}

func TestGatewayModelChat(t *testing.T) {
	var got struct {
		Model    string `json:"model"`
		Messages []struct {
			Role string `json:"role"`
		} `json:"messages"`
		Format map[string]string `json:"response_format"`
	}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("auth = %q", r.Header.Get("Authorization"))
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"model":"qwen-small","choices":[{"message":{"content":"{\"sentiment\":0.2}"}}]}`))
	}))
	defer srv.Close()

	m := &gatewayModel{gw: llm.New(map[llm.Tier]llm.ModelConfig{
		llm.Small: {BaseURL: srv.URL + "/v1", Model: "qwen-small", APIKey: "k", Timeout: time.Second, MaxTokens: 300},
	}, nil)}
	ctx := events.WithTraceID(context.Background(), "trace-1")
	out, err := m.Complete(ctx, "small", "system", "user")
	if err != nil {
		t.Fatal(err)
	}
	if out == "" || got.Model != "qwen-small" || len(got.Messages) != 2 || got.Format["type"] != "json_object" {
		t.Fatalf("reply %q request %+v", out, got)
	}
}
