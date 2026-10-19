package dataset

import (
	"testing"
	"time"
)

func TestProviderProvenanceDoesNotPromotePublicationOrClockIdentity(t *testing.T) {
	event := time.Date(2025, 1, 6, 0, 0, 0, 0, time.UTC)
	observed := time.Date(2026, 10, 19, 1, 0, 0, 0, time.UTC)
	proof := &Provenance{Provider: "tdx.com.cn", ProviderRawSHA256: "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", ProviderRawKind: "provider_http_zip_bytes", RecordsSHA256: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", AcquisitionMode: "recorded_provider_bytes"}
	a := Availability{ObservedAt: &observed, Provenance: proof, Evidence: "historical publication unavailable"}
	payload := Bar{Provenance: proof, Symbol: "600001.SH", Time: event, Freq: "1d", Open: 10, High: 11, Low: 9, Close: 10, Volume: 1000, Amount: 10000}
	first, err := NewRecord("bar", "fixture", "tdx_http_archive", event, observed, a, payload)
	if err != nil {
		t.Fatal(err)
	}
	later := observed.Add(time.Minute)
	a.ObservedAt = &later
	repeat, err := NewRecord("bar", "fixture", "tdx_http_archive", event, later, a, payload)
	if err != nil {
		t.Fatal(err)
	}
	if first.RevisionHash != repeat.RevisionHash || first.AvailableAt != nil || first.Availability != "unknown" || first.RawHash == proof.ProviderRawSHA256 {
		t.Fatal("observation clock or provider bytes changed PIT publication/hash semantics")
	}
	if err := first.Verify(); err != nil {
		t.Fatal(err)
	}
	changed := *proof
	changed.ProviderRawSHA256 = "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc"
	payload.Provenance = &changed
	corrected, err := NewRecord("bar", "fixture", "tdx_http_archive", event, later, a, payload)
	if err != nil {
		t.Fatal(err)
	}
	if corrected.RevisionHash == first.RevisionHash {
		t.Fatal("new provider byte evidence lost its distinct revision")
	}
}
