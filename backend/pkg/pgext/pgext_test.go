package pgext

import (
	"math"
	"reflect"
	"testing"
)

func TestVectorRoundTrip(t *testing.T) {
	v := Vector{1, -0.5, 0.25}
	raw, err := v.Value()
	if err != nil {
		t.Fatal(err)
	}
	if raw != "[1,-0.5,0.25]" {
		t.Fatalf("Value = %v", raw)
	}
	var back Vector
	if err := back.Scan([]byte("[1, -0.5,0.25]")); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(back, v) {
		t.Fatalf("Scan = %v", back)
	}
	if err := back.Scan(nil); err != nil || back != nil {
		t.Fatalf("Scan(nil) = %v, %v", back, err)
	}
	if _, err := (Vector{float32(math.NaN())}).Value(); err == nil {
		t.Fatal("NaN must be rejected")
	}
	if err := back.Scan("1,2"); err == nil {
		t.Fatal("bad literal must be rejected")
	}
}

func TestFormatTSVector(t *testing.T) {
	got := FormatTSVector([]Lexeme{{Word: "弱转", Positions: []int{1, 3}}, {Word: "it's", Positions: []int{20000}}, {Word: `a\b`}})
	want := TSVector(`'弱转':1,3 'it''s':16383 'a\\b'`)
	if got != want {
		t.Fatalf("FormatTSVector = %s", got)
	}
	if q := FormatTSQueryOr([]string{"回踩", "", "ma5"}); q != `'回踩' | 'ma5'` {
		t.Fatalf("FormatTSQueryOr = %s", q)
	}
}
