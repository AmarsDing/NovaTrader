// Package dataset defines immutable point-in-time research inputs. It has no database or network dependency.
package dataset

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"strconv"
	"strings"
	"time"
)

const Version = 1

// Availability is upstream evidence, never a replacement with the collector's wall clock.
type Availability struct {
	AvailableAt *time.Time  `json:"available_at,omitempty"`
	Evidence    string      `json:"availability_evidence,omitempty"`
	ObservedAt  *time.Time  `json:"-"` // Actual collection clock, never publication evidence or stable identity.
	Provenance  *Provenance `json:"provenance,omitempty"`
}

// Provenance keeps provider bytes separate from Record.RawHash's normalized-payload hash.
type Provenance struct {
	Provider          string `json:"provider"`
	ProviderRawSHA256 string `json:"provider_raw_sha256"`
	ProviderRawKind   string `json:"provider_raw_kind"`
	RecordsSHA256     string `json:"records_sha256"`
	AcquisitionMode   string `json:"acquisition_mode"`
}

type Record struct {
	Domain       string          `json:"domain"`
	BusinessKey  string          `json:"business_key"`
	Source       string          `json:"source"`
	EventTime    time.Time       `json:"event_time"`
	AvailableAt  *time.Time      `json:"available_at"`
	Availability string          `json:"availability"`
	Evidence     string          `json:"availability_evidence"`
	ObservedAt   time.Time       `json:"observed_at"`
	RawHash      string          `json:"raw_hash"`
	RevisionHash string          `json:"revision_hash"`
	Payload      json.RawMessage `json:"payload"`
}

// NewRecord hashes the canonical normalized payload (not an unavailable provider HTTP body).
// ObservedAt is collection evidence only and is deliberately absent from revision identity.
func NewRecord(domain, key, source string, event, observed time.Time, a Availability, value any) (Record, error) {
	p, err := json.Marshal(value)
	if err != nil {
		return Record{}, err
	}
	p, err = canonical(p)
	if err != nil {
		return Record{}, err
	}
	r := Record{Domain: domain, BusinessKey: key, Source: source, EventTime: event.UTC().Truncate(time.Microsecond), ObservedAt: observed.UTC().Truncate(time.Microsecond), Payload: p, RawHash: hash(p), Availability: "unknown", Evidence: a.Evidence}
	if a.AvailableAt != nil {
		t := a.AvailableAt.UTC().Truncate(time.Microsecond)
		r.AvailableAt = &t
		r.Availability = "known"
	}
	if err := r.validate(); err != nil {
		return Record{}, err
	}
	r.RevisionHash, err = r.fingerprint()
	return r, err
}

func (r Record) validate() error {
	if r.Domain == "" || r.BusinessKey == "" || r.Source == "" || r.EventTime.IsZero() || r.ObservedAt.IsZero() {
		return fmt.Errorf("dataset: missing record identity or event/observed time")
	}
	if len(r.Domain) > 32 || len(r.BusinessKey) > 256 || len(r.Source) > 64 || len(r.Evidence) > 256 {
		return fmt.Errorf("dataset: record identity/evidence too long")
	}
	if (r.Availability == "known") != (r.AvailableAt != nil) || (r.Availability != "known" && r.Availability != "unknown") {
		return fmt.Errorf("dataset: availability/time mismatch")
	}
	if r.AvailableAt != nil && (r.AvailableAt.IsZero() || r.Evidence == "" || r.AvailableAt.After(r.ObservedAt)) {
		return fmt.Errorf("dataset: known availability requires non-future upstream evidence")
	}
	p, err := canonical(r.Payload)
	if err != nil {
		return err
	}
	if r.RawHash != hash(p) {
		return fmt.Errorf("dataset: normalized payload hash mismatch")
	}
	return nil
}

func (r Record) fingerprint() (string, error) {
	r.RevisionHash = ""
	r.ObservedAt = time.Time{}
	r.EventTime = r.EventTime.UTC().Truncate(time.Microsecond)
	if r.AvailableAt != nil {
		at := r.AvailableAt.UTC().Truncate(time.Microsecond)
		r.AvailableAt = &at
	}
	p, err := json.Marshal(r)
	if err != nil {
		return "", err
	}
	p, err = canonical(p)
	if err != nil {
		return "", err
	}
	return hash(p), nil
}

func (r Record) Verify() error {
	if err := r.validate(); err != nil {
		return err
	}
	h, err := r.fingerprint()
	if err != nil {
		return err
	}
	if h != r.RevisionHash {
		return fmt.Errorf("dataset: record revision hash mismatch")
	}
	return nil
}

type Exclusion struct {
	Domain       string `json:"domain"`
	BusinessKey  string `json:"business_key"`
	RevisionHash string `json:"revision_hash"`
	Reason       string `json:"reason"`
}

type Request struct {
	From   time.Time `json:"from"`
	To     time.Time `json:"to"`
	Cutoff time.Time `json:"cutoff"`
	Freq   string    `json:"freq"`
}

type Manifest struct {
	SchemaVersion int    `json:"schema_version"`
	ManifestID    string `json:"manifest_id"`
	Hash          string `json:"hash"`
	Request
	Calendar       []time.Time `json:"calendar"`
	Universe       []string    `json:"universe"`
	Records        []Record    `json:"records"`
	Excluded       []Exclusion `json:"excluded"`
	ReplayExcluded []Exclusion `json:"replay_excluded"`
	// Normalized payloads are hashed; HTTP raw bytes are not claimed to be retained.
	RawHashKind string `json:"raw_hash_kind"`
}

func Freeze(req Request, calendar []time.Time, input []Record) (*Manifest, error) {
	if req.From.IsZero() || req.To.Before(req.From) || req.Cutoff.IsZero() || (req.Freq != "1d" && req.Freq != "1m") {
		return nil, fmt.Errorf("dataset: invalid manifest range/cutoff/frequency")
	}
	req.From = req.From.UTC().Truncate(time.Microsecond)
	req.To = req.To.UTC().Truncate(time.Microsecond)
	req.Cutoff = req.Cutoff.UTC().Truncate(time.Microsecond)
	m := &Manifest{SchemaVersion: Version, Request: req, Calendar: []time.Time{}, Universe: []string{}, Records: []Record{}, Excluded: []Exclusion{}, RawHashKind: "canonical_normalized_payload_sha256"}
	seenDays := map[time.Time]bool{}
	for _, d := range calendar {
		d = d.UTC()
		if !seenDays[d] {
			m.Calendar = append(m.Calendar, d)
			seenDays[d] = true
		}
	}
	sort.Slice(m.Calendar, func(i, j int) bool { return m.Calendar[i].Before(m.Calendar[j]) })
	unique := map[string]Record{}
	universe := map[string]bool{}
	for _, r := range input {
		if err := r.Verify(); err != nil {
			return nil, err
		}
		if old, ok := unique[r.RevisionHash]; !ok || r.ObservedAt.Before(old.ObservedAt) {
			unique[r.RevisionHash] = r
		}
	}
	for _, r := range unique {
		reason := ""
		switch {
		case r.AvailableAt == nil:
			reason = "available_at_unknown"
		case r.ObservedAt.After(req.Cutoff):
			reason = "observed_after_cutoff"
		case r.AvailableAt.After(req.Cutoff):
			reason = "available_after_cutoff"
		case r.EventTime.After(req.To):
			reason = "event_after_range"
		}
		if reason != "" {
			m.Excluded = append(m.Excluded, Exclusion{r.Domain, r.BusinessKey, r.RevisionHash, reason})
			continue
		}
		m.Records = append(m.Records, r)
		if r.Domain == "security" {
			universe[r.BusinessKey] = true
		}
	}
	sort.Slice(m.Records, func(i, j int) bool { return m.Records[i].RevisionHash < m.Records[j].RevisionHash })
	sort.Slice(m.Excluded, func(i, j int) bool { return m.Excluded[i].RevisionHash < m.Excluded[j].RevisionHash })
	for sym := range universe {
		m.Universe = append(m.Universe, sym)
	}
	sort.Strings(m.Universe)
	h, err := m.fingerprint()
	if err != nil {
		return nil, err
	}
	m.Hash = h
	m.ManifestID = "sha256:" + h
	return m, nil
}

func (m Manifest) fingerprint() (string, error) {
	m.Hash = ""
	m.ManifestID = ""
	p, err := json.Marshal(m)
	if err != nil {
		return "", err
	}
	p, err = canonical(p)
	if err != nil {
		return "", err
	}
	return hash(p), nil
}

// WithReplayExclusions freezes computation-specific eligibility alongside the complete known vintages.
func (m *Manifest) WithReplayExclusions(excluded []Exclusion) error {
	m.ReplayExcluded = excluded
	sort.Slice(m.ReplayExcluded, func(i, j int) bool {
		if m.ReplayExcluded[i].RevisionHash != m.ReplayExcluded[j].RevisionHash {
			return m.ReplayExcluded[i].RevisionHash < m.ReplayExcluded[j].RevisionHash
		}
		return m.ReplayExcluded[i].Reason < m.ReplayExcluded[j].Reason
	})
	h, err := m.fingerprint()
	if err != nil {
		return err
	}
	m.Hash = h
	m.ManifestID = "sha256:" + h
	return nil
}

func (m Manifest) Verify() error {
	if m.SchemaVersion != Version || m.RawHashKind != "canonical_normalized_payload_sha256" {
		return fmt.Errorf("dataset: unsupported manifest contract")
	}
	if m.From.IsZero() || m.To.Before(m.From) || m.Cutoff.IsZero() || (m.Freq != "1d" && m.Freq != "1m") {
		return fmt.Errorf("dataset: invalid frozen request")
	}
	for _, r := range m.Records {
		if err := r.Verify(); err != nil {
			return err
		}
		if r.AvailableAt == nil || r.AvailableAt.After(m.Cutoff) || r.ObservedAt.After(m.Cutoff) || r.EventTime.After(m.To) {
			return fmt.Errorf("dataset: manifest contains unavailable record")
		}
	}
	h, err := m.fingerprint()
	if err != nil {
		return err
	}
	if m.Hash != h || m.ManifestID != "sha256:"+h {
		return fmt.Errorf("dataset: manifest hash mismatch")
	}
	return nil
}

// At returns the newest *available* vintage per domain/business key. A later revision never rewrites an earlier decision.
// Same-time conflicts are ordered by revision hash, rather than collector arrival order.
func (m Manifest) At(at time.Time) []Record {
	byKey := map[string]Record{}
	for _, r := range m.Records {
		if r.AvailableAt == nil || r.AvailableAt.After(at) {
			continue
		}
		k := r.Domain + "\x00" + r.BusinessKey
		old, ok := byKey[k]
		if !ok || old.AvailableAt.Before(*r.AvailableAt) || (old.AvailableAt.Equal(*r.AvailableAt) && old.RevisionHash < r.RevisionHash) {
			byKey[k] = r
		}
	}
	out := make([]Record, 0, len(byKey))
	for _, r := range byKey {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Domain != out[j].Domain {
			return out[i].Domain < out[j].Domain
		}
		return out[i].BusinessKey < out[j].BusinessKey
	})
	return out
}

func hash(p []byte) string { h := sha256.Sum256(p); return hex.EncodeToString(h[:]) }
func canonical(p []byte) ([]byte, error) {
	if len(p) > 16*1024*1024 {
		return nil, fmt.Errorf("dataset: canonical object exceeds 16MiB safety bound; use smaller immutable shards")
	}
	d := json.NewDecoder(bytes.NewReader(p))
	d.UseNumber()
	var v any
	if err := d.Decode(&v); err != nil {
		return nil, err
	}
	var rest any
	if err := d.Decode(&rest); err != io.EOF {
		return nil, fmt.Errorf("dataset: multiple JSON values")
	}
	v, err := normalizeNumbers(v)
	if err != nil {
		return nil, err
	}
	return json.Marshal(v)
}

// JSONB expands exponent notation and trailing zeros. Normalize exactly, without float64 precision loss.
func normalizeNumbers(v any) (any, error) {
	switch x := v.(type) {
	case json.Number:
		s := string(x)
		neg := ""
		if strings.HasPrefix(s, "-") {
			neg = "-"
			s = s[1:]
		}
		exponent := 0
		if i := strings.IndexAny(s, "eE"); i >= 0 {
			n, e := strconv.Atoi(s[i+1:])
			if e != nil || n > 10000 || n < -10000 {
				return nil, fmt.Errorf("dataset: numeric exponent outside safety bound")
			}
			exponent = n
			s = s[:i]
		}
		if i := strings.IndexByte(s, '.'); i >= 0 {
			exponent -= len(s) - i - 1
			s = s[:i] + s[i+1:]
		}
		s = strings.TrimLeft(s, "0")
		if s == "" {
			return json.Number("0"), nil
		}
		for strings.HasSuffix(s, "0") {
			s = s[:len(s)-1]
			exponent++
		}
		if exponent >= 0 {
			s += strings.Repeat("0", exponent)
		} else {
			point := len(s) + exponent
			if point > 0 {
				s = s[:point] + "." + s[point:]
			} else {
				s = "0." + strings.Repeat("0", -point) + s
			}
		}
		return json.Number(neg + s), nil
	case []any:
		for i, a := range x {
			n, e := normalizeNumbers(a)
			if e != nil {
				return nil, e
			}
			x[i] = n
		}
		return x, nil
	case map[string]any:
		for k, a := range x {
			n, e := normalizeNumbers(a)
			if e != nil {
				return nil, e
			}
			x[k] = n
		}
		return x, nil
	default:
		return v, nil
	}
}
