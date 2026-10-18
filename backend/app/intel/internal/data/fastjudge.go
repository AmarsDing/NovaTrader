package data

import (
	"context"
	"encoding/json"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"server/app/intel/internal/biz"
	"server/ent"
	"server/pkg/events"
	"server/pkg/fastjudge"
)

type newsShadow struct {
	runner  *fastjudge.Shadow
	sources map[string]bool
}

func newNewsShadow(client *ent.Client) *newsShadow {
	if os.Getenv("NT_JEV_ENABLED") != "true" {
		return nil
	}
	allowed := map[string]bool{}
	for _, source := range strings.Split(os.Getenv("NT_JEV_PUBLIC_SOURCES"), ",") {
		if source = strings.TrimSpace(source); source == "cninfo" || source == "eastmoney" {
			allowed[source] = true
		}
	}
	budget, _ := strconv.Atoi(os.Getenv("NT_JEV_MAX_REQUESTS"))
	if budget > 10000 {
		budget = 10000
	}
	judge := fastjudge.New(fastjudge.Config{Enabled: true, APIKey: os.Getenv("NT_JEV_API_KEY"), Model: os.Getenv("NT_JEV_MODEL"), Timeout: 4 * time.Second, MaxAttempts: 2, MaxRequests: budget})
	return &newsShadow{runner: fastjudge.NewShadow(judge, judgeAudit{client}, 2), sources: allowed}
}
func (s *newsShadow) observe(ctx context.Context, scored *biz.Scored) {
	it := scored.Item
	if !s.sources[it.Source] || it.TimeGuessed || it.Kind == biz.KindReport || it.Kind == biz.KindSocial {
		return
	}
	u, err := url.Parse(it.URL)
	if err != nil || u.User != nil || (u.Scheme != "https" && u.Scheme != "http") {
		return
	}
	host := strings.ToLower(u.Hostname())
	domain := "eastmoney.com"
	if it.Source == "cninfo" {
		domain = "cninfo.com.cn"
	}
	if host != domain && !strings.HasSuffix(host, "."+domain) {
		return
	}
	now := time.Now().UTC()
	state := fastjudge.PublicText{Title: it.Title, Content: it.Content, Target: scored.Stock}
	req := fastjudge.Request{RequestID: "news:" + strconv.Itoa(it.ID) + ":" + fastjudge.QuestionVersion, TraceID: events.TraceID(ctx), InputSnapshotID: "news:" + strconv.Itoa(it.ID), InputHash: fastjudge.Hash(state), CutoffAt: now, ValidUntil: now.Add(10 * time.Second), PublishedAt: it.PublishTime, DataPolicy: "public_news_only", QuestionVersion: fastjudge.QuestionVersion, State: state, Questions: fastjudge.PublicNewsQuestions()}
	s.runner.Submit(req)
}

// judgeAudit records bounded, typed output and hashes, never the public body,
// private account state, API key, or an untrusted provider error body.
type judgeAudit struct{ client *ent.Client }

func (a judgeAudit) Record(ctx context.Context, audit fastjudge.Audit) error {
	r := audit.Result
	raw, err := json.Marshal(r)
	if err != nil {
		return err
	}
	status := "ok"
	if r.AbstainReason != "" {
		status = "unavailable"
	}
	return a.client.LlmCallLog.Create().SetTraceID(clip(audit.TraceID, 64)).SetTask("jev_shadow").SetTier("fastjudge").SetModel(clip(r.ActualModel, 128)).SetPromptVersion(clip(r.QuestionVersion, 64)).SetPromptHash(clip(r.QuestionHash, 16)).SetInputDigest(r.InputHash).SetInputExcerpt("").SetOutput(string(raw)).SetStatus(status).SetError(r.AbstainReason).SetLatencyMs(int(r.DurationMS)).SetPromptTokens(r.Usage.InputTokens).SetCompletionTokens(r.Usage.OutputTokens).SetAttempt(r.Attempts).Exec(ctx)
}
