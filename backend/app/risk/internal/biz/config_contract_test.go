package biz

import (
	"context"
	"testing"
)

func TestExpectedConfigRevisionChecksActualLoadedRules(t *testing.T) {
	r := newRig(t, nil)
	r.account(1000000, 1000000)
	expected := 1
	o := buy("wrong-config", 100)
	o.ExpectedConfigRevision = &expected
	d, _ := r.e.Check(context.Background(), o)
	if d.Approved || d.Rule != "R00" || d.Reason != "风控尚未加载计划绑定的配置版本" {
		t.Fatalf("mismatched configuration approved: %+v", d)
	}
	expected = 0
	o = buy("same-config", 100)
	o.ExpectedConfigRevision = &expected
	d, _ = r.e.Check(context.Background(), o)
	if !d.Approved {
		t.Fatalf("matching config rejected: %+v", d)
	}
}
