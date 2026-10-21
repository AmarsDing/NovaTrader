# TSP 与 new-term-radar 代码级对比与借鉴建议（2026-10-21）

> 参考仓：`research-worker/tick-stock-panel`（TSP，1111 文件，shallow clone）、`research-worker/new-term-radar`（radar，56 文件）。均只读分析，未执行其代码。
> 方法：6 路代码级深读 → 4 路对照 NT 代码映射（39 条建议）→ 两轮对抗性证伪全闭环（pass1 14 条＝5 KEEP / 9 DOWNGRADE，明细见第 8 节；pass2 22 条＝5 KEEP / 17 DOWNGRADE / 0 REJECT，明细见第 9 节）→ 完整性批判补 7 个被漏角度。
> 口径：KEEP=属实且可直接排期；DOWNGRADE=方向对但论据/定档有误，按「修正版」执行。39 行全闭环（D3/D4 已并入 B6，B7 随 D1 预标降档）；排期权威见同目录《TSP与radar借鉴-升级总方案-2026-10-21.md》。

## 0. 摘要（先看这节）

**最值得做的三件事**（KEEP，按价值排序）：

1. **题材雷达移植**（本次最大金矿）：radar 的「多信号早探 → LLM 名词判定 → 每日机会表 → T+14 相对基线复盘 → 按信号源归因回写晨报」整环，逐信号给出 A 股等价物与 intel/biz/theme 落法。NT 现在 HotWords 只答「热」，答不了「新」与「几个独立通道印证」。详见第 5 节。
2. **用户自定义选股器（白名单公式 DSL）**：TSP 的 EBNF→AST→E001-E016 语义码→表达式编译链路完整可抄（`dsl.py` 负 shift 未来函数编译期拒）。NT 选股只有 pkg/rules 内置模板调参，M06 停在模板层。核验 KEEP，修正：结果不入 strategy_candidates 官方漏斗，新建 user_screens 表隔离，入选再走现有风控漏斗。
3. **预测命中复盘升级**：prediction_check 现绝对口径（pct>0）+ T+1 单日 + source 硬编码 briefing。改「相对自身 20 日波动基线突破」hit 判定 + lead_days + T+3/T+5 复检 + 按信号源 60 天滚动命中率回写晨报页脚——「哪个源值得信」从观点变成数据。

**第二波（pass2，同日）**：P1/P2 未核验 22 条全部进同款对抗队列（7 组按文件邻域尽调 → 7 组逐条 ≥2 处 path:line 复核后双镜证伪），定判 5 KEEP（D2/D8/R6/R10/U10）/ 17 DOWNGRADE / 0 REJECT——方向全对、论据或定档普遍有伤（错引 6 处、撞既有权威单源 5 处、新依赖审批闸 2 处、并行在途撞车 3 处）。逐条修正版已回写第 2–5 节表格「核验/要点」列，勘误清单与定判索引见第 9 节。

**结论**：两仓与 NT 业务高度同构但取向互补——TSP 胜在「数据/策略/因子」的密度与开放工程纪律，radar 胜在「早发现+延迟真值复盘」的管道语义。NT 独有的风控闸→东财 CSV 文件单→模拟 20 日准入闭环两仓均无对应物，第 6 节明确这片不参照任何参考仓。

## 1. 两仓画像（代码级）

### 1.1 TSP（tick-stock-panel）：自托管 A 股量化工作台

- **后端核心**：单进程 FastAPI（uvicorn），main.py lifespan 顺序拉起 DataStore→数据源→能力探测→QuoteService 轮询（默认 6s）→APScheduler→策略引擎→扩展。四层 api/(30+ 路由）→services→tickflow（存储抽象/SDK 适配）→data_providers。
- **存储**：零外部数据库——date=* Hive 分区 Parquet 只追加 + DuckDB 只读视图冷查询 + 进程内存 Polars DataFrame 热缓存（ms 级全市场扫描的来源：~5500 行 enriched 全指标列，screener 表达式过滤 10-50ms）。
- **业务**：策略=.py 文件（META+basic_filter+scoring+ENTRY_SIGNALS）经 AST 白名单四道闸热加载；61 因子 FactorSpec 单一事实源（id/label/group/kind/deps/warmup/pit/direction）；DSL 编译器 EBNF→AST→语义检查→Polars Expr；挖掘=spawn 子进程嵌套 walk-forward + purge30/embargo5 + Beam 搜索 + 发布门槛（OOS Sharpe≥0.5/回撤≥-25%/交易数≥60/正折≥2/3）；监控 9 类 rule_type 统一引擎 + cooldown + scope；模拟盘 fills.jsonl append-only 台账重放物化。
- **前端**：React18+Vite5+TS，react-query 为唯一状态源 + SSE 失效前缀；双层扩展——源码级 custom/<ns>/extension.tsx 经 import.meta.glob 自动发现+注册表整体拒挂保护，运行时 page-pack 单文件 ES 模块上传（5MB 上限、apiVersion 契约）；12 个页面级画面（limit-ladder/concept-analysis/paper/monitor/review…）。
- **开放与工程**：同一能力三壳（UI SPA / 65+ Open API / MCP 12 工具 stdio 薄桥）；网关 _RULES 规则表唯一契约源 + openapi tier 快照测试冻结 68 端点；Token 五件套（前缀明文一次/SHA-256/默认只读/admin 永不签发/吊销即拒）；网络门禁先于鉴权；2400+ 测试含生产保障门类（watchdog 死开关、atomic 写重试、rate-limit 预算、job stall）。

### 1.2 radar（new-term-radar）：多信号新名早探雷达

- **管线**：双 cron 驱动 9 信号采集器（X 双闸/HF trending 基名归组/GitHub breakout <14天&日均200星或日增400/sitemap 词涌 ≥3新URL 共享词+lift/新站双日期反作弊/sellers 三分之二噪声规则/revenue/asks/GSC），各出归一 terms[]；gzip-JSON last-good 快照差分求新（抓失败保旧，存量不冒充新）；跨信号按 term 合并；SQLite leads 落账（first_seen 不可变）。
- **词表引擎**：names() 强名启发式（内大写/含数字/驼峰=铁证名）→ judge.py LLM 批打分（25 词/块，只排序不拦截，老熟名牌固定 0.5，fail-open）→ usecases 五步（evidence→propose→screen→量势测量→competition）把新名翻成通用搜索词出 prioritize/watch/skip。
- **复盘真值**：review.py 14 天后按变体逐查 Trends，**相对自身基线**判 hit/miss/no_data（peak≥max(2×base, base+10)，弃末 2 日未结算），记 lead_days；store.summary 60 天滚动按信号源算命中率+提前天中位数**回写日报页脚**；每调用 cost_micros 记账，贵信号 weekday 周闸。
- **代理面**：CLI/daily cron/MCP stdio 三入口共用 config+state/；MCP 工具描述写明价格与秒数；3 篇 SKILL.md 教代理诚实调用（「no rise seen」≠「did not rise」）；版本五处一致由测试断言。

## 2. 业务能力借鉴（10 条）

| # | 建议 | 定档 | 核验 | 落点 |
|---|------|------|------|------|
| B1 | 因子注册表单一事实源（FactorSpec 收口） | P0/L | ⚠ 降档 | backend/pkg/market（**修正**：非 pkg/rules）静态 Go FactorSpec 表 |
| B2 | 用户自定义选股器：白名单公式 DSL | P0/L | ✅ KEEP | 新 backend/pkg/screener + 选股器面板；**修正**：结果入新表 user_screens 隔离，v1 纯 Go 解释器内存执行禁 SQL 生成 |
| B3 | 策略晋级发布门槛（OOS 指标闸） | P0/M | ⚠ 降档 | **修正**：不新建 promote.go/新面板；复用 ParamProposal 链路，提案证据须来自 KindWalkForward 任务，门槛逐项结果（有效折数/正收益折比/OOS Sharpe/回撤/最小交易数，阈值复用 Admission 配置）落 metrics JSON。核验指出现状已有 OOS 优于基线门槛与 evolution.panel，真正缺口仅是 Approve 处缺多折/多指标量化下限 |
| B4 | 用户自定义策略沙箱（pyworker 执行） | P1/L | ⚠ 降档 | **修正**：不把 pyworker（章程「只做采集」）拉上实盘主链跑用户 Python；改 Go 侧声明式策略定义（JSON/表达式树存 strategy_library.code 并版本化，pkg/rules 解释器求值+refstrat 注册）——激活该死字段。策略代码字段全仓零引用已核实 |
| B5 | 模拟盘多账本对比 + FIFO 回合 | P1/M | ⚠ 降档 | **修正**：6c4e7f5 已闭 FIFO 重放大半缺口；只做多账本窄扩展（bookOf 放开命名 paper 子账本，LIVE 隔离不变）+ 只读「按账本重放对比」端点；坚持同事务物化唯一写路径 |
| B6 | 命中率复盘升级（相对基线+lead_days+源归因） | P1/M | ✅ KEEP | brain verify.go：对账窗 T+1 扩 T+5，hit=相对信号日前 20 日基线突破，加 lead_days/peak_after/base 追加列；per-source 汇总查询口先留 |
| B7 | 数据源声明式自定义接入 | P1/M | ⚠ 降档（为 P0 的那份在 D1 被压到更低） | **修正**：按 M01 §4.5 既定路径「Go 源+manifest 登记+yaml 默认关」，GenericHTTP 限定纯 intel/news JSON API，经 appcfg 键族白名单评审后启用 |
| B8 | 告警规则统一引擎（rule_type×scope×cooldown+flagged-once） | ~~P1~~→P2/M | ⚠ 降档 | **修正**：新建 pkg/rules/alertengine + ent monitor_rule 表（跨日持久冷却——engine 每交易日重建，5 分钟内存去重跨日失效属实）；先接 market/intel 两适配器，risk CRUD 后置；不与 D8/R6/U6 冷却窗族抢字段 |
| B9 | 「起没起来」形态判定纯函数+规则前置减 LLM | P2/S | ⚠ 降档 | **修正**：pkg/rules/shape.go 独立成件（与 R6/R8 共用）+ pkg/llm/cache.go（key=task+prompt hash+pack hash，cache_hit 行落 llm_call_log 追加迁移）；MissShape 归因先于 AITopN 执行（scan.go:166 前） |
| B10 | NT 数据出口 MCP 薄桥（只读） | P2/M | ⚠ 降档 | **修正**：backend/cmd/mcpbridge 独立 binary，桥内 GET+精确路径白名单（拒 /api/config PUT、/api/ops/backup POST 等写口）；MCP SDK 属新依赖须先行审批，不批则手写最小 stdio JSON-RPC |

## 3. 数据接口与数据架构借鉴（9 条）

| # | 建议 | 定档 | 核验 | 落点/要点 |
|---|------|------|------|-----------|
| D1 | 声明式自定义 HTTP 源（JSON 定义→GenericHTTP Source） | P0/M | ⚠ 降档至极低 | 核验：目标源（财联社/雪球/金十）是 M01 红线/绕开源，配置自助化既定走开发者动作；仅留「运行期可扩展」口子（白名单 DSL、纯 intel/news 域、评审启用） |
| D2 | 对账不合自动回补自愈（reconcile→scoped backfill→复对账） | P1/M | ✅ KEEP | 复用 SubmitBackfill 12 域白名单+护栏（滤 B6 不可回补源声明；独立 N 日计数器，不改 warn 语义与水印语义）；计数器持久化受 52fcd1c ent/data 冻结闸，先以 hits/last_seen 近似 |
| D3 | 预测对账改相对基线+lead_days+T+3/T+5 复检 | P1/M | 已并入 B6 ✅ | 见 B6 |
| D4 | 按信号源归因命中率滚动汇总回写晨报 | P1/S | 已并入 B6 ✅ | 纯读侧聚合（60 交易日窗按 source hit_rate/median lead_days），写晨报固定文案块，样本不足显「样本不足」不落键 |
| D5 | 外部付费源成本台账+报告页脚+贵源周闸 | P2/M | ⚠ 降档 | **修正**：仅 tushare/tavily 为真付费源需台账；页脚两列=外部计费 / 本地 token 记账（非计费，经 TierUsage 不加 cost 列）；unit_cost 落 catalog sourceBookRow 不动 conf.proto 表结构；周闸键 app.ds.<src>.weekly 同批进 pkg/params/validate.go 白名单 |
| D6 | 「零行即异常」按域显式合同（empty_ok 进 DomainConfig） | P1/M | ⚠ 降档 | **修正**：字段定名 empty_policy 枚举 never/nonempty/min_rows:n/always_ok，走 conf.proto 三步（proto→regen→yaml）；unlock/block_trade→always_ok、margin→min_rows:1000、snapshot 吸收 MinSnapshotCount=3000；走 Issue{CheckEmpty,error} 通道而非 StaleError，水位不前移为免费性质 |
| D7 | broker-gw Windows 文件写限重试 | P1/S | ⚠ 降档（小但关键） | **修正**：broker-gw 在仓根（本会话尽调曾误判缺位）；仅元数据 os.replace 重试参数化（上限 ~2s）+ transport_unknown>0 接 L2 告警（storage.py / pkg/brokerif/remote/client.go / trade live_channel.go），零新表；CSV 发布链 mkstemp+fsync+os.link 原子件勿碰 |
| D8 | 题材/热榜 first_seen 不变量+首过闸才告警 | P2/S | ✅ KEEP | M03 CAS 模板（AlertedAtIsNil→SetAlertedAt，n==1 才告警）复制入 market 数据层；theme_lead 表与 R2 同批共建（提前并入 R2 批，防双表）；收盘扫描 limit_board 主源/hot_rank 随源；禁碰 engine.go:50 的 5 分钟去重 |
| D9 | 分钟级行情 parquet 冷导出（回测/研究派生列存） | P2/L | ⚠ 降档 | **修正**：一期 dataset.go manifest 自动分片（破 25 万行冻结帽，纯 PG+ent 零新依赖）；二期 parquet 派生旁路（16:40 按 trade_date 分区 + dataset_manifest 增派生分区列表字段 + 回读校验闸）以 parquet-go 新依赖审批为闸，不批则不落二期 |

另（TSP 移植价值高、已并入对应条目的细节）：SSE 每连接独立订阅者队列防慢消费拖垮全推（→ws/hub）；APScheduler misfire_grace_time 停机补跑+管道期暂停实时写防竞态+refresh 缓存 finally（→datahub 数据同步 job）；启动时回收 stale 发布世代标记（→dbinit/outbox 半态自愈）。

## 4. UI 显示借鉴（10 条）

> UI 映射产出的文件引用已全部抽验属实（NtLadderTiers/emotion-review/limit-ladder/NtKline/ws/* …）。

| # | 建议 | 定档 | 核验 | 落点/要点 |
|---|------|------|------|-----------|
| U1 | 信号源归因命中率卡+三态状态章 | P0/M | ⚠ 降档 | **修正**只做已证实小修：emotion-review.panel.vue:389-392 结果列 pct==null 渲染「没取到」no_data 章；hit/miss/no_data 三键+涨停/收涨一起走 status-map 册（UI 总方案既定项）；源级汇总先用现有 listPredictionChecks 聚合，等 B6 的 lead_days 落地后再升级 |
| U2 | K 线关键位叠加层（压力/支撑/密集/枢轴） | P1/M | ⚠ 降档 | **修正**：GetKeyLevels 服务端合成——压力/支撑/枢轴由 GetBars 纯计算（摆动点+floor pivot），密集段调 B3 筹码内核（单一权威，禁另写 bars 版密集）；合同照 B3 四态（ok/missing/not_meaningful/error）+optional；色派改 压力=up/支撑=down/密集=text3 dotted/枢轴=accent2（原「压力 warn」撞 MarketView 图例既有四义琥珀）；顺带补 api/market.ts 的 getChipDistribution 断头消费；落 proto 须避让 market.proto 并行批 |
| U3 | 题材/概念热力面板与龙头分 | P1/M | ⚠ 降档 | **修正**：龙头行钉死 SectorHeat.leader_symbol/reason 读链单一权威（六因子降级为行内积分展示，量比因子标「日口径」或剔除）；NtHeatmap :70 空态改 empty prop 两态（无行 vs 未取数）——首个生产消费方须 golden 首批入库（66c7ba5 CI 第四道门）；截断常量置面板侧并优先服务端 top；与 R8 共口径为硬前置 |
| U4 | 涨停梯队卡片视觉语言升级 | P1/S | ⚠ 降档 | **修正**：砍常驻渐变底 tint（违视觉需求 :168 数据背板稳定底色，且不在 §7.1 五层枚举）；保留左 inset 1px 轨道（accent/track 色）+hover 亮度提亮（bg-raise 阶层，非 glow）；boardTag 作 display/NtBoardTag.vue 三色入册（board.cyb/kcb/bj）、utils/limit.ts 增 boardTag(symbol) 出口与 limitRatio 单源共存；梯队个股行右侧渲 compactAmount(sealAmount)，null→「—」 |
| U5 | 推送定向失效与焦点 symbol 原位合并 | P1/M | ⚠ 降档 | **修正**：范围收窄到两处——limit-ladder 行级 patch + quote-sidebar 焦点股原位更新；脉冲复用 QUOTE_FLASH_MS=300 不定新值；throttle key=topic:symbol 由 msg.data.symbol 派生；hub.ts:194-218 兴趣收窄口径不动（quote-sidebar:120-125 帧级 symbol 过滤已有，尽调补正），不新增失效总线 |
| U6 | 首次过闸「🆕 今日首现」标记 | P1/S | ⚠ 降档 | **修正**：新建 utils/alert-seen.ts（nt.alertseen.v1，30 天滚动，损坏→显式清空，对「localStorage 静默降级」惯例声明破例）；判定键复用 causeKey；status-map 增 alert.first_seen/alert.familiar 两键须走入册评审链；显示降档仅限 note/t3 明度层、禁改 levelOf 输出，L3/L4 永不降档 |
| U7 | 告警行色条+来源图标单 map+策略事件语义染色 | P1/S | ⚠ 降档 | **修正**：toneOf 是数值→方向唯一出口（format.ts:255-263），改立新 toneOfEvent mapper（进入→up、移出→down，不经 pnlClass）；SEVERITY_STRIP+SOURCE_BADGE_STYLE 两册落 ui/alert-levels.ts（入册评审，KINDS 先例）；NtTimeline 行首 3px strip 与 dot 并存（append-only）；notify-center/intraday-alerts/AlertCenter 三消费方切换 |
| U8 | 日报章节化+页脚成本行+降级留痕 ↓ | P2/M | ⚠ 降档（拆 U8a/U8b） | **修正**：U8a=M05 章节序合同文档先行+面板固定章节序+缺章渲染「本章缺位」占位（替 v-if 消失术）+风控降级 ↓原级徽注（risk.demoted 入册）+成本行「—」渲染；U8b=briefing.go 四新章（梯队/源命中率/源状态/成本）硬依赖 B6 命中率+D5 成本台账+briefing.go 并行在途合流（scrubBriefing/sealOf 收敛） |
| U9 | 异动监控三时段 tab（竞价/盘中/偏移榜） | P2/M | ⚠ 降档 | **修正**：先例勘误——「截至 HH:mm」在 PaperAdmission.vue:87（WorkbenchView.vue 不存在）；GetDeviationBoard 属全新 rpc，market.proto/monitor.go 并行在途未提交，须等批落定或与其合批；60s 轮询抄 document.hidden 降频先例（OverviewView.vue:342,348）；缺涨跌停价行按 P3-10c 标 missing 勿 0；GetAuction top 上限 50 若需扩名单须 proto 同步提限 |
| U10 | 新用户核心面板种子与 defaultHidden | P2/S | ✅ KEEP（拆步禁并） | 落实拆大步禁合批：①PanelDef 增 core?:boolean，8 张默认面板置 true、DEFAULT_LAYOUT 由 core 驱动或显式断言一致+测试；②defaultHidden 先落元数据+测试锁定，command-grammar/FunctionIndexOverlay/DockHost provide 三消费点过滤后置；③LAYOUT_STORAGE_VERSION 保持 3 不 bump（bump 即触老用户布局归档回默认）；与 R10 同改 panel-registry.ts 时 U10 先行 |

TSP 前端另有两条结构性启示供权衡（未入清单，因 NT 决策已定）：react-query+SSE 失效前缀的大表虚拟化阈值（>100 才虚拟化复用原生滚动）；扩展注册表「冲突即整体拒挂不拖垮宿主」姿态（对应 NT future 的页面包/二开）。

## 5. 题材雷达移植设计（本次金矿，10 条）

**总纲**（R1，✅ KEEP，已按 M01 活源白名单修正）：radar 六信号逐一给出 A 股等价物——

| radar 信号 | NT/A 股等价物 | 数据源 |
|---|---|---|
| X 双闸+共现 | 快讯多源 30h 窗 ≥2 独立 source 共现新词；单源须强名词（CPO/LPU 型缩写、含数字、「首台/新一代」） | 东财快讯（含 cls 电文）+新浪 7×24+Tavily（**勿用财联社/金十红线源**） |
| HF trending 差分 | 东财概念板块日快照差分：新 code/新名即新题材（交易所口径背书，权重最高）；改名=别名入 variants | AkShare stock_board_concept_name_em，pyworker 新 collector |
| GitHub breakout | hot_rank 截面斜率+新进 top50 加速度；个股→题材反向归组，多关联股同爆=题材级 breakout | 既有 hot_rank 30min 截面（N4-04 已启用） |
| sitemaps 新页 | 公告/快讯新词对 60 天词汇表差分：distinct news_id 计源数、lift=n/(baseline·ratio+1) | news_sentiment（announcement/news/flash），cninfo 巨潮 |
| Google Trends 复盘 | **T+14 行情独立裁决**：关联股等权相对沪深300 超额峰值 ≥max(2×20日波动基线, +6pp) 或任一关联股涨停→hit | market_data/limit_board（快讯永不参与裁决） |
| 关键词表 | 题材机会表（列见 R4） | 物化 theme_lead |

**分层**（核验修正）：现有 HotWords=热词层（双窗口斜率）保留；新增 intel/biz/theme 包=**新词层**（词汇表基线差分+distinct source 共现闸+sector 日快照差分+T+14 复盘）。

| # | 建议 | 核验 | 要点 |
|---|------|------|------|
| R1 | 信号等价物映射总纲 | ✅ KEEP | 上表；M03 设计文档新增「题材雷达」章 |
| R2 | ent 题材雷达表 | ⚠ 降档 | **修正**：四表缩两表同批交付——theme_lead（first_seen 不可变、source_set、leader_symbol、logic_text、partial/missing 沿用数据纪律）+ theme_review（term、review_date、verdict、lead_days、peak_rel_pct）；theme_vocabulary/theme_snapshot 随生产者代码再补（避免空壳表提前收割） |
| R3 | LLM 题材名词判定器（批量打分、只排序不拦截） | ⚠ 降档 | **修正**：先补中文候选产出（新闻正文 2-6 字 n-gram+停用词激增检测，BuildDictionary 排除已知股名/别名/概念）——judge 不能没有候选源单独落；再按蓝本 25 词/块批打分、named≥0.5、老名牌 0.5 先验、fail-open、llm_call_log 记成本 |
| R4 | 公告/快讯新词差分采集器 | ✅ KEEP | 修正两处：计源数用 distinct cluster_id（含 status!=filtered）真去转载；候选词抽取新写 n-gram 小模块（复用 filter.go 停用词清单），不依赖 kb/tokenize.go；挂 pkg/scheduler cron（盘前 08:30/盘后 15:35） |
| R5 | 概念板块榜单快照差分 | ⚠ 降档 | **修正**：删「pyworker 新 collector」——eastmoney Go 链每日全量供给板块名+成分（emBoards+Sectors()），新 AkShare collector 属同族重复取数；改为 datahub SyncSectors 事务内嵌集合级差分（新 code 首见／改名 vs 主档+dataset_record 昨 blob／退板）产事件，事件写 R2 theme_lead；AkShare 仅在 source_health 记冷备源。关键新发现：sector 表无 created_at，「新 code 首见」只能在采集时点产出故事件位不可替代；改名现被 OnConflictColumns.UpdateNewValues 静默覆盖无别名留存 |
| R6 | 热度加速度信号 | ✅ KEEP | 红线三条钉死：①斜率/基线只用 rank（东财 top100 接口不填 heat 列恒 NULL，禁写「人气值强度」口径）；②30min 节奏=消费方抽稀配置项（6 点合 1），非采集改造；③缺块读 source_health(hot_rank,eastmoney) consecutive_failures，不在 hot_rank 挂 missing。flagged-once 先例=intel_cluster.alerted_at(:38-40)；判定函数与 HotWords 窗口法共形但不并表；出口喂 R8 signals 徽章 |
| R7 | 多信号归并+每日题材报告注入晨报 | ✅ KEEP | intel/biz/theme 复用现有 HotWords 窗口对比与 dict 概念关联（不另起词抽取）；板块/人气两路经 data 仓储读；brain 按 degradeBriefing 模式缺块显式标缺失 |
| R8 | 题材机会表列设计 | ⚠ 降档 | **修正**：收窄为「机会表读模型+列装配层」——主键/归并键钉 R2 theme_lead，不另建字典、不重建归并、不与 emotion_day_frozen.theme_tree 的 reason 键归并存成双轨；物化行幂等键复用 prediction_check 模式（对齐 R9）；LLM 新 task=theme_logic（fail-open/llm_call_log/去重缓存全套照 M05）；拥挤度口径=sector_heat.amount ÷ SUM(daily_bar.amount) 纯 SQL 新算（勘误：DayAmounts 在 market 服务 repo.go:485 非 intel，跨服务必须走 market 仓储端口）；换手缺 float_share→not_meaningful 降格留痕 ↓ |
| R9 | T+14 题材复盘闭环 | ⚠ 降档 | **修正**：前提是题材信号先落库——每日收盘后把 HotWords/cluster 概念 Top-N 快照写入 theme_signal（纯读现有 intel_link 数据，无新采集），再复用 prediction_check 幂等键；复盘主体是第二阶段 |
| R10 | theme-radar 面板+theme.proto | ✅ KEEP | 硬依赖：R2 两表落库、R7/R8 归并产物、R9 theme_signal 先落——proto 先写即空口（ent 零 theme schema 实测）。落实时健康行直读 datahub ListSources(state/last_success_at) 避免与 datasource-health 面板出第三口径；missing/partial 沿用 market.proto:487-498 先例；admin 网关零改动（走 intel upstream /v1/intel/**）；与 U10 同改 panel-registry.ts：U10 不动数量断言先行，R10 后入注册表 27→28+两快照 -u+POPOUT_ALLOWED 21→22 |

**排期建议**：R1 设计文档 + R4 采集器 + R2 两表 + R9 信号落库为第一批（约 1-2 个迭代）；R3 judge + R7 归并晨报 + R10 面板第二批；R5/R6/R8 信号强化第三批。

## 6. 参考仓覆盖不到的自研区（边界声明）

以下 NT 能力两仓均无对应物，**不参照任何参考仓、沿用 NT 既定设计**：风控闸门与订单链路（决策 3，大模型不出单）；broker-gw 东财 CSV 文件单实盘闭环与模拟盘满 20 交易日准入；DGX 本地大模型四维分析的成本自足性（radar 的 cost_micros 是外部付费 API 语境，NT 的对应物是本地 GPU 显存/吞吐预算不是金钱台账，token 预算/批处理/去重/缓存策略需另定）；数据纪律「没取到≠0」dataformat.Result[T] 体系（两仓静默 0 处置反而差于 NT）。

## 7. 工程纪律启示（完整性批判补漏）

1. **升级与版本治理**：TSP 有 packaging/(Inno Setup+PyInstaller）与 upgrade_guard（sha256 清单后台扫描用户二开文件、升级前自动备份）。NT Tauri 安装包升级迁移、版本来源治理、sidecar 进程守护尚无对应——桌面版发布前应补。
2. **一键部署**：TSP 根目录 Dockerfile/docker-compose 一条命令拉起全栈；NT 多 Go 服务+pyworker/broker-gw 双边车在 DGX Spark 的一键编排、首战自检方案缺位。
3. **开放接口治理**：若做 B10 的 MCP，须连同一套带走——规则表唯一契约源+tier 快照契约测试、Token 五件套（前缀明文一次/SHA-256/默认只读/admin 永不签发/吊销即拒）、网络门禁先于鉴权、SSE 短票据（POST 换 60s ticket 再走 query）、heavy_job_limiter 并发闸与请求限流分离、examples 零依赖脚本当活文档契约探针。
4. **测试门对照**：TSP 250+ 域测试含生产保障门类（watchdog 死开关、atomic 写重试、rate-limit 预算、data_integrity、job stall 取消、heartbeat）；本报告每条新增（R4 采集器、U5 keyed invalidation、D2 回补环、B2 DSL）交付时必须带对等测试门类，不能只有单测。
5. **LLM 成本视角**：本报告新增多个 LLM 调用点（R3 判定、R7 背后逻辑、B9 前置闸）——对本地 DGX 模型应落：批处理（25 词/块）、去重（同 term 同日只判一次）、缓存（7 天 TTL）、fail-open 降级、llm_call_log 记 tokens+耗时。参考「外部付费台账」只适用于 Tavily 等付费源（D5）。
6. **CI 状态持久化**：radar 用 Actions 缓存 run_id 存 state+restore-keys 前缀复活+leads 30 天工件留存——NT 若做日级题材词表 CI 校验可用同款（仓库不长大，丢缓存只损失一天）。
7. **文档纪律**：radar 的 SKILL.md 三原则值得 NT 文档体系吸收——成本时间先说、「没查到」≠「不存在」、禁止凭记忆补数据；版本五处一致由测试断言（radar 版）对应 NT 的 proto/面板表/文档同步断言可加强。

## 8. 附录：对抗核验 14 条明细

✅ KEEP：B2 选股器 DSL（收紧：user_screens 隔离+禁 SQL 生成）、R1 信号总纲（绕行红线源）、R4 公告新词差分（distinct cluster_id+自写 n-gram）、R7 归并晨报（复用 HotWords/dict 不另起抽取）、B6 命中复盘（对账窗扩 T+5）、R7 前述。

⚠ DOWNGRADE 与原因：B1 因子注册表（NT 仅 ~20 固定因子非挖掘平台，P0 过档，落点改 pkg/market）；B3 晋级门（论据失实：ParamProposal 已有 OOS 门槛，真缺口仅 Approve 处量化下限）；D1 声明式源（目标源是红线+既定路径已在 M01 §4.5，降至极低留口子）；U1 命中率卡（当前 briefing 单源，只落 no_data 三态小修）；R2 四表（缩两表防空壳）；R3 judge（孤儿组件，先补中文候选产出）；R9 T+14（先落 theme_signal 快照，复盘第二阶段）；B4 沙箱（威胁模型：pyworker 章程只采集，改 Go 声明式）；B5 多账本（6c4e7f5 已闭大半，只做窄扩展+坚守同事务物化）。

pass2 明细见第 9 节：22 条全部定判，无 REJECT。自此 39 行全闭环。

## 9. 补遗：pass2 对抗核验 22 条明细（2026-10-21）

方法：7 组尽调代理按文件邻域只读取数 → 7 组证伪代理逐条复核 ≥2 处 path:line 后双镜攻击（A：定档与论据；B：既定路线冲突与重复）→ 定判。汇总：**KEEP 5（D2/D8/R6/R10/U10）／DOWNGRADE 17／REJECT 0**。无一方向性错误，但 17 条论据/定档/落点有伤——病灶四类：错引或尽调漏查 6 处、撞既有权威单源 5 处、新依赖/新框架审批闸 2 处、并行会话在途撞车 3 处。

### 9.1 尽调勘误清单（写进执行防错单）

- broker-gw 位于仓根 `broker-gw/`（本会话尽调一度误判「仓内无独立 broker-gw 目录」，D7 修正版已按实纠正）；Go 侧 `backend/pkg/brokerif/emfile` 仅镜像桩，trade 未调用。
- DayAmounts 在 `backend/app/market/internal/data/repo.go:485`（尽调误书 intel，行号巧合相同）；R8 拥挤度跨服务读面必须走 market 仓储端口，core 不持 ent.Client。
- 「截至 HH:mm」先例实为 `web/src/view/PaperAdmission.vue:87`（U9 引的 WorkbenchView.vue 不存在）。
- quote-sidebar.panel.vue:120-125 已有帧级 symbol 过滤——U5 收窄、不新增失效总线的事实基础。
- 行号勘正：brain.proto hit_rate 实为 :398（尽调写 :391-398）；stores/layout.ts migrate() 实为 :53-67；Quote.limit_*_price 为 market.proto:562-565；intraday_alert 字段 :38-45；market_data 唯一键 :49-52；reconcile.go severity 为空串非 warn。
- sector 表无 created_at（R5「新 code 首见」事件位不可替代的硬证据）；hot_rank.heat 恒 NULL（东财 top100 不填该列，R6 斜率只能用 rank）；emotion_day_frozen.theme_tree 已有 reason 键归并（R8 禁双归并的硬冲突）。

### 9.2 定判索引（批 / 量）

| # | 定判 | 批/量 | 一句话修正/红线 |
|---|------|-------|----------------|
| B8 | DOWNGRADE（P1→P2） | 2/M | pkg/rules/alertengine+monitor_rule 跨日持久冷却；先 market/intel 两适配器，risk CRUD 后置 |
| B9 | DOWNGRADE | 2/S | shape.go 独立成件与 R6/R8 共用；LLM 缓存 key=task+prompt hash，cache_hit 记 llm_call_log |
| B10 | DOWNGRADE | 3/M | mcpbridge 桥内 GET+精确路径白名单；MCP SDK 审批闸，不批手写最小 stdio JSON-RPC |
| D2 | KEEP | 1/M | 12 域白名单内 scoped backfill；独立 N 日计数不动 warn/水印语义；持久化受 52fcd1c 冻结 |
| D5 | DOWNGRADE | 2/M | 仅 tushare/tavily 计费；本地 token 记账非计费；unit_cost 落 sourceBookRow；周闸键入 validate.go 白名单 |
| D6 | DOWNGRADE | 1/M | empty_policy 四值枚举 conf.proto 三步；Issue{CheckEmpty} 通道非 StaleError；水位不前移免费 |
| D7 | DOWNGRADE | 1/S | 仅元数据 replace 重试参数化（~2s 帽）+transport_unknown 接 L2；零新表；CSV 原子件勿碰 |
| D8 | KEEP | 2/S | M03 CAS 复制入 market 数据层；theme_lead 提前并入 R2 批；禁碰 engine.go:50 |
| D9 | DOWNGRADE | 3/L | 一期 manifest 自动分片零新依赖；二期 parquet 以 parquet-go 审批为闸 |
| R5 | DOWNGRADE | 3/S | 删 pyworker collector；SyncSectors 内嵌差分写 theme_lead；AkShare 仅 source_health 冷备 |
| R6 | KEEP | 3/M | 斜率只用 rank；30min=抽稀配置；缺块读 source_health；出口喂 R8 徽章 |
| R8 | DOWNGRADE | 3/L | 收窄读模型层；归并键钉 theme_lead 禁双轨；theme_logic 全套 LLM 纪律；DayAmounts 走 market 端口 |
| R10 | KEEP | 3/M | 依赖 R2/R7/R8/R9 先落；健康行直读 ListSources；U10 先入册再 27→28 |
| U2 | DOWNGRADE | 2/M | 密集复用 B3 筹码内核单权威；色派 up/down/text3/accent2；补 api/market.ts 断头消费 |
| U3 | DOWNGRADE | 3/M | 龙头=SectorHeat.leader_symbol 单源；六因子仅展示；NtHeatmap 两态+golden 首批入库；与 R8 共口径硬前置 |
| U4 | DOWNGRADE | 1/S | 砍常驻渐变 tint，保轨道+hover 亮度；NtBoardTag 三册键；封单 compactAmount |
| U5 | DOWNGRADE | 2/M | 只改 limit-ladder+quote-sidebar 两处；复用 QUOTE_FLASH_MS=300；不新增失效总线 |
| U6 | DOWNGRADE | 1/S | causeKey 判定；降档仅限明度层禁碰 levelOf，L3/L4 永不降；两键入册评审 |
| U7 | DOWNGRADE | 1/S | toneOfEvent 新 mapper 不经 pnlClass；两册入 alert-levels.ts；strip append-only |
| U8 | DOWNGRADE（拆 a/b） | 3/M | U8a 文档合同先行+占位替 v-if+risk.demoted 入册；U8b 依赖 B6+D5+briefing.go 合流 |
| U9 | DOWNGRADE | 2/M | 等 market.proto/monitor.go 在途批落定合批；60s 轮询抄 document.hidden 降频；缺值标 missing |
| U10 | KEEP | 1/S | 拆步禁并：core 先行、defaultHidden 后置、v3 不 bump；先于 R10 |

### 9.3 跨组结论

- 测试门类全部按工程纪律#4 落对等门类：纯函数表驱动单测、隔离 PG 集成、vitest 册断言、viz golden 首批入库（U3/NtHeatmap）、告警转净族跨条断言对齐（D8/R6/U6 同族）——零「只单测」交付。
- 同批并件建议：U4/U6/U7 状态册新键（board.*、alert.first_seen/familiar、risk.demoted 候选）同批一件入册评审；R2+D8 theme_lead 同批建表防双轨；D6+B6 源声明同改 DomainConfig 防二次编辑；U6/U7 同改 alert-levels.ts 与 NtTimeline 合成一提交。
- 冻结闸（动手前必扫 git status）：ent 生成件 regen 在途、brain 四件（analyze/biz/prompts/briefing+validate）、market.proto+monitor.go、datahub source_worker 三件套——碰这些文件的条目（R2/B9/U2/U9/D5 写入侧）一律等批或合批，不硬闯。

---
数据来源：25 代理深读（184 万 tokens，654 次工具调用），参考仓文件引用均在分析中实测存在；NT 侧缺口断言均有 backend/web/doc 文件路径佐证。本分析对应 workflow run wf_e04f9d76-3fc（2026-10-21）。
