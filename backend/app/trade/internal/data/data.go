// Package data 用 ent 保存委托、成交和资金，并把事件写入 outbox。
package data

import (
	"context"
	"fmt"
	"time"

	"server/app/trade/internal/biz"
	"server/conf"
	"server/ent"
	"server/pkg/dbinit"
	"server/pkg/entclient"
	"server/pkg/events"
	"server/pkg/outbox"
	"server/pkg/registry"

	"github.com/go-kratos/kratos/v2/log"
	"github.com/go-redis/redis/v8"
	"github.com/google/wire"
)

var ProviderSet = wire.NewSet(
	NewEntClient, outbox.NewDispatcher, NewBus, NewRedis, NewRepoWithMarket, NewSettings, NewRiskClient, registry.NewBeater,
	wire.Bind(new(biz.Store), new(*Repo)),
	wire.Bind(new(biz.Checker), new(*RiskClient)),
)

func NewRedis(c *conf.Redis) (*redis.Client, func(), error) {
	if c == nil || c.GetAddr() == "" {
		return nil, nil, fmt.Errorf("trade: redis config is missing")
	}
	r := redis.NewClient(&redis.Options{Addr: c.GetAddr(), ReadTimeout: time.Second, WriteTimeout: time.Second})
	return r, func() { _ = r.Close() }, nil
}
func NewRepoWithMarket(client *ent.Client, rdb *redis.Client, logger log.Logger) *Repo {
	r := NewRepo(client, logger)
	r.rdb = rdb
	return r
}

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

// NewSettings 是 LIVE 出口双闸读取的统一收口（M13-T03）：
// LiveEnabled/BrokerConnected 默认 false → fail-closed，conf 不提供任何开关，
// 默认装配下 LIVE 委托一律 reject，SIM 链不受影响。
// T05（已落地）：LiveEnabled 的持久值在此映射 strategy_config 键
// app.trade.live.enabled（含批准人/时间/准入证据，经 GrantLive 先写审计
// 留痕后生效）；键缺失/读失败/损坏一律保持关闭，读故障大声记日志——
// 「没取到≠放行」也不因此拖垮 SIM 链启动。BrokerConnected 只允许 runner
// 健康循环经核验的 broker 适配链置位，掉线自动回落 false（无配置/界面旁路）。
// 本函数的其余输入只来自 conf 的模拟撮合门槛。
func NewSettings(c *conf.Trade, client *ent.Client, logger log.Logger) biz.Settings {
	s := biz.Settings{}
	if c != nil {
		s.InitialCash = c.GetInitialCash()
		s.PaperDaysRequired = int(c.GetPaperDaysRequired())
	}
	if client == nil { // 单测裸装配（无库）保持关闸
		return s
	}
	grant, known, err := NewRepo(client, logger).LoadLiveGrant(context.Background())
	if err != nil {
		log.NewHelper(logger).Errorf("live grant restore failed; LIVE gate stays closed: %v", err)
	}
	s.LiveEnabled = biz.LiveGateFromGrant(grant, known, err)
	return s
}
