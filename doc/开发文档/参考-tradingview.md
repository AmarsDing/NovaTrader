# 参考：TradingView 开源工程深析 × NovaTrader 对比提升清单

> 2026-10-18 静态源码审读。研究对象已下载到 `research-worker/vendor/`（该目录已被 .gitignore 忽略，仅作本地研究资料，不进库、勿执行其中任何脚本）。
> 依据用户指示按"可自由借鉴"对待；客观事实：lightweight-charts 为 Apache-2.0、charting-library-tutorial 为 MIT，均为宽松协议，正常使用只需保留版权声明。

## 0. 结论摘要

1. **NovaTrader 的基础设施不差**：前端 WS hub（epoch/seq/回放/resync_ack/引用计数订阅）已超过 tutorial 的 streaming.js；后端 gateway hub 已对 market 帧做"可替换报价状态合并"（key=kind:symbol:freq:bucket，上限 5000）。这两块可以抄的不多。
2. **最大短板在图表渲染与数据消费的"最后一公里"**：
   - `NtKline.vue` 每次 props 变化（含每次 tick 引发的父级重取）都走 **全量 setData + 全量重排重去重 + 重建 MA/成交量数组 + setMarkers + `fitContent()` 重置视口**，TradingView 的标准解法是"REST 快照一次 + WS 增量 `series.update()` + 250ms 脏标记合并"。
   - lightweight-charts 只用了最老一代 API（`addCandlestickSeries`/`addHistogramSeries`/`setMarkers`），**没用**增量 update、磁吸十字线、TrackingMode、多 pane、自定义系列、primitives、price line、watermark、log/百分比坐标、DPR 自适应数据合流(conflation)。
   - 依赖停在 ^4.2.2，上游已到 v5 时代（addSeries/addPane/addCustomSeries/createSeriesMarkers/pane primitives）。
3. **行情链路"推送只当闹钟"**：market 主题推送后 MarketView 做的是整段 REST 重取图表数据，而 tutorial 的范式是 frames 直接喂 `lastBar + update()`。推送通道里其实已经有 bar 级帧（`gateway/hub.go:189`），前端没有消费。
4. 可整体抄录的资产：`streaming.js` 的 WS 生命周期管理、`fetchKlinesBefore` 的 countBack 分页、`quotes` 双 ticker 状态机、depth 合成兜底、conflation 常量与平滑算法、canvas release/label 缓存等工程细节——见第 6、8 节。

---

## 1. lightweight-charts 源码深析（vendored master，v5 时代）

### 1.1 分层与依赖方向

`src/` 六层，**依赖严格单向：api → gui → model ← renderers/views**：

| 目录 | 职责 | 关键文件 |
| --- | --- | --- |
| `api/` | 公共 API 的薄壳：IChartApi/ISeriesApi/IPaneApi 实现、options 默认值（`api/options/*-defaults.ts`）、`create-chart.ts` 入口 | `chart-api.ts:206 addSeries`、`chart-api.ts:188 addCustomSeries`、`chart-api.ts:324 addPane` |
| `gui/` | DOM/画布控件层：ChartWidget、PaneWidget、PriceAxisWidget、TimeAxisWidget、PaneSeparator、鼠标触摸事件 | `chart-widget.ts`、`mouse-event-handler.ts`、`canvas-utils.ts` |
| `model/` | 纯数据与逻辑：ChartModel、Pane、Series、PriceScale、TimeScale、DataLayer、PlotList、Crosshair/Magnet、Conflater | `chart-model.ts`、`series.ts`、`data-layer.ts`、`data-conflater.ts` |
| `renderers/` | 每个系列类型一个 canvas 渲染器（bitmap 坐标系） | `candlesticks-renderer.ts`、`walk-line.ts` |
| `views/` | PaneView/PriceAxisView/TimeAxisView（渲染前的数据装配+z 序） | `views/pane/*`、`views/price-axis/*` |
| `plugins/` | v5 插件体系内置实现：series-markers、text/image watermark、up-down-markers | `plugins/series-markers/*`、`plugins/*-watermark/*` |

要点：公共 API（`index.ts`/`standalone.ts`）只导出 `api/` 与类型；`api/chart-api.ts` 把调用翻译成 model 操作（如 `paint(options)`/`applyOptions` 深合并默认值）。**NovaTrader 的 viz 层应模仿这种"组件壳只做翻译、状态全在 model"的边界**。

### 1.2 渲染管线：失效分级 + rAF 合并 + 双坐标系

- **失效分级**（`model/invalidate-mask.ts:3`）：`None / Cursor / Light / Full` 四级，外加 per-pane 的 `PaneInvalidation{level, autoScale}` 与 7 种 TimeScaleInvalidation（FitContent/ApplyRange/BarSpacing/RightOffset/Reset/Animation/StopAnimation）。十字线移动只发 Cursor 级——只重画十字线层，不重算数据。
- **合并调度**（`gui/chart-widget.ts:705-730`）：任何失效先 `merge` 进 `_invalidateMask`，首个失效时 `requestAnimationFrame` 一帧只画一次（`_drawPlanned` 防重复入队）；一帧内先应用 timeScale 失效、再 momentaryAutoScale、再 `paint()`（`chart-widget.ts:623`）。**这就是 TradingView 在 tick 风暴下不卡顿的第一性原理：状态可以高频突变，绘制永远合帧。**
- **双坐标系**（`gui/canvas-utils.ts:9-26` + `fancy-canvas` 包）：media（CSS 像素）与 bitmap（物理像素 × DPR）两套；`bindCanvasElementBitmapSizeTo` 用 `device-pixel-content-box` ResizeObserver 跟随 DPR 变化；渲染分支 `media-coordinates-pane-renderer.ts` vs `bitmap-coordinates-pane-renderer.ts`。
- **canvas 卫生**：`releaseCanvas()`（`canvas-utils.ts:28-36`）销毁前把 canvas 收到 1×1 并 clearRect，防 iOS "Total canvas memory use exceeds"——对频繁销毁建面板/弹窗的 dockview 布局有借鉴意义。

### 1.3 数据层：PlotList + DataLayer + 新增的 Conflation 引擎

- 时间点不是时间戳而是单调 `TimePointIndex`，日线用 business-day 权重对齐（`horz-scale-behavior-time/*`），空白点(whitespace)一等公民。
- `setData()` = 全量重建 plot；`update()` = 追加/更新末根 bar 的增量路径，配合二分查找（`helpers/algorithms.ts` 的 `lowerBound`，`plot-list.ts:170` 调用）O(log n) 定位。
- **数据合流（master 新能力，`model/data-conflater.ts`）**：当 `barSpacing < 1/DPR` 时按 2 的幂（2..512，`conflation/constants.ts:5`）把相邻 bar 递归合并成 OHLC 块；`calculateConflationLevelWithSmoothing`（:40）按 DPR 与平滑因子算级别；`updateLastConflatedChunk`（:95）只重建末块避免全量费标；缓存用 `WeakMap<数据数组, {version, levelResults: Map<level, rows>}>`（:38），数据引用变了自动失效。**物理上画不下的细节根本不渲染——这是大秒级数据缩放不卡的根因。**
- 对应的 e2e 用例已存在：`tests/e2e/coverage/test-cases/time-scale/conflation.js`、`plugins/custom-series-conflation.js`——自定义系列可提供 `CustomConflationReducer` 二合一归约（`icustom-series.ts:222`）。

### 1.4 交互系统：状态机式手势处理

- 单一入口 `gui/mouse-event-handler.ts`：鼠标与触摸统一为 compat 事件；长按定时器、Manhattan 距离阈值判定拖拽/点按、双击/双 tap、只跟踪一个 active touch（多指仅 pinch）。
- **惯性滚动** `model/kinetic-animation.ts`：保留最近 4 个位置采样，按距离加权平均速度、方向相反即截断；`dumpingCoeff` 指数衰减求时长与位移（`getPosition` :128）。撬动点：参数化 minSpeed/maxSpeed/dumping/minMove。
- **磁吸** `model/magnet.ts`：Normal（自由）/ Magnet（吸最近收盘）/ MagnetOHLC（吸 OHLC 四值）三模式；收集同 pane 非 overlay 可见系列的候选价 → 转坐标 → 取最近 → 反解回价格（:30-86）。**信号标注精确对线所需的交互手感全在这里。**
- TrackingMode（v5，`tests/e2e/coverage/test-cases/interactions/tracking-mode.js` 有验收）：十字线/图例只跟数据不跟鼠标的模式，配 `exitMode`；价格轴可直接拖拽缩放、pane 之间有 `pane-separator.ts` 分拖调高度、双击复位缩放。

### 1.5 扩展机制（v5）：自定义系列 + Primitives

- **自定义系列契约**（`model/icustom-series.ts`）：`ICustomSeriesPaneView{ renderer(), update(data, options), priceValueBuilder(), isWhitespace(), defaultOptions() }`；渲染器拿 `CanvasRenderingTarget2D` 与 `PriceToCoordinateConverter` 自绘，可提供 `hitTest`（返回距离/对象 id/自定义 cursor/hitTestData，:108-167）。`priceValueBuilder` 返回最多 3 个数（最大/最小/当前，末位即当前价）喂给 autoscale 与十字线。conflation 可通过 `CustomConflationReducer` 二合一参与合并。
- **series primitives**（`model/iseries-primitive.ts`）：`updateAllViews / paneViews / priceAxisViews / timeAxisViews / autoscaleInfo / attached / hitTest`——挂在单条 series 上的绘制层（如买卖点、成本线、框选测量），可与 series 共享 autoscale。
- 内置示例：`plugins/series-markers/`（v5 的 `createSeriesMarkers` 取代旧 `setMarkers`）、`plugins/text-watermark`、`plugins/image-watermark`、`plugins/up-down-markers`。pane 级 primitive（`pane-primitive-wrapper.ts`）可整窗格绘制。
- **多 pane**（v5）：`chart.addPane()`（`api/chart-api.ts:324`）、`pane.addSeries(...)`、pane 交换/移除/拉伸——MACD/RSI/量柱副图不再需要第二套图表库拼窗。

### 1.6 渲染器工程技巧（`renderers/candlesticks-renderer.ts` 为代表）

1. `optimalCandlestickWidth(barSpacing, DPR)` 取整物理像素宽度；
2. **奇偶对齐**：影线宽 `floor(DPR)` 要与柱体宽同奇偶，保证十字线压在蜡烛正中央的对称观感（:50-59）；
3. `fillRect` 批量化：同色的影线/边框/实体连续画，`fillStyle` 只在颜色变化时赋值（:99-102）；
4. `prevEdge` 防重叠：相邻 bar 像素重叠时左边界钳位到上一根的右沿+1（:114-117）；
5. 宽度过小时自动降级（不画边框/不画实体，:72-74）；
6. 折线走 `walk-line.ts` 预生成 path 减少状态切换。

### 1.7 工程化实践

- **图形级 e2e**：`tests/e2e/graphics/` 用 Puppeteer 截图与 golden 图比对（`helpers/screenshoter.ts`/`compare-screenshots.ts`），按特性一个用例一文件（update-data/markers/price-line/panes/conflation…）；
- **交互级 e2e**：`tests/e2e/coverage/test-cases/` 用程序化鼠标动作验收 kinetic-scroll、tracking-mode、价格轴拖拽；
- 字体宽度缓存 `model/text-width-cache.ts`、格式化标签缓存 `model/formatted-labels-cache.ts`——高频文本绘制不做重复 measure；
- options 一律"默认值深度合并"，公共 options 与内部选项字段级映射并做兼容转换。

---

## 2. charting-library-tutorial 深析（商业 Charting Library 的官方接入范式）

闭源终端不开源，但这个教程把**行情接入的完整业务契约**公开了，正是 NovaTrader "边车→后端→前端"链路的对标物。

### 2.1 Datafeed 契约（`src/datafeed/datafeed.js`）

| 方法 | 职责 | 值得抄的细节 |
| --- | --- | --- |
| `onReady` (:299) | 声明能力：supported_resolutions/exchanges/symbols_types/supports_marks | 能力声明驱动 UI，避免前端硬编码周期列表 |
| `getServerTime` (:304) | 用交易所时间校准倒计时/竞价倒计时，失败降级本地钟 | NT 的竞价/收盘倒计时应对齐服务端时钟 |
| `searchSymbols/resolveSymbol` (:328/:352) | 符号目录一次拉取+Promise 缓存（`symbolsCachePromise` :43）；`pricescale` 由 tickSize 推导（:61-68） | A 股即可照此：6 位代码、pct 价+0.01 tick 推导 pricescale |
| `getBars` (:407) | **countBack 优先于 from**：向过去翻页只问"再给我 N 根"；`firstDataRequest` 时把末根存入 `lastBarsCache` 供 subscribe 续接（:446-450）；返回 `noData` 终止分页 | `fetchKlinesBefore` (:125-155) 的分页循环：每页 1000、`endTime = 首根-1` 防重叠、最多 25 次上限 |
| `subscribeBars/unsubscribeBars` (:460) | 按 symbol+resolution 挂到共享流 | 见 2.2 |
| `getQuotes/subscribeQuotes` (:572/:593) | REST 快照（24hr+1h ticker 各一份）先播种，WS ticker 流做增量；`quoteTickerState` 双状态机、失败回退 `quotePriceCache` | 对照 NT quote-strip 的 REST 一次性 |
| `getMarks/getTimescaleMarks` (:483/:537) | bar 上图标层与时间轴标记层 | 对应 NT 的信号/公告/停复牌标记 |

### 2.2 实时流生命周期（`src/datafeed/streaming.js`）——可整段抄模板

1. **共享单连接 + 多路复用**：`streamToSubscription`/`subscriberToStream` 双向 Map，一条 socket 承载全部 kline 流（:8-9）。
2. **延迟建连**（:39-50）：首次 100ms 后再建 socket，让"开图→切图"的抖动期订阅先稳定，不做无意义连接。
3. **闲置即停**（:53-67）：最后一个订阅者走时清掉 connect/reconnect 定时器，归一重试间隔。
4. **指数退避**：1s 起步翻倍至 30s（:107-110）。
5. **重连即重订阅 + 失效缓存**：open 时遍历全部流重发 SUBSCRIBE，并对老连接过的调用 `onResetCacheNeededCallback` 让上层"REST 回填缺口"（:83-98）。
6. **脏标记 250ms 合并刷新**（:165-215）：WS 消息只写 `handler.lastBar + isDirty`，`setInterval(250ms)` 统一 `callback(lastBar)`——**渲染压力与 tick 频率解耦**。TradingView 官方对"tick 风暴"的答案。
7. 服务端聚合 K线：`@kline_<interval>` 直接用交易所聚合好的流，前端不拼 tick。

### 2.3 Depth（DOM）与兜底（`datafeed.js:648-799`）

每条 DOM 订阅独立 socket + 250ms 推送节拍 + 2s 重连；**真实深度到达前给合成阶梯**（用最近成交价+tickSize 生成 20 档，:771-799），让盘口面板永远没有"空屏"。tickSize 之间按价格档位启发式（:811-820），并可复用 `resolveSymbol` 算出的 priceScale。

---

## 3. NovaTrader 现状对照（均已核到代码行）

### 3.1 强于 TradingView 教程的部分（保持即可）

- **WS hub 可靠性协议**：`web/src/ws/hub.ts`——epoch+topic_seq 水位、断线 replay 有界缓冲（`MAX_BUFFER=1000` :41）、resync/resync_ack 握手（:205-237）、按 topic 引用计数订阅(:154-168)、1s→15s 退避(:438-446)。tutorial 只有裸退避，无缺口检测。
- **后端推送侧已会"报价合并"**：`backend/app/admin/internal/gateway/hub.go:189-245` 对 market 帧按 `kind:symbol:freq:bucket` 建可替换状态（上限 5000），等价于 TradingView 的服务端降频思想；bar 级帧格式与 publish 通道都已存在（:214 的 kinds）。
- **主题 token/玻璃霓虹/盯盘画布**：monitor 2D canvas + rAF + DPR 上限 2（`CanvasStage.vue:159/:272`），视觉规范自成体系。

### 3.2 关键差距（逐项对账）

| # | 差距 | 证据 | TradingView 参照 |
| --- | --- | --- | --- |
| G1 | K线更新=全量 setData+全量重算+`fitContent()` 重置视口 | `NtKline.vue:54-99`、`watch deep:true :166-170` | `streaming.js:165-215`（lastBar+脏标记+增量 update） |
| G2 | WS market 推送只当 REST 重取的闹钟 | `MarketView.vue:198-205`（push→fromPush→REST reload） | datafeed `subscribeBars` 直接喂图（`datafeed.js:460`） |
| G3 | 只用 v3 老 API，无 update/markers 新接口/磁吸/pane/custom series/primitives/watermark/价格线 | `NtKline.vue:141-157`（add*Series）、`:97 setMarkers`、`:137 CrosshairMode.Normal` | v5 addSeries/addPane/createSeriesMarkers、`magnet.ts`、plugins/* |
| G4 | lightweight-charts 卡在 ^4.2.2，上游 v5 已 GA（迁移成本可控：add*Series→addSeries、setMarkers→createSeriesMarkers） | `web/package.json:26` | vendored master `api/chart-api.ts:206/324` |
| G5 | 无数据合流：缩略到 barSpacing<1px 时仍全量画 | 无对应实现 | `data-conflater.ts:40-53` + DPR 阈值常量 |
| G6 | 副图指标靠 ECharts/自绘拼窗，无多 pane 联动十字线 | `NtIndicatorChart.vue`（ECharts 路线） | v5 `addPane`/`swapPanes`，unity crosshair 天然 |
| G7 | 盘口/深度面板无合成兜底，断档即空 | `NtDepthBoard.vue`（无合成数据路径） | `datafeed.js:771-820` 合成阶梯 |
| G8 | 无图形/交互 e2e；viz 只有单测 mock | `web/src/viz/__tests__/theme-chart.test.ts`（mock 级） | `tests/e2e/graphics`、`tests/e2e/coverage` |
| G9 | 无服务端时钟校准，倒计时/竞价计时可能漂 | 未实现 | `datafeed.js:304-325` getServerTime |
| G10 | 高频文本无 measure/label 缓存，监控画布每帧量字 | `CanvasStage.vue:1047 measureWidth` 每帧调用 | `text-width-cache.ts`、`formatted-labels-cache.ts` |

---

## 4. 提升清单（按优先级）

<!-- markdownlint-disable MD029 -->

### P0（渲染正确性与性能，1~2 天量级）

1. **K线增量更新管线**（G1/G2 合并治理）
   - 做法：REST 首载照旧；market 推送中的 bar 帧直接 `series.update(bar)`，不再触发整段重取；`fitContent` 只在首载与手动"复位"时调用；`watch` 去掉 `deep:true`，bars 容器改 `shallowRef + markRaw`。
   - 落点：`web/src/viz/NtKline.vue`（新增 `updateLast(bar)` 暴露给父级）、`web/src/view/MarketView.vue:198-205`（push 里解析 bar 走增量分支，快照 meta 仍走 REST）。
   - 参照：`streaming.js:165-215`、e2e `series/update-data.js`。
2. **WS→图表 250ms 脏标记合并层**（G1 的下盘）
   - 做法：新增 `web/src/ws/batch.ts`：`Map<key,{last,dirty}>` + 250ms flush，所有 market/signal 高频帧先落 map 再统一派发；与 hub 的 per-topic 有序队列不冲突（合并在 listeners 之后）。
   - 参照：`streaming.js:206-215`（UPDATE_FREQUENCY=250）。
3. **订阅最小化**：切 symbol/freq 时把订阅收窄到当前证券；后端已按 key 合并（`hub.go:189-200`），前端对齐即省带宽。
   - 参照：`streaming.js:271-291`（unsubscribe 引用归零即退流）。

### P1（交互体验与专业化，3~5 天量级）

4. **升级 lightweight-charts 到 v5 并换用新 API**（G3/G4）
   - 迁移点：`addCandlestickSeries`→`addSeries(CandlestickSeries)`；`setMarkers`→`createSeriesMarkers`；顺手引入 v5 的 conflation（G5）与 `addPane`（G6）。可视化回收看 `/dev/gallery` 矩阵 + 补两个 graphics 截图用例。
5. **专业化交互开关**（G3）：磁吸十字线（信号标注对齐 OHLC，`magnet.ts:67-69`）、TrackingMode 开关、价格轴拖拽缩放、双击复位、K线区 priceLine（市价/成本/止损止盈线，`api/options/price-line-options-defaults.ts`）、`text-watermark` 股票代码水印（现成插件）。
6. **多 pane 副图收敛**（G6）：成交量已用 overlay 方案（`NtKline.vue:149-153`）；MACD/RSI/KDJ 迁到 v5 pane，十字线/时间轴天然联动，逐步替换 `NtIndicatorChart` 的 ECharts 拼窗（ECharts 保留给热力/雷达等轻图）。
7. **自定义系列沉淀 NT 特色层**：集合竞价区段底色、涨跌停高亮 bar、龙虎榜/大宗交易标记、复权断点标注——一个 `ICustomSeriesPaneView` 一个，hitTest 挂气泡（`icustom-series.ts:139-167`）。
8. **信号/成本 primitives**：持仓成本线、止损止盈框、框选测量（区间涨跌统计）用 ISeriesPrimitive + hitTest 实现可拖拽（`iseries-primitive.ts` 契约 + `pane-primitive-with-hit-test.js` 用例）。

### P2（链路健壮性与工程化）

9. **重连续接范式对齐**（streaming.js:83-98）：WS 恢复时除 resync 外，让图表自动 REST 回填缺口区间（NT 已有 resync→load 机制，补上"K线级别的缺口时间窗回填"，而非整段重载）。
10. **服务端时钟校准**：admin 提供 `GET /api/system/time`，前端所有倒计时/竞价进度显示用 server clock + drift 补偿（参照 `datafeed.js:304-325` 的失败降级）。
11. **盘口合成兜底**（G7）：`NtDepthBoard` 无档时用最近价+涨跌停/tick 规则生成占位阶梯，标注"合成/待数据"，杜绝空屏（`datafeed.js:771-820`）。
12. **viz 图形回归**：借鉴 `tests/e2e/graphics` 思路——`/dev/gallery` 每组件固定数据+固定尺寸截图入 CI（Puppeteer/Playwright + pixelmatch），主题×密度矩阵天然适合 golden 比对。
13. **高频文本缓存**：监控画布与 TerminalStrip 引入 `text-width-cache`/`formatted-labels-cache` 等价物，按 (font+text) 缓存，周期清理；canvas 销毁处学 `releaseCanvas`（`canvas-utils.ts:28-36`）。
14. **行情边车合同文档化**：把 G2 落地后推送帧的 bar/tick schema 写进 `doc/开发文档/M01` 设计文档（proto-first 原则），前后端类型同时从它生成。

## 5. UI 走查（对照 `doc/开发文档/1.视觉需求.md` 与本次静态审读）

- ✔ 红涨绿跌、主题×密度、玻璃霓虹材质已实现并有 token 层（`theme/tokens`），NtKline 颜色全部走 palette，不留硬编码色。保持。
- ⚠ K线十字线处于 Normal 漂移态，信号点"贴价对位"的视觉精度不足——开 MagnetOHLC 后，十字线吸附+价格轴标签才符合"专业盯盘"预期（对应视觉需求第七节"数据精确感"）。
- ⚠ K线图无法式水印，安防屏截取/多屏弹窗下归属感弱——`plugins/text-watermark` 半透明代码水印，与毛玻璃材质叠加无碍。
- ⚠ 副图指标与主图两个渲染体系（ECharts vs lightweight-charts）风格断裂（legend/网格/字体密度不一）——统一 v5 pane 后，密度/间距/字阶用同一组 token。
- ⚠ 监控画布数字闪动与文字宽测量无缓存（G10），节能模式应同时降帧+减 measure；对齐视觉需求的"节能/减少动态"档。

## 6. 附录：可整体抄录的资产速查

| 资产 | 来源文件 | 落到 NT 的位置 |
| --- | --- | --- |
| WS 生命周期+脏标记合并 | `vendor/.../streaming.js` 全文 | `web/src/ws/batch.ts` + bar 订阅器 |
| countBack 翻页器 | `datafeed.js:125-155` | `web/src/api/market.ts` 历史 K 线分页 |
| quotes 双状态机(REST 播种+WS 增量) | `datafeed.js:175-295` | quote-strip / 自选行情 |
| 合成盘口+断档兜底 | `datafeed.js:771-820` | `NtDepthBoard.vue` |
| DPR 自适应数据合流算法 | `data-conflater.ts:40-53` + `conflation/constants.ts` | 升级 v5 后开箱即用；自监控画布可移植思想 |
| 手势/惯性/磁吸参数 | `kinetic-animation.ts`、`magnet.ts` | 直接开关 options；参数可调 |
| 像素级柱状渲染技巧 | `candlesticks-renderer.ts` | monitor 画布柱状元素（如 TerminalStrip 画块）复用奇偶对齐/prevEdge |
| canvas/文本缓存卫生 | `canvas-utils.ts:28-36`、`text-width-cache.ts`、`formatted-labels-cache.ts` | `web/src/monitor/*`、popout 销毁流程 |

> 建议下一步：先做 P0-1/P0-2（改动小、收益立竿见影），并以 `/dev/gallery` 增加"tick 风暴"节拍打样页作为验收基准。

---

## 7. 实施记录

### P0-1 K线增量更新管线（2026-10-18 落地）

- `web/src/viz/NtKline.vue`：
  - 新增 `updateLast(bar)` / `refit()` 暴露口（`defineExpose`）；增量帧只走 `candle.update` / `volume.update`，不再 `setData`。
  - 模块级收盘价账本（`closes`）+ 末根时间戳（`lastTimeNum`）：同根替换、新根追加、旧帧乱序拒绝；MA5/10/20 由本地收盘价即时续算（`updateMa`），账本封顶 2000。
  - `fitContent` 改为"首载一次 + 换 freq 一次"（`fittedFreq`）；用户缩放不再被数据回流重置；样式切换路径保留 `apply(false)`。
  - props 监听去 `deep`：数组整体替换才全量套用，盘中 tick 的原地变更不触发重排。
- `web/src/view/MarketView.vue`：
  - market 推送帧直接解析 bar（`pushBarRows`，兼容 `{bar}`/`{bars:[]}`/顶层平铺三种形态与备选时间键），按行内或帧级 `symbol`/`freq` 与当前图表匹配后 `applyLiveBar` 原地更新 + 调用 `updateLast`；推送不再经 `refreshes.fromPush()` 整段 REST 重取，无 bar 行的帧只刷快照行。
  - 兜底对账定时器：页面可见 + hub 在线 + 已选代码时每 60s 全量 `load()` 一次（对齐指标/消息/成交与长尾数据）；数组超 800 根或本地为空时自动回落全量重载。
  - 手动读取/切代码/周期/面板参数/路由深链统一走 `reloadWithRefit()`：全量重载后显式 `refit()`；resync 与可见性恢复保持 `load()` 不复位视口。
- 验收：`web/src/viz/__tests__/theme-chart.test.ts` 新增增量用例（同根替换 MA5=13.2、新根追加、旧帧拒绝、refit 才二次 fitContent）；全套 58 文件 508 用例通过，`vue-tsc` 与 eslint 对改动文件零告警。
- P0-2 已由数据帧调度器落地（见下条）；P0-3 按当前证券收窄订阅待做。

### P0-2 WS→图表脏标记合并层（2026-10-18 落地，复用 gloomberb 数据帧调度器）

- 评审结论：并行会话已从 gloomberb 移植 `web/src/ws/data-frame.ts`（单例 `dataFrame`：`schedule(key, task)` 同 key 后到覆盖先到；rAF 对齐 + 最小 66ms 帧间隔；负载自适应拉长间隔；隐藏页 1Hz 兜底；无 rAF 环境退化定时器）。它比固定 250ms 间隔的 streaming.js 节拍更适合渲染绑定负载，且与视觉需求 §7.3 节能档同向——**不另起第二个合并器**（原拟 `ws/batch.ts` 方案弃用）。
- `web/src/view/MarketView.vue`：
  - `queueLiveBar(row, …)` 把匹配当前图表的 bar 帧以 `market:kline:代码:周期:bar时间` 为 key 投入 `dataFrame.schedule`——同一根 bar 的窗口内连续更新只应用最后一次，相邻两根 bar 不互并（与后端 hub 的 `kind:symbol:freq:bucket` 合并粒度对齐）。
  - 新增 `frameEpoch` 代际：任何全量重载（`load()` 全路径）+1，窗口内排队中的旧帧在执行时自动放弃（调度器单例无 clear，代际守卫等价）。
  - 报价条接通 `utils/quote-flash.ts` 单例：applyLiveBar 应用成功后按相对最后展示价的方向 `mark`，`.q-last` 得 300ms 涨跌色短脉冲（`q-flash--up/down`，color-mix 半透明底色淡出；几何用 padding+负 margin 保持不动；减少动态档由 global.css 的 `data-motion='reduced'` 全局 animation:none 自动接管，符合 §7.2"真实事件驱动短脉冲"）。
  - 快照 meta 帧不进调度器，仍即时刷新快照行。
- 验收：改动文件 eslint / `vue-tsc` 零告警；全套 62 文件 561 用例通过（含并行会话的 `data-frame`/`quote-flash` 单测与 G4 色觉门禁）。
- 说明：调度器与闪烁钟的基础设施源自并行会话的 gloomberb 对标移植（本仓库另有 gloomberb 提升方案文档）；本条只登记 TradingView 对标视角下"推送→渲染合批"这一同一收敛点，两处工作在此会师。
