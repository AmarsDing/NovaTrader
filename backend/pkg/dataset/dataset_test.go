package dataset

import (
	"encoding/json"
	"os"
	"reflect"
	"testing"
	"time"
)

func instant(s string) time.Time {
	t, e := time.Parse(time.RFC3339, s)
	if e != nil {
		panic(e)
	}
	return t
}
func revision(t *testing.T, key string, at *time.Time, payload any) Record {
	t.Helper()
	r, e := NewRecord("finance", key, "fixture", instant("2025-12-31T00:00:00Z"), instant("2026-05-03T00:00:00Z"), Availability{AvailableAt: at, Evidence: "fixture publication timestamp"}, payload)
	if e != nil {
		t.Fatal(e)
	}
	return r
}

func TestPublicationNotReportPeriodAndRevisionPIT(t *testing.T) {
	a, b := instant("2026-04-01T07:00:00Z"), instant("2026-05-01T07:00:00Z")
	r1, r2 := revision(t, "600001.SH/2025", &a, map[string]any{"profit": 10}), revision(t, "600001.SH/2025", &b, map[string]any{"profit": 8})
	unknown := revision(t, "600002.SH/2025", nil, map[string]any{"profit": 99})
	m, e := Freeze(Request{From: instant("2025-01-01T00:00:00Z"), To: instant("2026-06-01T00:00:00Z"), Cutoff: instant("2026-05-03T00:00:00Z"), Freq: "1d"}, nil, []Record{r2, unknown, r1})
	if e != nil {
		t.Fatal(e)
	}
	if got := m.At(instant("2026-03-31T23:59:59Z")); len(got) != 0 {
		t.Fatalf("report period leaked: %+v", got)
	}
	got := m.At(instant("2026-04-02T00:00:00Z"))
	if len(got) != 1 || got[0].RevisionHash != r1.RevisionHash {
		t.Fatal("later correction changed earlier vintage")
	}
	got = m.At(instant("2026-05-02T00:00:00Z"))
	if len(got) != 1 || got[0].RevisionHash != r2.RevisionHash {
		t.Fatal("correction not visible after publication")
	}
	if len(m.Excluded) != 1 || m.Excluded[0].Reason != "available_at_unknown" {
		t.Fatalf("missing publication silently accepted: %+v", m.Excluded)
	}
}

func TestManifestDeterministicOrderAndTamper(t *testing.T) {
	a := instant("2026-04-01T07:00:00Z")
	r1, r2 := revision(t, "a", &a, map[string]any{"b": 2, "a": 1}), revision(t, "b", &a, map[string]any{"v": 3})
	req := Request{From: instant("2025-01-01T00:00:00Z"), To: instant("2026-06-01T00:00:00Z"), Cutoff: instant("2026-05-03T00:00:00Z"), Freq: "1d"}
	m1, e := Freeze(req, []time.Time{a, a}, []Record{r1, r2})
	if e != nil {
		t.Fatal(e)
	}
	m2, e := Freeze(req, []time.Time{a}, []Record{r2, r1, r1})
	if e != nil {
		t.Fatal(e)
	}
	if m1.Hash != m2.Hash || !reflect.DeepEqual(m1, m2) {
		t.Fatal("input order/duplicates changed frozen dataset")
	}
	lateObservation := r1
	lateObservation.ObservedAt = lateObservation.ObservedAt.Add(time.Hour)
	m3, e := Freeze(req, []time.Time{a}, []Record{lateObservation, r2, r1})
	if e != nil {
		t.Fatal(e)
	}
	if m3.Hash != m1.Hash {
		t.Fatal("duplicate arrival order changed first-observation provenance")
	}
	p, _ := json.Marshal(m1)
	var copy Manifest
	if e = json.Unmarshal(p, &copy); e != nil {
		t.Fatal(e)
	}
	if e = copy.Verify(); e != nil {
		t.Fatal(e)
	}
	copy.Records[0].Payload = json.RawMessage(`{"tampered":true}`)
	if copy.Verify() == nil {
		t.Fatal("tampered payload accepted")
	}
}

func TestCanonicalJSONBAndMicrosecondRoundTrip(t *testing.T) {
	a := instant("2026-04-01T07:00:00Z").Add(123456789)
	r, e := NewRecord("macro", "CPI/2026-03", "fixture", a.Add(-time.Hour), a.Add(time.Hour), Availability{AvailableAt: &a, Evidence: "upstream release"}, map[string]any{"long_name": 1, "v": 2})
	if e != nil {
		t.Fatal(e)
	}
	// PostgreSQL JSONB may return different whitespace/key order and timestamps have microsecond resolution.
	r.Payload = json.RawMessage(`{ "v": 2, "long_name": 1 }`)
	if e = r.Verify(); e != nil {
		t.Fatal(e)
	}
	if r.ObservedAt.Nanosecond()%1000 != 0 || r.AvailableAt.Nanosecond()%1000 != 0 {
		t.Fatal("timestamp precision not normalized")
	}
}

func TestCanonicalNumericDoesNotLoseLargeIntegers(t *testing.T) {
	a, e := canonical([]byte(`{"small":1e-7,"large":123456789012345678901234567890,"x":1e21}`))
	if e != nil {
		t.Fatal(e)
	}
	b, e := canonical([]byte(`{"x":1000000000000000000000.00,"large":123456789012345678901234567890.0,"small":0.000000100}`))
	if e != nil {
		t.Fatal(e)
	}
	if string(a) != string(b) {
		t.Fatalf("JSONB numeric drift: %s != %s", a, b)
	}
	c, e := canonical([]byte(`{"large":123456789012345678901234567891}`))
	if e != nil {
		t.Fatal(e)
	}
	d, e := canonical([]byte(`{"large":123456789012345678901234567890}`))
	if e != nil {
		t.Fatal(e)
	}
	if string(c) == string(d) {
		t.Fatal("large integer precision lost")
	}
}

func TestKnownRequiresEvidenceAndNoCollectionSubstitution(t *testing.T) {
	a := instant("2026-04-01T00:00:00Z")
	if _, e := NewRecord("macro", "CPI", "fixture", a, a, Availability{AvailableAt: &a}, 1); e == nil {
		t.Fatal("known publication without evidence accepted")
	}
	r, e := NewRecord("macro", "CPI", "fixture", a, a, Availability{}, 1)
	if e != nil {
		t.Fatal(e)
	}
	if r.AvailableAt != nil || r.Availability != "unknown" {
		t.Fatal("collector time substituted for publication")
	}
}

func TestExportFixtureVerifies(t *testing.T) {
	p, e := os.ReadFile("testdata/manifest_v1.json")
	if e != nil {
		t.Fatal(e)
	}
	var m Manifest
	if e = json.Unmarshal(p, &m); e != nil {
		t.Fatal(e)
	}
	if e = m.Verify(); e != nil {
		t.Fatal(e)
	}
}
