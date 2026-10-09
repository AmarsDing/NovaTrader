package server

import (
	"errors"
	"net/http"
	"time"

	"server/app/trade/internal/biz"
	"server/app/trade/internal/service"

	kerrors "github.com/go-kratos/kratos/v2/errors"
	khttp "github.com/go-kratos/kratos/v2/transport/http"
)

// mountDesk 补上桌面交易页要的三条路由：委托列表、成交列表、实盘确认。
// 生成代码里还没有这几个方法，路径和查询参数按客户端现有调用固定。
func mountDesk(srv *khttp.Server, svc *service.TradeService) {
	r := srv.Route("/")
	r.GET("/v1/trade/orders", func(ctx khttp.Context) error {
		rows, err := svc.Orders(ctx, ctx.Query().Get("account"))
		if err != nil {
			return tradeErr(err)
		}
		out := make([]orderView, 0, len(rows))
		for _, o := range rows {
			out = append(out, orderViewOf(o))
		}
		return ctx.Result(http.StatusOK, map[string]any{"orders": out})
	})
	r.GET("/v1/trade/fills", func(ctx khttp.Context) error {
		rows, err := svc.Fills(ctx, ctx.Query().Get("account"))
		if err != nil {
			return tradeErr(err)
		}
		out := make([]fillView, 0, len(rows))
		for _, f := range rows {
			out = append(out, fillViewOf(f))
		}
		return ctx.Result(http.StatusOK, map[string]any{"fills": out})
	})
	r.POST("/v1/trade/orders/{client_order_id}/confirm", func(ctx khttp.Context) error {
		o, err := svc.Confirm(ctx, ctx.Vars().Get("client_order_id"))
		if err != nil {
			return tradeErr(err)
		}
		return ctx.Result(http.StatusOK, orderViewOf(o))
	})
}

func tradeErr(err error) error {
	if errors.Is(err, biz.ErrInvalid) {
		return kerrors.BadRequest("TRADE_INVALID", err.Error())
	}
	if errors.Is(err, biz.ErrNotFound) {
		return kerrors.NotFound("TRADE_NOT_FOUND", err.Error())
	}
	return err
}

type orderView struct {
	ClientOrderID string  `json:"client_order_id"`
	Account       string  `json:"account"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Status        string  `json:"status"`
	Price         float64 `json:"price"`
	Volume        int     `json:"volume"`
	Filled        int     `json:"filled"`
	Reason        string  `json:"reason"`
}

type fillView struct {
	ClientOrderID string  `json:"client_order_id"`
	Symbol        string  `json:"symbol"`
	Side          string  `json:"side"`
	Qty           int     `json:"qty"`
	Price         float64 `json:"price"`
	Amount        float64 `json:"amount"`
	Time          string  `json:"time"`
}

func orderViewOf(o biz.Order) orderView {
	account := "SIM"
	if o.Book == biz.BookLive {
		account = "LIVE"
	}
	return orderView{
		ClientOrderID: o.ClientOrderID, Account: account, Symbol: o.Symbol, Side: o.Side,
		Status: o.Status, Price: o.Price, Volume: o.Volume, Filled: o.Filled, Reason: o.Reason,
	}
}

func fillViewOf(f biz.Fill) fillView {
	at := ""
	if !f.CreatedAt.IsZero() {
		at = f.CreatedAt.Format(time.RFC3339)
	}
	return fillView{
		ClientOrderID: f.ClientOrderID, Symbol: f.Symbol, Side: f.Side,
		Qty: f.Qty, Price: f.Price, Amount: f.Amount, Time: at,
	}
}
