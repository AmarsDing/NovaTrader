// Package harness 是 NovaTrader 对接智能体底座（aranea-agents 机器面
// /api/v1/*）的客户端适配包（M14，2026-11-02）。
//
// 定位与诚实边界（如实声明，勿夸大）：
//   - 本包是底座的【客户端】——健康探测、agent 目录、run 提交/查询/取消；
//     不是「宿主机监护/进程管理」的承载者。进程级监护（重启、守护）属底座
//     侧能力边界，NT 仓不实现（M11 开发进度 FR-11-08 边界声明已按此修订）。
//   - driver=blocked 为恒阻断兜底（永驻，对齐 HCF ADR-AGT-01 同构纪律）：
//     token 未配置、契约未在实机验证前，一切调用返回
//     ErrHarnessContractUnverified，绝不静默降级为「空数据」或伪造可用。
//   - driver=http 为真实适配器：机器面 Bearer（token 走 NT 密钥纪律——
//     明文/env:/enc: 三写法，复用 pkg/llm.ResolveAPIKey；严禁密钥进库）。
//
// 安全闸（与底座 P0 改造配套的客户端侧纪律）：
//   - 幂等：SubmitRun 必须携带调用方生成的 idempotency_key（底座按
//     (caller, key) 持久化去重，重试/对账窗口 7 天）；
//   - 归属：底座按 Bearer token 识别 caller=novatrader 并做租户门禁
//     （读面=共享+自有，写面=仅自有 workspace），NT 侧无需也不应伪造
//     他租户身份；
//   - 回调：webhook_url 走底座提交闸+拨号闸双闸（内网限定），secret 由
//     NT 侧生成并自存（验签用），严禁复用 Bearer token 当 webhook secret。
//   - 审批（M14-T06）：ResumeRun 仅为能力面——决议仅来自人工审批面，
//     本包绝无自动 approve（LLM 只产出评分与理由，不直接推进执行）。
//
// 接收端（M14-T05）：webhook.go 提供无状态验签入口（SignV1/VerifyV1 +
// NewWebhookHandler），与底座 pkg/outboundwebhook.SignBody 逐字节同构；
// 本包不建 run 追踪面、不新立进程（run 状态归属提交方业务记账面）。
package harness
