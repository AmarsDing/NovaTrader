package alert

import (
	"errors"
	"testing"
)

func TestDecodeEmptyIsEmptyList(t *testing.T) {
	for _, blob := range [][]byte{nil, {}, []byte("   \n\t ")} {
		rules, err := Decode(blob)
		if err != nil || rules != nil {
			t.Fatalf("empty blob %q must decode to an empty list without error, got %v, %v", blob, rules, err)
		}
	}
}

func TestDecodeCorruptReportsExplicitly(t *testing.T) {
	cases := map[string][]byte{
		"bad json":       []byte("{not json"),
		"non array":      []byte(`{"id":"a"}`),
		"missing fields": []byte(`[{"id":"a-1"}]`),
		"bad condition":  []byte(`[{"id":"a-1","symbol":"600519.SH","condition":"sideways","target_price":10,"status":"active"}]`),
		"bad status":     []byte(`[{"id":"a-1","symbol":"600519.SH","condition":"above","target_price":10,"status":"paused"}]`),
		"bad target":     []byte(`[{"id":"a-1","symbol":"600519.SH","condition":"above","target_price":-1,"status":"active"}]`),
		"element broken": []byte(`[{"id":"a-1","symbol":"600519.SH","condition":"above","target_price":10,"status":"active"}, 1]`),
	}
	for name, blob := range cases {
		rules, err := Decode(blob)
		var corrupt *CorruptError
		if !errors.As(err, &corrupt) {
			t.Errorf("%s: corrupt store must surface a CorruptError, got rules=%v err=%v", name, rules, err)
		}
		if rules != nil {
			t.Errorf("%s: nothing may be salvaged silently, got %v", name, rules)
		}
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	r := mustCreate(t, Crosses, 100)
	r, _ = Step(r, 99, t0)
	r.Note = "整点关口"
	blob, err := Encode([]Rule{r})
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	rules, err := Decode(blob)
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(rules) != 1 {
		t.Fatalf("round trip lost rules: %v", rules)
	}
	got := rules[0]
	if got.ID != r.ID || got.Symbol != r.Symbol || got.Condition != r.Condition ||
		got.TargetPrice != r.TargetPrice || got.Status != r.Status || got.Note != r.Note ||
		!got.CreatedAt.Equal(r.CreatedAt) || !got.ArmedAt.Equal(r.ArmedAt) ||
		got.LastPrice == nil || *got.LastPrice != 99 || got.TriggeredAt != nil {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	// 触发态也随清单持久化：重启后不会把同一次触发再报一遍。
	r2, fired := Step(r, 101, t0)
	if !fired {
		t.Fatal("cross up should fire before encoding")
	}
	blob, _ = Encode([]Rule{r2})
	rules, _ = Decode(blob)
	if rules[0].Status != StatusTriggered || rules[0].TriggeredAt == nil {
		t.Fatalf("triggered state must persist, got %+v", rules[0])
	}
	if _, again := Step(rules[0], 102, t0); again {
		t.Fatal("a restored triggered rule must not refire")
	}
}
