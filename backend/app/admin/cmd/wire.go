//go:build wireinject
// +build wireinject

package main

import (
	"server/app/admin/internal/data"
	"server/app/admin/internal/gateway"
	"server/app/admin/internal/server"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Admin, *conf.Discovery, *conf.Nats, gateway.Settings, *conf.Postgres, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, data.NewAuditStore, gateway.NewHub, newApp))
}
