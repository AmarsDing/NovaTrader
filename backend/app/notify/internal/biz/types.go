package biz

import (
	"errors"
	"time"
)

const (
	PriCritical = "critical"
	PriHigh     = "high"
	PriMedium   = "medium"

	StatusPending = "pending"
	StatusSent    = "sent"
	StatusPartial = "partial"
	StatusFailed  = "failed"
	StatusHeld    = "held"
	StatusMerged  = "merged"

	ChannelSent    = "sent"
	ChannelSkipped = "skipped"
	ChannelFailed  = "failed"
)

// ErrSkipped 表示这条渠道没有配置，不算失败。
var ErrSkipped = errors.New("notify: channel skipped")

// ErrNotFound 表示记录不存在。
var ErrNotFound = errors.New("notify: not found")

// Message 是一条待投递或已投递的通知。
type Message struct {
	ID              int
	EventID         string
	TraceID         string
	Source          string
	Subject         string
	Category        string
	Priority        string
	Title           string
	Body            string
	Fields          map[string]string
	DedupKey        string
	MergeCount      int
	MergedInto      int
	Status          string
	Rendered        string
	DesktopStatus   string
	DesktopAttempts int
	DesktopError    string
	FeishuStatus    string
	FeishuAttempts  int
	FeishuError     string
	CreatedAt       time.Time
	SentAt          time.Time
}

// DesktopNote 是发给 admin 的桌面载荷。没有确认动作。
type DesktopNote struct {
	ID       int       `json:"id"`
	EventID  string    `json:"event_id"`
	Priority string    `json:"priority"`
	Category string    `json:"category"`
	Title    string    `json:"title"`
	Body     string    `json:"body"`
	At       time.Time `json:"at"`
	TraceID  string    `json:"-"`
}

// Config 是运行参数。零值用默认。
type Config struct {
	MergeWindow time.Duration
	// Windows 按 category 覆盖合并窗口（T07③ market.alert 5 分钟桶，对齐引擎侧
	// 同票同种 5 分钟去重）；缺省全部走 MergeWindow。
	Windows   map[string]time.Duration
	RetryMax  int
	RetryBase time.Duration
	Templates map[string]string
}

func (c Config) window() time.Duration {
	if c.MergeWindow <= 0 {
		return time.Minute
	}
	return c.MergeWindow
}

func (c Config) windowFor(category string) time.Duration {
	if c.Windows != nil {
		if d, ok := c.Windows[category]; ok && d > 0 {
			return d
		}
	}
	return c.window()
}

func (c Config) retries() int {
	if c.RetryMax < 1 {
		return 3
	}
	return c.RetryMax
}

func (c Config) base() time.Duration {
	if c.RetryBase <= 0 {
		return time.Second
	}
	return c.RetryBase
}
