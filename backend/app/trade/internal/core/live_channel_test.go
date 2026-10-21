package core

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"server/conf"
	"server/pkg/brokerif"
	"server/pkg/brokerif/remote"
	"server/pkg/events"

	"github.com/go-kratos/kratos/v2/log"
	"google.golang.org/protobuf/types/known/durationpb"
)

func TestNewLiveChannelUnconfigured(t *testing.T) {
	for name, c := range map[string]*conf.Trade{
		"nil trade": nil,
		"no broker": {},
		"empty url": {BrokerFile: &conf.BrokerFile{}},
		"blank url": {BrokerFile: &conf.BrokerFile{Url: "   "}},
	} {
		if got := NewLiveChannel(c, log.DefaultLogger); got != nil {
			t.Errorf("%s: want nil channel, got %+v", name, got)
		}
	}
}

func TestNewLiveChannelStartupFailuresHonestDown(t *testing.T) {
	t.Run("missing pem files", func(t *testing.T) {
		lc := NewLiveChannel(&conf.Trade{BrokerFile: &conf.BrokerFile{
			Url: "https://broker.local:9443", AccountId: "T123456",
			CaFile: filepath.Join(t.TempDir(), "ca.pem"),
		}}, log.DefaultLogger)
		assertHonestDown(t, lc)
	})
	t.Run("garbage pem and non-https url", func(t *testing.T) {
		dir := t.TempDir()
		files := map[string]string{}
		for _, name := range []string{"ca.pem", "cert.pem", "key.pem"} {
			files[name] = filepath.Join(dir, name)
			if err := os.WriteFile(files[name], []byte("not a pem at all"), 0o600); err != nil {
				t.Fatal(err)
			}
		}
		lc := NewLiveChannel(&conf.Trade{BrokerFile: &conf.BrokerFile{
			// URL 非 https 与 CA 非法：remote.New 任一拒绝都必须如实 down。
			Url: "http://broker.local:9443", AccountId: "T123456",
			CaFile: files["ca.pem"], CertFile: files["cert.pem"], KeyFile: files["key.pem"],
		}}, log.DefaultLogger)
		assertHonestDown(t, lc)
	})
}

func assertHonestDown(t *testing.T, lc *LiveChannel) {
	t.Helper()
	if lc == nil || lc.Client != nil || lc.Tracker == nil {
		t.Fatalf("broken startup must yield a tracker-only channel, got %+v", lc)
	}
	if got, since := lc.Tracker.Current(); got != brokerif.Down || since.IsZero() {
		t.Fatalf("broken startup must be honestly down with a timestamp, got %s %v", got, since)
	}
	if lc.Interval() <= 0 {
		t.Fatal("probe interval must have a default")
	}
	// 半件实例的 Probe 是幂等空操作：down 不因空探而迁移，也不 panic。
	if _, err := lc.Probe(context.Background()); err != nil {
		t.Fatalf("clientless probe must be a no-op, got %v", err)
	}
	if got, _ := lc.Tracker.Current(); got != brokerif.Down {
		t.Fatalf("clientless probe must not move the state, got %s", got)
	}
	var nilChannel *LiveChannel
	if _, err := nilChannel.Probe(context.Background()); err != nil || nilChannel.Interval() <= 0 {
		t.Fatalf("nil channel must be safe: probe %v interval %v", err, nilChannel.Interval())
	}
}

// TestLiveProbeTransitions 用自签 CA + 互验 TLS 的 httptest 夹具把整条
// 「探测 → remote.Client 记账 → Tracker 迁移」链路走一遍：
// 健康语义合法 → connected；服务端拉倒后连败满阈值 → down
// （且从未建连的 idle 期失败只累计、不降级——G7 口径同一份折算）。
func TestLiveProbeTransitions(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	fixture := newBrokerFixture(t, 0)

	lc := NewLiveChannel(fixture.tradeConfig(), log.DefaultLogger)
	if lc == nil || lc.Client == nil {
		t.Fatal("valid config must build a probed client")
	}
	if got, _ := lc.Tracker.Current(); got != brokerif.Idle {
		t.Fatalf("fresh channel must start idle, got %s", got)
	}
	if _, err := lc.Probe(ctx); err != nil {
		t.Fatalf("probe against healthy fixture: %v", err)
	}
	if got, _ := lc.Tracker.Current(); got != brokerif.Connected {
		t.Fatalf("healthy probe must connect the channel, got %s", got)
	}

	// 连上之后服务端拉倒：第一次失败降 degraded，连败满阈值（3）→ down。
	fixture.close()
	for i := 0; i < brokerif.FailThreshold; i++ {
		if _, err := lc.Probe(ctx); err == nil {
			t.Fatalf("probe %d against dead fixture must fail", i+1)
		}
	}
	if got, since := lc.Tracker.Current(); got != brokerif.Down || since.IsZero() {
		t.Fatalf("consecutive failures must take the channel down, got %s %v", got, since)
	}

	// 从未建连过的通道碰死服务端：失败只累计（idle 期不降级），满阈值才 down。
	dead := newDeadBrokerFixture(t)
	lc2 := NewLiveChannel(dead.tradeConfig(), log.DefaultLogger)
	if lc2 == nil || lc2.Client == nil {
		t.Fatal("valid config must build a probed client even against a dead gateway")
	}
	for i := 0; i < brokerif.FailThreshold; i++ {
		_, _ = lc2.Probe(ctx)
		if i < brokerif.FailThreshold-1 {
			if got, _ := lc2.Tracker.Current(); got != brokerif.Idle {
				t.Fatalf("idle channel before threshold must not degrade, probe %d → %s", i+1, got)
			}
		}
	}
	if got, _ := lc2.Tracker.Current(); got != brokerif.Down {
		t.Fatalf("idle channel must go down at the failure threshold, got %s", got)
	}
}

// TestLiveProbeSurfacesTransportUnknown 是 D7 的数据出口：探测成功时 Probe 必须把
// broker-gw 健康载荷原样带回来（告警边沿判定在 Runner 侧，本层不做任何折算）。
func TestLiveProbeSurfacesTransportUnknown(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	fixture := newBrokerFixture(t, 2)
	lc := NewLiveChannel(fixture.tradeConfig(), log.DefaultLogger)
	if lc == nil || lc.Client == nil {
		t.Fatal("valid config must build a probed client")
	}
	health, err := lc.Probe(ctx)
	if err != nil {
		t.Fatalf("probe against healthy fixture: %v", err)
	}
	if health.TransportUnknown != 2 {
		t.Fatalf("probe must surface transport_unknown verbatim, got %d", health.TransportUnknown)
	}
}

// TestNoteLiveUnknownEdge 锁定 transport_unknown 告警的边沿/转净纪律（D7，带件矩阵）：
// 0 → 不告警；0→N 上升沿打一条；计数不变不重复打屏；继续上升再打；归零重新武装
// 后再次上升仍可提醒。载荷合同对齐 notify classify.fromRequest 与前端 levelOf：
// level=warn（→L2）、category=trade、dedup_key 稳定供 M10 合并。
func TestNoteLiveUnknownEdge(t *testing.T) {
	var envs []events.Envelope
	r := &Runner{log: log.NewHelper(log.DefaultLogger)}
	r.publish = func(_ context.Context, env events.Envelope) error {
		envs = append(envs, env)
		return nil
	}
	r.noteLiveUnknown(0)
	if len(envs) != 0 {
		t.Fatalf("zero unknowns must not alert, got %d envelopes", len(envs))
	}
	r.noteLiveUnknown(2)
	r.noteLiveUnknown(2) // 同源滞留数不变：不重复打屏
	if len(envs) != 1 {
		t.Fatalf("rising edge must alert exactly once, got %d envelopes", len(envs))
	}
	r.noteLiveUnknown(3)
	if len(envs) != 2 {
		t.Fatalf("further rise must alert again, got %d envelopes", len(envs))
	}
	r.noteLiveUnknown(0) // 转净：重新武装
	r.noteLiveUnknown(1)
	if len(envs) != 3 {
		t.Fatalf("re-arm after clearing must allow a fresh alert, got %d envelopes", len(envs))
	}

	env := envs[0]
	if env.Subject != events.SubjectNotifyRequest || env.Source != "trade" {
		t.Fatalf("alert must ride notify.request from trade, got %s/%s", env.Source, env.Subject)
	}
	var body struct {
		Level    string `json:"level"`
		Category string `json:"category"`
		Title    string `json:"title"`
		Body     string `json:"body"`
		DedupKey string `json:"dedup_key"`
	}
	if err := json.Unmarshal(env.Payload, &body); err != nil {
		t.Fatalf("alert payload must unmarshal: %v", err)
	}
	if body.Level != "warn" || body.Category != "trade" || body.DedupKey != "trade:live-transport-unknown" {
		t.Fatalf("alert contract drifted: %+v", body)
	}
	if body.Title == "" || !strings.Contains(body.Body, "2 笔") || !strings.Contains(body.Body, "严禁重发") {
		t.Fatalf("alert body must carry the count and the no-resubmit discipline: %+v", body)
	}

	// 无发布链的 Runner（未挂 bus）：边沿只翻基线，不 panic。
	bare := &Runner{log: log.NewHelper(log.DefaultLogger)}
	bare.noteLiveUnknown(5)
	if bare.liveUnknownBaseline != 5 {
		t.Fatalf("baseline must advance even without a publish chain, got %d", bare.liveUnknownBaseline)
	}
}

const fixtureAccount = "T123456"

type brokerFixture struct {
	server *httptest.Server
	dir    string
	url    string
	closed bool
}

func newBrokerFixture(t *testing.T, unknown int) *brokerFixture {
	t.Helper()
	caPEM, caPool, caCert, caKey := makeFixtureCA(t)
	serverPair := makeFixtureLeaf(t, caCert, caKey, "nt-test-broker-gw", false)
	clientPair := makeFixtureLeaf(t, caCert, caKey, "nt-test-trade", true)
	serverCert, err := tls.X509KeyPair(serverPair.cert, serverPair.key)
	if err != nil {
		t.Fatal(err)
	}
	env := fixtureEnvelope(t, unknown)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-NT-Protocol") != remote.Protocol || r.Header.Get("X-NT-Account") != fixtureAccount {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(env)
	}))
	server.TLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
		ClientCAs:    caPool,
		ClientAuth:   tls.RequireAndVerifyClientCert,
	}
	server.StartTLS()
	fix := &brokerFixture{server: server, url: server.URL}
	t.Cleanup(fix.close)
	fix.writeFiles(t, caPEM, clientPair)
	return fix
}

// newDeadBrokerFixture 起服务立刻关掉：配置完全合法、通道从未建连。
func newDeadBrokerFixture(t *testing.T) *brokerFixture {
	t.Helper()
	fixture := newBrokerFixture(t, 0)
	fixture.close()
	return fixture
}

// close 幂等关停夹具（用例里会主动拉倒服务端再交还给 t.Cleanup）。
func (fx *brokerFixture) close() {
	if !fx.closed {
		fx.closed = true
		fx.server.Close()
	}
}

type fixturePEMPair struct{ cert, key []byte }

func makeFixtureCA(t *testing.T) (pemBytes []byte, pool *x509.CertPool, cert *x509.Certificate, key *ecdsa.PrivateKey) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber:          fixtureSerial(t),
		Subject:               pkix.Name{CommonName: "nt-fixture-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(2 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, err = x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	pemBytes = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	pool = x509.NewCertPool()
	if !pool.AppendCertsFromPEM(pemBytes) {
		t.Fatal("fixture CA must parse")
	}
	return pemBytes, pool, cert, key
}

func makeFixtureLeaf(t *testing.T, ca *x509.Certificate, caKey *ecdsa.PrivateKey, cn string, client bool) fixturePEMPair {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: fixtureSerial(t),
		Subject:      pkix.Name{CommonName: cn},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(2 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	if client {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
	} else {
		tmpl.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
		tmpl.IPAddresses = []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")}
		tmpl.DNSNames = []string{"localhost"}
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, ca, &key.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	keyDER, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatal(err)
	}
	return fixturePEMPair{
		cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}),
		key:  pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: keyDER}),
	}
}

func fixtureSerial(t *testing.T) *big.Int {
	t.Helper()
	n, err := rand.Int(rand.Reader, big.NewInt(1<<62))
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// fixtureEnvelope 是合法的隔离健康回包：信封与载荷双层的 protocol/账户必须回声，
// 载荷须满足 remote.Health 语义（mode=ISOLATED 且无实盘授权旗标）。
// unknown 参数化 transport_unknown 计数，供 D7 告警边沿用例模拟回执滞留。
func fixtureEnvelope(t *testing.T, unknown int) []byte {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"protocol": remote.Protocol, "mode": "ISOLATED", "account_id": fixtureAccount,
		"reports_ready": true, "reasons": []string{},
		"file_published": 0, "transport_unknown": unknown, "order_unknown": 0,
		"terminal_login_verified": false, "can_open_live": false,
	})
	if err != nil {
		t.Fatal(err)
	}
	envelope, err := json.Marshal(map[string]any{
		"protocol": remote.Protocol, "account_id": fixtureAccount, "payload": json.RawMessage(payload),
	})
	if err != nil {
		t.Fatal(err)
	}
	return envelope
}

func (fx *brokerFixture) writeFiles(t *testing.T, caPEM []byte, client fixturePEMPair) {
	t.Helper()
	fx.dir = t.TempDir()
	for name, data := range map[string][]byte{"ca.pem": caPEM, "cert.pem": client.cert, "key.pem": client.key} {
		if err := os.WriteFile(filepath.Join(fx.dir, name), data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (fx *brokerFixture) tradeConfig() *conf.Trade {
	return &conf.Trade{BrokerFile: &conf.BrokerFile{
		Url: fx.url, AccountId: fixtureAccount,
		CaFile:   filepath.Join(fx.dir, "ca.pem"),
		CertFile: filepath.Join(fx.dir, "cert.pem"),
		KeyFile:  filepath.Join(fx.dir, "key.pem"),
		Timeout:  durationpb.New(3 * time.Second), ProbeInterval: durationpb.New(2 * time.Second),
	}}
}
