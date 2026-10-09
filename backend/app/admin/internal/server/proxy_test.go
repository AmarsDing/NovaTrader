package server

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/app/admin/internal/gateway"

	khttp "github.com/go-kratos/kratos/v2/transport/http"
	"golang.org/x/crypto/bcrypt"
)

func TestLoginThroughRouter(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/market/sentiment" {
			t.Errorf("path %s", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"phase":"hot"}`))
	}))
	defer up.Close()
	gate := &gateway.Gate{
		Settings: gateway.BuildSettings(gateway.FileConfig{Users: []gateway.User{{
			Name: "ada", DisplayName: "Ada", Role: "owner", PasswordHash: string(hash),
		}}}, up.URL, "secret"),
		Audit: &allowAudit{},
		Now:   func() time.Time { return time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC) },
	}
	gate.Settings.Upstreams["market"] = up.URL
	srv := khttp.NewServer(khttp.Address("127.0.0.1:2097"), khttp.Filter(withCORS))
	srv.HandlePrefix("/api/", gate)
	go func() { _ = srv.Start(context.Background()) }()
	defer srv.Stop(context.Background())

	var login *http.Response
	deadline := time.Now().Add(3 * time.Second)
	body := bytes.NewBufferString(`{"user_name":"ada","password":"pw"}`)
	for {
		req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:2097/api/v1/login", body)
		req.Header.Set("Content-Type", "application/json")
		login, err = http.DefaultClient.Do(req)
		if err == nil || time.Now().After(deadline) {
			break
		}
		body = bytes.NewBufferString(`{"user_name":"ada","password":"pw"}`)
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer login.Body.Close()
	raw, _ := io.ReadAll(login.Body)
	if login.StatusCode != 200 {
		t.Fatalf("login %d %s", login.StatusCode, raw)
	}
	var token struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(raw, &token); err != nil || token.Token == "" {
		t.Fatalf("token %s", raw)
	}
	req, _ := http.NewRequest(http.MethodGet, "http://127.0.0.1:2097/api/market/sentiment", nil)
	req.Header.Set("Authorization", "Bearer "+token.Token)
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer res.Body.Close()
	got, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || string(got) != `{"phase":"hot"}` {
		t.Fatalf("market %d %s", res.StatusCode, got)
	}
}

type allowAudit struct{}

func (allowAudit) Enabled() bool { return false }
func (allowAudit) Write(context.Context, gateway.Entry) error {
	return nil
}

func TestHealthzAllowsDesktopOrigin(t *testing.T) {
	srv := khttp.NewServer(khttp.Address("127.0.0.1:2098"), khttp.Filter(withCORS))
	srv.HandleFunc("/healthz", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})
	go func() { _ = srv.Start(context.Background()) }()
	defer srv.Stop(context.Background())

	var resp *http.Response
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for {
		resp, err = http.Get("http://127.0.0.1:2098/healthz")
		if err == nil || time.Now().After(deadline) {
			break
		}
		time.Sleep(30 * time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 || string(body) != `{"status":"ok"}` {
		t.Fatalf("status %d body %s", resp.StatusCode, body)
	}
	if resp.Header.Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("healthz missing CORS")
	}
}
