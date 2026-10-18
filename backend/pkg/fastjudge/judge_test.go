package fastjudge

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

type roundTrip func(*http.Request) (*http.Response, error)

func (f roundTrip) RoundTrip(r *http.Request) (*http.Response, error) { return f(r) }
func requestFixture() Request {
	now := time.Now().UTC()
	state := PublicText{Title: "合成测试公告", Content: "虚构公司否认重组传闻，仅用于协议测试。", Target: "000001.SZ"}
	return Request{RequestID: "fixture:1", InputSnapshotID: "news:1", InputHash: Hash(state), CutoffAt: now.Add(-time.Second), PublishedAt: now.Add(-time.Hour), ValidUntil: now.Add(time.Minute), DataPolicy: "public_news_only", QuestionVersion: QuestionVersion, State: state, Questions: PublicNewsQuestions()}
}
func validReply() string {
	return `{"model":"jev-1.13.0","answers":{"relevant":{"type":"noul","noul":0.9},"event_status":{"type":"choice","choice":"denial","probabilities":{"fact":0,"proposal":0,"rumor":0.05,"denial":0.95,"unknown":0},"confidence":0.9},"severity":{"type":"score","score":0.5,"legend":{"0":"No material evidence","1":"Routine update","2":"Material business event","3":"Explicit regulatory or solvency risk"},"probabilities":{"0":0.5,"1":0.5,"2":0,"3":0},"confidence":0.8}},"usage":{"input_tokens":120,"output_tokens":20}}`
}
func clientFixture(body string) *Client {
	return New(Config{Enabled: true, APIKey: "test-secret", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(body))}, nil
	})}})
}
func TestOfficialWireContractAndAuditProjection(t *testing.T) {
	var received map[string]json.RawMessage
	c := New(Config{Enabled: true, APIKey: "test-secret", Client: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
		if r.URL.String() != Endpoint || r.Header.Get("Authorization") != "Bearer test-secret" {
			t.Error("wrong official endpoint/auth")
		}
		if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
			t.Fatal(err)
		}
		return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(validReply()))}, nil
	})}})
	out := c.Judge(context.Background(), requestFixture())
	if out.AbstainReason != "" || len(out.Answers) != 3 || out.ActualModel != "jev-1.13.0" || out.Usage.InputTokens != 120 {
		t.Fatalf("unexpected result: %+v", out)
	}
	if len(received) != 3 || received["questions"] == nil || received["state"] == nil || received["model"] == nil {
		t.Fatalf("wrong request: %v", received)
	}
	audit, _ := json.Marshal(Audit{Result: out})
	if strings.Contains(string(audit), "合成测试公告") || strings.Contains(string(audit), "test-secret") {
		t.Fatal("audit leaked input or key")
	}
}
func TestInvalidOrIncompleteAnswersAbstain(t *testing.T) {
	cases := map[string]string{"missing": strings.Replace(validReply(), `"relevant":{"type":"noul","noul":0.9},`, "", 1), "unknown_type": strings.Replace(validReply(), `"type":"noul"`, `"type":"trade"`, 1), "probability": strings.Replace(validReply(), `"noul":0.9`, `"noul":1.9`, 1), "choice": strings.Replace(validReply(), `"choice":"denial"`, `"choice":"buy"`, 1), "score": strings.Replace(validReply(), `"score":0.5`, `"score":1.5`, 1), "model": strings.Replace(validReply(), `"model":"jev-1.13.0"`, `"model":"jev-latest"`, 1), "usage": strings.Replace(validReply(), `,"usage":{"input_tokens":120,"output_tokens":20}`, "", 1)}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			out := clientFixture(body).Judge(context.Background(), requestFixture())
			if out.AbstainReason == "" || out.Answers != nil {
				t.Fatal("invalid output was accepted")
			}
		})
	}
	for _, body := range []string{
		strings.Replace(validReply(), `"usage":{"input_tokens":120,"output_tokens":20}`, `"usage":{}`, 1),
		strings.Replace(validReply(), `"usage":{"input_tokens":120,"output_tokens":20}`, `"usage":{"input_tokens":120}`, 1),
		strings.ReplaceAll(strings.Replace(validReply(), `"choice":"denial"`, `"choice":"unknown"`, 1), `"denial":0.95,"unknown":0`, `"denial":0,"unknown":0.95`),
	} {
		if out := clientFixture(body).Judge(context.Background(), requestFixture()); out.AbstainReason == "" || out.Answers != nil {
			t.Fatal("missing token usage or unknown evidence accepted", out)
		}
	}
}
func TestUnavailableAndInputGuardsAvoidHTTP(t *testing.T) {
	cases := map[string]func(*Request){"unknown_time": func(r *Request) { r.PublishedAt = time.Time{} }, "future": func(r *Request) { r.PublishedAt = time.Now().Add(time.Hour) }, "expired": func(r *Request) { r.ValidUntil = time.Now().Add(-time.Second) }, "private": func(r *Request) { r.DataPolicy = "private_account" }, "hash": func(r *Request) { r.InputHash = "wrong" }, "missing_body": func(r *Request) { r.State.Content = ""; r.InputHash = Hash(r.State) }}
	for name, change := range cases {
		t.Run(name, func(t *testing.T) {
			c := clientFixture(validReply())
			c.cfg.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) { t.Fatal("invalid input reached network"); return nil, nil })
			r := requestFixture()
			change(&r)
			if out := c.Judge(context.Background(), r); out.AbstainReason == "" {
				t.Fatal("missing abstention")
			}
		})
	}
	c := New(Config{})
	if got := c.Judge(context.Background(), requestFixture()); got.AbstainReason != "disabled" {
		t.Fatal(got)
	}
}
func TestRetry429529RespectsDeadlineAndBudget(t *testing.T) {
	for _, status := range []int{429, 529} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			calls := 0
			c := clientFixture(validReply())
			c.cfg.Client.Transport = roundTrip(func(*http.Request) (*http.Response, error) {
				calls++
				return &http.Response{StatusCode: status, Header: http.Header{"Retry-After": []string{"120"}}, Body: io.NopCloser(strings.NewReader(`{"secret":"must not log"}`))}, nil
			})
			start := time.Now()
			out := c.Judge(context.Background(), requestFixture())
			if calls != 1 || time.Since(start) > time.Second || out.AbstainReason == "" {
				t.Fatal(out, calls)
			}
		})
	}
	c := clientFixture(validReply())
	c.cfg.MaxRequests = 1
	c.Judge(context.Background(), requestFixture())
	if out := c.Judge(context.Background(), requestFixture()); out.AbstainReason != "request_budget_exhausted" {
		t.Fatal(out)
	}
}
func TestTimeoutAndCancellationAbstain(t *testing.T) {
	c := clientFixture(validReply())
	c.cfg.Timeout = 10 * time.Millisecond
	c.cfg.Client.Transport = roundTrip(func(r *http.Request) (*http.Response, error) { <-r.Context().Done(); return nil, r.Context().Err() })
	if out := c.Judge(context.Background(), requestFixture()); out.AbstainReason != "timeout" {
		t.Fatal(out)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if out := c.Judge(ctx, requestFixture()); out.AbstainReason != "cancelled" {
		t.Fatal(out)
	}
}

type blockingJudge struct{ block chan struct{} }

func (b blockingJudge) Judge(context.Context, Request) Result { <-b.block; return Result{Shadow: true} }

type auditMemory struct {
	mu   sync.Mutex
	rows []Audit
}

func (a *auditMemory) Record(_ context.Context, r Audit) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.rows = append(a.rows, r)
	return nil
}
func TestShadowDoesNotBlockAndBoundsConcurrency(t *testing.T) {
	block := make(chan struct{})
	sink := &auditMemory{}
	s := NewShadow(blockingJudge{block}, sink, 1)
	start := time.Now()
	if !s.Submit(requestFixture()) || s.Submit(requestFixture()) {
		t.Fatal("concurrency not bounded")
	}
	if time.Since(start) > time.Second {
		t.Fatal("shadow blocked ingestion")
	}
	close(block)
	s.Wait()
	if len(sink.rows) != 1 || s.Dropped.Load() != 1 {
		t.Fatal("missing audit/drop evidence")
	}
}
