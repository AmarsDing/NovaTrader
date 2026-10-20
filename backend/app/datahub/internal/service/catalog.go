package service

import (
	"encoding/json"
	"net/http"

	"server/app/datahub/internal/biz"
	"server/conf"
)

// CatalogHTTP 输出源目录（管理面只读元数据，直写 JSON——与 server.go 的 healthz/metrics
// 挂法同先例，不走 proto/gateway 新路由；数据归属方在 datahub）。
// 挂在 /v1/datahub/source-catalog，经既有 gateway 反代 {prefix:/api/datahub/, join:/v1/datahub}
// 出网为 GET /api/datahub/source-catalog。
// principal 校验不做：网关在 /api/datahub/ 前缀已统一鉴权。
// Cache-Control: no-store——目录每进程固定但注册状态随配置变，不缓存免口径争议。
func (s *DatahubService) CatalogHTTP(c *conf.Datahub) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		cat := biz.BuildCatalog(s.c.Pipeline(), c)
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("Cache-Control", "no-store")
		_ = json.NewEncoder(w).Encode(cat)
	}
}
