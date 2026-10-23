# UI 走查 — B1 margin_mkt（2026-10-20）

本刀交付为 datahub 采集链路（ent schema + biz/data/worker/config），**前端零文件改动**。
对照 doc/开发文档/1.视觉需求.md 逐项：

| 维度 | 结论 |
|---|---|
| 样式（token/霓虹/毛玻璃） | 无改动面 |
| 结构（dockview/面板注册） | 无改动面——margin_market 暂只落库，未新增面板/读出口（消费侧属 M02/M05 后续增补） |
| 简洁（无冗余装饰） | 无改动面 |
| 科技感（材质/动态模式） | 无改动面 |
| 排版（栅格/密度/对齐） | 无改动面 |

发现项：无（本刀无 UI 代码）。数据纪律例外说明：margin_market 的 Nillable 列
将来出面板时，前端按 `number | null` +「没取到」兜底，与 intraday-alerts/AuctionRow
既有链条同构——届时单独立项走查。
