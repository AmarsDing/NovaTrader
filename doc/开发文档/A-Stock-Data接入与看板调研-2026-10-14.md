# A-Stock-Data 接入与专业科幻看板调研

日期：2026-10-14（Asia/Shanghai）  
状态：调研与设计提案，未实施业务代码改动。

> 后续设计基线补充（2026-10-14）：用户已确认采用原有监控画布与票式盯盘设计，深化业务功能，详见[监控与盯盘业务深化方案](监控与盯盘业务深化方案.md)。本报告的市场参考与概念稿仅供局部借鉴；后续布局和交互以原方案及该补充为准。

> 后续实施补充：第一批已修新鲜度、界面口径和模拟确认衔接，并增加连线检视及白话行动层。下文缺陷和测试记录保留为调研时点记录，当前实施状态以深化方案第9节及M09/M12开发进度为准；A-Stock-Data新增源尚未接入。

## 1. 结论

**可以接入，建议按数据能力选取适配器，优先补历史数据、盘后数据和独立备源。** NovaTrader 已有完整采集与分发骨架，不需要替换 Go 服务、PostgreSQL/ent、Tauri 或 Vue 工作台。

**现有监控有科幻感，交易台有专业终端骨架，但尚不能认为信息已经足够准确、明确和可靠。** 首要问题是数据时间、指标口径、SIM/LIVE 身份与风控等级、交易状态更新。视觉优化应建立在这些契约修正之后。

推荐组合：盯盘借鉴 Bloomberg/LSEG 的高密度工作区与 TradingView 的标的联动；监控借鉴 Grafana 的异常下钻与 NASA Open MCT 的统一时间控制。保留深空底色、细边框、等宽数字和有限青紫强调，用真实事件驱动动效。

## 2. 本轮审读和验证范围

梳理了全仓目录、架构约束、模块文档，以及以下手写业务代码的主要入口和调用链：

| 范围 | 阅读重点 |
| --- | --- |
| 平台与网关 | bootstrap、配置、调度、交易日历、事件、outbox、admin gateway、WebSocket、运维探针 |
| 数据与行情 | datahub 的 source/pipeline/collector/validate/reconcile/backfill/repo；pyworker；market 聚合、涨停、情绪、板块、资金、因子 |
| 分析与交易 | intel/内置 kb、brain、strategy、backtest、risk、trade、notify 及相关手写 ent schema |
| 客户端 | 路由、Tauri 壳、workspace、业务 view、21 个面板、api、store、ws、theme、viz、monitor 的场景/指标/时段/新鲜度/详情 |
| 外部 | simonlin1212/a-stock-data 的 README、SKILL 中发布代码、更新日志、失效问题记录；6 类产品的官方资料 |

生成的 ent/pb 文件、第三方注册中心及二进制资源未逐行人工审计。本报告是全项目主链路调研，不是所有代码行的安全审计。

本地前端原先未运行；本轮启动 Vite 后，在浏览器 1280×720 视口核验了离线本地会话的监控与盯盘页。admin 2001 当时不可达，未注入模拟后端响应，未启动真实采集或交易服务。因此可以评价布局、离线状态和代码口径，不能据此宣称在线成交、真实行情延迟或多屏 Tauri 联动已经验收。

已保存实际截图：

- [监控离线截图](F:/股票/NovaTrader/doc/开发文档/监控面板离线评审-2026-10-14.jpg)
- [盯盘离线截图](F:/股票/NovaTrader/doc/开发文档/盯盘面板离线评审-2026-10-14.jpg)

## 3. A-Stock-Data 是什么，可以提供什么

本轮将用户所指项目定位为 [simonlin1212/a-stock-data](https://github.com/simonlin1212/a-stock-data)。当前 README 标识 V3.10.2，作者列出 15 层、87 个能力入口、34 个来源。它主要是附有 Python 实现和测试的自包含技能文件，并非已有 SLA 的托管行情服务。上述数量属于上游声明，本轮没有对全部入口进行真实网络验收。[项目说明](https://github.com/simonlin1212/a-stock-data/blob/main/README.md)

上游发布代码给出腾讯近期 K 线、通达信 HTTP 盘后包、腾讯当日分笔等能力。腾讯 K 线存在成交额缺失、分钟历史窗口及北交所覆盖边界；腾讯分笔也不能视为交易所 Level-2 数据。选用时必须逐能力验收。[发布实现](https://github.com/simonlin1212/a-stock-data/blob/main/SKILL.md)

上游记录了 2026 年 9 月通达信公开 TCP 的行情类命令静默返回空的问题。这个记录针对公开 TCP 路径，不能推导为通达信所有本地或授权接口均不可用。[Issue #52](https://github.com/simonlin1212/a-stock-data/issues/52)、[更新日志](https://github.com/simonlin1212/a-stock-data/blob/main/CHANGELOG.md)

代码仓库采用 Apache-2.0，移植时应保留适用的许可、署名和修改说明；代码许可不能作为第三方行情数据商用、转发或再分发权利的证明。每个上游数据源的用途与使用条件仍需单独核实。[LICENSE](https://github.com/simonlin1212/a-stock-data/blob/main/LICENSE)

### 接入价值与优先级

以下是结合 NovaTrader 现状的工程建议，不代表上游所有能力都已经通过本机验证：

| 能力 | NovaTrader 现状 | 建议 |
| --- | --- | --- |
| 实时价格/五档快照 | Go 已直连腾讯、新浪、东财，pyworker 有通达信路径 | 复用已有实现，重点对拍和修新鲜度；不重复包一层同源请求 |
| 通达信 HTTP 盘后日线包 | 已有本地文件读取，尚无对应官网包解析 | **首批接入候选**：适合日线补数与盘后核对，保留通达信方向 |
| 腾讯日/周/月与近期分钟 K | 当前腾讯适配器只实现快照；历史线主要依赖其他源 | **首批试点候选**：补价格历史/近期分钟；先解决缺失成交额的表达，不能当无限历史分钟库 |
| 复权与证券生命周期 | 已有后复权因子、上市/退市等数据模型 | 对拍后补缺口；保持不复权原价落库，查询时复权 |
| 公告/资讯/研报 | 有 cninfo、RSS、Tavily 及 IntelSource 入口 | 优先选择独立于当前故障源的来源；研报先入情报链，PDF 深度解析另行设计 |
| 官方两融/指数成分等 | 有部分资金与两融领域接口 | 作为盘后补充，核对是否具备所需证券级粒度 |
| 腾讯当日分笔 | 尚无完整逐笔存储与订单流图链路 | 第二阶段研究用途；不作为低延迟盘口或 Level-2 替代 |
| 宏观/期货/ETF/可转债 | 系统主要聚焦 A 股超短线，领域模型不完全匹配 | 后续按策略需求接入，不能直接写入股票表 |

“A-Stock-Data → 东财”和“AkShare → 东财”不是两个独立数据源。备源统计应按真实上游/故障面归类，而非按 Python 包名归类。

## 4. 推荐接入架构

保留当前链路：

```text
公开/授权上游接口
  → 经审阅、固定版本的采集实现（Go 直连或 Python pyworker）
  → datahub 的独立 Source 能力
  → 配额/超时/断路/逐记录质量校验/覆盖检查
  → PostgreSQL + ent / outbox / Redis
  → market / intel
  → brain / strategy / risk / trade
  → 网关 REST + WebSocket
  → Tauri / Vue 盯盘台与监控驾驶舱
```

### 4.1 具体代码接入点

| 文件 | 用途 |
| --- | --- |
| [source.go](F:/股票/NovaTrader/backend/app/datahub/internal/biz/source.go:15) | 已按日线、分钟线、因子、快照、资金、财务、情报等拆出能力接口；只实现经过验证的能力 |
| [sources.go](F:/股票/NovaTrader/backend/app/datahub/internal/data/sources.go:104) | 注册新适配器；不绕开统一流水线 |
| [pipeline.go](F:/股票/NovaTrader/backend/app/datahub/internal/biz/pipeline.go:207) | 复用按域优先级、超时、配额、断路与同轮切源 |
| [config.yaml](F:/股票/NovaTrader/backend/configs/config.yaml:184) | 配置数据源开关与各域主备顺序 |
| [backfill.go](F:/股票/NovaTrader/backend/app/datahub/internal/biz/backfill.go:16) | 复用按日期/证券/指定来源补数 |
| [repo.go](F:/股票/NovaTrader/backend/app/datahub/internal/data/repo.go:50) | 复用事务 upsert 与 ready/outbox；无需另建独立数据库 |
| [pyworker/app.py](F:/股票/NovaTrader/pyworker/app.py:1) | Python 实现可在现有边车内模块化提供；边车继续只采集、不写业务库 |
| [gateway/config.go](F:/股票/NovaTrader/backend/app/admin/internal/gateway/config.go:80) | 如需桌面配置，扩展白名单和配置目录；YAML 新键不会自动出现在 UI |

建议给可独立观测的能力分配独立源名，如 `tdx_http`、`qq_kline`；另外记录上游 provider、dataset 和实现版本。不要将多种实际来源隐藏在一个“AStockData 正常”的绿灯后面。现有行情表 source 长度为 16，命名和元数据应与当前 schema 约束一致。

移植时将必要函数整理成普通、可审阅的模块，固定上游版本/提交并保留许可；生产服务不动态下载并执行远程 SKILL.md，也不依赖每次由大模型拼装采集代码。

### 4.2 首批接入前必须确定的字段契约

| 字段 | 内部约定 / 验收要求 |
| --- | --- |
| 证券 | `600519.SH` / `000001.SZ`；指数必须显式市场与类型，不能靠裸 `000001` 推断 |
| 价格与复权 | 元；原价不复权落库，因子另存；明确因子基准和计算公式 |
| 成交量 | 股；股票板块/ETF/分笔的原始单位分别核实，禁止统一盲乘 100 |
| 成交额 | 元；缺失与真实 0 区分，不能拿换手率字段顶替 |
| 时间 | 上海交易时区；K 线统一开始时刻；分钟频率与时间转换一致 |
| 新鲜度 | 源头行情时间、接收时间、最后成功轮询、最后推送分别保留 |
| 覆盖 | 分市场、证券类型和停牌状态统计覆盖率，不能只看总行数 |
| 失败 | 不支持、未发布、真实空结果、格式改变、限流、过期分别处理 |
| 情报 | 原文 URL、发布时间、采集时间、来源 ID 与去重键保留 |

腾讯 K 线缺失成交额尤其需要前置处理：当前 [biz.Bar.Amount](F:/股票/NovaTrader/backend/app/datahub/internal/biz/model.go:73) 是非空 float64，[UpsertBars](F:/股票/NovaTrader/backend/app/datahub/internal/data/repo.go:168) 直接 SetAmount。尽管 ent 的 amount 可空，当前采集 DTO/仓储路径仍不能诚实表达未知成交额。应扩展可空契约/质量标记，或限制该源的使用范围，不能悄悄写 0。

现有行情唯一键为 symbol/bar_time/freq，补源可能覆盖已有行。应定义数据质量和来源优先级，避免字段不完整的备源覆盖更完整记录；保留必要的原始证据，才便于对拍和追溯。

## 5. 当前最影响“专业、明确”的问题

### P0：先修数据可信度和监控身份

| 问题 | 当前证据与影响 | 建议 |
| --- | --- | --- |
| 旧报价可能被整批保鲜 | [validate.go:66](F:/股票/NovaTrader/backend/app/datahub/internal/biz/validate.go:66) 只检查整批最新时间；[collector.go:136](F:/股票/NovaTrader/backend/app/datahub/internal/biz/collector.go:136) 给全部记录写当前 AsOf/Stale=false。风险服务会采用 AsOf 判断时间 | 逐证券检查源时间，接收时间单列；同日旧报价不能因此进入新开仓 |
| 离线仍显示健康 80 | [health.ts:39](F:/股票/NovaTrader/web/src/monitor/health.ts:39) 的未知项多不扣分；本轮离线 UI 实际复现 | 显示“不可评估 / 已测 x/5”；业务阻断项独立于健康总分持续可见 |
| LIVE 与 L0–L3 混淆 | [health.ts:118](F:/股票/NovaTrader/web/src/monitor/health.ts:118)、[TerminalStrip.vue:451](F:/股票/NovaTrader/web/src/monitor/TerminalStrip.vue:451) 用 risk.mode 判实盘，但该字段是风控等级 | 用 account.book 判 SIM/LIVE，用真实 brokerFiles 探针判文件单通道，用 risk.mode 显示风险等级 |
| 无关 WS 让旧 REST 数据显得刚读到 | [freshness.ts:186](F:/股票/NovaTrader/web/src/monitor/freshness.ts:186) 依赖 input.now；[MonitorView.vue:557](F:/股票/NovaTrader/web/src/monitor/MonitorView.vue:557) 接收脉冲重写它 | 分离每个数据域的 sampledAt/asOf 和 lastPushAt |

静态链路还发现 [trade/core/runner.go:42](F:/股票/NovaTrader/backend/app/trade/internal/core/runner.go:42) 直接把 NATS 消息解成 bars，而 market 发布的是 Envelope。按现有结构会得到空 bars，分钟线自动模拟撮合入口需修正并做集成验收。现有 biz 撮合测试通过不能证明这条事件链已打通。

### P1：修指标口径和盯盘状态

| 问题 | 证据 | 建议 |
| --- | --- | --- |
| 后验收益放大 100 倍 | [proto:77](F:/股票/NovaTrader/backend/api/strategy/v1/strategy.proto:77) 定义 4.8=4.8%；[SignalView:225](F:/股票/NovaTrader/web/src/view/SignalView.vue:225)、[decision-desk:299](F:/股票/NovaTrader/web/src/view/decision-desk.ts:299) 再乘 100 | 区分百分点与比率，建立字段格式字典 |
| 1 分钟 K 被当成当日行情 | [MarketView:42](F:/股票/NovaTrader/web/src/view/MarketView.vue:42) 用上一根 close 计算涨跌，最后一根 OHLC/量额直接做报价条 | 当前价/前收/当日 OHLC 取 snapshot；临时用 bars 时明确标“本根 K 线” |
| 交易列表缺盘中更新 | [TradeView:204](F:/股票/NovaTrader/web/src/view/TradeView.vue:204) 只在 load 更新本地 refs；看门狗不更新这些表 | 统一交易 store、订阅成交/仓位事件并在重连后补拉 |
| 推荐限价未正确填入 | [OverviewView:147](F:/股票/NovaTrader/web/src/view/OverviewView.vue:147) 传 number；[TradeView:325](F:/股票/NovaTrader/web/src/view/TradeView.vue:325) 只接 string | 接受有限正数；换标的时清理不适用的旧价格并标价源 |
| HUD 标签与窗口不一致 | [hud.ts:55](F:/股票/NovaTrader/web/src/monitor/hud.ts:55)、[hud.ts:84](F:/股票/NovaTrader/web/src/monitor/hud.ts:84)、[hud.ts:142](F:/股票/NovaTrader/web/src/monitor/hud.ts:142) | 封板数/炸板数、漏选数/候选数分别算；近20笔胜率不能标成“今日/昨日胜率” |
| 模型统计未按说明持续刷新 | [model-stats.panel.vue:12](F:/股票/NovaTrader/web/src/panels/brain/model-stats.panel.vue:12) 只读 store；[watchdog:14](F:/股票/NovaTrader/web/src/workspace/connection-watchdog.ts:14) 未刷新 brain | 可见期间刷新/订阅，展示窗口与最后成功时间 |

### 数据能力与文档差异

- **通达信本地官方接口仍是占位**：[app.py:450](F:/股票/NovaTrader/pyworker/app.py:450) 起的 local_snapshot/local_bars/local_securities 直接 unavailable；不只是安装插件即可恢复。
- **默认全市场分钟采集链不完整**：[collect_daily.go:51](F:/股票/NovaTrader/backend/app/datahub/internal/biz/collect_daily.go:51) 未填 securities；文件分钟读取要求指定证券，AkShare 分钟源也不支持空证券池。不要据文档承诺已具备完整分钟历史。
- **外围数据覆盖窄**：[app.py:464](F:/股票/NovaTrader/pyworker/app.py:464) 实际只尝试少量项目；US_SPOT 列表首行不能当作明确的美股指数。
- **来源摘要损失细节**：[ops.go:96](F:/股票/NovaTrader/backend/app/admin/internal/gateway/ops.go:96) 将状态压成 name/status/detail。完整 datahub API 和前端 normalizeSource 已保留不少详细字段，问题主要在部分摘要/HUD 消费与状态语义，不能说整个前端没有源明细。
- **交易通道尚未完成**：仓库有 CSV 写出工具，运维接口也如实报告 broker-gw 缺失；新增行情源不会自动补齐文件单回报、对账与实盘准入。
- **仍需真实样本核验**：腾讯科创板成交量单位、5 分钟结束时间转换、多源复权一致性。此处列为待验证，未当作已实测成立的缺陷。

## 6. 现有界面应保留什么

现有两类页面定位正确：独立 /monitor 用于业务监控，/ 工作台承载行情、信号、决策与交易。dockview 的拖拽、布局保存、21 个面板和多屏能力是良好基础。

监控已有业务拓扑、四维研判、漏斗、活动条、事件带和详情下钻，不需要重写成另一套“大屏框架”。当前数据源明细、信号字段、有效期、降级提示、决策票、模型意见、扫描结果和全撤等能力已经存在，不能照搬旧评审声称它们缺失。

本轮离线实看得到两点：

1. 交易台明确显示 admin 不可达和本地会话，决策台用“未返回/—”呈现不少未知项，这是应保留的诚实表达；但有些表格仍写“暂无持仓/成交”，错误态与真实空表需要统一。
2. 监控拓扑辨识度较强，但自动 fit 后节点副文很小，画布与底部信息带竞争空间。应按语义折叠次要节点、提供概览/诊断层级，避免所有节点整体缩成难读的小字。

最新视觉规范已经收紧玻璃拟态和 glow 预算。继续执行红涨绿跌；青色表示选择/操作/AI，紫色表示模型，橙色表示注意，风险同时给出文字与图标。不要重新放大全屏常驻动画、3D K 线和发光数字。

## 7. 官方市场参考

以下功能来自官方公开资料，“借鉴”列为本报告设计推论；本轮未登录这些商业终端进行体验测试。

| 参考 | 官方可确认能力 | NovaTrader 借鉴 |
| --- | --- | --- |
| [Bloomberg Terminal / Launchpad](https://professional.bloomberg.com/products/bloomberg-terminal/) | 自定义工作区，证券监视、图表、新闻、警报 | 稳定位置、高密度、从标的到证据到执行的工作流 |
| [LSEG Workspace](https://www.lseg.com/en/data-analytics/products/workspace)、[入门指南](https://www.lseg.com/content/dam/lseg/learning-centre/documents/workspace-quick-start-guide.pdf) | 多应用工作区、布局、书签与警报 | 盘前/盘中/复盘预设，保存证券与参数，事件进入相关标的 |
| [TradingView 图表联动](https://www.tradingview.com/support/solutions/43000629992-how-to-sync-the-charts-of-my-layout/) | 证券、周期、十字线、时间范围同步 | A/B 联动组，让自选、图表、情报、信号统一选中证券 |
| [Bookmap Main Chart](https://bookmap.com/knowledgebase/docs/KB-SettingUpAndOperating-HeatmapMainChart) | 当前/历史盘口流动性热力图、成交圆点、买卖报价 | 未来有真实盘口与逐笔后再借鉴；不能用日 K 与快照伪造订单流 |
| [Grafana 看板实践](https://grafana.com/docs/grafana/latest/visualizations/dashboards/build-dashboards/best-practices/)、[缺失数据指南](https://grafana.com/docs/grafana/latest/alerting/guides/missing-data/) | 分层看板、下钻、查询错误与无数据/序列缺失的区别 | 先显示业务受损，再进入延迟/失败/资源；未知与故障分别呈现 |
| [NASA Open MCT](https://nasa.github.io/openmct/)、[官方插件](https://nasa.github.io/openmct/plugins/) | 实时与历史遥测、时间线、Time Conductor、状态摘要 | 统一业务时间带、实时/历史视角、异常影响链与紧凑最新值 |

最符合“专业且科幻”的参考是：**TradingView 的交易交互 + Bloomberg/LSEG 的工作区密度 + Open MCT 的任务控制表达**。这属于设计组合，不要求引入这些产品或替换现有技术栈。

## 8. 两类看板的目标信息结构

### 8.1 盯盘工作台

- 顶部常驻：当前账本 SIM/LIVE、风险等级 L0–L3、市场时段、关键指数、行情源与截至时间、重要阻断项。
- 左侧：自选/持仓高密度表，证券名与代码、现价、当日涨跌、量额、异动和源新鲜度。
- 中央最大区域：分时/日 K/分钟 K 与成交量，事件和成交标记；周期切换不改变当日行情条的统计口径。
- 右侧：当前证券信号、规则/AI 分数、证据、触发/失效条件、有效期、止损与仓位、风控意见。
- 底部：持仓/委托/成交与相关事件；盘中更新、重连补拉，始终保留数据时间。

默认“盘中盯盘”预设应让行情、梯队/自选和风险同时可见。当前默认右侧多个标签组一次只能看一个，可继续保留为紧凑预设，但不能承担所有同屏任务。

### 8.2 监控驾驶舱

- 顶部先回答：业务能否继续、哪项被阻断、是否降级、数据是否新鲜、测量覆盖是否完整。
- 中央保留：采集 → 加工/扫描 → 四维分析 → 裁决 → 风控 → 账本；连线含义必须来自真实依赖或事件。
- 右侧诊断：发生了什么、证据时间、影响哪些证券/链路、下一步进入哪个详情。
- 底部：按统一窗口展示事件流、任务/准入、模型和依赖状态；GPU 作为诊断证据，不与交易阻断同权抢主视觉。
- 配置修改仍按最新方案保留明确例外；只读监控和配置动作在文案与权限上有清楚边界。

每个关键指标都应能回答：**名称、值、单位、窗口、来源、源时间、质量状态**。状态至少区分“未接入、等待首条、真实为零、已陈旧、故障、主动关闭、正在降级”。

## 9. 实施顺序与验收

| 阶段 | 范围 | 完成标准 |
| --- | --- | --- |
| P0 契约修正 | 逐证券新鲜度、健康未知态、账本/风控分离、WS 与 REST 时钟、模拟撮合事件解包 | 旧报价不能被新批次保鲜；离线不显示正常高分；LIVE+L3 不判为模拟；分钟事件可真正进入撮合 |
| P1 接入试点 | 通达信 HTTP 日线包；腾讯历史/近期分钟按能力接；独立资讯备源 | 先小样本对拍；成交量/额/复权/时间与缺失字段明确；分市场覆盖与失败原因可观测 |
| P1 盯盘口径 | 当日行情、收益单位、HUD 指标窗口、交易列表更新、推荐限价 | 页面数字与后端契约一致；切票不残留旧价；成交后状态自动更新 |
| P2 视觉与交互 | 联动组、盘中预设、监控层级与字体、异常下钻、有限事件动效 | 1280×720、1920×1080、常见缩放可读；键盘可用；减少动效有效 |
| P3 扩展数据 | 官方两融/指数/更多宏观、分笔与其他资产 | 每个能力独立验收，新增领域模型同步 ent/schema 与协议 |

接入验收应包含：沪/深/北、主板/创业板/科创板/ETF/指数；停牌与代码迁移；除权前后；午休、尾盘、节假日；超时/限流/格式变化/部分市场缺失；逐证券源时间；切源恢复与重复补数。真实运行验收需覆盖实际交易时段，不能只凭 HTTP 200 或离线解析测试通过。

## 10. 本轮测试结果和限制

- 前端 `npm run test`：18 个测试文件、271 项通过。
- 前端 `npm run typecheck`：通过。
- 后端 `go test ./app/datahub/internal/biz ./app/market/internal/biz ./pkg/market ./pkg/tradecal ./pkg/risk ./pkg/brokerif/sim ./app/risk/internal/biz ./app/trade/internal/biz`：8 包通过。
- datahub/data 6 个现有解析/本地 httptest、events 7 个纯内存测试、strategy/core 测试：通过。
- 刻意未运行依赖本机 NATS 的 2 个 events 集成测试、live 标签、真实通达信插件、数据库仓储集成或实盘链路。
- 本轮没有执行上游所有数据接口；真实可用性、覆盖率、端到端 P95 与授权条件仍需在试点阶段验证。
- 本轮只新增调研材料和概念展示，未接入新源、修改业务逻辑、执行委托或开启实盘。

代码和现有测试同时显示：单模块测试通过，仍可能存在跨层字段、时间和事件外壳不一致。后续测试应围绕这些真实契约和故障场景补充。
