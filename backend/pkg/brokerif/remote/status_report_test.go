package remote

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"server/pkg/brokerif"
)

// toggleFixture 是一个可远程切换「断线/恢复」的协议夹具：fail 打开时
// hijack 并直接断开连接（制造http层传输错误），关闭时回合法协议信封；
// bogus 打开时信封身份合法但回执不回声（SID 对不上）——制造语义层伪回执。
// 状态机回放不依赖 Python 网关进程，CI 常跑。
type toggleFixture struct {
	srv   *httptest.Server
	fail  atomic.Int32
	bogus atomic.Int32
}

func newToggleFixture(t *testing.T, ca, server testCertificate, account string) *toggleFixture {
	t.Helper()
	f := &toggleFixture{}
	serverCert, err := tls.X509KeyPair(server.cert, server.key)
	if err != nil {
		t.Fatal(err)
	}
	mux := http.NewServeMux()
	wrap := func(payload any) []byte {
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		env, err := json.Marshal(map[string]any{"protocol": Protocol, "account_id": account, "payload": json.RawMessage(raw)})
		if err != nil {
			t.Fatal(err)
		}
		return env
	}
	mux.HandleFunc("/v1/broker/health", func(w http.ResponseWriter, r *http.Request) {
		if f.cut(w) {
			return
		}
		_, _ = w.Write(wrap(map[string]any{
			"protocol": Protocol, "mode": "ISOLATED", "account_id": account,
			"reports_ready": true, "reasons": []string{},
			"file_published": 0, "transport_unknown": 0, "order_unknown": 0,
			"terminal_login_verified": false, "can_open_live": false,
		}))
	})
	mux.HandleFunc("/v1/broker/orders", func(w http.ResponseWriter, r *http.Request) {
		if f.cut(w) {
			return
		}
		var in struct {
			Order Order `json:"order"`
		}
		if json.NewDecoder(r.Body).Decode(&in) != nil {
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		sid := in.Order.SID
		if f.bogus.Load() != 0 {
			sid = "forged-" + sid // 信封合法、回执身份不回声
		}
		_, _ = w.Write(wrap(map[string]any{
			"protocol": Protocol, "kind": "order", "state": "FILE_PUBLISHED",
			"sid": sid, "key": sid,
			"broker_ack": false, "retry_allowed": false, "duplicate": false,
			"cancel_confirmed": false, "reason": "",
		}))
	})
	f.srv = httptest.NewUnstartedServer(mux)
	f.srv.TLS = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{serverCert}}
	f.srv.StartTLS()
	t.Cleanup(f.srv.Close)
	return f
}

// cut 为真时断掉连接并报告已处理。hijack 失败则落 500（对客户端同样算失败）。
func (f *toggleFixture) cut(w http.ResponseWriter) bool {
	if f.fail.Load() == 0 {
		return false
	}
	if hj, ok := w.(http.Hijacker); ok {
		if conn, _, err := hj.Hijack(); err == nil {
			_ = conn.Close()
			return true
		}
	}
	w.WriteHeader(http.StatusInternalServerError)
	return true
}

func (f *toggleFixture) cutNow()   { f.fail.Store(1) }
func (f *toggleFixture) recover()  { f.fail.Store(0) }
func (f *toggleFixture) bogusNow() { f.bogus.Store(1) }
func (f *toggleFixture) honest()   { f.bogus.Store(0) }

func (f *toggleFixture) client(t *testing.T, ca, cli testCertificate, account string, track *brokerif.Tracker) *Client {
	t.Helper()
	c, err := New(Config{
		URL: f.srv.URL, AccountID: account,
		CAPEM: ca.cert, CertificatePEM: cli.cert, PrivateKeyPEM: cli.key,
		Timeout: 5 * time.Second, Track: track,
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(c.Close)
	return c
}

// TestStatusReplayG7 验收②：状态机迁移在同一通道上按「连上→降级→停机→恢复」全程回放。
func TestStatusReplayG7(t *testing.T) {
	ca, server, cli, _ := testPKI(t)
	const account = "TEST_ACCOUNT"
	fx := newToggleFixture(t, ca, server, account)

	type event struct{ from, to brokerif.Status }
	var got []event
	track := brokerif.NewTracker(time.Now())
	track.Subscribe(func(from, to brokerif.Status, _ time.Time) { got = append(got, event{from, to}) })
	c := fx.client(t, ca, cli, account, track)
	ctx := context.Background()

	// 1. 首次成功：idle → connected。
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if s, _ := track.Current(); s != brokerif.Connected {
		t.Fatalf("after first success want connected, got %s", s)
	}

	// 2. 断线三次：connected → degraded（幂等一次）→ down。
	fx.cutNow()
	ord := Order{SID: "wire-status-1", Symbol: "SHSE.600519", Side: "BUY", Price: "1700.00", Volume: 100}
	for i := 0; i < brokerif.FailThreshold; i++ {
		if _, err := c.Submit(ctx, ord); err == nil {
			t.Fatalf("attempt %d should fail while cut", i)
		}
	}
	if s, _ := track.Current(); s != brokerif.Down {
		t.Fatalf("after %d failures want down, got %s", brokerif.FailThreshold, s)
	}

	// 3. 恢复：down → connected；连败已清零（再断一次只降级，不停机）。
	fx.recover()
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("health after recover: %v", err)
	}
	if s, _ := track.Current(); s != brokerif.Connected {
		t.Fatalf("after recovery want connected, got %s", s)
	}
	fx.cutNow()
	if _, err := c.Submit(ctx, ord); err == nil {
		t.Fatal("should fail while cut again")
	}
	if s, _ := track.Current(); s != brokerif.Degraded {
		t.Fatalf("single post-recovery failure should degrade only, got %s", s)
	}

	want := []event{
		{brokerif.Idle, brokerif.Connected},
		{brokerif.Connected, brokerif.Degraded},
		{brokerif.Degraded, brokerif.Down},
		{brokerif.Down, brokerif.Connected},
		{brokerif.Connected, brokerif.Degraded},
	}
	if len(got) != len(want) {
		t.Fatalf("transition log %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("transition %d = %v, want %v", i, got[i], want[i])
		}
	}
}

// TestStatusNotTrackedWithoutConfig 保持纯传输语义：没配 Track 不配 Observer，请求照常。
func TestStatusNotTrackedWithoutConfig(t *testing.T) {
	ca, server, cli, _ := testPKI(t)
	const account = "TEST_ACCOUNT"
	fx := newToggleFixture(t, ca, server, account)
	c := fx.client(t, ca, cli, account, nil)
	if _, err := c.Health(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
}

// TestBogusReceiptCountsAsFailure 钉死记账矩阵的语义层那条边：
// 信封身份合法但回执不回声（SID 对不上）必须按 Observer.Fail 契约
// （「回包身份不符」）折算成一次通道失败，而不是拿「信封能解开」误记 Ok
// 把通道钉在 connected。一次伪回执应恰好计一笔失败（connected→degraded）。
func TestBogusReceiptCountsAsFailure(t *testing.T) {
	ca, server, cli, _ := testPKI(t)
	const account = "TEST_ACCOUNT"
	fx := newToggleFixture(t, ca, server, account)
	track := brokerif.NewTracker(time.Now())
	c := fx.client(t, ca, cli, account, track)
	ctx := context.Background()

	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("health: %v", err)
	}
	if s, _ := track.Current(); s != brokerif.Connected {
		t.Fatalf("want connected after health, got %s", s)
	}

	fx.bogusNow()
	ord := Order{SID: "wire-bogus-1", Symbol: "SHSE.600519", Side: "BUY", Price: "1700.00", Volume: 100}
	if _, err := c.Submit(ctx, ord); err == nil {
		t.Fatal("bogus receipt must be rejected")
	}
	if s, _ := track.Current(); s != brokerif.Degraded {
		t.Fatalf("one semantic-bogus receipt must count as exactly one failure (connected→degraded), got %s", s)
	}

	// 恢复诚实回包后一次成功：degraded→connected 且连败清零（再伪一次只降级）。
	fx.honest()
	if _, err := c.Health(ctx); err != nil {
		t.Fatalf("health after honest: %v", err)
	}
	if s, _ := track.Current(); s != brokerif.Connected {
		t.Fatalf("want recovered connected, got %s", s)
	}
	fx.bogusNow()
	if _, err := c.Submit(ctx, ord); err == nil {
		t.Fatal("bogus receipt must be rejected")
	}
	if s, _ := track.Current(); s != brokerif.Degraded {
		t.Fatalf("post-recovery bogus receipt must degrade only (streak was reset), got %s", s)
	}
}
