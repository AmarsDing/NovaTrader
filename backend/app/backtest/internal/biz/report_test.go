package biz

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/go-kratos/kratos/v2/log"

	"server/pkg/backtest"
)

// drillFakeRepo 只覆盖 Admission 触到的方法；其余走嵌入 nil interface 的缺省实现。
type drillFakeRepo struct {
	Repo
	vals map[string]string
	err  error
}

func (r *drillFakeRepo) ConfigValues(context.Context, []string) (map[string]string, error) {
	return r.vals, r.err
}
func (r *drillFakeRepo) LatestSucceeded(context.Context, string, ...string) (*Task, error) {
	return nil, nil
}
func (r *drillFakeRepo) Equity(context.Context, string, time.Time, time.Time) ([]backtest.EquityPoint, error) {
	return nil, nil
}

func findAdmissionItem(items []AdmissionItem, key string) AdmissionItem {
	for _, it := range items {
		if it.Key == key {
			return it
		}
	}
	return AdmissionItem{Key: key, Status: "missing"}
}

// T15 三态之一：无演练记录 → 两项保持 Unknown，不冒充；ready 必为 false。
func TestAdmissionDrillNoRecordStaysUnknown(t *testing.T) {
	uc := NewUsecase(&drillFakeRepo{vals: map[string]string{}}, nil, nil, NewSettings(nil), nil, log.DefaultLogger)
	items, ready, err := uc.Admission(context.Background(), "breakout_ref")
	if err != nil {
		t.Fatal(err)
	}
	if ready {
		t.Fatal("无演练记录时 ready 必须为 false")
	}
	for _, key := range []string{"risk_drill", "broker"} {
		it := findAdmissionItem(items, key)
		if it.Status != Unknown {
			t.Fatalf("%s: 无回执必须 Unknown，实际 %s", key, it.Status)
		}
		if !strings.Contains(it.Detail, "尚无演练记录") {
			t.Fatalf("%s: 详情应如实说明无记录，实际 %q", key, it.Detail)
		}
		if strings.Contains(it.Detail, "result=") {
			t.Fatalf("%s: 无回执不得伪造演练结论，实际 %q", key, it.Detail)
		}
	}
}

// T15 三态之二：有回执 → 如实呈现 at/operator/result/evidence 摘要；
// result=pass→pass，result=fail→fail（fail 必须如实暴露而不是吞掉）。
func TestAdmissionDrillReceiptRendered(t *testing.T) {
	vals := map[string]string{
		"risk.drill.risk_drill.receipt": `{"at":"2026-10-21T15:00:00+08:00","operator":"ops.zhang","result":"pass","evidence_hash":"0123456789abcdefEXTRA"}`,
		"risk.drill.broker.receipt":     `{"at":"2026-10-21T16:00:00+08:00","operator":"ops.li","result":"fail","evidence_hash":"deadbeef"}`,
	}
	uc := NewUsecase(&drillFakeRepo{vals: vals}, nil, nil, NewSettings(nil), nil, log.DefaultLogger)
	items, ready, err := uc.Admission(context.Background(), "breakout_ref")
	if err != nil {
		t.Fatal(err)
	}
	drill := findAdmissionItem(items, "risk_drill")
	if drill.Status != Pass {
		t.Fatalf("pass 回执应如实呈现 pass，实际 %s（%q）", drill.Status, drill.Detail)
	}
	for _, want := range []string{"at=2026-10-21 15:00", "operator=ops.zhang", "result=pass", "evidence=0123456789ab…"} {
		if !strings.Contains(drill.Detail, want) {
			t.Fatalf("详情缺 %q，实际 %q", want, drill.Detail)
		}
	}
	if strings.Contains(drill.Detail, "0123456789abcdef") {
		t.Fatalf("evidence 只呈现摘要，不得整段贴出：%q", drill.Detail)
	}
	broker := findAdmissionItem(items, "broker")
	if broker.Status != Fail {
		t.Fatalf("fail 回执应如实呈现 fail，实际 %s（%q）", broker.Status, broker.Detail)
	}
	if ready {
		t.Fatal("存在 fail 回执时 ready 必须为 false")
	}
}

// T15 三态之三：回执损坏 → 显式缺失（Unknown + 说明），接口不炸、不冒充。
func TestAdmissionDrillBadReceiptExplicitUnknown(t *testing.T) {
	vals := map[string]string{
		"risk.drill.risk_drill.receipt": `{"at":`,
		// broker 键缺失 → 走无记录分支
	}
	uc := NewUsecase(&drillFakeRepo{vals: vals}, nil, nil, NewSettings(nil), nil, log.DefaultLogger)
	items, _, err := uc.Admission(context.Background(), "breakout_ref")
	if err != nil {
		t.Fatalf("坏回执不得让接口报错: %v", err)
	}
	drill := findAdmissionItem(items, "risk_drill")
	if drill.Status != Unknown || !strings.Contains(drill.Detail, "无法解析") {
		t.Fatalf("坏回执应显式 unknown 且说明，实际 %s（%q）", drill.Status, drill.Detail)
	}
	broker := findAdmissionItem(items, "broker")
	if broker.Status != Unknown || !strings.Contains(broker.Detail, "尚无演练记录") {
		t.Fatalf("缺键应按无记录呈现，实际 %s（%q）", broker.Status, broker.Detail)
	}
}
