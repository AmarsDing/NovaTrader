package biz

import (
	"context"
	"time"
)

// Store 是通知记录。Insert 在 event_id 已存在时返回已有行且 dup 为真。
type Store interface {
	Insert(ctx context.Context, msg Message) (Message, bool, error)
	Recent(ctx context.Context, key string, since time.Time, excludeID int) (*Message, error)
	MarkMerged(ctx context.Context, id, anchorID int, latest Message) error
	MarkHeld(ctx context.Context, id int) error
	SaveDelivery(ctx context.Context, msg Message) error
	Get(ctx context.Context, id int) (Message, error)
	List(ctx context.Context, limit int) ([]Message, error)
	ListStatus(ctx context.Context, status string) ([]Message, error)
}

// Desktop 把通知推到 admin。
type Desktop interface {
	Push(ctx context.Context, note DesktopNote) error
}

// Feishu 发出一条文本。Webhook 为空时返回 ErrSkipped。
type Feishu interface {
	Send(ctx context.Context, text string) error
}
