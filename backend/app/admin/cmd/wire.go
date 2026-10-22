//go:build wireinject
// +build wireinject

package main

import (
	"server/app/admin/internal/core"
	"server/app/admin/internal/data"
	"server/app/admin/internal/gateway"
	"server/app/admin/internal/server"
	"server/conf"

	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
)

func wireApp(*conf.Admin, *conf.Nats, gateway.Settings, *conf.Postgres, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(server.ProviderSet, data.NewAuditStore, data.NewBus, data.NewSchedulerStore, gateway.NewHub, core.ProviderSet, newApp))
}
