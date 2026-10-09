// Package biz 是 M03 情报处理的业务层：规范化、去重、关联、打分、告警、热词。
package biz

import "github.com/google/wire"

var ProviderSet = wire.NewSet(NewUsecase)
