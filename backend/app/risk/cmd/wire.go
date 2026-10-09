//go:build wireinject
// +build wireinject

package main

import (
	"server/app/risk/internal/biz"
	"server/app/risk/internal/core"
	"server/app/risk/internal/data"
	"server/app/risk/internal/server"
	"server/app/risk/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Risk, *conf.Postgres, *conf.Nats, *conf.Redis, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
