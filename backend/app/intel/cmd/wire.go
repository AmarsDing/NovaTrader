//go:build wireinject
// +build wireinject

package main

import (
	"server/app/intel/internal/biz"
	"server/app/intel/internal/core"
	"server/app/intel/internal/data"
	"server/app/intel/internal/server"
	"server/app/intel/internal/service"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Intel, *conf.Kb, *conf.Postgres, *conf.Nats, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, service.ProviderSet, server.ProviderSet, core.ProviderSet, kbProviderSet, newApp))
}
