package biz

import (
	"context"
	"testing"
	"time"

	"server/pkg/tradecal"

	"github.com/go-kratos/kratos/v2/log"
)

// signals_verify_test.go：M13-T08 source=signal 信号级对账。partial（缺行情）路径必测。
// 交易日历取 2026-09 普通周：checkDay=2026-09-11(周五)，D1=09-10、D3=09-08、D5=09-04。

var signalCheckDay = time.Date(2026, 9, 11, 0, 0, 0, 0, tradecal.Shanghai())

func day(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, tradecal.Shanghai()) }

func bar(d time.Time, lo, hi, close float64) Bar {
	return Bar{Time: d, Open: close, High: hi, Low: lo, Close: close, Volume: 1e6, Amount: 1e7, AdjFactor: 1}
}

func newSignalEnv(signals []SignalRow, bars []Bar) (*Usecase, *fakeReviews) {
	rv := &fakeReviews{signals: signals}
	mk := &fakeMarket{bars: bars}
	uc := NewUsecase(NewSettings(nil), nil, mk, nil, nil, rv, stubLlmCalls{}, nil, nil, log.DefaultLogger)
	return uc, rv
}

func TestVerifySignalsHappyAndStopFirst(t *testing.T) {
	d1 := day(2026, 9, 10)
	signals := []SignalRow{
		// 止盈先触发：low≥stop、high≥tp、触及入场带 → Hit=true，pct=+5
		{ID: 1, StockCode: "600519.SH", StockName: "贵州茅台", Strategy: "first_board", TradeDate: &d1,
			EntryPrice: 10, StopLoss: 9.5, TakeProfit: 10.6, Status: "done"},
		// 同根 bar 止损止盈双触发 → 按止损优先，Hit=false，pct 仍计
		{ID: 2, StockCode: "600519.SH", StockName: "贵州茅台", Strategy: "first_board", TradeDate: &d1,
			EntryPrice: 10, StopLoss: 9.9, TakeProfit: 10.6, Status: "closed"},
	}
	bars := []Bar{bar(day(2026, 9, 11), 9.85, 10.70, 10.50)}
	uc, rv := newSignalEnv(signals, bars)

	c, err := uc.VerifySignals(context.Background(), signalCheckDay)
	if err != nil {
		t.Fatal(err)
	}
	if c.Source != "signal" || c.Partial || len(c.Missing) != 0 {
		t.Fatalf("check = %+v", c)
	}
	if c.CandidateCount == nil || *c.CandidateCount != 2 || len(c.Results) != 2 {
		t.Fatalf("candidates = %+v results = %d", c.CandidateCount, len(c.Results))
	}
	if len(rv.checks) != 1 {
		t.Fatalf("upsert 次数 = %d（应幂等一次）", len(rv.checks))
	}
	win, loss := c.Results[0], c.Results[1]
	if win.Pct == nil || *win.Pct != 5 || !win.Hit || win.Filled == nil || !*win.Filled || *win.StopHit || !*win.TpHit {
		t.Fatalf("止盈先中 result = %+v", win)
	}
	if loss.Pct == nil || *loss.Pct != 5 || loss.Hit || !*loss.Filled || !*loss.StopHit || !*loss.TpHit {
		t.Fatalf("止损优先 result = %+v", loss)
	}
	if c.HitCount == nil || *c.HitCount != 1 || c.HitRate == nil || *c.HitRate != 50 {
		t.Fatalf("聚合 = hit %v rate %v", c.HitCount, c.HitRate)
	}
	if c.RelimitCount != nil {
		t.Fatalf("信号源不适用 relimit，应显式缺失: %v", *c.RelimitCount)
	}
	if win.Horizon == nil || *win.Horizon != 1 || win.TradeDate == nil || *win.TradeDate != "2026-09-10" {
		t.Fatalf("horizon/trade_date = %+v", win)
	}
}

func TestVerifySignalsPartialWhenBarsMissing(t *testing.T) {
	d3 := day(2026, 9, 8)
	signals := []SignalRow{
		{ID: 3, StockCode: "000858.SZ", StockName: "五粮液", Strategy: "breakout", TradeDate: &d3,
			EntryPrice: 10, StopLoss: 9.5, TakeProfit: 10.6, Status: "done"},
	}
	// T+3 需要 3 根（09-09/10/11），只取到 1 根 → partial，Pct 不落不进分母。
	bars := []Bar{bar(day(2026, 9, 11), 9.85, 10.70, 10.50)}
	uc, rv := newSignalEnv(signals, bars)

	c, err := uc.VerifySignals(context.Background(), signalCheckDay)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Partial || !contains(c.Missing, "bars") {
		t.Fatalf("缺行情必须 partial + missing bars: %+v", c)
	}
	if len(c.Results) != 1 || c.Results[0].Pct != nil {
		t.Fatalf("缺行情条 Pct 应缺失: %+v", c.Results)
	}
	if c.HitCount != nil || c.HitRate != nil || c.AvgPct != nil || c.MaxPct != nil {
		t.Fatalf("无有效样本时聚合应整组缺失: %+v", c)
	}
	if len(rv.checks) != 1 {
		t.Fatalf("partial 也要幂等 upsert, 次数 = %d", len(rv.checks))
	}
}

func TestVerifySignalsTradeDateFallback(t *testing.T) {
	// trade_date 为 NULL：按 signal_time 的上海日期回退到 D1。
	signals := []SignalRow{
		{ID: 4, StockCode: "600519.SH", StockName: "贵州茅台", SignalTime: day(2026, 9, 10).Add(14*time.Hour+30*time.Minute),
			EntryPrice: 10, StopLoss: 9.5, TakeProfit: 10.6, Status: "done"},
	}
	bars := []Bar{bar(day(2026, 9, 11), 9.98, 10.70, 10.50)}
	uc, _ := newSignalEnv(signals, bars)

	c, err := uc.VerifySignals(context.Background(), signalCheckDay)
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Results) != 1 || c.Results[0].Horizon == nil || *c.Results[0].Horizon != 1 {
		t.Fatalf("回退后应按 D1 到期: %+v", c.Results)
	}
	if c.Results[0].Pct == nil || c.Partial {
		t.Fatalf("回退后窗口应完整: %+v", c.Results[0])
	}
}

func TestVerifySignalsNoMaturedSignals(t *testing.T) {
	uc, rv := newSignalEnv(nil, nil)
	c, err := uc.VerifySignals(context.Background(), signalCheckDay)
	if err != nil {
		t.Fatal(err)
	}
	if c == nil || len(c.Candidates) != 0 || c.CandidateCount != nil {
		t.Fatalf("无到期信号应返回空 Check: %+v", c)
	}
	if len(rv.checks) != 0 {
		t.Fatalf("无到期信号属正常态，不落空对账行: %d", len(rv.checks))
	}
}
