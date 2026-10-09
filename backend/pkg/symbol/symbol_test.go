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
