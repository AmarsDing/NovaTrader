//go:build wireinject
// +build wireinject

package main

import (
	"server/app/datahub/internal/core"
	"server/app/datahub/internal/data"
	"server/app/datahub/internal/server"
	"server/app/datahub/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Datahub, *conf.Postgres, *conf.Redis, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
