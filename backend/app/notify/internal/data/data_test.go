package data

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"server/app/notify/internal/biz"
	"server/conf"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
)

func TestFeishuSkippedAndText(t *testing.T) {
	f := NewFeishu(&conf.Notify{})
	if err := f.Send(context.Background(), "x"); !errors.Is(err, biz.ErrSkipped) {
		t.Fatalf("empty webhook: %v", err)
	}
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
			t.Error(err)
		}
		_, _ = w.Write([]byte(`{"code":0,"msg":"success"}`))
	}))
	defer srv.Close()
	f.webhook = srv.URL
	f.client = srv.Client()
	f.secret = "s3cret"
	f.now = func() time.Time { return time.Unix(100, 0) }
	if err := f.Send(context.Background(), "只是文本"); err != nil {
		t.Fatal(err)
	}
	if got["msg_type"] != "text" || got["sign"] == "" {
		t.Fatalf("body %+v", got)
	}
	content, _ := got["content"].(map[string]any)
	if content["text"] != "只是文本" {
		t.Fatalf("content %+v", content)
	}
	for _, banned := range []string{"card", "button", "interactive"} {
		if _, ok := got[banned]; ok {
			t.Fatalf("payload has %s", banned)
		}
	}

	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"code":19024,"msg":"keyword"}`))
	}))
	defer bad.Close()
	f.webhook = bad.URL
	f.client = bad.Client()
	if err := f.Send(context.Background(), "no keyword"); err == nil {
		t.Fatal("feishu error code must fail")
	}
}

func TestRepoRoundTrip(t *testing.T) {
	client, cleanup, err := NewEntClient(&conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres",
		Database: "novatrader_notifytest", SslMode: "disable",
	}, log.DefaultLogger)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	if _, err := client.NotifyMessage.Delete().Exec(ctx); err != nil {
		t.Fatal(err)
	}
	repo := NewRepo(client, log.DefaultLogger)
	now := time.Date(2026, 10, 9, 2, 0, 0, 0, time.UTC)
	msg := biz.Message{
		EventID: "e1", TraceID: "t1", Source: "risk", Subject: events.SubjectRiskAlert,
		Category: "risk", Priority: biz.PriCritical, Title: "killswitch", Body: "停机",
		Fields: map[string]string{"kind": "killswitch"}, DedupKey: "risk:killswitch:", CreatedAt: now,
	}
	row, dup, err := repo.Insert(ctx, msg)
	if err != nil || dup || row.ID == 0 || row.Status != biz.StatusPending {
		t.Fatalf("insert %+v dup %v err %v", row, dup, err)
	}
	again, dup, err := repo.Insert(ctx, msg)
	if err != nil || !dup || again.ID != row.ID {
		t.Fatalf("dup %+v %v %v", again, dup, err)
	}
	if err := repo.MarkHeld(ctx, row.ID); err != nil {
		t.Fatal(err)
	}
	second := msg
	second.EventID = "e2"
	second.Body = "还在停"
	second.CreatedAt = now.Add(time.Second)
	row2, _, err := repo.Insert(ctx, second)
	if err != nil {
		t.Fatal(err)
	}
	if err := repo.MarkMerged(ctx, row2.ID, row.ID, second); err != nil {
		t.Fatal(err)
	}
	anchor, err := repo.Get(ctx, row.ID)
	if err != nil {
		t.Fatal(err)
	}
	if anchor.MergeCount != 1 || anchor.Body != "还在停" || anchor.Status != biz.StatusHeld {
		t.Fatalf("anchor %+v", anchor)
	}
	merged, err := repo.Get(ctx, row2.ID)
	if err != nil || merged.Status != biz.StatusMerged || merged.MergedInto != row.ID {
		t.Fatalf("merged %+v %v", merged, err)
	}
	recent, err := repo.Recent(ctx, msg.DedupKey, now.Add(-time.Minute), row2.ID)
	if err != nil || recent == nil || recent.ID != row.ID {
		t.Fatalf("recent %+v %v", recent, err)
	}
	anchor.Status = biz.StatusSent
	anchor.Rendered = "killswitch\n还在停"
	anchor.DesktopStatus = biz.ChannelSent
	anchor.DesktopAttempts = 1
	anchor.FeishuStatus = biz.ChannelSkipped
	anchor.SentAt = now.Add(2 * time.Second)
	if err := repo.SaveDelivery(ctx, anchor); err != nil {
		t.Fatal(err)
	}
	list, err := repo.List(ctx, 10)
	if err != nil || len(list) != 2 || list[0].ID != row2.ID {
		t.Fatalf("list %+v %v", list, err)
	}
	if _, err := repo.Get(ctx, 999999); !errors.Is(err, biz.ErrNotFound) {
		t.Fatalf("missing: %v", err)
	}
}
