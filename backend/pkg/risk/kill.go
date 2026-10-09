package risk

import (
	"errors"
	"strings"
	"time"
)

// ResetConfirm 是恢复 Kill Switch 时必须输入的确认字样。
const ResetConfirm = "CONFIRM"

// Kill Switch 来源。
const (
	KillFromClient    = "client"
	KillFromReconcile = "reconcile"
	KillFromTerminal  = "terminal"
	KillFromMarket    = "market"
)

// KillState 是 Kill Switch 状态。触发后模式降到 L0；恢复后模式仍是 L0，由人另行选择。
type KillState struct {
	Active   bool      `json:"active"`
	Source   string    `json:"source,omitempty"`
	Reason   string    `json:"reason,omitempty"`
	Operator string    `json:"operator,omitempty"`
	At       time.Time `json:"at"`
}

// Trigger 返回触发后的状态。已经触发时 changed 为 false，保留第一次的来源。
func (k KillState) Trigger(source, reason, operator string, at time.Time) (next KillState, changed bool) {
	if k.Active {
		return k, false
	}
	return KillState{Active: true, Source: source, Reason: reason, Operator: operator, At: at}, true
}

// Reset 恢复 Kill Switch。必须有操作人和确认字样。
func (k KillState) Reset(operator, confirm, reason string, at time.Time) (KillState, error) {
	if !k.Active {
		return k, errors.New("risk: kill switch is not active")
	}
	if strings.TrimSpace(operator) == "" {
		return k, errors.New("risk: operator is required")
	}
	if confirm != ResetConfirm {
		return k, errors.New("risk: confirm text must be " + ResetConfirm)
	}
	return KillState{Active: false, Source: KillFromClient, Reason: reason, Operator: operator, At: at}, nil
}

// ModeAllowed 检查能否切到目标模式：Kill Switch 触发时只能 L0，L2 以上要求实盘准入。
func ModeAllowed(target Mode, kill KillState, p Params) error {
	if kill.Active && target != L0 {
		return errors.New("risk: kill switch is active, reset it first")
	}
	if target >= L2 && !p.LiveAdmitted {
		return errors.New("risk: live admission has not passed")
	}
	return nil
}
