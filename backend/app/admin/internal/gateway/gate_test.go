package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func TestTokenRoundTrip(t *testing.T) {
	now := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	user := User{Name: "owner", DisplayName: "主人", Role: "owner"}
	token, err := Issue("secret", user, now)
	if err != nil {
		t.Fatal(err)
	}
	got, err := Parse("secret", token, now.Add(time.Hour))
	if err != nil {
		t.Fatal(err)
	}
	if got.Name != "owner" || got.Role != "owner" || got.Display != "主人" {
		t.Fatalf("principal %+v", got)
	}
	if _, err := Parse("secret", token, now.Add(tokenTTL)); err == nil {
		t.Fatal("expired token accepted")
	}
	if _, err := Parse("other", token, now); err == nil {
		t.Fatal("wrong secret accepted")
	}
	if AllowSocket("secret", token+"x", now) {
		t.Fatal("tampered token allowed")
	}
	if !AllowSocket("secret", token, now) {
		t.Fatal("valid token rejected")
	}
}

func TestLoginAndProxy(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	var seen string
	var user string
	up := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen = r.URL.Path
		user = r.Header.Get("X-User")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer up.Close()
	audit := &memAudit{}
	gate := testGate(string(hash), audit)
	gate.Settings.Upstreams["backtest"] = up.URL
	gate.Settings.Upstreams["trade"] = up.URL

	res := postJSON(t, gate, "/api/v1/login", `{"user_name":"ada","password":"nope"}`, "")
	if res.Code != http.StatusUnauthorized {
		t.Fatalf("bad password %d %s", res.Code, res.Body)
	}
	res = postJSON(t, gate, "/api/v1/login", `{"user_name":"ada","password":"pw"}`, "")
	if res.Code != http.StatusOK {
		t.Fatalf("login %d %s", res.Code, res.Body)
	}
	var body struct {
		Token string `json:"token"`
		User  struct {
			Role string `json:"role"`
		} `json:"user"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if body.User.Role != "owner" || body.Token == "" {
		t.Fatalf("body %s", res.Body)
	}

	viewer := login(t, gate, "bob", "pw")
	denied := postJSON(t, gate, "/api/trade/orders", `{}`, viewer)
	if denied.Code != http.StatusForbidden {
		t.Fatalf("viewer %d %s", denied.Code, denied.Body)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/backtest/strategies?limit=1", nil)
	req.Header.Set("Authorization", "Bearer "+body.Token)
	got := httptest.NewRecorder()
	gate.ServeHTTP(got, req)
	if got.Code != 200 || seen != "/v1/backtest/strategies" || user != "ada" {
		t.Fatalf("proxy status %d path %s user %s body %s", got.Code, seen, user, got.Body)
	}
	if got.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Fatal("missing CORS")
	}

	ordered := postJSON(t, gate, "/api/trade/orders", `{"symbol":"000001"}`, body.Token)
	if ordered.Code != 200 || seen != "/v1/trade/orders" {
		t.Fatalf("order %d path %s", ordered.Code, seen)
	}
	if !hasAction(audit, "login") || !hasAction(audit, "trade.order") {
		t.Fatalf("audit %+v", audit.rows)
	}
}

func TestOpsStatus(t *testing.T) {
	hash, err := bcrypt.GenerateFromPassword([]byte("pw"), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	datahub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"sources":[{"source":"tdx","state":"closed"},{"source":"ak","state":"unavailable"}]}`))
	}))
	defer datahub.Close()
	brain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/healthz" {
			_, _ = w.Write([]byte(`{"status":"ok"}`))
			return
		}
		_, _ = w.Write([]byte(`{"tiers":[{"model":"small","configured":false,"p95Ms":"12"}]}`))
	}))
	defer brain.Close()
	gate := testGate(string(hash), &memAudit{})
	gate.NATSUp = func() bool { return true }
	gate.PingDB = func(context.Context) error { return nil }
	for name := range gate.Settings.Upstreams {
		gate.Settings.Upstreams[name] = "http://127.0.0.1:1"
	}
	gate.Settings.Upstreams["datahub"] = datahub.URL
	gate.Settings.Upstreams["brain"] = brain.URL

	token := login(t, gate, "ada", "pw")
	req := httptest.NewRequest(http.MethodGet, "/api/ops/status", nil)
	req.Header.Set("Authorization", "Bearer "+token)
	res := httptest.NewRecorder()
	gate.ServeHTTP(res, req)
	if res.Code != 200 {
		t.Fatalf("ops %d %s", res.Code, res.Body)
	}
	var body struct {
		Services  []map[string]string `json:"services"`
		Sources   []map[string]string `json:"sources"`
		Models    []map[string]string `json:"models"`
		Resources []map[string]string `json:"resources"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Sources) != 2 || body.Sources[1]["status"] != "down" {
		t.Fatalf("sources %+v", body.Sources)
	}
	if len(body.Models) != 1 || body.Models[0]["status"] != "down" || body.Models[0]["latency"] != "12" {
		t.Fatalf("models %+v", body.Models)
	}
	if !hasNode(body.Services, "admin", "ok") || !hasNode(body.Services, "nats", "ok") || !hasNode(body.Services, "market", "down") {
		t.Fatalf("services %+v", body.Services)
	}
	if !hasName(body.Resources, "磁盘") || !hasName(body.Resources, "显存") {
		t.Fatalf("resources %+v", body.Resources)
	}
}

func hasName(rows []map[string]string, name string) bool {
	for _, row := range rows {
		if row["name"] == name || strings.HasPrefix(row["name"], name) {
			return true
		}
	}
	return false
}

func testGate(hash string, audit Auditor) *Gate {
	return &Gate{
		Settings: BuildSettings(FileConfig{Users: []User{
			{Name: "ada", DisplayName: "Ada", Role: "owner", PasswordHash: hash},
			{Name: "bob", DisplayName: "Bob", Role: "viewer", PasswordHash: hash},
		}}, "", "secret"),
		Audit: audit,
		Now:   func() time.Time { return time.Date(2026, 10, 9, 3, 0, 0, 0, time.UTC) },
	}
}

func postJSON(t *testing.T, gate *Gate, path, raw, token string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, path, bytes.NewBufferString(raw))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	res := httptest.NewRecorder()
	gate.ServeHTTP(res, req)
	return res
}

func login(t *testing.T, gate *Gate, name, password string) string {
	t.Helper()
	res := postJSON(t, gate, "/api/v1/login", `{"user_name":"`+name+`","password":"`+password+`"}`, "")
	if res.Code != 200 {
		t.Fatalf("login %s %d %s", name, res.Code, res.Body)
	}
	var body struct {
		Token string `json:"token"`
	}
	if err := json.Unmarshal(res.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return body.Token
}

func hasAction(a *memAudit, action string) bool {
	for _, row := range a.rows {
		if row.Action == action && row.OK {
			return true
		}
	}
	return false
}

func hasNode(rows []map[string]string, name, status string) bool {
	for _, row := range rows {
		if row["name"] == name && row["status"] == status {
			return true
		}
	}
	return false
}

type memAudit struct {
	rows []Entry
}

func (m *memAudit) Enabled() bool { return true }

func (m *memAudit) Write(_ context.Context, e Entry) error {
	m.rows = append(m.rows, e)
	return nil
}
