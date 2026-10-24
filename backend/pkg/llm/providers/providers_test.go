package providers

import (
	"strings"
	"testing"
	"time"
)

// TestRegistryShape 钉死登记册质量门：必填域齐、日期合法、id 唯一（重复由 mustLoad 直接 panic，
// 这条只盯内容完整性）。任何一项破了就是往「坑散逸回记忆/注释」的开倒车。
func TestRegistryShape(t *testing.T) {
	all := All()
	if len(all) == 0 {
		t.Fatal("registry empty")
	}
	for _, p := range all {
		if p.ID == "" || p.Name == "" || p.BaseURL == "" {
			t.Fatalf("provider missing required fields: %+v", p)
		}
		if p.Verified() {
			if _, err := time.Parse("2006-01-02", p.VerifiedAt); err != nil {
				t.Fatalf("%s verified_at not a date: %q", p.ID, p.VerifiedAt)
			}
			if p.VerifiedScope == "" {
				t.Fatalf("%s verified but scope empty", p.ID)
			}
		}
		for _, k := range p.KnownIncompatibilities {
			if k == "" {
				t.Fatalf("%s has empty incompatibility entry", p.ID)
			}
		}
	}
}

// TestKnownPitsRegistered 钉死两条「曾咬过人」的坑必须留在登记册里，
// 防止模板建好后坑又被简化掉。
func TestKnownPitsRegistered(t *testing.T) {
	d, ok := Lookup("deepseek")
	if !ok || len(d.KnownIncompatibilities) == 0 {
		t.Fatal("deepseek pits missing")
	}
	joined := ""
	for _, k := range d.KnownIncompatibilities {
		joined += k
	}
	if !strings.Contains(joined, "disable_json_mode") {
		t.Fatal("deepseek reasoner disable_json_mode pit lost")
	}
	q, ok := Lookup("qiniu")
	if !ok || q.Verified() {
		t.Fatal("qiniu must stay unverified")
	}
	if ws := Warns("https://api.qiniu.com/v1"); len(ws) < 2 || ws[0] == "" {
		t.Fatal("qiniu warns missing", ws)
	}
	if ws := Warns("https://totally-unknown.example.com/v1"); len(ws) != 1 {
		t.Fatal("unknown provider should yield exactly one guidance warn", ws)
	}
}

func TestMatchURLTrims(t *testing.T) {
	if _, ok := MatchURL("https://api.deepseek.com/v1/"); !ok {
		t.Fatal("trailing slash not normalized")
	}
	if _, ok := MatchURL("HTTPS://API.DEEPSEEK.COM/v1"); !ok {
		t.Fatal("case not normalized")
	}
	if _, ok := MatchURL(" http://127.0.0.1:8000/v1 "); !ok {
		t.Fatal("whitespace not normalized")
	}
	if _, ok := MatchURL(""); ok {
		t.Fatal("empty url matched")
	}
}
