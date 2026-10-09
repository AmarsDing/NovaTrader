package biz

import "server/pkg/tradecal"

// Quiet 为真时，只有 critical 可以发出。放行时段含盘前，晨报在 08:30 就能到。
func Quiet(s tradecal.Session) bool {
	switch s.Phase {
	case tradecal.PreOpen, tradecal.CallAuction, tradecal.PreMatch, tradecal.AMTrading, tradecal.PMTrading:
		return false
	default:
		return true
	}
}
