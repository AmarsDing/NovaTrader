package params

import "testing"

func TestStrictCatalogueAndRanges(t *testing.T) {
	if _, err := ValidatePatch(nil, map[string]string{"market.sentiment.rules": "{} {}"}); err == nil {
		t.Fatal("trailing JSON accepted")
	}
	bad := []map[string]string{{"m06.unknown": "1"}, {"m06.rule_top_n": "2.5"}, {"m06.select_score": "NaN"}, {"m06.select_score": "101"}, {"app.brain.large.url": "http://user:secret@localhost"}, {"market.sentiment.rules": `{"injected":1}`}, {"market.sector.weights": `{"min_members":0}`}, {"risk.max_single_pct": "2"}, {"risk.live_admitted": "true"}}
	for _, v := range bad {
		if _, e := ValidatePatch(nil, v); e == nil {
			t.Errorf("accepted invalid patch keys %v", v)
		}
	}
	valid := map[string]string{"app.brain.large.key": "secret", "m06.rule_top_n": "20", "risk.max_single_pct": "0.05", "market.sentiment.rules": `{"hot_min_up":80}`}
	if _, err := ValidatePatch(nil, valid); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePatch(valid, map[string]string{"app.brain.large.key": ""}); err != nil {
		t.Fatal(err)
	}
	if _, err := ValidatePatch(map[string]string{"m06.fb.gap_min": "0.01"}, map[string]string{"m06.fb.gap_max": "0"}); err == nil {
		t.Fatal("inverted range accepted")
	}
}
