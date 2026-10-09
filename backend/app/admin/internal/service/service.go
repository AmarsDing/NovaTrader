package service

import "github.com/google/wire"

// ProviderSet 目前只有鉴权中间件还引用的空服务。
// 业务接口按 proto-first 再加回来。
var ProviderSet = wire.NewSet(NewAdminService)
