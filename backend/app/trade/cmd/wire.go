//go:build wireinject
// +build wireinject

package main

import (
	"server/app/trade/internal/biz"
	"server/app/trade/internal/core"
	"server/app/trade/internal/data"
	"server/app/trade/internal/server"
	"server/app/trade/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Trade, *conf.Postgres, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
