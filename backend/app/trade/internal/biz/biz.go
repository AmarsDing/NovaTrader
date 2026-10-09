// Package biz 是 trade 的订单状态机和模拟账本。撮合公式在 pkg/brokerif/sim，手数和费用在 pkg/ashare。
package biz

import (
	"context"
	"errors"
	"time"

	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(NewEngine)

var (
	ErrInvalid  = errors.New("invalid")
	ErrNotFound = errors.New("not found")
)

const (
	StatusRejected  = "rejected"
	StatusPending   = "pending_confirm" // 实盘风控通过后，等客户端确认
	StatusSubmitted = "submitted"
	StatusPartial   = "partial"
	StatusFilled    = "filled"
	StatusCancelled = "cancelled"
)

// ConfirmWindow 是实盘委托等待确认的时间，和桌面确认窗一致。
const ConfirmWindow = 30 * time.Second

const (
	BookPaper  = "paper"
	BookLive   = "live"
	ChannelSim = "sim"
)

// Checker 是风控端口。返回错误时按拒绝处理，不提交委托。
type Checker interface {
	Check(ctx context.Context, in CheckIn) (CheckOut, error)
}

type CheckIn struct {
	ClientOrderID string
	Account       string
	Symbol        string
	Side          string
	Price         float64
	Volume        int
	Source        string
	Operator      string
}

type CheckOut struct {
	Approved bool
	Volume   int
	Reason   string
}

// Store 持久化委托、成交、资金和持仓。Commit 必须在一个事务里完成。
type Store interface {
	LoadOrder(ctx context.Context, clientID string) (Order, bool, error)
	LoadCash(ctx context.Context, book string) (Cash, bool, error)
	LoadPosition(ctx context.Context, book, symbol string) (Position, bool, error)
	ListPositions(ctx context.Context, book string) ([]Position, error)
	ListOpen(ctx context.Context, book, symbol string) ([]Order, error)
	ListOrders(ctx context.Context, book string, limit int) ([]Order, error)
	ListFills(ctx context.Context, book string, limit int) ([]Fill, error)
	ListByStatus(ctx context.Context, status string) ([]Order, error)
	Commit(ctx context.Context, batch Batch) error
	SaveSnapshot(ctx context.Context, book string, view AccountView) error
}

// Batch 是一次状态变化要落库的内容。
type Batch struct {
	Orders    []Order
	Fills     []Fill
	Cash      *Cash
	Positions []Position
	Trails    []Trail
	Events    []Event
}

type Event struct {
	Subject string
	Payload any
}
