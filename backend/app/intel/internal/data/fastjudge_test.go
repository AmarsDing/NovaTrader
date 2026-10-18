package data

import (
	"context"
	"testing"
	"time"

	"server/app/intel/internal/biz"
	"server/pkg/fastjudge"
)

type captureJudge struct{ requests chan fastjudge.Request }

func (c captureJudge) Judge(_ context.Context, request fastjudge.Request) fastjudge.Result {
	c.requests <- request
	return fastjudge.Result{Shadow: true, InputHash: request.InputHash}
}

type discardJudgeAudit struct{}

func (discardJudgeAudit) Record(context.Context, fastjudge.Audit) error { return nil }

func TestJevObserverPublicSourcePolicy(t *testing.T) {
	for _, tc := range []struct {
		name, source, url, kind string
		guessed, allowed        bool
	}{
		{"public", "eastmoney", "https://finance.eastmoney.com/a/1.html", biz.KindNews, false, true},
		{"announcement", "cninfo", "https://www.cninfo.com.cn/a/1", biz.KindAnnouncement, false, true},
		{"spoof", "eastmoney", "https://eastmoney.com.example.org/a", biz.KindNews, false, false},
		{"credentials", "eastmoney", "https://user:password@eastmoney.com/a", biz.KindNews, false, false},
		{"private_source", "private", "https://eastmoney.com/a", biz.KindNews, false, false},
		{"report", "eastmoney", "https://eastmoney.com/a", biz.KindReport, false, false},
		{"social", "eastmoney", "https://eastmoney.com/a", biz.KindSocial, false, false},
		{"unknown_time", "eastmoney", "https://eastmoney.com/a", biz.KindNews, true, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			requests := make(chan fastjudge.Request, 1)
			shadow := &newsShadow{sources: map[string]bool{"eastmoney": true, "cninfo": true}, runner: fastjudge.NewShadow(captureJudge{requests}, discardJudgeAudit{}, 1)}
			shadow.observe(context.Background(), &biz.Scored{Stock: "000001.SZ", Item: &biz.Item{ID: 1, Source: tc.source, URL: tc.url, Kind: tc.kind, TimeGuessed: tc.guessed, PublishTime: time.Now().Add(-time.Hour), Title: "合成协议样本", Content: "只用于测试的公开文本"}})
			shadow.runner.Wait()
			if (len(requests) == 1) != tc.allowed {
				t.Fatal("incorrect public source boundary")
			}
			if tc.allowed {
				r := <-requests
				if r.DataPolicy != "public_news_only" || r.InputHash != fastjudge.Hash(r.State) || r.InputSnapshotID != "news:1" {
					t.Fatal("missing frozen input identity")
				}
			}
		})
	}
}

func TestJevObserverDisabledByDefault(t *testing.T) {
	t.Setenv("NT_JEV_ENABLED", "")
	if newNewsShadow(nil) != nil {
		t.Fatal("Jev must be opt-in")
	}
	t.Setenv("NT_JEV_ENABLED", "true")
	t.Setenv("NT_JEV_PUBLIC_SOURCES", "")
	if got := newNewsShadow(nil); len(got.sources) != 0 {
		t.Fatal("no source may be exported by default")
	}
}
