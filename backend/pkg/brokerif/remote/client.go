// Package remote is an opt-in mTLS transport to the isolated Windows broker-gw.
// It deliberately does NOT implement brokerif.Adapter: a published CSV cannot
// be represented as a successful broker order submission. trade is not wired.
package remote

import (
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const Protocol = "eastmoney-file-csv-isolated-v1"

var safeAccount = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
var safeSID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9_.:#-]{0,95}$`)
var safeError = regexp.MustCompile(`^[A-Z_]{1,96}$`)

type Config struct {
	URL                                  string
	AccountID                            string // single allowlisted account, never selected by an HTTP caller
	CAPEM, CertificatePEM, PrivateKeyPEM []byte
	Timeout                              time.Duration
}

type Client struct {
	base, account string
	http          *http.Client
}

func New(config Config) (*Client, error) {
	target, err := url.Parse(config.URL)
	if err != nil || target.Scheme != "https" || target.Host == "" || target.User != nil || target.ForceQuery || target.RawQuery != "" || target.Fragment != "" || target.RawPath != "" || (target.Path != "" && target.Path != "/") || !safeAccount.MatchString(config.AccountID) {
		return nil, errors.New("broker remote: explicit HTTPS endpoint and account required")
	}
	roots := x509.NewCertPool()
	if !roots.AppendCertsFromPEM(config.CAPEM) {
		return nil, errors.New("broker remote: CA required")
	}
	certificate, err := tls.X509KeyPair(config.CertificatePEM, config.PrivateKeyPEM)
	if err != nil {
		return nil, errors.New("broker remote: valid client certificate required")
	}
	if config.Timeout == 0 {
		config.Timeout = 10 * time.Second
	}
	if config.Timeout < time.Second || config.Timeout > 30*time.Second {
		return nil, errors.New("broker remote: timeout out of range")
	}
	transport := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12, RootCAs: roots, Certificates: []tls.Certificate{certificate}},
		TLSHandshakeTimeout: 5 * time.Second, ResponseHeaderTimeout: 5 * time.Second, MaxConnsPerHost: 8, MaxIdleConnsPerHost: 2}
	return &Client{base: strings.TrimSuffix(config.URL, "/"), account: config.AccountID,
		http: &http.Client{Transport: transport, Timeout: config.Timeout, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("broker remote: redirects forbidden") }}}, nil
}

func (c *Client) Close() { c.http.CloseIdleConnections() }

type Order struct {
	SID    string `json:"sid"`
	Symbol string `json:"symbol"`
	Side   string `json:"side"`
	Price  string `json:"price"` // decimal string; transport must never round the price
	Volume int    `json:"volume"`
}
type Receipt struct {
	Protocol        string `json:"protocol"`
	Kind            string `json:"kind"`
	State           string `json:"state"`
	SID             string `json:"sid"`
	Key             string `json:"key"`
	BrokerACK       bool   `json:"broker_ack"`
	RetryAllowed    bool   `json:"retry_allowed"`
	Duplicate       bool   `json:"duplicate"`
	CancelConfirmed bool   `json:"cancel_confirmed"`
	Reason          string `json:"reason"`
}
type QueryResult struct {
	Protocol      string            `json:"protocol"`
	AccountID     string            `json:"account_id"`
	SID           string            `json:"sid"`
	State         string            `json:"state"`
	BrokerACK     bool              `json:"broker_ack"`
	EvidenceReady bool              `json:"evidence_ready"`
	RetryAllowed  bool              `json:"retry_allowed"`
	CanOpenLive   bool              `json:"can_open_live"`
	StatusVersion string            `json:"status_version"`
	Reason        string            `json:"reason"`
	Order         json.RawMessage   `json:"order"`
	Executions    []json.RawMessage `json:"executions"`
	Transport     *Receipt          `json:"transport"`
}
type Health struct {
	Protocol              string   `json:"protocol"`
	Mode                  string   `json:"mode"`
	AccountID             string   `json:"account_id"`
	ReportsReady          bool     `json:"reports_ready"`
	Reasons               []string `json:"reasons"`
	FilePublished         int      `json:"file_published"`
	TransportUnknown      int      `json:"transport_unknown"`
	OrderUnknown          int      `json:"order_unknown"`
	TerminalLoginVerified bool     `json:"terminal_login_verified"`
	CanOpenLive           bool     `json:"can_open_live"`
}

// Submit returns ONLY transport evidence; caller must persist an intent before
// calling and query the original SID following ANY network/UNKNOWN outcome.
func (c *Client) Submit(ctx context.Context, value Order) (Receipt, error) {
	var result Receipt
	if !safeSID.MatchString(value.SID) {
		return result, errors.New("broker remote: invalid sid")
	}
	err := c.request(ctx, http.MethodPost, "/v1/broker/orders", map[string]any{"order": value}, &result)
	if err == nil && (result.BrokerACK || result.RetryAllowed || result.Protocol != Protocol || result.SID != value.SID || result.Key != value.SID || result.Kind != "order" || !receiptState(result.State)) {
		err = errors.New("broker remote: invalid transport receipt")
	}
	return result, err
}
func (c *Client) Query(ctx context.Context, sid string) (QueryResult, error) {
	var result QueryResult
	if !safeSID.MatchString(sid) {
		return result, errors.New("broker remote: invalid sid")
	}
	err := c.request(ctx, http.MethodGet, "/v1/broker/orders/"+url.PathEscape(sid), nil, &result)
	if err == nil && (result.SID != sid || result.AccountID != c.account || result.Protocol != Protocol || result.CanOpenLive || result.RetryAllowed || !queryState(result.State) || (result.State != "UNKNOWN" && (len(result.Order) == 0 || result.StatusVersion == ""))) {
		err = errors.New("broker remote: invalid query evidence")
	}
	return result, err
}
func (c *Client) Cancel(ctx context.Context, requestID, sid string) (Receipt, error) {
	var result Receipt
	if !safeSID.MatchString(sid) || !safeSID.MatchString(requestID) {
		return result, errors.New("broker remote: invalid identity")
	}
	err := c.request(ctx, http.MethodPost, "/v1/broker/cancels", map[string]string{"request_id": requestID, "sid": sid}, &result)
	if err == nil && (result.BrokerACK || result.CancelConfirmed || result.RetryAllowed || result.SID != sid || result.Key != requestID || result.Protocol != Protocol || result.Kind != "cancel" || !receiptState(result.State)) {
		err = errors.New("broker remote: invalid cancel receipt")
	}
	return result, err
}

func receiptState(state string) bool { return state == "FILE_PUBLISHED" || state == "UNKNOWN" }

func queryState(state string) bool {
	switch state {
	case "UNKNOWN", "PENDING", "ACKNOWLEDGED", "PARTIAL", "FILLED", "CANCELLED", "REJECTED", "EXPIRED":
		return true
	default:
		return false
	}
}
func (c *Client) Health(ctx context.Context) (Health, error) {
	var result Health
	err := c.request(ctx, http.MethodGet, "/v1/broker/health", nil, &result)
	if err == nil && (result.Protocol != Protocol || result.AccountID != c.account || result.Mode != "ISOLATED" || result.CanOpenLive || result.TerminalLoginVerified) {
		err = errors.New("broker remote: invalid isolated health")
	}
	return result, err
}
func (c *Client) Snapshot(ctx context.Context) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.request(ctx, http.MethodGet, "/v1/broker/snapshot", nil, &result)
	return result, err
}
func (c *Client) Reconcile(ctx context.Context, expected json.RawMessage) (json.RawMessage, error) {
	var result json.RawMessage
	err := c.request(ctx, http.MethodPost, "/v1/broker/reconcile", map[string]json.RawMessage{"expected": expected}, &result)
	return result, err
}

func (c *Client) request(ctx context.Context, method, path string, input, output any) error {
	var body []byte
	var err error
	if input != nil {
		body, err = json.Marshal(input)
		if err != nil {
			return errors.New("broker remote: invalid request")
		}
		if len(body) > 65536 {
			return errors.New("broker remote: request too large")
		}
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base+path, bytes.NewReader(body))
	if err != nil {
		return errors.New("broker remote: invalid endpoint")
	}
	req.Header.Set("X-NT-Protocol", Protocol)
	req.Header.Set("X-NT-Account", c.account)
	if input != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return errors.New("broker remote: transport unknown; query original sid, do not resubmit")
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(io.LimitReader(resp.Body, 16*1024*1024+1))
	if err != nil || len(data) > 16*1024*1024 {
		return errors.New("broker remote: incomplete or oversized response")
	}
	if resp.StatusCode != http.StatusOK {
		var failure struct {
			Error string `json:"error"`
		}
		_ = json.Unmarshal(data, &failure)
		if !safeError.MatchString(failure.Error) {
			failure.Error = "TRANSPORT_REJECTED"
		}
		return fmt.Errorf("broker remote: %s (HTTP %d)", failure.Error, resp.StatusCode)
	}
	var envelope struct {
		Protocol  string          `json:"protocol"`
		AccountID string          `json:"account_id"`
		Payload   json.RawMessage `json:"payload"`
	}
	if json.Unmarshal(data, &envelope) != nil || envelope.Protocol != Protocol || envelope.AccountID != c.account || len(envelope.Payload) == 0 || string(envelope.Payload) == "null" {
		return errors.New("broker remote: response identity mismatch")
	}
	if json.Unmarshal(envelope.Payload, output) != nil {
		return errors.New("broker remote: invalid response shape")
	}
	return nil
}
