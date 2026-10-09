package redact

import "testing"

func TestMapAndLine(t *testing.T) {
	got := Map(map[string]any{"token": "abc", "symbol": "600519.SH"})
	if got["token"] != "***" || got["symbol"] != "600519.SH" {
		t.Fatal(got)
	}
	line := Line("login token=super-secret symbol=600519")
	if line != "login token=*** symbol=600519" {
		t.Fatal(line)
	}
}
