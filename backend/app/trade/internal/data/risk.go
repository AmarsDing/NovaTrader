package data

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	riskv1 "server/api/risk/v1"
	"server/app/trade/internal/biz"
	"server/conf"

	"google.golang.org/protobuf/encoding/protojson"
)

// RiskClient 调用 risk 的 CheckOrder。超时或失败由引擎当成拒绝。
type RiskClient struct {
	addr string
	http *http.Client
}

func NewRiskClient(c *conf.Trade) *RiskClient {
	addr := ""
	if c != nil {
		addr = strings.TrimRight(c.GetRiskAddr(), "/")
	}
	return &RiskClient{addr: addr, http: &http.Client{Timeout: 200 * time.Millisecond}}
}

func (c *RiskClient) Check(ctx context.Context, in biz.CheckIn) (biz.CheckOut, error) {
	if c.addr == "" {
		return biz.CheckOut{}, fmt.Errorf("risk addr is empty")
	}
	body, err := protojson.Marshal(&riskv1.CheckOrderRequest{
		ClientOrderId: in.ClientOrderID,
		AccountType:   in.Account,
		Symbol:        in.Symbol,
		Side:          in.Side,
		Price:         in.Price,
		Volume:        int32(in.Volume),
		Source:        in.Source,
		Operator:      in.Operator,
	})
	if err != nil {
		return biz.CheckOut{}, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.addr+"/risk/v1/check", bytes.NewReader(body))
	if err != nil {
		return biz.CheckOut{}, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return biz.CheckOut{}, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return biz.CheckOut{}, err
	}
	if resp.StatusCode >= 300 {
		return biz.CheckOut{}, fmt.Errorf("risk http %d", resp.StatusCode)
	}
	var out riskv1.CheckOrderReply
	if err := protojson.Unmarshal(raw, &out); err != nil {
		return biz.CheckOut{}, err
	}
	return biz.CheckOut{Approved: out.GetApproved(), Volume: int(out.GetVolume()), Reason: out.GetReason()}, nil
}
