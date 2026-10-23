# UI 走查 — B2 seat_stats（2026-10-20）

本刀交付为 market 服务内部计算链路（ent schema + biz/data/service/worker + market.proto
读出口），**前端零文件改动**（`web/src` 无 diff）。
对照 doc/开发文档/1.视觉需求.md 逐项：

| 维度 | 结论 |
|---|---|
| 样式（token/霓虹/毛玻璃） | 无改动面 |
| 结构（dockview/面板注册） | 无改动面——`GET /v1/market/seat-stats` proto 已宣泄，但前端未消费（消费侧面板属 M05 复盘 lhb 段 / 监控驾驶舱后续增补） |
| 简洁（无冗余装饰） | 无改动面 |
| 科技感（材质/动态模式） | 无改动面 |
| 排版（栅格/密度/对齐） | 无改动面 |

发现项：无（本刀无 UI 代码）。数据纪律例外说明：`seat_stats` 的六收益列
（win_rate/avg_ret/med_ret × t1/t5）为可选可空列，proto 侧 `optional double` 已钉死
「没取到≠0」——将来出面板时，前端按 `number | null` +「样本不足」/「—」兜底，
与 intraday-alerts/AuctionRow 既有链条同构；`sample`/`sample_t5` 为事实列恒 int，
可直接渲染，t5 三列为 NULL 而 t1 三列有值的形态（T+5 未发生）是合法业务态，
面板不得改写为 0。届时单独立项走查。
