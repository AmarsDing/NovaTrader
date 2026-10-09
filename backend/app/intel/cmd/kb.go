package main

import (
	"server/app/intel/internal/biz/kb"
	"server/app/intel/internal/core/kbjobs"
	"server/app/intel/internal/data/kbstore"
	"server/app/intel/internal/service/kbsvc"

	"github.com/google/wire"
)

// kbProviderSet 是 M04 知识库在 intel 服务里的依赖，复用 M03 的 ent 客户端和 NATS 连接。
var kbProviderSet = wire.NewSet(
	kbstore.NewKnowledgeRepo,
	kbstore.NewEmbedder,
	kb.NewKnowledgeUsecase,
	kbsvc.NewKnowledgeService,
	kbjobs.New,
)
