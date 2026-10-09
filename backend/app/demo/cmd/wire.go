//go:build wireinject
// +build wireinject

// The build tag makes sure the stub is not built in the final build.

package main

import (
	"github.com/go-kratos/kratos/v2"
	"github.com/go-kratos/kratos/v2/log"
	"github.com/google/wire"
	tracesdk "go.opentelemetry.io/otel/sdk/trace"
	"server/app/demo/internal/server"
	"server/app/demo/internal/biz"
	"server/app/demo/internal/core"
	"server/app/demo/internal/data"
	// "server/app/datacquisition/internal/service"
	"server/conf"
)

func wireApp(*conf.Demo, *conf.Discovery, *conf.Nats, *conf.Log, *conf.Other, *tracesdk.TracerProvider, log.Logger) (*kratos.App, func(), error) {
	panic(wire.Build(data.ProviderSet, biz.ProviderSet, core.ProviderSet, server.ProviderSet, newApp))
}
