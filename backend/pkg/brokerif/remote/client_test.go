package remote

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"encoding/json"
	"encoding/pem"
	"math/big"
	"net"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

type testCertificate struct{ cert, key, der []byte }

func testPKI(t *testing.T) (testCertificate, testCertificate, testCertificate, testCertificate) {
	t.Helper()
	caKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	caTemplate := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: "NovaTrader isolated test CA"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), IsCA: true, BasicConstraintsValid: true, KeyUsage: x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatal(err)
	}
	caPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	issue := func(serial int64, server bool) testCertificate {
		key, e := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
		if e != nil {
			t.Fatal(e)
		}
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "isolated-fixture"}, NotBefore: time.Now().Add(-time.Minute), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature, BasicConstraintsValid: true}
		if server {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}
			template.IPAddresses = []net.IP{net.ParseIP("127.0.0.1")}
		} else {
			template.ExtKeyUsage = []x509.ExtKeyUsage{x509.ExtKeyUsageClientAuth}
		}
		der, e := x509.CreateCertificate(rand.Reader, template, caTemplate, &key.PublicKey, caKey)
		if e != nil {
			t.Fatal(e)
		}
		keyDER, e := x509.MarshalPKCS8PrivateKey(key)
		if e != nil {
			t.Fatal(e)
		}
		return testCertificate{cert: pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der}), key: pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: keyDER}), der: der}
	}
	return testCertificate{cert: caPEM, der: caDER}, issue(2, true), issue(3, false), issue(4, false)
}

func TestRejectInsecureConfiguration(t *testing.T) {
	ca, _, client, _ := testPKI(t)
	base := Config{URL: "https://127.0.0.1:1", AccountID: "TEST_ACCOUNT", CAPEM: ca.cert, CertificatePEM: client.cert, PrivateKeyPEM: client.key}
	for _, endpoint := range []string{"http://127.0.0.1:1", "https://user:password@127.0.0.1", "https://127.0.0.1/?secret=x", "https://127.0.0.1/base"} {
		value := base
		value.URL = endpoint
		if _, err := New(value); err == nil {
			t.Fatal("accepted insecure endpoint")
		}
	}
	value := base
	value.CertificatePEM = nil
	if _, err := New(value); err == nil {
		t.Fatal("accepted missing client certificate")
	}
	value = base
	value.CAPEM = nil
	if _, err := New(value); err == nil {
		t.Fatal("accepted missing CA")
	}
	value = base
	value.AccountID = "../escape"
	if _, err := New(value); err == nil {
		t.Fatal("accepted invalid account")
	}
}

// This starts a real Python TLS server and Go HTTPS client on loopback using
// ephemeral certificates and an initialized temp-only protocol simulator root.
// No terminal, account credentials or real order directory is accessed.
func TestPythonMTLSIsolatedRoundTrip(t *testing.T) {
	python := os.Getenv("NT_BROKER_PYTHON")
	if python == "" {
		t.Skip("set NT_BROKER_PYTHON to run cross-process isolated gateway contract")
	}
	if _, err := os.Stat(python); err != nil {
		t.Fatal("NT_BROKER_PYTHON must reference an executable")
	}
	_, source, _, _ := runtime.Caller(0)
	moduleDir := filepath.Clean(filepath.Join(filepath.Dir(source), "..", "..", "..", "..", "broker-gw"))
	temp := t.TempDir()
	root := filepath.Join(temp, "isolated")
	runPython := func(arguments ...string) []byte {
		t.Helper()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		cmd := exec.CommandContext(ctx, python, arguments...)
		cmd.Dir = moduleDir
		cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
		output, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("isolated Python command failed: %v %s", err, output)
		}
		return output
	}
	runPython("-m", "novatrader_broker", "init", "--root", root)
	ca, server, certificate, other := testPKI(t)
	write := func(name string, value []byte) string {
		t.Helper()
		path := filepath.Join(temp, name)
		if err := os.WriteFile(path, value, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	caPath := write("ca.pem", ca.cert)
	serverPath := write("server.pem", server.cert)
	serverKey := write("server.key", server.key)
	fingerprint := sha256.Sum256(certificate.der)
	configuration, _ := json.Marshal(map[string]any{"host": "127.0.0.1", "port": 0, "ca_file": caPath, "cert_file": serverPath, "key_file": serverKey, "allowed_client_sha256": []string{hex.EncodeToString(fingerprint[:])}})
	configPath := write("tls.json", configuration)
	cmd := exec.Command(python, "-m", "novatrader_broker", "serve", "--root", root, "--account", "TEST_ACCOUNT", "--tls-config", configPath)
	cmd.Dir = moduleDir
	cmd.Env = append(os.Environ(), "PYTHONUTF8=1")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err = cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = cmd.Process.Kill(); _ = cmd.Wait() }()
	ready := make(chan string, 1)
	go func() { line, _ := bufio.NewReader(stdout).ReadString('\n'); ready <- line }()
	var startup struct {
		Port  int    `json:"port"`
		Event string `json:"event"`
	}
	select {
	case line := <-ready:
		if json.Unmarshal([]byte(line), &startup) != nil || startup.Port == 0 || startup.Event != "LISTENING" {
			t.Fatal("Python gateway failed before listening")
		}
	case <-time.After(10 * time.Second):
		t.Fatal("Python gateway startup timeout")
	}
	endpoint := "https://" + net.JoinHostPort("127.0.0.1", new(big.Int).SetInt64(int64(startup.Port)).String())
	configurationClient := Config{URL: endpoint, AccountID: "TEST_ACCOUNT", CAPEM: ca.cert, CertificatePEM: certificate.cert, PrivateKeyPEM: certificate.key, Timeout: 5 * time.Second}
	client, err := New(configurationClient)
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()
	ctx := context.Background()
	health, err := client.Health(ctx)
	if err != nil || health.ReportsReady || health.CanOpenLive {
		t.Fatalf("unexpected initial health: %+v %v", health, err)
	}
	value := Order{SID: "wire-order-1", Symbol: "600519.SH", Side: "buy", Price: "10.50", Volume: 200}
	receipt, err := client.Submit(ctx, value)
	if err != nil || receipt.State != "FILE_PUBLISHED" || receipt.BrokerACK {
		t.Fatalf("transport receipt: %+v %v", receipt, err)
	}
	query, err := client.Query(ctx, value.SID)
	if err != nil || query.State != "UNKNOWN" || query.BrokerACK {
		t.Fatalf("must await terminal evidence: %+v %v", query, err)
	}
	receipt, err = client.Submit(ctx, value)
	if err != nil || !receipt.Duplicate {
		t.Fatalf("expected duplicate receipt: %+v %v", receipt, err)
	}
	changed := value
	changed.Volume = 300
	if _, err = client.Submit(ctx, changed); err == nil || !strings.Contains(err.Error(), "IDENTITY_CONFLICT") {
		t.Fatal("same identity accepted different order", err)
	}
	files, _ := filepath.Glob(filepath.Join(root, "input", "*.order.csv"))
	if len(files) != 1 {
		t.Fatal("duplicate CSV published")
	}
	wrongAccount := configurationClient
	wrongAccount.AccountID = "OTHER_ACCOUNT"
	denied, _ := New(wrongAccount)
	defer denied.Close()
	if _, err = denied.Health(ctx); err == nil || !strings.Contains(err.Error(), "ACCOUNT_NOT_ALLOWED") {
		t.Fatal("account allowlist bypass", err)
	}
	unpinned := configurationClient
	unpinned.CertificatePEM = other.cert
	unpinned.PrivateKeyPEM = other.key
	deniedPin, _ := New(unpinned)
	defer deniedPin.Close()
	if _, err = deniedPin.Health(ctx); err == nil {
		t.Fatal("unpinned CA-signed client accepted")
	}
	roots := x509.NewCertPool()
	roots.AppendCertsFromPEM(ca.cert)
	anonymous := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, MinVersion: tls.VersionTLS12}}, Timeout: 3 * time.Second}
	if response, e := anonymous.Get(endpoint + "/v1/broker/health"); e == nil {
		response.Body.Close()
		t.Fatal("missing client certificate accepted")
	}
	anonymous.CloseIdleConnections()
	pair, _ := tls.X509KeyPair(certificate.cert, certificate.key)
	badProtocol := &http.Client{Transport: &http.Transport{TLSClientConfig: &tls.Config{RootCAs: roots, Certificates: []tls.Certificate{pair}, MinVersion: tls.VersionTLS12}}, Timeout: 3 * time.Second}
	req, _ := http.NewRequestWithContext(ctx, http.MethodGet, endpoint+"/v1/broker/health", nil)
	req.Header.Set("X-NT-Account", "TEST_ACCOUNT")
	req.Header.Set("X-NT-Protocol", "unknown-version")
	response, e := badProtocol.Do(req)
	if e != nil {
		t.Fatal(e)
	}
	response.Body.Close()
	if response.StatusCode != 409 {
		t.Fatal("protocol mismatch not rejected")
	}
	badProtocol.CloseIdleConnections()
	runPython("-c", "import sys; from novatrader_broker.simulator import Simulator; s=Simulator(sys.argv[1], 'TEST_ACCOUNT'); s.scan(); s.fill('wire-order-1', 100, 'wire-fill-1')", root)
	query, err = client.Query(ctx, value.SID)
	if err != nil || query.State != "PARTIAL" || !query.EvidenceReady || len(query.Executions) != 1 {
		t.Fatalf("real CSV readback: %+v %v", query, err)
	}
	cancel, err := client.Cancel(ctx, "wire-cancel-1", value.SID)
	if err != nil || cancel.CancelConfirmed || cancel.State != "FILE_PUBLISHED" {
		t.Fatalf("cancel transport: %+v %v", cancel, err)
	}
	query, err = client.Query(ctx, value.SID)
	if err != nil || query.State != "PARTIAL" {
		t.Fatal("cancel publication incorrectly confirmed", err)
	}
	expected := json.RawMessage(`{"account_id":"TEST_ACCOUNT","balance":"98950.00","available":"97900.00","frozen":"1050.00","positions":[{"symbol":"600519.SH","volume":100,"available_now":0}],"orders":[{"sid":"wire-order-1","state":"PARTIAL","filled_volume":100}]}`)
	reconciliation, err := client.Reconcile(ctx, expected)
	if err != nil || !strings.Contains(string(reconciliation), "ISOLATED_MATCHED") {
		t.Fatalf("isolated reconciliation: %s %v", reconciliation, err)
	}
	if snapshot, err := client.Snapshot(ctx); err != nil || len(snapshot) == 0 {
		t.Fatal("snapshot contract", err)
	}
}
