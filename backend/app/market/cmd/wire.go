//go:build wireinject
// +build wireinject

package main

import (
	"server/app/market/internal/biz"
	"server/app/market/internal/core"
	"server/app/market/internal/data"
	"server/app/market/internal/server"
	"server/app/market/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Market, *conf.Postgres, *conf.Redis, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
