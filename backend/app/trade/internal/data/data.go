// Package data 用 ent 保存委托、成交和资金，并把事件写入 outbox。
package data

import (
	"context"
	"fmt"

	"server/app/trade/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/outbox"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, outbox.NewDispatcher, NewBus, NewRepo, NewSettings, NewRiskClient,
	wire.Bind(new(biz.Store), new(*Repo)),
	wire.Bind(new(biz.Checker), new(*RiskClient)),
)

func dbConfig(c *conf.Postgres) dbinit.Config {
	cfg := dbinit.Config{
		DSN: c.GetDsn(), Host: c.GetHost(), Port: c.GetPort(),
		User: c.GetUser(), Password: c.GetPassword(), Database: c.GetDatabase(), SSLMode: c.GetSslMode(),
	}
	if d := c.GetDialTimeout(); d != nil {
		cfg.Timeout = d.AsDuration()
	}
	return cfg
}

func NewEntClient(c *conf.Postgres, logger log.Logger) (*ent.Client, func(), error) {
	if c == nil {
		return nil, nil, fmt.Errorf("data: postgres config is missing")
	}
	conn, err := entclient.Open(context.Background(), entclient.Config{
		DB:           dbConfig(c),
		MaxOpenConns: int(c.GetMaxOpenConns()),
		MaxIdleConns: int(c.GetMaxIdleConns()),
	})
	if err != nil {
		return nil, nil, err
	}
	_ = logger
	return conn.Client, func() { _ = conn.Close() }, nil
}

func NewBus(c *conf.Nats) (*events.Bus, func(), error) {
	bus, err := events.Dial(c.GetUrl())
	if err != nil {
		return nil, nil, err
	}
	return bus, bus.Close, nil
}

func NewSettings(c *conf.Trade) biz.Settings {
	s := biz.Settings{}
	if c == nil {
		return s
	}
	s.InitialCash = c.GetInitialCash()
	s.PaperDaysRequired = int(c.GetPaperDaysRequired())
	return s
}
