package tradecal

import "time"

// Phase 是交易时段。非交易日全天 CLOSED。
type Phase string

const (
	Closed      Phase = "CLOSED"
	PreOpen     Phase = "PRE_OPEN"
	CallAuction Phase = "CALL_AUCTION"
	PreMatch    Phase = "PRE_MATCH"
	AMTrading   Phase = "AM_TRADING"
	NoonBreak   Phase = "NOON_BREAK"
	PMTrading   Phase = "PM_TRADING"
	PostClose   Phase = "POST_CLOSE"
)

// Session 是某一时刻的时段。Late 只在 PM_TRADING 且已到 14:30 时为真。
type Session struct {
	Phase   Phase
	Late    bool
	Trading bool
}

// SessionAt 用上海时间判断时段。调用方应使用返回值，而不是自己比较钟点。
func (c *Calendar) SessionAt(t time.Time) (Session, error) {
	open, err := c.Open(t)
	if err != nil {
		return Session{}, err
	}
	if !open {
		return Session{Phase: Closed}, nil
	}
	d := t.In(location())
	min := d.Hour()*60 + d.Minute()
	switch {
	case min < 7*60+30:
		return Session{Phase: Closed}, nil
	case min < 9*60+15:
		return Session{Phase: PreOpen}, nil
	case min < 9*60+25:
		return Session{Phase: CallAuction}, nil
	case min < 9*60+30:
		return Session{Phase: PreMatch}, nil
	case min < 11*60+30:
		return Session{Phase: AMTrading, Trading: true}, nil
	case min < 13*60:
		return Session{Phase: NoonBreak}, nil
	case min < 15*60:
		return Session{Phase: PMTrading, Late: min >= 14*60+30, Trading: true}, nil
	case min < 15*60+30:
		return Session{Phase: PostClose}, nil
	default:
		return Session{Phase: Closed}, nil
	}
}

// Changed 表示时段或尾盘标记发生了变化，应当发送 sys.session.changed。
func Changed(a, b Session) bool {
	return a.Phase != b.Phase || a.Late != b.Late
}
