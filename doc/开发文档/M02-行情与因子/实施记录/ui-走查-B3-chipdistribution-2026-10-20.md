# UI 走查 — B3 筹码分布 CYQ（2026-10-20）

本刀交付为 market 服务纯计算链路（`pkg/market/chip.go` 纯函数 + biz 聚合 +
market.proto 读出口），纯计算不落库——零新表零调度零配置，**前端零文件改动**
（`web/src` 无 diff）。
对照 doc/开发文档/design/01-视觉风格.md 逐项：

| 维度 | 结论 |
|---|---|
| 样式（token/霓虹/毛玻璃） | 无改动面 |
| 结构（dockview/面板注册） | 无改动面——`GET /v1/market/chip-distribution` proto 已宣泄，但前端未消费（消费侧面板候选：K 线副图筹码峰带 / M05 复盘段） |
| 简洁（无冗余装饰） | 无改动面 |
| 科技感（材质/动态模式） | 无改动面 |
| 排版（栅格/密度/对齐） | 无改动面 |

发现项：无（本刀无 UI 代码）。数据纪律例外说明：`GetChipDistributionReply`
四态 status（ok / missing=无券或空窗 / not_meaningful=数据不足或 float_share、
adj_factor 缺失 / error=请求非法）显式表达缺失——十衍生列（profit/avg_cost/
cost90_lo/hi/cost70_lo/hi/concentration90/70/peak_price/price）proto 侧全
`optional double` 已钉死「没取到≠0」，status≠ok 时全缺省，前端将来出面板按
`number | null` 显「数据不足/缺失」兜底，不画 0 分布；`days_used`/
`cum_turnover_pct`/window 事实列 status≠ok 时也照常回波可直接渲染。
与 B2 seat_stats（t5 NULL 而 t1 有值是合法业务态）同构：筹码分布的
not_meaningful（float_share/adj_factor 缺失禁用 0/1 冒充）禁止前端改写为
「获利 0%/全筹码亏损」假分布。届时单独立项走查。
