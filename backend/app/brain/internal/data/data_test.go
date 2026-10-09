package data

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"
	"time"

	"server/app/brain/internal/biz"
	"server/conf"
	"server/ent/agentdecision"
	"server/ent/intellink"
	"server/ent/llmcalllog"
	"server/ent/marketdata"
	"server/ent/morningbriefing"
	"server/ent/newssentiment"
	"server/ent/outbox"
	"server/ent/position"
	"server/ent/stockbasic"
	"server/ent/stockfactor"
	"server/pkg/events"
	"server/pkg/llm"
	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/nats-io/nats.go"
)

const testSymbol = "688999.SH"

var newsID = regexp.MustCompile(`N:\d+`)

type noProgress struct{}

func (noProgress) Publish(context.Context, biz.ProgressEvent) {}

// fakeVLLM 按系统提示判断任务，返回引用事实包编号的合格输出。
func fakeVLLM(t *testing.T) *httptest.Server {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []llm.Message `json:"messages"`
		}
		_ = json.NewDecoder(r.Body).Decode(&body)
		sys, user := body.Messages[0].Content, body.Messages[1].Content
		nid := newsID.FindString(user)
		var content string
		switch {
		case strings.Contains(sys, "盘前晨报"):
			content = fmt.Sprintf(`{"headline":"测试晨报","market_view":"平稳","risks":[],
				"watchlist":[{"symbol":"%s","reason":"有公告","evidence":["%s"]}],
				"positions":[{"symbol":"%s","view":"持有","evidence":["F:pos.paper.%s.qty"]}]}`, testSymbol, nid, testSymbol, testSymbol)
		case strings.Contains(sys, "四维结论"):
			content = `{"summary":"偏多","evidence":["F:close"]}`
		default:
			ev := "F:close"
			switch {
			case strings.Contains(sys, "【消息面】"):
				ev = nid
			case strings.Contains(sys, "【资金面】"):
				ev = "F:amount"
			case strings.Contains(sys, "【外围/情绪】"):
				ev = "F:industry"
			}
			content = fmt.Sprintf(`{"score":70,"reasons":[{"text":"数据支持","evidence":["%s"]}],"risks":[]}`, ev)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{
			"model":   "fake-qwen",
			"choices": []any{map[string]any{"message": map[string]any{"content": content}}},
		})
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestProgressOnCoreNATS(t *testing.T) {
	bus, closeBus, err := NewBus(&conf.Nats{})
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(closeBus)
	nc, err := nats.Connect(nats.DefaultURL)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(nc.Close)
	sub, err := nc.SubscribeSync(events.SubjectBrainProgress)
	if err != nil {
		t.Fatal(err)
	}
	_ = nc.Flush()
	NewProgress(bus, log.DefaultLogger).Publish(context.Background(), biz.ProgressEvent{TraceID: "t-progress", Symbol: testSymbol, Stage: "facts", OK: true})
	msg, err := sub.NextMsg(2 * time.Second)
	if err != nil {
		t.Fatal(err)
	}
	env, err := events.Unmarshal(msg.Data)
	if err != nil || env.TraceID != "t-progress" || !strings.Contains(string(env.Payload), `"stage":"facts"`) {
		t.Fatalf("envelope = %+v, %v", env, err)
	}
}

func TestBrainAgainstPostgres(t *testing.T) {
	logger := log.DefaultLogger
	client, cleanup, err := NewEntClient(&conf.Postgres{
		Host: "127.0.0.1", Port: 5432, User: "postgres", Password: "postgres", Database: "novatrader", SslMode: "disable",
	}, logger)
	if err != nil {
		t.Skip(err)
	}
	t.Cleanup(cleanup)
	ctx := context.Background()
	start := time.Now()
	now := time.Now().In(tradecal.Shanghai())
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, tradecal.Shanghai())

	purge := func() {
		client.IntelLink.Delete().Where(intellink.TargetEQ(testSymbol)).ExecX(ctx)
		client.StockBasic.Delete().Where(stockbasic.StockCodeEQ(testSymbol)).ExecX(ctx)
		client.MarketData.Delete().Where(marketdata.SymbolEQ(testSymbol)).ExecX(ctx)
		client.NewsSentiment.Delete().Where(newssentiment.Or(
			newssentiment.StockCodeEQ(testSymbol),
			newssentiment.NewsTitleEQ("关联表带入"),
		)).ExecX(ctx)
		client.Position.Delete().Where(position.SymbolEQ(testSymbol)).ExecX(ctx)
		client.StockFactor.Delete().Where(stockfactor.SymbolEQ(testSymbol)).ExecX(ctx)
	}
	purge()
	t.Cleanup(purge)

	industry := "测试行业"
	client.StockBasic.Create().SetStockCode(testSymbol).SetStockName("测试股份").SetMarket("SH").SetIndustry(industry).ExecX(ctx)
	for i := 0; i < 25; i++ {
		c := 20 + float64(i)*0.2
		hi, lo, amt := c+0.3, c-0.3, c*2e6
		vol := int64(2e6)
		client.MarketData.Create().SetSymbol(testSymbol).SetFreq("1d").
			SetBarTime(today.AddDate(0, 0, -25+i)).
			SetOpen(c).SetHigh(hi).SetLow(lo).SetClose(c).SetVolume(vol).SetAmount(amt).ExecX(ctx)
	}
	pub := today.Add(9 * time.Hour)
	if limit := now.Add(-time.Hour); limit.Before(pub) {
		pub = limit
	}
	title, body, src := "测试股份发布回购公告", "拟回购不超过 1 亿元", "test"
	client.NewsSentiment.Create().SetStockCode(testSymbol).SetNewsTitle(title).SetContent(body).SetSource(src).
		SetImportance(4).SetSentimentScore(0.4).SetPublishTime(pub).SetStatus("scored").ExecX(ctx)
	linked := client.NewsSentiment.Create().SetNewsTitle("关联表带入").SetContent("只通过关联表挂上").
		SetImportance(3).SetPublishTime(pub).SetStatus("scored").SaveX(ctx)
	client.IntelLink.Create().SetNewsID(linked.ID).SetClusterID(0).SetTargetType("stock").SetTarget(testSymbol).
		SetConfidence(0.9).SetMethod("name").ExecX(ctx)
	client.NewsSentiment.Create().SetStockCode(testSymbol).SetNewsTitle("过滤掉").SetContent("不该进事实包").
		SetImportance(5).SetPublishTime(pub).SetStatus("filtered").ExecX(ctx)
	client.StockFactor.Create().SetSymbol(testSymbol).SetTradeDate(today).SetKind("close").SetAsOf(pub).
		SetFactors(map[string]float64{"ma5": 21.5, "atr14": 0.3}).ExecX(ctx)
	client.Position.Create().SetBook("paper").SetSymbol(testSymbol).SetQuantity(100).SetAvailable(100).ExecX(ctx)

	srv := fakeVLLM(t)
	gw := llm.New(map[llm.Tier]llm.ModelConfig{
		llm.Large: {BaseURL: srv.URL + "/v1", Model: "big"},
		llm.Small: {BaseURL: srv.URL + "/v1", Model: "small"},
	}, llm.EntRecorder{Client: client})
	uc := biz.NewUsecase(biz.NewSettings(nil), gw, NewMarketRepo(client), NewDecisionRepo(client), NewBriefingRepo(client), noProgress{}, logger)

	res, err := uc.Analyze(ctx, biz.AnalyzeInput{Symbol: testSymbol, RuleScore: 55, Phase: "WARM"})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		client.LlmCallLog.Delete().Where(llmcalllog.CreatedAtGTE(start), llmcalllog.ModelEQ("fake-qwen")).ExecX(ctx)
		client.AgentDecision.Delete().Where(agentdecision.CreatedAtGTE(start), agentdecision.AgentNameEQ("brain")).ExecX(ctx)
	})
	if res.Degraded || res.Discarded || res.Composite != 70 {
		t.Fatalf("analysis = %+v", res)
	}
	row := client.AgentDecision.GetX(ctx, res.DecisionID)
	if row.Symbol != testSymbol || row.Confidence == nil || *row.Confidence != 70 || row.Model != "fake-qwen" || row.Content["pack"] == nil {
		t.Fatalf("decision row = %+v", row)
	}
	calls := client.LlmCallLog.Query().Where(llmcalllog.DecisionIDEQ(res.DecisionID)).AllX(ctx)
	if len(calls) != 5 {
		t.Fatalf("llm_call_log rows = %d", len(calls))
	}
	var excerpt string
	for _, c := range calls {
		if c.Status != "ok" || c.PromptVersion == "" || c.PromptHash == "" || c.InputDigest == "" || c.TraceID != res.TraceID {
			t.Fatalf("call row = %+v", c)
		}
		if c.Task == "technical" {
			excerpt = c.InputExcerpt
		}
	}
	if !strings.Contains(excerpt, "F:ma5 | MA5 | 21.5") || !strings.Contains(excerpt, "关联表带入") || strings.Contains(excerpt, "过滤掉") || strings.Contains(excerpt, "F:ma10") {
		t.Fatalf("fact pack excerpt:\n%s", excerpt)
	}

	ans, err := uc.Ask(ctx, res.DecisionID, "技术面依据？")
	if err != nil || ans.Degraded {
		t.Fatalf("ask = %+v, %v", ans, err)
	}

	if n := client.MorningBriefing.Query().Where(morningbriefing.DateEQ(today)).CountX(ctx); n > 0 {
		t.Logf("today already has a briefing, skip briefing check")
		return
	}
	t.Cleanup(func() {
		client.MorningBriefing.Delete().Where(morningbriefing.DateEQ(today)).ExecX(ctx)
		client.Outbox.Delete().Where(outbox.SubjectEQ(events.SubjectBriefing), outbox.CreatedAtGTE(start)).ExecX(ctx)
	})
	for i := 0; i < 2; i++ {
		b, err := uc.GenerateBriefing(ctx, now)
		if err != nil {
			t.Fatal(err)
		}
		if b.Degraded || len(b.Watchlist) != 1 || len(b.Positions) != 1 {
			t.Fatalf("briefing = %+v", b)
		}
	}
	if n := client.MorningBriefing.Query().Where(morningbriefing.DateEQ(today)).CountX(ctx); n != 1 {
		t.Fatalf("briefing rows for today = %d, want 1 (overwrite)", n)
	}
	got, err := uc.GetBriefing(ctx, today)
	if err != nil || got.Headline != "测试晨报" || got.Date.Format("2006-01-02") != today.Format("2006-01-02") {
		t.Fatalf("get briefing = %+v, %v", got, err)
	}
	if n := client.Outbox.Query().Where(outbox.SubjectEQ(events.SubjectBriefing), outbox.CreatedAtGTE(start)).CountX(ctx); n != 2 {
		t.Fatalf("outbox briefing events = %d", n)
	}
}
