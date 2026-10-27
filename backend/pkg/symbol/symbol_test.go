package symbol

import "testing"

func TestParseRoundTrip(t *testing.T) {
	cases := []struct {
		in   string
		tdx  string
		east string
	}{
		{"600519.SH", "600519.SH", "SHSE.600519"},
		{"sh600519", "600519.SH", "SHSE.600519"},
		{"SHSE.600519", "600519.SH", "SHSE.600519"},
		{"000001.SZ", "000001.SZ", "SZSE.000001"},
		{"sz000001", "000001.SZ", "SZSE.000001"},
		{"830001.BJ", "830001.BJ", "BJSE.830001"},
	}
	for _, tc := range cases {
		got, err := Parse(tc.in)
		if err != nil {
			t.Fatalf("Parse(%q): %v", tc.in, err)
		}
		if got.Tongdaxin() != tc.tdx || got.EastMoney() != tc.east {
			t.Fatalf("Parse(%q) = %s / %s, want %s / %s", tc.in, got.Tongdaxin(), got.EastMoney(), tc.tdx, tc.east)
		}
	}
}

func TestParseRejects(t *testing.T) {
	for _, in := range []string{"", "600519", "600519.HK", "SHSE.60051", "ABC.SH"} {
		if _, err := Parse(in); err == nil {
			t.Fatalf("Parse(%q) should fail", in)
		}
	}
}

func TestFromAShareCode(t *testing.T) {
	for in, want := range map[string]string{
		"600519": "600519.SH", "688001": "688001.SH", "000001": "000001.SZ", "300750": "300750.SZ",
		"830799": "830799.BJ", "430047": "430047.BJ", "920001": "920001.BJ",
	} {
		s, err := FromAShareCode(in)
		if err != nil || s.Tongdaxin() != want {
			t.Fatalf("FromAShareCode(%q) = %v, %v; want %s", in, s, err, want)
		}
	}
	for _, in := range []string{"", "12345", "200001", "900901", "ABCDEF"} {
		if _, err := FromAShareCode(in); err == nil {
			t.Fatalf("FromAShareCode(%q) should fail", in)
		}
	}
}

func TestParseAShare(t *testing.T) {
	// 用户请求边界放宽：裸 6 位 A 股代码段可推断市场（GP/QB 助记符直达）。
	// Parse 本身保持严格继续拒裸码（TestParseRejects 锁定），放宽只发生在 ParseAShare。
	for in, want := range map[string]string{
		"600519": "600519.SH", "688001": "688001.SH", "000001": "000001.SZ", "300750": "300750.SZ",
		"830799": "830799.BJ", "430047": "430047.BJ", "920001": "920001.BJ",
	} {
		s, err := ParseAShare(in)
		if err != nil || s.Tongdaxin() != want {
			t.Fatalf("ParseAShare(%q) = %v, %v; want %s", in, s, err, want)
		}
	}
	// 三种标准写法照旧走 Parse。
	if s, err := ParseAShare("sh600519"); err != nil || s.Tongdaxin() != "600519.SH" {
		t.Fatalf("ParseAShare(sh600519) = %v, %v; want 600519.SH", s, err)
	}
	// 指数/基金代码段（1/2/9 开头）与错形态仍报错。
	for _, in := range []string{"", "12345", "200001", "900901", "600519.HK", "ABCDEF"} {
		if _, err := ParseAShare(in); err == nil {
			t.Fatalf("ParseAShare(%q) should fail", in)
		}
	}
}
