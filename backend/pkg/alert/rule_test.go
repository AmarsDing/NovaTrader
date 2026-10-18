package alert

import (
	"strings"
	"testing"
	"time"
)

var t0 = time.Date(2026, 10, 19, 9, 30, 0, 0, time.UTC)

func mustCreate(t *testing.T, cond Condition, target float64) Rule {
	t.Helper()
	r, err := Create("a-1", "600519.sh", cond, target, t0)
	if err != nil {
		t.Fatalf("create: %v", err)
	}
	return r
}

func TestCreateValidates(t *testing.T) {
	if _, err := Create("", "600519.SH", Above, 100, t0); err == nil {
		t.Fatal("missing id should fail")
	}
	if _, err := Create("a", "  ", Above, 100, t0); err == nil {
		t.Fatal("missing symbol should fail")
	}
	if _, err := Create("a", "600519.SH", Above, 0, t0); err == nil {
		t.Fatal("non-positive target should fail")
	}
	r := mustCreate(t, Above, 100)
	if r.Symbol != "600519.SH" || r.Status != StatusActive || !r.ArmedAt.Equal(t0) || r.LastPrice != nil {
		t.Fatalf("unexpected fresh rule: %+v", r)
	}
}

func TestParseCondition(t *testing.T) {
	for raw, want := range map[string]Condition{"Above": Above, " below ": Below, "CROSSES": Crosses} {
		got, err := ParseCondition(raw)
		if err != nil || got != want {
			t.Fatalf("ParseCondition(%q) = %v, %v; want %v", raw, got, err, want)
		}
	}
	if _, err := ParseCondition("sideways"); err == nil {
		t.Fatal("unknown condition should fail")
	}
}

func TestStepAboveBelow(t *testing.T) {
	// 严格比较：贴上目标价不触发。
	up := mustCreate(t, Above, 100)
	if _, fired := Step(up, 100, t0); fired {
		t.Fatal("above: touching the target must not fire")
	}
	next, fired := Step(up, 100.01, t0)
	if !fired || next.Status != StatusTriggered || next.TriggeredAt == nil {
		t.Fatalf("above: should fire over target, got fired=%v rule=%+v", fired, next)
	}
	down := mustCreate(t, Below, 100)
	if _, fired = Step(down, 100, t0); fired {
		t.Fatal("below: touching the target must not fire")
	}
	if _, fired = Step(down, 99.99, t0); !fired {
		t.Fatal("below: should fire under target")
	}
}

func TestStepNoDoubleFire(t *testing.T) {
	r := mustCreate(t, Above, 100)
	r, fired := Step(r, 101, t0)
	if !fired {
		t.Fatal("first evaluation should fire")
	}
	// 防重复：同一只价（以及此后任何价）在编辑/重挂前不再触发。
	if _, again := Step(r, 101, t0.Add(time.Second)); again {
		t.Fatal("triggered rule must not fire again")
	}
	if _, again := Step(r, 200, t0.Add(2*time.Second)); again {
		t.Fatal("triggered rule must not fire on new highs either")
	}
}

func TestStepNonPositivePriceIgnored(t *testing.T) {
	r := mustCreate(t, Below, 100)
	next, fired := Step(r, 0, t0)
	if fired || next.LastPrice != nil {
		t.Fatal("zero/negative quotes are not market data and must not move the baseline")
	}
}

func TestStepCrosses(t *testing.T) {
	r := mustCreate(t, Crosses, 100)
	// 第一笔没有基线，只建档不触发。
	r, fired := Step(r, 99, t0)
	if fired || r.LastPrice == nil || *r.LastPrice != 99 {
		t.Fatalf("first tick sets the baseline, got fired=%v rule=%+v", fired, r)
	}
	// 向上穿越触发。
	r, fired = Step(r, 100.5, t0.Add(time.Second))
	if !fired {
		t.Fatal("crossing up through the target should fire")
	}
	// 触发后停评；重挂给全新基线。
	rearmed, err := Rearm(r, t0.Add(2*time.Second))
	if err != nil {
		t.Fatalf("rearm: %v", err)
	}
	rearmed, fired = Step(rearmed, 100.4, t0.Add(3*time.Second))
	if fired {
		t.Fatal("re-armed rule must establish a fresh baseline before judging")
	}
	// 向下穿越同样触发。
	if _, fired = Step(rearmed, 99.8, t0.Add(4*time.Second)); !fired {
		t.Fatal("crossing down through the target should fire")
	}
}

func TestStepCrossesTouchingIsNoFire(t *testing.T) {
	r := mustCreate(t, Crosses, 100)
	r, _ = Step(r, 99, t0)
	r, fired := Step(r, 100, t0.Add(time.Second))
	if fired {
		t.Fatal("touching the target exactly is not a cross")
	}
	// 基线停在目标价上时，两侧都算 wasOrAt，但 isAbove/isBelow 仍要求严格离开。
	if _, fired = Step(r, 100, t0.Add(2*time.Second)); fired {
		t.Fatal("sitting on the target is not a cross")
	}
}

func TestEditWhitelistRebuild(t *testing.T) {
	r := mustCreate(t, Crosses, 100)
	r, _ = Step(r, 100.5, t0) // 触发 + 携带 LastPrice/TriggeredAt
	edited, err := Edit(r, "000001.SZ", Below, 88, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Status != StatusActive || edited.LastPrice != nil || edited.TriggeredAt != nil {
		t.Fatalf("edit must clear every trigger-lifecycle field, got %+v", edited)
	}
	if edited.ArmedAt != t0.Add(time.Minute) || !edited.CreatedAt.Equal(t0) {
		t.Fatal("edit re-arms but keeps the original creation time")
	}
	if edited.ID != r.ID || edited.Symbol != "000001.SZ" || edited.Condition != Below || edited.TargetPrice != 88 {
		t.Fatalf("whitelist fields wrong: %+v", edited)
	}
}

func TestEditKeepsNote(t *testing.T) {
	r := mustCreate(t, Above, 100)
	r.Note = "放量突破再提醒"
	edited, err := Edit(r, r.Symbol, Above, 120, t0.Add(time.Minute))
	if err != nil {
		t.Fatalf("edit: %v", err)
	}
	if edited.Note != "放量突破再提醒" {
		t.Fatal("note is not trigger state and must survive edits")
	}
}

func TestDescription(t *testing.T) {
	for cond, want := range map[Condition]string{Above: ">", Below: "<", Crosses: "↕"} {
		r := mustCreate(t, cond, 1700)
		if got := Description(r); !strings.Contains(got, want) || !strings.Contains(got, "600519.SH") {
			t.Fatalf("Description(%s) = %q", cond, got)
		}
	}
}
