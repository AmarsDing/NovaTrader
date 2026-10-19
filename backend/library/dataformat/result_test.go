package dataformat

import (
	"strings"
	"testing"
)

func TestOKValue(t *testing.T) {
	r := OK(3.14)
	if !r.IsOK() {
		t.Fatal("OK must be ok")
	}
	v, ok := r.Value()
	if !ok || v != 3.14 {
		t.Fatalf("Value=%v,%v", v, ok)
	}
	if r.Why() != "" || r.Note() != "" {
		t.Fatalf("OK must not carry why/note: %q %q", r.Why(), r.Note())
	}
}

func TestMissingZeroValueSafe(t *testing.T) {
	// 零值不赋值也是缺失，不会伪装成 0=有值。
	var r Result[float64]
	if r.IsOK() {
		t.Fatal("zero Result must be missing")
	}
	if _, ok := r.Value(); ok {
		t.Fatal("zero Result Value must be not-ok")
	}
	if r.Why() != "" && r.Why() != WhyUnknown {
		t.Fatalf("unexpected why %q", r.Why())
	}
	r = Missing[float64](WhySourceDown, "HTTP 429")
	if r.IsOK() {
		t.Fatal("Missing must not be ok")
	}
	if r.Why() != WhySourceDown || r.Note() != "HTTP 429" {
		t.Fatalf("why/note lost: %q %q", r.Why(), r.Note())
	}
}

func TestMissingEmptyWhyFallsToUnknown(t *testing.T) {
	r := Missing[int]("", "no candidates")
	if r.Why() != WhyUnknown {
		t.Fatalf("empty why must fall to unknown, got %q", r.Why())
	}
}

func TestMapPropagatesMissing(t *testing.T) {
	m := Missing[float64](WhyStale, "10s 前")
	out := Map(m, func(v float64) float64 {
		t.Fatal("f must not run on missing")
		return v
	})
	if out.IsOK() || out.Why() != WhyStale || out.Note() != "10s 前" {
		t.Fatalf("missing not propagated: %v", out)
	}
	r := Map(OK(2.0), func(v float64) float64 { return v * 3 })
	if v, ok := r.Value(); !ok || v != 6.0 {
		t.Fatalf("Map ok: %v", r)
	}
}

func TestFlatMap(t *testing.T) {
	f := func(v int) Result[string] {
		if v > 0 {
			return OK("pos")
		}
		return Missing[string](WhyEmpty, "non-positive")
	}
	if r := FlatMap(OK(1), f); !r.IsOK() || r.value != "pos" {
		t.Fatalf("FlatMap ok: %v", r)
	}
	if r := FlatMap(OK(-1), f); r.IsOK() || r.Why() != WhyEmpty {
		t.Fatalf("FlatMap inner missing: %v", r)
	}
	if r := FlatMap(Missing[int](WhyNotRun, "skip"), f); r.IsOK() || r.Why() != WhyNotRun {
		t.Fatalf("FlatMap outer missing: %v", r)
	}
}

func TestFirst(t *testing.T) {
	r := First(Missing[int](WhySourceDown, "a"), OK(7), Missing[int](WhyEmpty, "b"))
	if v, ok := r.Value(); !ok || v != 7 {
		t.Fatalf("First picks ok: %v", r)
	}
	r = First(Missing[int](WhyNotRun, "x"), Missing[int](WhyEmpty, "y"))
	if r.IsOK() || r.Why() != WhyEmpty || r.Note() != "y" {
		t.Fatalf("First all-missing keeps last: %v", r)
	}
	if r := First[int](); r.IsOK() || r.Why() != WhyUnknown {
		t.Fatalf("First empty: %v", r)
	}
}

func TestValueOrAndString(t *testing.T) {
	if v := Missing[float64](WhyStale, "").ValueOr(-1); v != -1 {
		t.Fatalf("ValueOr missing: %v", v)
	}
	if v := OK(9.0).ValueOr(-1); v != 9.0 {
		t.Fatalf("ValueOr ok: %v", v)
	}
	s := Missing[float64](WhySourceDown, "HTTP 429").String()
	if !strings.Contains(s, "missing(source_down)") || !strings.Contains(s, "HTTP 429") {
		t.Fatalf("String: %q", s)
	}
	if s := OK(1).String(); !strings.HasPrefix(s, "ok(") {
		t.Fatalf("String ok: %q", s)
	}
}
