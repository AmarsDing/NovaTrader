/*
 * @Author: hu 2245749018@qq.com
 * @Date: 2022-08-19 11:20:40
 * @LastEditors: hu 2245749018@qq.com
 * @LastEditTime: 2022-08-19 15:06:17
 * @FilePath: \weatherMaster-server\app\main\internal\server\server.go
 * @Description:
 *
 * Copyright (c) 2022 by hu 2245749018@qq.com, All Rights Reserved.
 */
package server

import (
	"context"
	"sync"

	"server/conf"

	"github.com/go-kratos/kratos/contrib/registry/discovery/v2"
	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
)

// ProviderSet is server providers.
var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer, NewRegistrar, NewWebsocketServer)

// lazyRegistrar 把注册中心连接推迟到 HTTP 已经开始监听之后。
// discovery.New 会一直等到注册中心应答；连不上时不能挡住 2001。
type lazyRegistrar struct {
	conf *conf.Discovery
	once sync.Once
	reg  registry.Registrar
	err  error
}

func NewRegistrar(conf *conf.Discovery) registry.Registrar {
	return &lazyRegistrar{conf: conf}
}

func (l *lazyRegistrar) client() (registry.Registrar, error) {
	l.once.Do(func() {
		l.reg = discovery.New(&discovery.Config{
			Nodes:  l.conf.Nodes,
			Env:    l.conf.Env,
			Region: l.conf.Region,
			Zone:   l.conf.Zone,
			Host:   l.conf.Host,
		})
	})
	return l.reg, l.err
}

func (l *lazyRegistrar) Register(ctx context.Context, service *registry.ServiceInstance) error {
	reg, err := l.client()
	if err != nil {
		return err
	}
	return reg.Register(ctx, service)
}

func (l *lazyRegistrar) Deregister(ctx context.Context, service *registry.ServiceInstance) error {
	if l.reg == nil {
		return nil
	}
	return l.reg.Deregister(ctx, service)
}
