# gloomberb 对标提升方案（G 序列）

> 版本：v1.0｜日期：2026-10-18｜状态：待评审
> 依据：[`参考-gloomberb.md`](参考-gloomberb.md)（分析底稿）+ gloomberb v0.17.6 源码（副本在 `research-worker/gloomberb/`）。
> 定位：与 N 序列（`量化智脑与自动交易总体实施方案.md`）**并行**的体验与工程质量线。**不阻塞 N9 实盘准入**；G1–G5 不等 N 序列完成。
> 编号约定：G1–G8 排期实施；G9 为瞭望项不排期。各模块落地时在对应 `M**-*/开发进度.md` 引用本文件章节号。

## 0. 目标与边界

把 gloomberb 的可抄项变成可执行、可验收的任务，只取三类价值：①盯盘实时体验（帧纪律）；②键盘与命令效率（终端级交互密度）；③工程守护（把"约定"升级成"CI 强制"）。

**明确不做**（重申 参考-gloomberb.md §5）：不碰终端渲染栈、不做云端账号/团队协作、不引 Bun、不铺 120 功能广度。

**与现有资产的关系**：本方案多数是**增强已有雏形**而非新建——命令面板（`workspace/CommandPalette.vue`）、快捷键（`shortcuts.ts`）、布局预设（`layout.presets`）、Tauri 原生通知（`utils/notify.ts`）、ws 断线恢复（`ws/hub.ts`）均已存在，G2/G3/G6 在其上升级。

## G1 数据帧调度器与行情跳动节律（P0）

> 状态：**已实施（2026-10-18）**。落地范围按真实代码面调整：全前端**没有**逐 tick 行情渲染消费方（tick 负载无人消费；面板推送→整表 REST 重拉），故本期交付统一帧调度 + 闪动钟基础设施，并将涨停梯队的手写节流收编；闪动钟待盯盘深化的逐 tick 表格接入。实施记录：[M02 开发进度 G1 节](M02-行情与因子/开发进度.md)。

**需求**：盘中 tick 高峰时，行情/涨停梯队/自选扫描每一次 ws 推送都直接触发一轮 Vue 渲染（`ws/hub.ts` `acceptData` 逐条 `await Promise.all(listeners)`，无合帧），价格高频跳动时 UI 与输入争抢；监控画布（`CanvasStage.vue`）已有自有 rAF 循环但与其他面板不共用一套时钟。

**设计要点**（抄 `research-worker/gloomberb/src/market-data/frame-scheduler.ts`）：

- 新增 `web/src/ws/data-frame.ts`：单一数据帧时钟，驱动器=rAF（≥66ms，约 15Hz）；`document.hidden` 时降 1Hz 兜底（告警与市值心跳不断）；无 rAF 环境（测试）退化为宏任务合帧。
- 负载自适应：一帧引出的应用+提交+绘制工作最多占帧间隔 1/4（`FRAME_LOAD_FACTOR=4`），超限自动拉长间隔；帧成本 0.3 指数平滑，毛刺不影响估计；单帧内排队任务最多重跑 4 轮。
- 接入点不改 hub 协议：hub 仍为唯一真相源；行情类消费方（market/signal/limit-ladder/watchlist）改为经 `dataFrame.schedule(apply)` 应用数据，store 写与通知落在同一帧。
- 闪动钟 `web/src/utils/quote-flash.ts`（抄 `components/quote-flash.ts`）：per-symbol 变动闪 300ms + 300ms 冷静期；`nextChangeAt` 精确调度，不轮询；无变化不产新对象避免无谓重渲。涨跌色用既有 `--nt-up/--nt-down`（`theme/tokens.ts`）。
- 行级渲染：长列表行组件 `v-memo` + 行 version stamp，单票 tick 只重渲一行（对照 gloomberb `getRowVersion` 行级 memo）。

**验收**：① vitest 覆盖帧调度（合帧、hidden 降级、负载降频）与闪动钟（节律、无变化免渲染）；② 盯盘面板在 1k tick/s 回放注入下输入延迟无明显劣化，CPU 占用较现状下降；③ `/dev/gallery` 矩阵走查通过；④ 关闭页面可见性时告警 toast 与原生的桌通知仍工作。

**规模**：前端 2 新文件 + 3–4 个面板接线，预计 2–3 天。无前后端协议变更。

## G2 命令面板增强：助记符语法与股票联想（P0）

> 状态：**已实施（2026-10-19）**。助记符语法核心（`workspace/command-grammar.ts` 纯函数）+ 21 面板 shortcut 元数据 + recent 纪律（只收 6 位代码）+ 命令面板接线交付：`GP 600519` 开行情（SH/SZ/BJ 前后缀归一）、`JY buy` 预填买单（与 B/S 热键同一参数通道，已挂面板即时推参=窗口身份纪律）、页脚功能卡随聚焦行出描述与用法；prefix/aliases 唯一性与全表快照由完整性测试锁定。**范围诚实调整**：股票名称/拼音联想本期不做（后端无代码目录检索接口，不做假数据源，待 M02 侧补目录后增量接入）；独立 F1 帮助浮层同日补版交付（定格当前卡 + 全局热键表 + 目录过滤；Esc 单关/open 去重随审查闭环加进 useOverlay 栈），palette「帮助 <助记符>」入口同日二次补版交付（帮助态整段接管结果池、精确卡置顶、Enter 开浮层定格该卡）。实施记录：[M12 开发进度 G2 节](M12-桌面客户端/开发进度.md)。

**需求**：现有 CommandPalette 是 VSCode 式过滤（面板/预设/命令三类 + `>` 命令态，命令表硬编码十来条），缺彭博式「助记符 + 参数」语法：敲 `GP 600519` 直接开贵州茅台 K 线、敲 `DB` 开打板梯队，是终端级交互密度的核心。

**设计要点**（抄 `src/components/command-bar/commands/registry.ts`、`matchPrefix`、`pane 模板 shortcut`）：

- `panel-registry.ts` 的 `PanelDef` 增加 `shortcut?: { prefix: string; aliases?: string[]; argKind: 'ticker' | 'ticker-list' | 'text' | 'none' }`；命令来源收敛为三类的**注册表**（面板助记符 > 内置命令 > 预设/动作），去重按优先级（抄 dedupeById 顺序语义）。
- 解析：输入去空格段——首段转大写按 prefix 长度降序匹配，剩余为 arg。`argKind=ticker` 时弹股票联想（6 位代码/拼音首字母/名称子串，数据源走 `web/src/api` 既有搜索接口，`symbol` 包兼容 SH/SZ 后缀）。
- 窗口身份：同面板 + 同 ticker 再打开 = 聚焦更新参数，不新开实例（抄 pane-conventions §8b 三类策略：单例/按参分窗/窗内切换）。
- recent 纪律：recent 列表只收 ticker 类参数，自由文本（如复盘备注）**绝不**写入持久化/同步（防泄漏，抄 `recentPaneTemplateArg`）。
- F1 功能卡：每个面板一张「显示什么/有哪些热键/数据多新鲜」卡片，注册表自动生成（抄 `builtin/help/function-index`），入口在面板菜单与命令面板 `帮助 <助记符>`。
- 建议助记符表（首版，可评审）：`HQ` 行情、`GP` K线(带 ticker 参数)、`DB` 涨停梯队、`XG` 信号、`HY` 自选扫描、`FK` 风控链、`JY` 交易(带 buy/sell arg)、`CB` 晨报、`BR` 模型链路、`ZS` 自定预设…英文别名可选挂。

**验收**：① 纯键盘完成「GP 600519 开图 → B 买入预填 → Shift+K 急停查看」全程；② 连开两次 `HQ` 不产生重复实例；③ recent 不含任何非代码文本；④ 命令注册表与 F1 卡片有快照测试。

**规模**：注册表扩展 + 解析器 + 联想组件 + 身份推导，3–4 天。无后端变更。

## G3 告警活跃感知与规则引擎 headless（P0）

> 状态：**部分实施（2026-10-19）**。后端规则语义切片交付（同日一切）：`pkg/alert` 三态评估、re-arm 白名单重建、坏存储显式报错，单测（race）覆盖三态与 re-arm。运行时接线交付（同日二切）：`alert_rule` ent 表、`biz.AlertCenter` 评估中心（2s 节流、批量快照、零写库短路、单规则失败隔离）、Runner 快照节拍+3s poll 双路 Tick、`ApplyAlertStep` 乐观 CAS 单事务落库+`notify.request` outbox（category=alert-rule；评估期间的 Edit/Rearm 或并发先触发会让读面过期、整步作废不落库不投 outbox，节拍落库不改写 operator），5 条 CRUD proto-first（`/risk/v1/alert/rules` 系，经 admin 前缀转发直达，`go test -race` 全绿）；实施记录：[M08 开发进度 G3 节](M08-风控/开发进度.md)。前端切片交付：deskNotify 活跃感知三 sink（活跃仅 in-app toast / 不活跃走原生 / 无壳兜底 in-app，severity 映 toast 色调，hub 仅透传第三参）、toast 栈上限 5 + 按级 TTL、任务栏标题未读徽标（零依赖替代 OS badge）、notify-center 逐条已读/未读 + 全部已读（localStorage 持久化，坏数据容错）；**同日三切**：规则管理前端面板全件交付（`api/alert-rules.ts` 双口径归一 + 写口全量整换客户端、`panels/risk/alert-rules.panel.vue` 列表 + 新建/编辑/重置布防/两步删除 + 15s 轻轮询读面，12 项测试；注册进 panel-registry 因该文件属并行会话在途脏态延后微提交）；实施记录：[M10 开发进度 G3 节](M10-通知/开发进度.md)。后端已读回写接口未做。

**需求**：现状 `ws/hub.ts` `handlePush` 收到任何 alert/notify 都直接 `deskNotify` 原生通知——人在屏幕前也被系统响铃打扰；规则触发逻辑前后端边界未明，缺 re-arm/防重复语义。

**设计要点**（抄 `notifications/app-notifier.ts`、`plugins/builtin/alerts/alert-engine.ts`）：

- **三 sink + 活跃感知**：应用活跃（窗口聚焦且未 hidden）→ 仅 in-app toast；不活跃 → Tauri 原生通知；Dock/任务栏徽标只放未读计数（0 空、>99 显 "99+"，永不放消息文本）。活跃判定复用 monitor 已有的 `visibilitychange` 机制。
- **规则引擎 headless**：触发评估全部在后端（`pkg/risk` 盯盘与独立 alert 规则），前端只渲染事件流与历史；WS alert 主题沿用现有 epoch/seq 恢复协议。
- **规则数据模型**：价格三态 above/below/crosses（crosses 带基线价与 `armedAt`，re-arm 重置基线）；编辑/重建规则用**白名单字段重建**而非 spread，触发态字段清零，防幽灵基线；坏存储显式报错而非静默当空表（抄 `readAlertsStoreError`）。
- 通知历史 notify-center 补已读/未读与全部已读。

**验收**：① 聚焦应用时触发告警只有 in-app toast、无原生弹窗；最小化/锁屏后同事件只走原生；② re-arm 后以新基线判定，单 tick 不重复触发；③ 后端规则单测覆盖三态与 re-arm；④ 毁坏的规则存储在前端报明确错误。

**规模**：后端规则评估小模块 + 前端 sink 分流，2–3 天。涉及 M08（风险规则复用）与 M10。

## G4 视觉工程化守护（P0）

> 状态：**已实施（2026-10-18）**。守护双套件 + 色觉安全模式交付：ui-literals 扫描（countLock 从 0 起档，存量 3 处裸色已收编 --nt-danger）；Machado/OKLab 色觉门禁对默认与安全两组四块 up/down、danger/success 硬卡 ≥0.15（实测默认红涨绿跌本就达标；warn 弱项 2026-10-19 已加刀：up-warn 暗 0.044→0.080、亮 0.088→0.133 并升级为四类二色觉硬地板，暗 #ffe852 / 亮 #705600 黄琥珀定色，glow 派生同源守护）；安全模式朱红/钴蓝经全候选模拟测距选型，走 `data-colorvision` + ConfigCenter 开关。实施记录：[M12 开发进度 G4 节](M12-桌面客户端/开发进度.md)。

**需求**：`1.视觉需求.md` 是纸面规范、`/dev/gallery` 是人工走查，没有人能拦住"新面板裸写 hex 色值"。

**设计要点**（抄 `src/architecture/ui-literals.test.ts` + allowlist、`color-vision.test.ts`、`normalizeTheme`）：

- web CI 加 **UI 字面量守护**：eslint 规则或 vitest 扫描 `panels/`、`display/`、`viz/` 禁裸 hex/rgb 字面量（必须走 `--nt-*` token），例外登记 `ui-literals-allowlist.json` 且**计数只降不升**（每 PR 检查）。
- **色觉无障碍测试**：vitest 实现 Machado-Oliveira-Fernandes 三色盲模拟矩阵，对暗/亮两套主题的 `--nt-up/--nt-down/--nt-warn` 要求 OKLab 感知距离 ≥0.15（红涨绿跌在色盲下仍须可辨）。
- 主题对比度归一可选做：token 生成侧加 WCAG 4.5 下限校验函数，超标在测试期报而不改色。

**验收**：① 新增裸色面板的 PR 被 CI 拦下；② allowlist 计数上升的 PR 失败；③ 色盲测试对现有两主题全绿；④ 视觉走查结论从"人工记忆"变成"CI 报告 + 人工抽查"。

**规模**：eslint 规则 + 两个 vitest 套件，1–2 天。

## G5 面板上下文注入 LLM（P0）

> 状态：**部分实施（2026-10-19）**。前端首版交付：`workspace/pane-context.ts` 附件纯库（账面键/敏感键正则剔除、仅标量 ≤8、值 ≤80、deny 面板 settings 参数整片不出、嵌入段 ≤800 尾部裁对，验收②快照测试锁定）+ panel-bus `peekPanelParams` 参数快照回读（pending/推送两路同落、只读副本）+ `WorkbenchApi.activePanelId` 聚焦面板回读 + 命令面板「问 AI」：≥3 字符且无助记符命中时候选置顶，Enter 发送答案内嵌面板呈卡（问题/正文/证据/模型/随发上下文一句话摘要），Esc 先清回答层再关面板，Esc/重开后迟到响应按序号作废。**范围诚实调整**：附件经规范化文本内嵌问题送达——AskRequest proto 无 pane 字段，不发明结构化通道（字段化入 proto + brain topic 推流渲染留给后端窗口）；gloomberb 式去抖活体校验候选不做（选中才调用，省本地算力）；面板旁浮层延后。验收①「回答含正确代码与数据」中「随问随带面板参数」已由前端保证，模型正确性依赖 M05 联调（vLLM 未就绪，见 M05 阻塞条）。实施记录：[M05 开发进度 G5 节](M05-AI智脑/开发进度.md)。

**需求**：M05 目前只有"推"模式（定量产出评分信号），无"问"模式——盯盘时针对当前面板发问「这只票为什么跳水/这条风控链卡在哪」是 AI 副驾最短路径。

**设计要点**（抄 `src/ask-gloom/pane-context.ts`、`assist/model.ts`）：

- 前端 `buildPaneAttachment(focusedPane)`：抽取**聚焦面板语义**——面板 id/标题、当前 ticker、≤8 个标量参数（数值/短字符串），附隐私纪律：upsert 键长/值长上限、过滤布局簿记键（scroll/width/sort 等，gloomberb 用 `BOOKKEEPING_KEYS` 正则表直接抄）、面板声明的 private 字段不出参数。
- 后端 `pkg/llm` 增加 `AskWithPaneContext` 入口：pane 附件 + 用户问题拼 prompt，走既有 vLLM 网关；回答以脑链路主题（brain topic）推送，前端在发问面板旁浮层渲染。
- 命令面板集成：查询 ≥3 字符时去抖调同一接口给「问 AI」候选置顶（Esc 先退回答案再关面板）。

**验收**：① 在行情面板聚焦 600519 时问「这个面板的股票今天涨多少」回答含正确代码与数据；② 簿记键与 private 字段不出现在上下文（快照测试）；③ 全程走本地 vLLM，无外发；④ 复用 brain 主题 epoch/seq 恢复无新协议。

**规模**：前端附件构造 + 后端一个 biz 用例 + 面板浮层，3–4 天。依赖 M05 llm 网关（已有）。

## G6 交易时段桌面包（P1）

> 状态：**已实施（2026-10-19）**。四桌（盘前准备 / 竞价监控 / 打板作战 / 盘后复盘）+ Shift+1..4 切桌 + 布局 undo/redo（Ctrl+Z / Ctrl+Shift+Z，容量 20）交付。desk = 面板实例+参数+落位声明（与 `DEFAULT_LAYOUT` 同一形态），套用 = 记录快照 → clear → 按声明 addPanel（参数走 panel-bus，竞价桌预绑 `GP 000001` 由此直达）→ 聚焦；**不手写 dockview toJSON**——布局永远走验证过的构建管线（范围调整：比「desk 存独立 JSON」少一层序列化脆弱面）。撤销只在程序化整版切换时记录（手拖不入栈），新切换清空重做栈。入口三处同源：热键 / 命令面板 desk 行 / CommandBar 预设菜单顶部「交易时段桌」区。切桌销毁浮窗、桌内全 docked，验收④天然成立。desk 定义快照 + 历史栈语义测试锁定（新增 17 项）。实施记录：[M12 开发进度 G6 节](M12-桌面客户端/开发进度.md)。

**需求**：超短线一天四种工作面（盘前研究/竞价盯盘/盘中打板/盘后复盘），每次手拖 dockview 是低效常态。

**设计要点**（抄 `src/layout/desks.ts`）：

- `layout.presets` 升级为**桌面包**：每个 desk = JSON（dock 布局 + 面板实例 + 参数，如行情桌面预绑 `GP 000001`），与命令面板同源——套用 desk 等价于顺序执行一组面板命令。
- 首版四桌：盘前准备（晨报+数据源+股池）、竞价监控（行情+涨停梯队+自选扫描）、打板作战（行情+信号+交易+风控链）、盘后复盘（回测+影子进化+风控日报）。
- 布局 undo/redo（dockview 序列化栈，容量 20）。
- Shift+1..4 快速切桌（复用 `shortcuts.ts` 动作表）。

**验收**：① 四桌一键切换且面板参数正确落地；② undo 三步内还原；③ desk 定义快照测试防劣化；④ popout 白名单约束（trade/settings/ops 禁弹）在桌面包内同样生效。

**规模**：2 天（presets 管线已通，主要是 desk 数据与快捷键）。

## G7 brokerif 对齐：previewOrder 与状态机（P1）

> 状态：**部分实施（2026-10-19）**。后端交付：`pkg/brokerif.PreviewOrder`（R01–R15 逐条预检 + 金额/费用/现金干跑）与 `Tracker`（idle→connected→degraded→down 状态机）；`app/risk` 引擎侧 `Engine.Preview` 已接——与 Check 同装配同守卫、零副作用（无审计/预约/计数）；proto-first 暴露已接——`PreviewOrder` RPC（请求与 CheckOrder 同型，回包带逐条风控结论 + 金额/费用/现金），前端经 admin 前缀聚合 `POST /api/risk/preview` 直达（免改网关）。UI 验收①已接——交易面板表单合法自动预演摘要（可下/拦截 + 金额/费用/现金影响 + 未过规则标红），落单前再以全新试算强制复核，拦截或试算不可用一律不生成委托。适配器状态上报已接（验收②③）——`brokerif.Observer` 统一两通道折算口径（连败 3 停机、idle 期失败不降级），remote 全链路可选 Track 记账 + Go TLS 夹具全程回放迁移（②），sim 侧 Runner 快照心跳 + 迁移发布 `trade.channel.status`（JetStream 留痕）；remote 刻意不实现下单 Adapter（CSV 发布≠下单成功），双实现落在状态口径上。通道状态对外读面已接——`GET /v1/trade/channel/status`（proto-first，admin 前缀直达），wire 单例 Tracker 由 Runner 心跳记账、service 读面，只回真实跟踪的 sim 通道（emfile 服务端未接线不虚报）；交易面板头徽标已接——状态色 token 呈现、读取失败显式「状态未知」不沿用旧值。真实终端心跳验收未做；实施记录：[M09 开发进度 G7 节](M09-交易执行/开发进度.md)。

**需求**：下单前没有"干跑试算"入口；文件单网关（写 CSV → 终端读走 → 回报）的状态对 UI 不够一阶。

**设计要点**（抄 `src/types/broker.ts` `BrokerAdapter.previewOrder / getStatus / subscribeStatus`）：

- `pkg/brokerif` 增加 `PreviewOrder(req) → {价格、手数、预计金额、费用估算、风控预检结果(R01–R15 逐条 pass/block)}`，模拟撮合与东财文件单共用；UI 交易面板下单前强制展示 preview 摘要。
- 连接状态机：`idle → connected → degraded → down`，文件单侧映射为"目录可写/终端读取中/回报超时"；`subscribeStatus` 推 position/alert 主题既有链路。
- 与 allocation 包（只读份额试算）边界：allocation 管"分多少"，preview 管"能不能下、下下去什么样"，不落单。

**验收**：① 风控链任一规则 block 时 preview 明确标红并禁提交；② 状态机迁移在 broker-gw 集成测试可重放；③ 模拟/实盘两 adapter 行为一致（同接口双实现）。

**规模**：后端 2–3 天 + 交易面板半日。不触碰实盘授权边界（N9 前置不变）。

## G8 读模型缓存语义与因子序列化（P1）

**需求**：行情/因子读路径对"数据多旧"不可感知；因子计算散落在各面板，无法同图叠加。

**设计要点**（抄 `types/persistence.ts`、`docs/quote-board-data.md` 纪律、`src/series` 注册表）：

- 所有行情类 REST/WS 读模型带 `{fetchedAt, staleAfter}`；UI 侧 stale 数据**置灰保留 + footer 显式报 stale**，绝不清空（抄 quote-board-data 纪律）；慢批超时放弃该批保留已得行（5s 上限）。
- 因子目录：`pkg/market` 的指标产出登记为 **series 定义**（id/参数/单位/取数器），前端 viz 图表组件按 series-id 取数，任意因子可与价格同图叠加——回测、盯盘、复盘三处共用一套取数与绘图。

**验收**：① 数据源停供 60s 后看板呈 stale 态而非空白；② 两个因子 + 价格同图渲染 demo 面板；③ series 注册表现状文档化进 M02 设计文档。

**规模**：前后端各 2 天。属渐进改造，可与 G1 同批面板接入。

## G9 瞭望项（不排期，仅立设计卡）

- **前端控制协议**（抄 `src/remote/`：localhost 随机端口 + token 文件 + 语义树 + json-patch）：为 E2E 自动化与未来 AI 代理操作前端预留；放 M12 后续版本评议。
- **面板插件契约收敛**：`PanelDef` 向 gloomberb `PaneDef` 形态（id/name/component/defaultPosition/tickerSource/tickerFollower）靠拢，为策略/数据源插件化留口；借 G2 注册表扩展顺带完成一半。
- **诊断脱敏导出**（抄 `feedback/diagnostics.ts` + `redact.ts`，我们后端已有 `pkg/redact`）：运维一键打包日志+配置脱敏版 ZIP。
- **功能选题库**：板块热力图、market-movers 动量榜、季节性因子、恐惧贪婪复合情绪、板块轮动 RRG、A 股催化剂日历（解禁/业绩披露）——按 参考-gloomberb.md §4 表逐季评，进 M02/M03/M06 需求文档。

## 10. 里程碑与依赖

| 批次 | 内容 | 预计 | 依赖 |
| --- | --- | --- | --- |
| 批 1（本周可启） | G1 数据帧、G4 视觉守护 | 3–4 天合 | 无 |
| 批 2 | G2 命令语法、G6 桌面包 | 5 天合 | G1 无；G6 借 G2 注册表 |
| 批 3 | G3 告警、G5 面板上下文 LLM | 6 天合 | G5 依赖 M05 llm（已有） |
| 批 4 | G7 previewOrder、G8 缓存语义 | 5 天合 | G7 依赖 M08/M09 现状稳定 |

每批交付按记忆规则附带 UI 走查（对照 1.视觉需求.md）；每 G 完成同步其涉及模块的 `开发进度.md` 并勾掉本文件状态。

## 附：关键证据钉点

gloomberb 侧：`src/market-data/frame-scheduler.ts`、`components/quote-flash.ts`、`components/command-bar/commands/registry.ts`、`plugins/builtin/alerts/alert-engine.ts`、`notifications/app-notifier.ts`、`ask-gloom/pane-context.ts`、`architecture/ui-literals.test.ts`、`types/broker.ts`、`types/persistence.ts`、`layout/desks.ts`、`docs/pane-conventions.md`、`docs/quote-board-data.md`。
本项目侧：`web/src/ws/hub.ts`（无合帧点）、`web/src/workspace/CommandPalette.vue`、`web/src/workspace/shortcuts.ts`、`web/src/utils/notify.ts`、`web/src/theme/tokens.ts`、`web/src/workspace/panel-registry.ts`、`backend/pkg/risk`、`backend/pkg/brokerif`、`backend/pkg/llm`、`backend/pkg/outbox`。
