//go:build wireinject
// +build wireinject

package main

import (
	"server/app/strategy/internal/biz"
	"server/app/strategy/internal/core"
	"server/app/strategy/internal/data"
	"server/app/strategy/internal/server"
	"server/app/strategy/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Strategy, *conf.Postgres, *conf.Redis, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
