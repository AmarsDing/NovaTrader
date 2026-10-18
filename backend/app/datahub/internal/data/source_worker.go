package data

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"server/app/datahub/internal/biz"
	"server/pkg/dataset"
	"server/pkg/symbol"
	"server/pkg/tradecal"
)

const sourceWorkerResponseMax = 8 << 20

type sourceWorkerClient struct {
	name, base string
	http       *http.Client
	slots      chan struct{}
	now        func() time.Time
}

func newSourceWorkerClient(name, base string) (*sourceWorkerClient, error) {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, fmt.Errorf("source-worker requires an explicit loopback HTTP port without credentials, path or query")
	}
	port, err := strconv.Atoi(u.Port())
	if err != nil || port < 1024 || port > 65535 {
		return nil, fmt.Errorf("invalid source-worker port")
	}
	return &sourceWorkerClient{name: name, base: strings.TrimRight(base, "/"), slots: make(chan struct{}, 2), now: time.Now,
		http: &http.Client{Timeout: 25 * time.Second, Transport: &http.Transport{MaxIdleConnsPerHost: 2}, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}}, nil
}

type sourceWorkerReply struct {
	Version       int             `json:"schema_version"`
	Status        string          `json:"status"`
	Domain        string          `json:"domain"`
	Provider      string          `json:"provider"`
	RawHash       string          `json:"provider_raw_sha256"`
	RawKind       string          `json:"provider_raw_kind"`
	Mode          string          `json:"acquisition_mode"`
	RecordsHash   string          `json:"records_sha256"`
	Count         int             `json:"record_count"`
	Records       json.RawMessage `json:"records"`
	ObservedAt    time.Time       `json:"observed_at"`
	AvailableAt   *time.Time      `json:"available_at"`
	TrainingReady bool            `json:"training_ready"`
	Coverage      json.RawMessage `json:"coverage"`
	Metadata      json.RawMessage `json:"metadata"`
	Reason        string          `json:"reason,omitempty"`
}

func sourceHash(body []byte) string { sum := sha256.Sum256(body); return hex.EncodeToString(sum[:]) }
func validSourceHash(hash string) bool {
	b, err := hex.DecodeString(hash)
	return err == nil && len(b) == 32 && strings.ToLower(hash) == hash
}

func (c *sourceWorkerClient) Name() string        { return c.name }
func (*sourceWorkerClient) CoverageUnknown() bool { return true }
func (c *sourceWorkerClient) call(ctx context.Context, request any, domain, provider, rawKind string) (*sourceWorkerReply, error) {
	ctx, cancel := context.WithTimeout(ctx, 25*time.Second)
	defer cancel()
	select {
	case c.slots <- struct{}{}:
		defer func() { <-c.slots }()
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	body, err := json.Marshal(request)
	if err != nil || len(body) > 4096 {
		return nil, fmt.Errorf("source-worker request outside contract")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.base+"/v1/source/collect", bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.http.Do(req)
	if err != nil {
		return nil, biz.Unavailable(c.name, "source-worker connection or timeout failure")
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return nil, biz.Unavailable(c.name, "source-worker status %d; provider result unavailable", res.StatusCode)
	}
	raw, err := io.ReadAll(io.LimitReader(res.Body, sourceWorkerResponseMax+1))
	if err != nil || len(raw) > sourceWorkerResponseMax {
		return nil, biz.Unavailable(c.name, "source-worker unreadable or oversized response")
	}
	var reply sourceWorkerReply
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	if err := d.Decode(&reply); err != nil {
		return nil, fmt.Errorf("source-worker invalid response contract")
	}
	var rest any
	if d.Decode(&rest) != io.EOF {
		return nil, fmt.Errorf("source-worker trailing response data")
	}
	if reply.Version != 1 || reply.Status != "succeeded" || reply.Domain != domain || reply.Provider != provider || reply.RawKind != rawKind || reply.AvailableAt != nil || reply.TrainingReady || reply.Count < 1 || reply.Count > 10000 || !validSourceHash(reply.RawHash) || !validSourceHash(reply.RecordsHash) || sourceHash(reply.Records) != reply.RecordsHash || (reply.Mode != "recorded_provider_bytes" && reply.Mode != "provider_fetch") {
		return nil, fmt.Errorf("source-worker identity/hash/availability mismatch")
	}
	if age := c.now().Sub(reply.ObservedAt); reply.ObservedAt.IsZero() || age < -5*time.Second || age > time.Minute {
		return nil, fmt.Errorf("source-worker observation clock unavailable or outside freshness window")
	}
	return &reply, nil
}

func (r sourceWorkerReply) availability() dataset.Availability {
	return dataset.Availability{ObservedAt: &r.ObservedAt, Evidence: "provider response does not prove historical publication time",
		Provenance: &dataset.Provenance{Provider: r.Provider, ProviderRawSHA256: r.RawHash, ProviderRawKind: r.RawKind, RecordsSHA256: r.RecordsHash, AcquisitionMode: r.Mode}}
}

type TdxArchiveSource struct{ *sourceWorkerClient }

func NewTdxArchiveSource(base string) (*TdxArchiveSource, error) {
	c, e := newSourceWorkerClient(biz.SourceTdxArchive, base)
	return &TdxArchiveSource{c}, e
}

type sourceWorkerBar struct {
	Symbol                         string    `json:"symbol"`
	Name                           string    `json:"name"`
	Time                           time.Time `json:"time"`
	Freq                           string    `json:"freq"`
	Open, High, Low, Close, Amount float64
	PreClose                       *float64   `json:"pre_close"`
	Volume                         int64      `json:"volume"`
	AvailableAt                    *time.Time `json:"available_at"`
	Availability                   string     `json:"availability"`
	Evidence                       string     `json:"availability_evidence"`
}

func (s *TdxArchiveSource) DailyBars(ctx context.Context, q biz.BarQuery) (biz.BarBatch, error) {
	if q.Freq != "1d" || !day(q.Start).Equal(day(q.End)) || q.Start.IsZero() {
		return biz.BarBatch{}, biz.ErrUnsupported
	}
	d := q.Start.In(tradecal.Shanghai())
	current := s.now().In(tradecal.Shanghai())
	if day(d).After(day(current)) {
		return biz.BarBatch{}, fmt.Errorf("source archive date is in the future")
	}
	if day(d).Equal(day(current)) {
		session, err := tradecal.Default.SessionAt(current)
		if err != nil || (session.Phase != tradecal.PostClose && session.Phase != tradecal.Closed) || current.Before(day(current).Add(15*time.Hour)) {
			return biz.BarBatch{}, biz.Unavailable(s.name, "daily archive is after-close data only")
		}
	}
	reply, err := s.call(ctx, map[string]any{"schema_version": 1, "operation": "tdx_history", "date": d.Format("2006-01-02")}, biz.DomainDailyBar, "tdx.com.cn", "provider_http_zip_bytes")
	if err != nil {
		return biz.BarBatch{}, err
	}
	var rows []sourceWorkerBar
	if err := json.Unmarshal(reply.Records, &rows); err != nil || len(rows) != reply.Count {
		return biz.BarBatch{}, fmt.Errorf("archive row count/shape mismatch")
	}
	seen, markets := map[string]bool{}, map[string]int{}
	requested := map[string]bool{}
	for _, sym := range q.Symbols {
		requested[sym] = true
	}
	out := biz.BarBatch{CoverageUnknown: true, LastDate: d.Format("2006-01-02"), Expect: d.Format("2006-01-02")}
	for _, row := range rows {
		parts := strings.Split(row.Symbol, ".")
		parsed, parseErr := symbol.Parse(row.Symbol)
		validMarket := len(parts) == 2 && ((parts[1] == "SH" && (strings.HasPrefix(parts[0], "60") || strings.HasPrefix(parts[0], "68"))) || (parts[1] == "SZ" && (strings.HasPrefix(parts[0], "00") || strings.HasPrefix(parts[0], "30"))) || (parts[1] == "BJ" && (strings.HasPrefix(parts[0], "4") || strings.HasPrefix(parts[0], "8") || strings.HasPrefix(parts[0], "92"))))
		finite := true
		for _, n := range []float64{row.Open, row.High, row.Low, row.Close, row.Amount} {
			finite = finite && !math.IsNaN(n) && !math.IsInf(n, 0)
		}
		if parseErr != nil || parsed.Tongdaxin() != row.Symbol || !validMarket || seen[row.Symbol] || strings.TrimSpace(row.Name) == "" || len(row.Name) > 128 || !row.Time.Equal(day(d)) || row.Freq != "1d" || row.AvailableAt != nil || row.Availability != "unknown" || !finite || math.Min(row.Open, row.Low) <= 0 || row.Close <= 0 || row.High < math.Max(row.Open, row.Close) || row.Low > math.Min(row.Open, row.Close) || row.Volume < 0 || row.Amount < 0 {
			return biz.BarBatch{}, fmt.Errorf("archive invalid equity/date/OHLC/availability record")
		}
		seen[row.Symbol] = true
		markets[strings.ToLower(parts[1])]++
		if len(requested) > 0 && !requested[row.Symbol] {
			continue
		}
		out.Bars = append(out.Bars, biz.Bar{Availability: reply.availability(), Symbol: row.Symbol, Time: row.Time, Freq: "1d", Open: row.Open, High: row.High, Low: row.Low, Close: row.Close, PreClose: row.PreClose, Volume: row.Volume, Amount: row.Amount})
	}
	var coverage struct {
		Ratio   *float64 `json:"coverage_ratio"`
		Markets map[string]struct {
			Status string `json:"status"`
			Count  *int   `json:"a_stock_count"`
		} `json:"markets"`
	}
	if err := json.Unmarshal(reply.Coverage, &coverage); err != nil || coverage.Ratio != nil || len(coverage.Markets) != 3 {
		return biz.BarBatch{}, fmt.Errorf("archive coverage metadata missing or false known ratio")
	}
	for _, m := range []string{"sh", "sz", "bj"} {
		proof, ok := coverage.Markets[m]
		if !ok {
			return biz.BarBatch{}, fmt.Errorf("archive market coverage missing")
		}
		if m == "bj" && d.Format("20060102") < "20220506" && proof.Status == "unsupported_before_2022_05_06" && proof.Count == nil {
			continue
		}
		if proof.Status != "observed" || proof.Count == nil || *proof.Count != markets[m] || markets[m] == 0 {
			return biz.BarBatch{}, fmt.Errorf("archive incomplete market coverage")
		}
	}
	if len(out.Bars) == 0 {
		return biz.BarBatch{}, biz.Unavailable(s.name, "no requested priced rows")
	}
	return out, nil
}

type OpenBBFredSource struct{ *sourceWorkerClient }

func NewOpenBBFredSource(base string) (*OpenBBFredSource, error) {
	c, e := newSourceWorkerClient(biz.SourceOpenBBFred, base)
	return &OpenBBFredSource{c}, e
}
func (s *OpenBBFredSource) Macro(ctx context.Context) ([]biz.MacroPoint, error) {
	var out []biz.MacroPoint
	for _, code := range []string{"CPIAUCSL", "FEDFUNDS", "UNRATE"} {
		hi := s.now().UTC()
		lo := hi.AddDate(-1, 0, 0)
		r, e := s.call(ctx, map[string]any{"schema_version": 1, "operation": "openbb_macro", "series": code, "start": lo.Format("2006-01-02"), "end": hi.Format("2006-01-02")}, biz.DomainMacro, "fred.stlouisfed.org", "sdk_normalized_result_json")
		if e != nil {
			return nil, e
		}
		var rows []struct {
			Code         string     `json:"code"`
			Date         string     `json:"date"`
			Value        float64    `json:"value"`
			Unit         *string    `json:"unit"`
			AvailableAt  *time.Time `json:"available_at"`
			Availability string     `json:"availability"`
		}
		if json.Unmarshal(r.Records, &rows) != nil || len(rows) != r.Count {
			return nil, fmt.Errorf("macro row count/shape mismatch")
		}
		seen := map[string]bool{}
		for _, row := range rows {
			d, e := time.Parse("2006-01-02", row.Date)
			if e != nil || row.Code != code || row.Date < lo.Format("2006-01-02") || row.Date > hi.Format("2006-01-02") || seen[row.Date] || row.AvailableAt != nil || row.Availability != "unknown" || math.IsNaN(row.Value) || math.IsInf(row.Value, 0) {
				return nil, fmt.Errorf("macro identity/date/value mismatch")
			}
			seen[row.Date] = true
			unit := ""
			if row.Unit != nil {
				unit = *row.Unit
			}
			if len(unit) > 64 {
				return nil, fmt.Errorf("macro unit outside bound")
			}
			out = append(out, biz.MacroPoint{Availability: r.availability(), Code: code, Name: code, Date: d, Value: row.Value, Unit: unit})
		}
	}
	return out, nil
}
