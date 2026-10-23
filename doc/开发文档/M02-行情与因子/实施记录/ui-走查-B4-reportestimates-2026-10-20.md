# UI 走查 — B4 report_estimate 读出口（2026-10-20）

本刀交付为 market 服务纯读链路（proto `GetReportEstimates` + biz 窗口聚合 +
data 读仓储 + service），读 `report_estimate` PIT 明细；写入链归 M01 datahub
report 采集分支（`CollectIntel` 挂钩 `EstimateRowsOf`→`UpsertReportEstimates`，
先落库后发事件，已完结，见 M01 进度 B4 条），**前端零文件改动**（本刀 `web/src` 零改动）。
对照 doc/开发文档/1.视觉需求.md 逐项：

| 维度 | 结论 |
|---|---|
| 样式（token/霓虹/毛玻璃） | 无改动面 |
| 结构（dockview/面板注册） | 无改动面——`GET /v1/market/report-estimates` proto 已宣泄，但前端未消费（消费面候选：M05 估测段新建 / 个股研报面板） |
| 简洁（无冗余装饰） | 无改动面 |
| 科技感（材质/动态模式） | 无改动面 |
| 排版（栅格/密度/对齐） | 无改动面 |

发现项：无（本刀无 UI 代码）。数据纪律例外说明：`ReportEstimateRow` 是
「多券商分歧 PIT 明细」——同券同日多家券商各自成行是合法业务态，将来出面板
得按行渲染或自行聚合（一致预期均值/分歧度属消费侧推导，后端不下放预聚合假数）；
披露列（eps/pe/目标价上下限/rating/last_rating/rating_change/report_type/
researcher/industry/org_code）proto 侧全 `optional`，DB NULL↔proto 缺省一一对应
已钉死「没取到≠0」——面板按 `number | null` / `string | null` 兜底显「—」，
**禁止**把缺失的 EPS/目标价渲染成 0 或「评级：无」；`org_sname` 为必有列可直接渲染；
`found=false` 表示发布日窗口内无披露行（区别「券无研报覆盖」与「数据缺失」——
都显「近 90 日无研报」文案即可，不告警）。届时单独立项走查（含 M05 估测段需求先行）。
