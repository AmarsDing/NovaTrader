# NovaTrader backend

Go 1.24 + Kratos v2 后端（module 名 `server`）。工程约定见仓库根 `CLAUDE.md`。

## API 契约唯一来源

**`api/<服务>/v1/*.proto` 是全部 HTTP/gRPC 接口的唯一契约源**（proto-first）：

- 新增/变更接口：先改 proto，再 `make proto` / `buf generate` 生成 pb 代码，再实现 service。禁止手写生成物。
- 前端 REST 调用走 admin 网关（HTTP :2001），实时推送走 WebSocket :2003；路由聚合在 `app/admin/internal/gateway/`。
- 本目录**不维护 openapi.yaml**：曾经的 `openapi.yaml` 是 Kratos 脚手架生成残留（PrecisionAirConditionerService 模板件），2026-11-08 删除。需要 OpenAPI 文档时从 proto 重新生成，不得以手写件顶替。

## 事件契约

NATS subject 与事件载荷见 `pkg/events/envelope.go`（JetStream 信封七元数据）；outbox 投递见 `pkg/outbox/`。
