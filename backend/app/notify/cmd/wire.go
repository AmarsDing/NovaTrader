//go:build wireinject
// +build wireinject

package main

import (
	"server/app/notify/internal/core"
	"server/app/notify/internal/data"
	"server/app/notify/internal/server"
	"server/app/notify/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Notify, *conf.Postgres, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
