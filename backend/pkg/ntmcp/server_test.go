package ntmcp

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	kratoslog "github.com/go-kratos/kratos/v2/log"
	trpcmcp "trpc.group/trpc-go/trpc-mcp-go"

	"server/pkg/market"
)

// ============================================================================
// nt-mcp 机器面闸测试（P3 测试组）：
//   - 启动闸——token 解析失败/空值即拒启动（NT 无设置页运行时改密面，
//     fail-fast 比 401 空转更诚实）；
//   - Bearer 恒拒——缺失/失配一律 401；路径外一律 404；
//   - 协议级端到端——真 client 过闸 initialize + list_tools + call_tool 全通。
// ============================================================================

func testConf() *Conf {
	c := &Conf{
		Enabled: true, Listen: ":0", Path: "/mcp",
		TokenRef: "env:NT_MCP_TOKEN_TEST", PageSizeMax: 50, HTTPTimeout: 5 * time.Second,
	}
	c.Tools = ToolGates{MarketSnapshot: true, Positions: true, TradeSignals: true, BacktestReport: true}
	return c
}

func staticToken(v string, err error) func(string) (string, error) {
	return func(string) (string, error) { return v, err }
}

func TestNewServer_TokenResolveFailureRejected(t *testing.T) {
	if _, err := NewServer(testConf(), staticToken("", errors.New("env NT_MCP_TOKEN_TEST missing")),
		ToolDeps{MaxPageSize: 50}, nil, kratoslog.DefaultLogger); err == nil {
		t.Fatalf("resolve failure must fail startup (未配置≠放行)")
	}
	if _, err := NewServer(testConf(), staticToken("", nil),
		ToolDeps{MaxPageSize: 50}, nil, kratoslog.DefaultLogger); err == nil {
		t.Fatalf("empty token must fail startup")
	}
}

func TestGuard_NoToken401_WrongToken401_PathOutside404(t *testing.T) {
	srv, err := NewServer(testConf(), staticToken("secret-1", nil),
		ToolDeps{MaxPageSize: 50}, nil, kratoslog.DefaultLogger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.http.Handler)
	t.Cleanup(ts.Close)

	post := func(path, token string) int {
		req, err := http.NewRequest(http.MethodPost, ts.URL+path, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"initialize"}`))
		if err != nil {
			t.Fatalf("new request: %v", err)
		}
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatalf("do: %v", err)
		}
		resp.Body.Close()
		return resp.StatusCode
	}

	if code := post("/mcp", ""); code != http.StatusUnauthorized {
		t.Errorf("no token => %d, want 401", code)
	}
	if code := post("/mcp", "wrong"); code != http.StatusUnauthorized {
		t.Errorf("wrong token => %d, want 401", code)
	}
	for _, p := range []string{"/", "/other", "/mcpother"} {
		if code := post(p, "secret-1"); code != http.StatusNotFound {
			t.Errorf("path %s => %d, want 404 (路由边界自把牢)", p, code)
		}
	}
}

func TestProtocolE2E(t *testing.T) {
	conf := testConf()
	conf.Tools = ToolGates{MarketSnapshot: true} // gate 计数语义落到 list_tools
	srv, err := NewServer(conf, staticToken("secret-1", nil), ToolDeps{
		Snapshots: &fakeSnapshots{allFn: func(ctx context.Context) ([]market.Snapshot, error) {
			return []market.Snapshot{{Symbol: "600519.SH", Time: time.Now().UTC(), Last: 1520}}, nil
		}},
		MaxPageSize: 50,
	}, nil, kratoslog.DefaultLogger)
	if err != nil {
		t.Fatalf("new server: %v", err)
	}
	ts := httptest.NewServer(srv.http.Handler)
	t.Cleanup(ts.Close)

	hdr := http.Header{}
	hdr.Set("Authorization", "Bearer secret-1")
	cli, err := trpcmcp.NewClient(ts.URL,
		trpcmcp.Implementation{Name: "nt-mcp-test", Version: "0.0.1"},
		trpcmcp.WithHTTPHeaders(hdr), trpcmcp.WithClientPath("/mcp"))
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	t.Cleanup(func() { _ = cli.Close() })

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	initRes, err := cli.Initialize(ctx, nil)
	if err != nil {
		t.Fatalf("initialize: %v", err)
	}
	if initRes.ServerInfo.Name != "nt-mcp" {
		t.Errorf("server name = %q, want nt-mcp", initRes.ServerInfo.Name)
	}

	listed, err := cli.ListTools(ctx, &trpcmcp.ListToolsRequest{})
	if err != nil {
		t.Fatalf("list tools: %v", err)
	}
	if len(listed.Tools) != 1 || listed.Tools[0].Name != "query_market_snapshot" {
		names := make([]string, 0, len(listed.Tools))
		for _, tl := range listed.Tools {
			names = append(names, tl.Name)
		}
		t.Errorf("gated list_tools = %v, want only [query_market_snapshot]", names)
	}

	res, err := cli.CallTool(ctx, &trpcmcp.CallToolRequest{
		Params: trpcmcp.CallToolParams{Name: "query_market_snapshot", Arguments: map[string]interface{}{"limit": 10}},
	})
	if err != nil {
		t.Fatalf("call tool: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool error: %+v", res.Content)
	}
	tc, ok := res.Content[0].(trpcmcp.TextContent)
	if !ok {
		t.Fatalf("content[0] = %T, want TextContent", res.Content[0])
	}
	var out marketSnapshotOutPage
	if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
		t.Fatalf("unmarshal tool payload: %v", err)
	}
	if out.Total != 1 || out.Items[0].Symbol != "600519.SH" {
		t.Errorf("payload = %+v, want total=1 symbol=600519.SH", out)
	}

	// 未注册工具名必须报工具错误——而不是 panic / 空响应。
	bad, err := cli.CallTool(ctx, &trpcmcp.CallToolRequest{
		Params: trpcmcp.CallToolParams{Name: "place_order", Arguments: nil},
	})
	if err == nil && (bad == nil || !bad.IsError) {
		t.Errorf("unknown tool must error, got res=%+v err=%v", bad, err)
	}
}
