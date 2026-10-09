package risk

import "time"

// CheckMark 是一次校验的结果，用来算拒单率。
type CheckMark struct {
	At       time.Time
	Approved bool
}

// RejectAlert 判断窗口内拒单率是否该告警。样本不足、未达比例、或还在冷却期内都不告警。
// min ≤ 0 或 rate ≤ 0 表示关闭。
func RejectAlert(marks []CheckMark, now time.Time, p Params, last time.Time) (n int, rate float64, fire bool) {
	if p.RejectAlertMin <= 0 || p.RejectAlertRate <= 0 || p.RejectAlertWindow <= 0 {
		return 0, 0, false
	}
	cutoff := now.Add(-p.RejectAlertWindow)
	rejected := 0
	for _, m := range marks {
		if m.At.Before(cutoff) {
			continue
		}
		n++
		if !m.Approved {
			rejected++
		}
	}
	if n == 0 {
		return 0, 0, false
	}
	rate = float64(rejected) / float64(n)
	if n < p.RejectAlertMin || rate < p.RejectAlertRate {
		return n, rate, false
	}
	cool := p.RejectAlertCooldown
	if !last.IsZero() && now.Sub(last) < cool {
		return n, rate, false
	}
	return n, rate, true
}
