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

	"github.com/go-kratos/kratos/v2/registry"
	"github.com/google/wire"
)

// ProviderSet is server providers.
var ProviderSet = wire.NewSet(NewHTTPServer, NewGRPCServer, NewRegistrar, NewWebsocketServer)

// nopRegistrar 单机部署：前后端同一台机器，不跑服务注册中心。
// admin 仍要满足 kratos.Registrar 的注入，这里给一个不连任何后端的实现。
type nopRegistrar struct{}

func NewRegistrar() registry.Registrar {
	return nopRegistrar{}
}

func (nopRegistrar) Register(context.Context, *registry.ServiceInstance) error {
	return nil
}

func (nopRegistrar) Deregister(context.Context, *registry.ServiceInstance) error {
	return nil
}
