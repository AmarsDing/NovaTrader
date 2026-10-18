# 参考：gloomberb（开源彭博式金融终端）分析与借鉴清单

> 分析对象：[`gloom-sh/gloomberb`](https://github.com/gloom-sh/gloomberb) v0.17.6，MIT 协议。
> 本地副本：`research-worker/gloomberb/`（浅克隆去 .git，仅作参考，勿提交进主仓库）。
> 分析日期：2026-10-18。性质同 `参考-ai-hedge-fund.md` / `参考-nautilus-trader.md`：外部产品对标，沉淀可抄项。

## 1. 它是什么

「终端里的 Bloomberg」：开源金融终端，一句话定位 = **Market research and portfolio tracker for the terminal**。

- 技术栈：TypeScript + Bun，一份 React 声明式代码跑三端——终端（自研 OpenTUI 渲染层）、桌面（electrobun，macOS/Windows）、Web（Cloudflare Workers，`term.gloom.sh`）。
- 规模：~4450 文件；**120 个内置插件**（`src/plugins/builtin/`），产品功能全是插件（Portfolio、Ticker Research 都是插件自身，dogfooding）。
- 数据模式：客户端开源（MIT），行情数据走云端 Gloom Cloud（闭源），免费层延迟 15 分钟、Pro 实时。api-client 有 90+ 端点模块（catalysts、cot、cpi、mna、perps、distress、supply-chain……），即功能长尾主要靠服务端数据面撑起来。
- 商业模式：外壳免费开源 + 数据订阅收费。与我们「私有化单机部署」不同，但**交互与产品形态完全可抄**。

## 2. 与 NovaTrader 的总体对比

| 维度 | gloomberb | NovaTrader 现状 | 差距判断 |
| --- | --- | --- | --- |
| 前端壳 | 终端 + electrobun + Web 三端 | Tauri(Rust) + Vue3 桌面驾驶舱（21 面板 dockview） | 形态不同，不抄其三端；但交互密度可抄 |
| 命令入口 | **Ctrl+P 命令栏 + 彭博式助记符**（`DES AAPL`/`GP NVDA`）+ AI 补全 + 股票联想 | 已有地基：Ctrl+K 面板/预设/命令过滤面板（`workspace/CommandPalette.vue`）+ 快捷键层（`shortcuts.ts`：B/S、Shift+T/K/M/R、1-9），但**无助记符参数语法、无 ticker 联想、无 AI 补全、命令表硬编码** | 有地基缺语法，P0 **增强而非新建** |
| 布局 | 布局=纯值（JSON 二分树+实例表），undo/同步/市场分享白送 | dockview 序列化 + `layout.presets` 预设（已可被命令面板套用） | 补「面向交易时段的桌面包 + 命令生成布局」 |
| 行情刷新性能 | 统一数据帧调度器 + per-symbol 闪动钟 + 行级 memo + 负载自适应降频 | ws hub 很强（epoch/topic_seq 断线恢复、背压），但**每条消息直发全部监听器，无合帧/无闪动节律**；仅 monitor 画布有自有 rAF 循环（`CanvasStage.vue`） | **核心差距，P0 抄** |
| 主题 | 24 套主题、17 角色 token、WCAG 对比度自动归一、色盲矩阵数学测试 | 玻璃霓虹 token + 主题/密度/动效模式 | 各有侧重，抄其对比度/色盲工程化测试 |
| 插件体系 | 四扩展点 + minGloom 版本兼容 + doctor CI + 市场 | 面板注册表（硬编码 21 面板） | 长期演进方向（P1/P2） |
| 券商抽象 | `BrokerAdapter`：导入持仓/预览订单/订阅行情/状态机 | broker-gw 文件单 + brokerif 包 | 抄 `previewOrder`/状态机语义 |
| AI | Ask Gloom：读当前面板语义上下文作答 + 命令栏 AI 补全 | M05 AI 智脑（四维评分） | 抄「面板上下文注入」与命令栏 AI |
| 告警 | 价格三态（above/below/crosses，re-arm 语义）+ 催化剂事件 + in-app/原生通知/badge 三 sink（活跃感知：活跃时只 in-app） | `utils/notify.ts` Tauri 原生通知已通、hub 收到 alert 即触发，但**无活跃感知（焦点内也响）**、规则不成引擎 | 抄活跃感知 + 规则引擎 headless 化 |
| 功能广度 | 120 功能插件（研报广度惊人） | 21 面板，聚焦 A 股超短线闭环 | 抄选题（见 §6），不抄广度本身 |
| 工程 | 5 套 tsconfig、架构边界 CI 测试（import 边界/静默 catch/UI 字面量 allowlist） | 三道 CI 门（typecheck/lint/build） | 抄 allowlist 式架构守护 |

## 3. 可抄清单（按优先级）

### P0-1 命令面板 + 彭博式助记符（gap 最大、体验提升最直接）

gloomberb 的一切功能入口是 `Ctrl+P` 命令栏，三类来源合并去重：`src/components/command-bar/commands/registry.ts`（内置命令 `{id, prefix, aliases, hasArg, argOptions, category, execute}`）、插件命令、以及大头——每个 pane 模板的 `shortcut` 助记符（如 chart-composer 的 `GP/GIP/CMP/GF/GE`）。

值得逐条抄的细节：

- **解析**：`matchPrefix`——输入转大写，prefix 按长度降序匹配，`前缀+空格` 后全部视为 arg。`argKind: "ticker" | "ticker-list" | "text"` 决定参数类型与补全行为。
- **模式化**：一个输入框三模式（BROWSE / FILTER / 命令执行），顶栏 badge 显示当前模式，Esc 逐级回退（AI 答案 → 抽屉路由 → 关栏）。
- **AI 补全**：≥3 字符去抖调 `/assist/command`，候选置顶 `Ask AI` 分组，Esc 先退回 AI 答案。
- **隐私纪律**：`recentPaneTemplateArg` 只允许 ticker 类参数进最近历史，自由文本绝不写入 config/云同步——防泄漏。我们对应：股票代码可进 recent，自然语言指令不进。
- **窗口身份推导（"再打开=聚焦，不新开"）**：模板声明 `createInstance` 返回 `instanceId`——单例功能返回固定 id、按参数分窗（id 含 ticker）、窗口内切内容三类策略；有回归测试自动把每个功能连开两遍防重复开窗。我们的 21 面板目前是单例注册表，引入命令后"信号面板·按股票分窗"需要这层身份推导。
- **F1 函数卡**：`builtin/help/function-index` 为每个功能生成「显示什么/有哪些键/数据多新鲜」卡片——对我们的 21 面板做同款的成本很低、对无人值守系统的可解释性价值高。

落地映射（**增强现有，非新建**）：现状已有 Ctrl+K 过滤式面板（`web/src/workspace/CommandPalette.vue`，面板/预设/命令三类 + `>` 命令态）和快捷键层（`shortcuts.ts`：B/S 买卖、Shift+T/K/M/R、1-9 切面板），缺的是**语法层**：面板模板声明助记符（`panel-registry.ts` 增加 `shortcut/argKind` 字段）、`matchPrefix` 式解析、ticker 类参数的 6 位代码联想（接 `symbol` pkg / stock_basic 搜索 API）、窗口身份推导（同面板同 ticker 聚焦不新开）、recent 只记代码不记文本。命令表从现在硬编码数组收敛为注册表。详细任务分解见 `gloomberb对标提升方案.md` G2。

### P0-2 行情刷新性能三板斧（盯盘体验的核心工程）

`src/market-data/frame-scheduler.ts`，我们 `ws/hub.ts → Pinia` 链路可直接对齐改造：

1. **统一数据帧时钟**：流 tick、store 写、监听器通知全落在同一帧 → 一波 tick = 一次 commit 一次 paint。GUI 端 rAF 对齐 ≥66ms（~15Hz）；document hidden 降 1Hz 兜底（告警、市值心跳照走）。终端固定 100ms（重绘贵）。
2. **负载自适应**：`FRAME_LOAD_FACTOR=4`——一帧引出的活（应用+commit+paint）最多占帧间隔 1/4，超了就拉长间隔而不是卡死输入；帧成本用 0.3 指数平滑，毛刺（GC）不影响估计；单帧任务最多重跑 4 轮。
3. **per-symbol 闪动钟**（`components/quote-flash.ts`）：价格变动闪 300ms + 300ms 冷静期（防止高频股常驻变暗更难读），`nextChangeAt` 精确 setTimeout 不轮询，无变化时不发新 Map 避免触发 render。
4. **行级 memo**：表格行 `getRowVersion` 只参与 memo 比较，单票 tick 只重渲一行；`renderCell` 经 ref 读数保持稳定。Vue 侧对应：`v-memo` + 行级 version stamp。

落地映射：`web/src/ws/hub.ts` 之后加一层 `data-frame.ts`（RAF 驱动 + load pacing + hidden 降级），行情/打板梯队/监控画布统一走它；盯盘面板报价行加 flash tracker。

### P0-3 告警引擎产品形态 + 三 sink 通知

gloomberb alerts 插件（`plugins/builtin/alerts/`）：`levels.ts`（价格水平位触碰）、`catalysts.ts`（事件日历临近）、事件流 pane + 历史 pane + headless 规则引擎（`alert-engine.ts` 与 UI 解耦，CLI 可跑）。通知侧 `notifications/app-notifier.ts` 三 sink：应用活跃=in-app toast；不活跃=桌面原生通知；宏徽标只放计数（0→空、>99→"99+"）永不放消息文本。

对应我们：告警规则的极简性值得学习——gloomberb 价格告警实际只有 above/below/crosses 三态（`alerts/alert-engine.ts`），crosses 有 re-arm 语义（`armedAt` 重置基线、触发字段白名单重建而非 spread）；我们的规则丰富度由后端 `pkg/risk`/`pkg/rules` 决定，不限三类。**现状**：`utils/notify.ts` 已通 Tauri 原生通知、hub `handlePush` 收到 alert 即触发——双 sink 雏形已有。**抄点**：① 补活跃感知（焦点内只 in-app toast，不活跃才走原生通知 + Dock badge 计数，gloomberb 徽标只放数字、>99 显示 "99+"，永不放消息文本）；② 告警规则引擎 headless 化（后端触发 → `pkg/outbox` → WS，前端只渲染）；③ 规则编辑丢弃触发态字段用白名单重建（防幽灵基线）；④ 通知历史已读态对齐（notify-center 已有）。详见 `gloomberb对标提升方案.md` G3。

### P0-4 pane 宪法 + 例外 allowlist 的 CI 守护

`docs/pane-conventions.md`（714 行 UI 宪法）：一个元素一种样式、全部 token 化、层级差 ≥15%、"扩展 kit 而非 fork"、改动对照全应用截图审。兜底是 CI：`src/architecture/` 下 `ui-literals.test.ts` + `ui-literals-allowlist.json`（UI 字面量例外必须登记且带理由，**计数只降不升**）、`import-boundaries.test.ts`（目录间 import 边界）、`silent-catches.test.ts`（静默 catch 登记）。

对应我们：`doc/开发文档/1.视觉需求.md` 是规范但没有 CI 兜底；`/dev/gallery` 走查是人工的。**抄点**：web 侧加 eslint 自定义规则或 vitest 守护——禁裸 hex 色值（必须走 tokens）、任一面板 JSON 快照对比、allowlist 计数单调降。

### P0-5 Ask Gloom 的面板上下文注入

`src/ask-gloom/` 很薄：`pane-context.ts` 把当前聚焦面板的语义化数据（它是什么 pane、当前 ticker、表格行、图表区间）抽成上下文喂给 AI 问答；命令栏 AI 补全同一后端。

对应我们 M05：LLM 四维评分是"推"的模式（定时产出信号），而"问"的模式（用户盯盘时就当前面板发问"这只票为什么跳水"）完全缺。**抄点**：在 biz 侧加一个 `pane-context` 采集（前端上报当前面板语义 → 后端拼进 prompt），这是让大模型从"评分员"变"副驾"的最短路径。

### P1-1 布局=纯值 + 命名布局/预设桌面（Desks）

gloomberb 布局是纯 JSON（`LayoutConfig{dockRoot 二分树, instances[], floating[], detached[]}`），所有变换是纯函数 → undo/redo、跨设备同步、市场分享、快照测试全免费。上层产品化：`Desks`（`src/layout/desks.ts`）= 面向交易风格的现成布局包（Rates desk、Options desk），每个 tile 等价于"敲一条命令会建出来的 pane"，与命令体系同一来源。

对应我们：dockview 序列化 + `layout.presets` 预设已存在（命令面板已可套用），缺的是：①布局 undo；②**面向交易时段的桌面包**——集合竞价桌面/盘面监控桌面/尾盘复盘桌面/盘前准备桌面，每桌 = 一组带参数面板的命令序列，且与命令面板同源（预设即命令脚本）；③布局跨窗口漂移检测（多屏 popout 已有，保存冲突语义待补）。详见 `gloomberb对标提升方案.md` G6。

### P1-2 BrokerAdapter 抽象对齐

`src/types/broker.ts` 的 `BrokerAdapter`：validate/importPositions/importPortfolioSnapshot/listAccounts/**previewOrder**/listOpenOrders/listExecutions/canStreamQuotes/subscribeQuotes/searchInstruments/getQuote/getPriceHistory + 状态机（connect/disconnect/getStatus/subscribeStatus）+ `configSchema` 声明式配置表单。

对应我们 `pkg/brokerif` + broker-gw CSV 文件单：**抄 `previewOrder` 语义**（下单前试算：价格/仓位/预计成交/风控预检的返回结构）和 **subscribeStatus 状态机**（文件单网关的写盘成功/终端读走/回报到达可以建模为同一套 connection status）。

### P1-3 数据缓存语义：stale 保留 + provenance

`types/persistence.ts`：每资源 `{staleMs, expireMs}` 缓存策略 + `fetchedAt/staleAt/expiresAt/sourceKey/schemaVersion/provenance` 元数据；`docs/quote-board-data.md` 把正确性写成纪律："stale 保留旧值 + footer 报 stale，绝不清空"、"live quote 更新行但不假装是新鲜的全市场排名"、FUT 板最多并发 4 批 5s 超时放弃慢批。

对应我们 datasource-health 面板：**抄语义而非代码**——所有行情/因子读路径标注 fetchedAt + stale 态展示；看板"旧数据置灰不消失"。

### P1-4 系列注册表（任何序列可与任何序列同图）

`src/series` + 插件 `series?: SeriesDefinition[]`：数据目录里的任意序列（价、量、宏观利率、days-to-cover/AAPL 这类带参序列）都可与其他序列同图叠加，chart-composer 只管组合。

对应我们 M02 行情与因子：**因子即 series**，因子-catalog 注册 → 盯盘/回测/复盘三处共用同一套绘图与取数。我们目前因子计算与图表耦合在不同面板里，值得抽。

### P1-5 remote 控制协议（自动化/AI 代理控制前端）

每个运行实例在 127.0.0.1 随机端口开 HTTP 服务，token 落在数据目录 endpoint 文件（`src/remote/server.ts`），语义树（semantic-tree）+ json-patch 做状态同步——测试、CLI、AI 代理共用这一个控制面。

对应我们：pyworker/research-worker 与前端无直接通道，E2E 测试也缺自动化钩子。**抄点**：Tauri 侧暴露一个 localhost 控制端点（读面板树/执行命令/截图），即可同时服务自动化测试与未来的 AI 代理操作前端。

### P1-6 主题工程化：对比度归一 + 色盲数学测试

我们视觉走玻璃霓虹路线（比它炫），但它有两点工程纪律值得抄：

- `normalizeTheme`：主题作者随便配色，系统把所有正文/正负色 `blendForContrastOnSurfaces` 拉到 WCAG 4.5/3.6 下限——**写法自由、可读性兜底**。
- `color-vision.test.ts`：用 Machado-Oliveira-Fernandes 矩阵在线性 sRGB 模拟三种色盲，要求涨/跌色 OKLab 距离 ≥0.15，对全部主题跑 AAA。我们红涨绿跌在色觉障碍下的可读性应加同款 vitest。

### P2-1 插件体系四扩展点（长期）

`GloomPlugin` 接口（`src/types/plugin.ts`）：`panes / paneTemplates / broker / capabilities / series / slots / cliCommands / configSchema`，配套 `minGloom` 版本兼容声明、`plugin doctor`（CI 可跑的插件体检）、GitHub 直装 + 官方插件自更新。我们的 21 面板registry 是硬编码的；短期不需要市场，但**面板定义可向其 PaneDef 形态收敛**（id/name/component/defaultPosition/tickerSource/tickerFollower/identitySettings），为将来策略/数据源插件化留口。

### P2-2 telemetry 分级 + 诊断脱敏导出

崩溃报告/用量计数默认开、Attention Counts 默认关且独立同意弹窗；`DO_NOT_TRACK` 环境变量一键全关；`feedback/redact.ts` 诊断打包前脱敏（家目录→`~` 等）。我们单机私有部署不上报，但**诊断包导出**（运维排障时一键打包日志+配置脱敏版）可抄 `feedback/diagnostics.ts`。

## 4. 功能选题库（A 股可迁移的插件/功能，按价值排序）

120 个内置插件里筛出对超短线 A 股可迁移的：

| gloomberb 功能 | 迁移价值 | 对应我们的模块 |
| --- | --- | --- |
| market-heatmap（板块热力） | 高：情绪面核心视图 | M02/盯盘（viz/echarts 可做） |
| seasonality（季节性/日历效应） | 高：A 股日历效应强 | M02 因子 |
| market-movers（涨跌动量榜，"live 更新行但不假装新鲜排名"纪律） | 高 | 盯盘 |
| fear-greed（恐惧贪婪指数） | 中：可做 A 股情绪复合指标 | M03 |
| relative-rotation（板块相对轮动 RRG） | 中：板块轮动视角 | M02/M06 |
| pe-band（估值通道） | 中（长线向，可作过滤条件） | M06 |
| scanner（全市场扫描器，条件组合） | 高：= 我们的选股信号前置 | M06 |
| kelly-sizer（凯利仓位） | 中：仓位规则参考 | M08 |
| short-interest/short-volume | 低：A 股两融数据可做对应物 | M02/M03 |
| catalysts（催化剂日历：财报/FDA/临床试验等） | 中：对应 A 股=业绩披露/解禁/分红除权日历 | M03 情报 |
| earnings-ripple（业绩涟漪：同业联动） | 中：A 股"同板块跟风"模型 | M05/M06 |
| supply-chain（供应链图谱） | 低 | M04 |
| macro-day（宏观日历日视图） | 中：已有 tradecal，补宏观事件层 | M03 |

## 5. 明确不抄的部分

- **OpenTUI 终端渲染 / kitty protocol / braille fallback**：终端是自渲染单元格的整套栈，与 Tauri+Vue 路线冲突，复杂度极高且无收益。
- **Gloom Cloud 中心化数据/账号/团队协作云同步**：我们是私有化单机部署；cloud 目录、teams、sync、layout-marketplace 的云侧不动。
- **Bun/electrobun/五套 tsconfig 工具链**：不引入。
- **120 功能的广度本身**：它的"全资产全市场"广度是商业模式（订阅）驱动的；我们聚焦超短线闭环，选题按需抄，不铺广度。

## 6. 建议落地顺序

1. **P0-2 数据帧调度器 + 闪动钟**（纯前端、无依赖、盯盘体验立升）→ 列入监控深化线。
2. **P0-1 命令面板**（Ctrl+P + 助记符 + 股票联想）→ 借 panel-registry 扩展 shortcut 字段，先行最小版。
3. **P0-3 告警三 sink + headless 规则引擎** → 对齐 M10。
4. **P0-4 UI 字面量 allowlist CI** + **P1-6 色盲测试** → 并入 web CI 门。
5. **P0-5 面板上下文注入 LLM** → M05 交互化起点。
6. **P1-1 预设桌面 Desks**（竞价/打板/复盘三套）→ workspace 层。
7. 其后按 M 模块节奏吸收 P1-2/3/4/5 与功能选题库。

> 本文件与 `参考-ai-hedge-fund.md`、`参考-nautilus-trader.md` 并列；逐项落地时在各模块 `开发进度.md` 里引用本节编号（如「参考-gloomberb §3 P0-2」）。
