//go:build wireinject
// +build wireinject

package main

import (
	"server/app/backtest/internal/biz"
	"server/app/backtest/internal/core"
	"server/app/backtest/internal/data"
	"server/app/backtest/internal/server"
	"server/app/backtest/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Backtest, *conf.Postgres, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, data.ResearchProviderSet, biz.ProviderSet, biz.ResearchProviderSet, service.ProviderSet, service.ResearchProviderSet, server.ProviderSet, core.ProviderSet, core.ResearchProviderSet, newApp))
}
