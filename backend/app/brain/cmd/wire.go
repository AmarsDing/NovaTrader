//go:build wireinject
// +build wireinject

package main

import (
	"server/app/brain/internal/biz"
	"server/app/brain/internal/core"
	"server/app/brain/internal/data"
	"server/app/brain/internal/server"
	"server/app/brain/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Brain, *conf.Postgres, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, newApp))
}
