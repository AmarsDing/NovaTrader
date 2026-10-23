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
| G7 | 盘口/深度面板无合成兜底，断档即空 | `NtDepthBoard.vue`（2026-10-19 已落地合成兜底，见 §7 P2-11） | `datafeed.js:771-820` 合成阶梯 |
| G8 | 无图形/交互 e2e；viz 只有单测 mock | `web/src/viz/__tests__/theme-chart.test.ts`（mock 级） | `tests/e2e/graphics`、`tests/e2e/coverage` |
| G9 | 无服务端时钟校准，倒计时/竞价计时可能漂 | `web/src/utils/server-clock.ts` + `GET /api/ops/time`（2026-10-19 已落地，见 §7 P2-10） | `datafeed.js:304-325` getServerTime |
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
7. **自定义系列沉淀 NT 特色层**：集合竞价区段底色、涨跌停高亮 bar、龙虎榜/大宗交易标记、复权断点标注——一个 `ICustomSeriesPaneView` 一个，hitTest 挂气泡（`icustom-series.ts:139-167`）。（2026-10-19 前两项已落地，见 §7 P1-7；后两项等 M01/M03 结构化数据到了再做）
8. **信号/成本 primitives**：持仓成本线、止损止盈框、框选测量（区间涨跌统计）用 ISeriesPrimitive + hitTest 实现可拖拽（`iseries-primitive.ts` 契约 + `pane-primitive-with-hit-test.js` 用例）。（2026-10-19 框选测量已落地见 §7 P1-8；成本线 P1-5 已用 priceLine 实现；止损止盈框等 M05/M06 信号数据进行情页后再用同一 primitive 管线做可拖拽版）

### P2（链路健壮性与工程化）

9. **重连续接范式对齐**（streaming.js:83-98）：WS 恢复时除 resync 外，让图表自动 REST 回填缺口区间（NT 已有 resync→load 机制，补上"K线级别的缺口时间窗回填"，而非整段重载）。（2026-10-19 已落地见 §7 P2-9：缺口窗口回填 + 抽离纯函数合并 + 三重整载兜底；未动后端——proto 的 start/end/limit 自 e6ac3a6 就在，P2-11 记录里"需先扩后端接口"的判断证伪已更正）
10. **服务端时钟校准**：admin 提供 `GET /api/system/time`，前端所有倒计时/竞价进度显示用 server clock + drift 补偿（参照 `datafeed.js:304-325` 的失败降级）。（2026-10-19 已落地见 §7 P2-10：路由按 /api/ops/* 家族落为 `GET /api/ops/time`——字面路由偏离如实记录；vendor 参照实为纯透传，offset 估算/RTT 丢弃/单调钳制为 NT 自行实现）
11. **盘口合成兜底**（G7）：`NtDepthBoard` 无档时用最近价+涨跌停/tick 规则生成占位阶梯，标注"合成/待数据"，杜绝空屏（`datafeed.js:771-820`）。（2026-10-19 已落地见 §7 P2-11：viz 层能力就位，刻意不合成挂单量；业务侧等 M01 五档数据通道后传 prop 即生效）
12. **viz 图形回归**：借鉴 `tests/e2e/graphics` 思路——`/dev/gallery` 每组件固定数据+固定尺寸截图入 CI（Puppeteer/Playwright + pixelmatch），主题×密度矩阵天然适合 golden 比对。（2026-10-20 已落地见 §7 P2-12：Playwright golden 矩阵 23 用例 × 主题 × 密度 = 92 张；golden 权威源钉 CI 镜像 + workflow_dispatch 重刷通道；落地过程顺带根治 CanvasStage ResizeObserver 自激——gallery 内监控画布 golden 全空白的根因在宿主塌缩+内联支距，详见 §7）
13. **高频文本缓存**：监控画布与 TerminalStrip 引入 `text-width-cache`/`formatted-labels-cache` 等价物，按 (font+text) 缓存，周期清理；canvas 销毁处学 `releaseCanvas`（`canvas-utils.ts:28-36`）。（2026-10-19 已落地见 §7 P2-13：宽测缓存 4bac98e 已有、本轮补卸载清零+releaseCanvas；formatted-labels 不另造——热点全在 Vue computed，框架已 memo）
14. **行情边车合同文档化**：把 G2 落地后推送帧的 bar/tick schema 写进 `doc/开发文档/M01` 设计文档（proto-first 原则），前后端类型同时从它生成。（2026-10-19 已落地见 §7 P2-14：合同立档 M01 §7，制图+对账实测纠正六处分歧——bar 帧已产未路由、tick 属预留无生产者、stale 死显示等；前后端类型同生成的 proto 化留作后续项 M01 §7.9）

## 5. UI 走查（对照 `doc/开发文档/1.视觉需求.md` 与本次静态审读）

- ✔ 红涨绿跌、主题×密度、玻璃霓虹材质已实现并有 token 层（`theme/tokens`），NtKline 颜色全部走 palette，不留硬编码色。保持。
- ✔ ~~K线十字线处于 Normal 漂移态~~（2026-10-19 P1-5 已开 MagnetOHLC，十字线吸附 OHLC + 价格轴精确对价）。
- ✔ ~~K线图无法式水印~~（2026-10-19 P1-5 已接 `createTextWatermark` 证券代码水印，主题联动，与毛玻璃材质叠加无碍）。
- ✔ ~~副图指标与主图两个渲染体系（ECharts vs lightweight-charts）风格断裂~~（2026-10-19 P1-6 已统一 v5 pane：MACD/RSI 迁入 NtKline，fontFamily/fontSize/网格/文字色与主图同一份 ChartOptions，ECharts 只留 dev-gallery 走查）。
- ✔ ~~监控画布数字闪动与文字宽测量无缓存（G10），节能模式应同时降帧+减 measure；对齐视觉需求的"节能/减少动态"档。~~（2026-10-19 P2-13：宽测缓存已有（256 条、主题切换/卸载清零、二分 clip），reduce 档由 `createVisualFrameLoop` 的 canRender 与全局 data-motion 接管降帧；数字闪动保留为事件脉冲）

## 6. 附录：可整体抄录的资产速查

| 资产 | 来源文件 | 落到 NT 的位置 |
| --- | --- | --- |
| WS 生命周期+脏标记合并 | `vendor/.../streaming.js` 全文 | `web/src/ws/batch.ts` + bar 订阅器 |
| countBack 翻页器 | `datafeed.js:125-155` | `web/src/api/market.ts` 历史 K 线分页 |
| quotes 双状态机(REST 播种+WS 增量) | `datafeed.js:175-295` | quote-strip / 自选行情 |
| 合成盘口+断档兜底 | `datafeed.js:771-820` | `NtDepthBoard.vue` + `viz/depth-synth.ts`（2026-10-19 已落地，剔伪量） |
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
- P0-2 已由数据帧调度器落地（见下条）；P0-3 订阅收窄已于 2026-10-19 落地（见 P0-3 条）。

### P0-2 WS→图表脏标记合并层（2026-10-18 落地，复用 gloomberb 数据帧调度器）

- 评审结论：并行会话已从 gloomberb 移植 `web/src/ws/data-frame.ts`（单例 `dataFrame`：`schedule(key, task)` 同 key 后到覆盖先到；rAF 对齐 + 最小 66ms 帧间隔；负载自适应拉长间隔；隐藏页 1Hz 兜底；无 rAF 环境退化定时器）。它比固定 250ms 间隔的 streaming.js 节拍更适合渲染绑定负载，且与视觉需求 §7.3 节能档同向——**不另起第二个合并器**（原拟 `ws/batch.ts` 方案弃用）。
- `web/src/view/MarketView.vue`：
  - `queueLiveBar(row, …)` 把匹配当前图表的 bar 帧以 `market:kline:代码:周期:bar时间` 为 key 投入 `dataFrame.schedule`——同一根 bar 的窗口内连续更新只应用最后一次，相邻两根 bar 不互并（与后端 hub 的 `kind:symbol:freq:bucket` 合并粒度对齐）。
  - 新增 `frameEpoch` 代际：任何全量重载（`load()` 全路径）+1，窗口内排队中的旧帧在执行时自动放弃（调度器单例无 clear，代际守卫等价）。
  - 报价条接通 `utils/quote-flash.ts` 单例：applyLiveBar 应用成功后按相对最后展示价的方向 `mark`，`.q-last` 得 300ms 涨跌色短脉冲（`q-flash--up/down`，color-mix 半透明底色淡出；几何用 padding+负 margin 保持不动；减少动态档由 global.css 的 `data-motion='reduced'` 全局 animation:none 自动接管，符合 §7.2"真实事件驱动短脉冲"）。
  - 快照 meta 帧不进调度器，仍即时刷新快照行。
- 验收：改动文件 eslint / `vue-tsc` 零告警；全套 62 文件 561 用例通过（含并行会话的 `data-frame`/`quote-flash` 单测与 G4 色觉门禁）。
- 说明：调度器与闪烁钟的基础设施源自并行会话的 gloomberb 对标移植（本仓库另有 gloomberb 提升方案文档）；本条只登记 TradingView 对标视角下"推送→渲染合批"这一同一收敛点，两处工作在此会师。

### P0-3 订阅收窄到当前证券（2026-10-19 落地，跨端）

- 协议（向后兼容）：sub/resync 帧新增可选 `market_filters: [{symbol, freq?}]`；**字段整体缺席 = 全量**——旧客户端连新服务端、新客户端连旧服务端行为都不变。
- 后端 `backend/app/admin/internal/gateway/hub.go`：
  - `frame` 增加 `Symbol/Freq`（发布时解析一次，复用 marketKey 的 symbol/freq 提取：嵌套 bar、平铺、`stock_code|code` 别名）。
  - `clientSubscription` 增加行情域（`marketAll/marketKeys/marketSymbols`）：`filters` 为空或条目全缺 symbol → `marketAll`（宁可全量不饿死连接）；freq 省略 = 该证券全周期。
  - `allowsMarket` 匹配顺序：非 market 主题/全量 → 放；无 symbol 的帧（快照 meta、事件）→ 放；freq 不可判定 → 证券级放行（宁多勿丢）；否则精确 symbol+freq。
  - 三条路径同一把尺：`addFrame` 实时分发、`beginSubscription` 与 `completeRecovery` 的回放收集都过 `allowsMarket`——重连不会把别人证券的 500 根回放补进来。
- 前端 `web/src/ws/hub.ts`：
  - `subscribe(topic, interest?)`：`market` 订阅可带 `{symbol, freq?}`；每个订阅者一个兴趣 handle，**期望并集 = 全部 handle 的去重合集**；任何一个 handle 未声明（或 symbol 空白）即一票全量、字段不下发。
  - `syncTopics` 统一计算并随帧下发 `market_filters`；`subscribe`/退订只在并集 JSON 变化时才重发 sub（同代码重复订阅、换账户等不动线）。
  - 单连接多面板共享：这是"订阅者兴趣的并集"而不是"全局过滤"，监控画布/涨停梯队等全量消费者在场时自动回退全量。
- `web/src/view/MarketView.vue`：`resubMarket()` 挂到 onMounted 与 `[symbol,freq,account]` watch（symbol/freq 变化才重订阅；account 不动线）；无代码时保持全量（快照行可用）。
- 验收：后端 `go build/vet/test ./app/admin/internal/gateway` 全绿（新增收窄/通配 freq/freq 缺失放行/回放过滤/无效条目回退 5 用例）；前端 hub 26/26（新增 filters 下发/一票全量/并集去重/切换重发/空白回退 5 用例）；eslint 与 `vue-tsc` 零告警；全套 65 文件 596 用例通过。
- 参照：`streaming.js:271-291`（订阅引用归零即退流）；单机 localhost 部署下带宽收益有限，但对齐后有源节流才有意义（后端推送侧 key 合并已于早先具备，见 §3.1）。

### P1-4 lightweight-charts v5 迁移（2026-10-19 落地）

- 依赖：`web/package.json` ^4.2.2 → **^5.2.1**（已安装并 lock）。lwc 独立 chunk 177.07 kB（gzip 57.36 kB），`npm run build` 全绿。
- API 迁移（消费面只在 `web/src/viz/NtKline.vue` 与其测试 mock，属意料之外的小）：
  - `addCandlestickSeries`/`addHistogramSeries`/`addLineSeries` → 统一 `chart.addSeries(CandlestickSeries|HistogramSeries|LineSeries, options)`；
  - `series.setMarkers` → `createSeriesMarkers(candle, [])` 插件 API（模块级 `markersApi`，卸载置空；全量 `apply()` 里 `setMarkers` 替换语义不变）；
  - 其余触点（`setData/update/applyOptions/subscribeCrosshairMove/priceScale('vol')/fitContent/ColorType/CrosshairMode`）v5 保持兼容，未动。
- 测试 mock 对齐 v5 形态：`addSeries` 按 series 定义派发（line 系列用独立游标，不依赖调用序号——多次挂载也不错位），标记断言改到 `markers.setMarkers`；6/6 通过。
- 验收：`vue-tsc` 与 eslint 零告警；全套 65 文件 596 用例通过；`npm run build`（typecheck+lint+打包）通过。
- 解锁的后续能力（未随本条启用，避免一次改动过大）：v5 `addPane` 多窗格（G6）、`chart.options().crosshair` 磁吸/TrackingMode（P1-5）、`addCustomSeries`+`CustomConflationReducer` 数据合流（G5）、`pane-primitive` 特色图层（P1-7/P1-8）。vendored master 已到 v6 时代命名（`HorzScaleItem`、markers 变 primitives），后续升 v6 时注意二次迁移。

### P1-5 专业化交互开关（2026-10-19 落地，首批三项）

- **磁吸十字线**：`NtKline` crosshair `Normal` → `MagnetOHLC`——十字线贴最近 K 线的开/高/低/收，价格轴标签精确对价（对应 §5 走查"数据精确感"项；`magnet.ts:30-86` 的候选收集→坐标最近→反解回价由此开关启用）。
- **证券代码法式水印**：`NtKline` 新增 `watermark` prop（viz 层保持纯组件，文本由父级给）；v5 `createTextWatermark(panes()[0], …)`，颜色 `text3` 28% 透明度随主题联动，`MarketView` 传当前代码_trim 后文本，空串不画。多屏弹窗/安防截屏场景归属感（§5 走查第二项）。
- **价格参考线（priceLine）**：`NtKline` 新增 `levels` prop：`{price, color?, title?, lineStyle?}`，重复价去重、非法（非有限/≤0）跳过，`apply()`/主题切换整体重建（量小，重建优于逐条 diff 无残留）。`MarketView` 接**摊薄成本线**＝（Σ买成交额−Σ卖成交额）/净持仓，填充数据推导、琥珀虚线带轴标"摊薄成本"，净额 ≤0 或数据不足不画；legend 在成本线存在时补"琥珀虚线为摊薄成本"，与同为琥珀的 MA20（曲线）区分。
- 价格轴拖拽缩放、双击复位在 v5 为默认内置（`handleScale`/`handleScroll`/double-click reset），零代码获得；TrackingMode 开关做成何 UI 形态未定，本轮不硬做。
- 验收：mock 补 `createTextWatermark`/`LineStyle`/`createPriceLine` 与 `chart.panes()`；新增用例覆盖磁吸 mode=3、水印主题联动（暗→亮 rgba(97,98,99,0.28)）、参考线去重/非法跳过/样式映射/主题重建；7/7 viz 用例、`vue-tsc`/eslint 零告警、全套 67 文件 614 用例通过。
- 止损止盈/入场价三线等信号级 priceLine：数据在信号面板（trade_signals），等 M05/M06 链路把信号带到行情页时直接用 `levels` 传入即可，无需再改 viz。

### P1-6 多 pane 副图收敛（2026-10-19 落地，G6）

- **MACD/RSI 迁入 v5 pane**：`NtKline` 新增副图管线（`ensureSubPanes/feedSubPanes/destroySubPanes`）——MACD 窗（柱按符号红涨绿跌 + 电蓝 DIF + 琥珀 DEA，stretch 0.35）、RSI6 窗（冰青线 + 80/20 虚线参考位，stretch 0.25），与主图共用时间轴/十字线（MagnetOHLC 全域生效），fontFamily/fontSize/网格/文字色与主图同一份 ChartOptions，风格断裂消除。
- **懒建饿拆**：指标行 macd/dif/dea/rsi6 全空时两窗格整体拆除（`removePane` 先高索引后低索引，索引实时取防错位）；主题切换先拆再由 `apply(false)` 按新色重建（序列色在建图时固化，增量 patch 改不到）。
- **信息升级**：旧 ECharts `NtIndicatorChart` 只画 macd+rsi6，v5 版补上 `dif`/`dea` 两条线（`MarketIndicator` 字段本就有，零协议改动）。
- **ECharts 指标图退场**：`MarketView` 删除 `NtIndicatorChart` 渲染分支与孤儿 v-else；`NtIndicatorChart.vue` 保留给 `/dev/gallery` 走查，业务零引用。legend 增"副图 MACD 柱/电蓝 DIF/琥珀 DEA、冰青 RSI6（虚线 80/20）"按需后缀；指标未返回仍保留诚实提示行。
- 验收：测试 mock 补 `addPane/removePane`（`paneIndex()` 动态取当前索引）与 `setStretchFactor`；新增用例覆盖建仓（2 pane + 拉伸系数 + 柱色符号性 + DIF/DEA/RSI 全量 setData + 80/20 参考线）与指标清空拆窗；8/8 viz 用例、eslint 零告警。
- 残留待真机复核：260px 窄容器三窗格高度分配（0.35/0.25 拉伸比效小）、pane 分隔线拖拽手感、RSI 80/20 虚线在深海的克制度——走查项已随本轮交付列出。

### P1-7 自定义系列沉淀 NT 特色层（2026-10-19 落地，首批两项）

- **集合竞价时段底色（首个 custom series 落地件）**：新文件 `web/src/viz/session-bands.ts` 产出 `createSessionBandPaneView`——沪深 09:15–09:25 开盘竞价（冰青 7%）、09:25–09:30 静默报单（text3 6%）、14:57–15:00 收盘竞价（电蓝 7%），1m 图整高铺底、相邻同类合并 fillRect；颜色 draw 时实时读 palette，主题切换零重建。v5 两条关键契约：序列按创建顺序分层（无 zIndex），色带必须在蜡烛之前建（`NtKline` onMounted 首个序列）；`priceValueBuilder: () => []` + `autoscaleInfoProvider: () => null` 让色带既不碰价格轴缩放也不进十字线取值。数据只放窗内的根，时段外天然不占位。
- **涨/跌停封板高亮**：新文件 `web/src/utils/limit.ts` 纯函数——按代码前缀推断涨跌幅档（创业 300/301/302、科创 68x → 20%；北交所 920/4/8 段 → 30%；其余 10%；ST 的 ±5% 从代码不可见，已知启发式缺口：**只会漏亮不会误亮**）；判定=收盘精确落在「昨收×(1±档)」取整 0.01 的理论价 且 最高/最低=收盘（尾盘封死未开板）。命中即单项 `borderColor/wickColor=warn` 琥珀描边，实体保持红涨绿跌；全量 setData 与 updateLast 增量通道同规则（同根替换取前根收盘、新根取末根收盘，均在 closes 账本更新前算）。
- **NtKline 新增 `symbol` prop**：与 `watermark` 独立传参（显示文本不兼任逻辑输入）；watch 键含上 symbol；MarketView 接 `symbol.trim()`，legend 按需补「底色=集合竞价时段」（仅分钟）与「琥珀描边=涨/跌停封板（板块推断）」。
- 未做（数据未到，不造假图层）：龙虎榜/大宗交易标记、复权断点标注——等 M01/M03 的结构化数据落地后复用同一 custom series 管线加 hitTest 气泡；纯背景色带无可悬停信息，故本轮不实现 hitTest。
- 测试三件：`utils/__tests__/limit.test.ts`（9 例：档位映射/封板/触板未封/20% 档不误亮/昨收缺失与非法值）、`viz/__tests__/session-bands.test.ts`（窗边界 09:15/09:25/09:30/14:57/15:00、数据过滤、fake canvas target 下的合并矩形几何断言）、`theme-chart.test.ts` 新增集成例（1m 铺带→切日线清空、全量封板描边、updateLast 新根封板与同根解封复位）。
- 验收：焦点 23/23、全套 **72 文件 648 用例**、vue-tsc/eslint 零告警。中途自修两处类型对齐：`Bar` 字段实为 `number | null`（`limitFrame`/`limitState` 入参放宽，Number.isFinite 不转型故天然兜住 null）、`crosshairMarkerVisible` 不属于 `CustomSeriesOptions`（删）。

### P1-8 框选测量 primitive（2026-10-19 落地，信号/成本层首件）

- **框选测量（区间涨跌统计）**：新文件 `web/src/viz/measure-box.ts` 产出 `createMeasurePrimitive`——半透明填充（accent 8%）+ 单像素描边（accent 55%，半像素对齐防发虚）；状态存**逻辑索引×价格双区间**（不存像素/时间戳），缩放平移后框仍贴住原批 K 线；角越出可视区即整框跳过（`logicalToCoordinate/priceToCoordinate` 返回 null，不做半框裁剪）。hitTest 按 range 级（priority 0）命中，不抢 K 线点/线语义。
- **交互在组件侧**：`NtKline` 新增 `measure` prop；pointerdown/move/up 以**捕获相**绑在图表容器（先于 lwc 内层 canvas 的手势处理，免得被平移抢走）；拖拽起点=anchor、move 更新对角；统计读数走 overlay chip（`N 根 · Δ价 (Δ%) · 振幅 %`，方向性：基准=起点收盘），Esc 或 chip × 清框摘件；关闭开关同样清框。颜色 draw 时实时读 palette，主题切换零重建（与色带同策略）。
- **逻辑索引即行号**的隐含约定：测量统计按 `seriesRows()` 全量行的数组下标对齐 lwc 逻辑索引（主图无 whitespace 错位）；`apply()` 里 `lastRows` 快照供统计。
- MarketView：表单行加「框选测量」开关钮（无数据禁用，开启时高亮），测量提示行；换 symbol/周期/账户一律复位测量态（防框住旧数据）。NtButton 默认 `type="button"`，表单内免 prevent。
- 未做（依赖前置）：可拖拽止损止盈框——等 M05/M06 把 trade_signals 带进行情页后，在同一 primitive 管线（attached/detached + hitTest 已有雏形）加边线拖拽与气泡；命中层暂不区分「边/面」两级。
- 测试两件：`viz/__tests__/measure-box.test.ts`（正/反向统计、越界收敛、fake 换算下的 fillRect/strokeRect 几何断言、框内/框外/出屏 hitTest、detached 复位）；`theme-chart.test.ts` 集成例（mock 的 detachPrimitive 仿真 lwc 回调 `detached()`；pointerdown→move→up 出框、chip 读数 3 根/+18.18%/36.36%、Esc 清框、关开关不再出框）。
- 验收：焦点 16/16、全套 **75 文件 671 用例**、vue-tsc/eslint 零告警。自修：`Logical` 在 5.2.1 是品牌类型（`number & {[Symbol.species]:'Logical'}`），坐标换算处补 cast。

### P2-11 盘口合成兜底（2026-10-19 落地，G7）

- **合成阶梯纯函数**：新文件 `web/src/viz/depth-synth.ts` 产出 `buildSyntheticDepth(last, { symbol, preClose, tick })`——最近价 ± tick 累进上下各五档、价格 round2；封顶复用 `utils/limit.ts` 板块档（封板价本身可排，越过即停：封涨停时卖侧空、买侧照出，封跌停镜像）；昨收缺失不封顶；两侧合计为空返回 null（交给空态）。对照 `datafeed.js:771-820` 但**刻意剔掉伪造挂单量**：volume 恒 null → 量位显示 '—'，挂单量是无法合成的事实，画柱子最有欺骗性（诚实数据：宁缺勿假）。
- **组件接通**：`NtDepthBoard` 新增 `lastPrice/symbol/preClose/tick` 四个可选 prop；**双边都无档才触发合成**（任意一侧有真实档位即原样渲染，真/合不混）；合成态量柱不渲染、行价加 `--syn` 类透明度 0.55（保留卖红买绿方向色），中线显示「最近价 + 合成·待数据」琥珀标注。价单元格顺势统一走 `price()` 展示格式（两位小数），真实档位同样生效。
- 展示面：`/dev/gallery` 新增两卡——常规无档占位（上下各五）与涨停封板（只出买侧）。
- **边界诚实交代**：全仓 Grep 核实 `NtDepthBoard` 目前仅 `/dev/gallery` 消费（业务面板无五档落点），本轮交付的是 viz 能力与兜底行为本体；M01 行情边车接通五档后，行情面板传 `:last-price/:symbol/:pre-close` 即自动获得断档兜底。~~顺带核实：P2-9（重连缺口时间窗回填）被 `api/market.ts` 的 `getBars(symbol, freq)` 无时间窗参数卡住，需先扩后端 bars 接口再动，暂搁置。~~（2026-10-19 更正：该判断失实——`market.proto:113-123` 的 start/end/limit 自 e6ac3a6 即存在，P2-9 已由纯前端落地，见 §7 P2-9。）
- 测试一件：`viz/__tests__/depth-board.test.ts`（纯函数 4 例：常规五档含 null 量、临近涨停截断到封板价（含 20% 档）、封板两侧镜像、无昨收不封顶+自定义 tick+无效最近价 null；组件 4 例：合成标注与卖五→卖一倒序、量柱隐藏、封板不越价、有真档不合成、无档无价纯空态）。
- 验收：焦点 8/8、全套 **76 文件 679 用例**、vue-tsc/eslint 零告警。

### P2-13 高频文本缓存与画布销毁卫生（2026-10-19 收尾，G10）

- **现状核实（不重复造）**：`CanvasStage` 的文字宽测量缓存已由 4bac98e（监控驾驶舱 UI 整改）落地——font+text 复合键、256 条到顶清、主题/画质切换在 `syncVisualMode` 清、`clipText` 二分 8 次测量封顶；`TerminalStrip` 是 DOM 磁带条，文本全走 Vue computed（框架自带 memo），无 canvas/无 measureText——本条对 TerminalStrip 无客可做，诚实销项。
- **本轮补齐销毁卫生**：
  - `canvas-stage.ts` 新增 `releaseCanvas`（对照 lwc `canvas-utils.ts:28-36`）：宽高缩 1×1，GPU 位图立刻让位不等 GC；null 兜底（getContext 失败路径 ref 未接上）。多屏 popout 关窗时确定性回收背板。
  - `CanvasStage.onBeforeUnmount` 补 `measureCache.clear()`（防御性清零；该宽测缓存是 `<script setup>` 顶层的**每实例** Map、随卸载随 GC，主题切换另有 `syncVisualMode` 清零路径——本轮评审曾把此表述误写为「模块级 Map」，已修正）+ `releaseCanvas(canvas.value)` + `ctx = null`。
- **未做并说明理由**：formatted-labels-cache 等价物不引入——逐帧文本的热路径都在 scene-model/computed 层，Vue 响应式已 memo，再加一层等于重造 computed。
- 测试：`canvas-stage.test.ts` 新增 releaseCanvas 例（1×1 收缩 + null/undefined 安全）；监控全套 **18 文件 261 用例** 绿。
- 验收注意：全量测试当时有并行会话半场红（`panels/__tests__` 整目录未跟踪 + registry-integrity 与 .snap 在其 WIP；vue-tsc 仅剩其 `panels/risk/alert-rules.panel.vue` 类型错），按惯例（门禁红先查归属）核实与本条无关。评审 workflow（3 维审视 + 逐条反证，lifecycle/coverage 两维被 429 打断）回归维发现 1 条措辞错误（measureCache 被误称模块级 Map）经反证确认并修正，覆盖/生命周期两维由主会话亲自复核补齐（全 src 唯一自绘 2D 画布即 CanvasStage；NtEChart `dispose():102`、NtKline `chart?.remove():545` 库层自管；TerminalStrip 纯 DOM）。

### P2-14 行情推送帧合同立档（2026-10-19）

- **做法**：5-agent 制图 workflow（后端 / 前端 / 测试钉 / 文档插点）→ 合同审计员逐项对账 → 主会话一手代码复核关键声称；合同写入 `doc/开发文档/M01-数据采集/设计文档.md` 新增 **§7 推送帧合同**（原 §7–§10 顺延 §8–§11；全库无按节号引用 M01 的外部文档，重排安全），M02 §1 的 `{"as_of","count"}` 简写收紧为指向 M01 §7.3，双源漂移销案。
- **对账实测纠正（写进合同诚实面，六处分歧全部以代码销案）**：
  - bar 帧（`market.bar.1m`）**已产未路由**——hub `natsRoutes`（hub.go:598-614）无绑定，前端整条增量消费管线今日消费零帧，图表收敛 100% 靠 REST（初始 load + 60s 对账 + resync 全量）；P0-1 记录中「REST 权威胜过在途增量帧」的叙事降级为**待用声明**。
  - `stale=true` meta 帧线上不可达（失败路径只标 Redis 不发帧）——前端 stale 展示位是死显示，真实陈旧信号 = 帧停更或 REST Quote.stale。
  - tick/quote 仅是 hub 合流白名单的预留帧类名，全仓无生产者——合同不得按「既有 tick schema」书写。
  - 「无 bar 行即 meta」兜底（MarketView.vue:363）无形态校验；freq 守卫对真实 bar 帧结构性失效（Bar 无 freq 键、前端忽略 metadata.subject）——列为 bar 接线前的两条必修。
  - bar 时间双解析器隐患销案：槽时间恒带 +08:00，Date.parse 与 chartTime 结果一致。
  - `{"bars":[…]}` 批量帧不参与服务端合流（行级键对 marketKey 不可见 → 空 key 直放；前端 data-frame 合批可正确归并，无害但须防误依赖）。
- **一致口径 11 项**（信封别名序/seq 序号语义/epoch 代际/慢消费者对称保护/at-most-once+REST 恢复/时间格式/market_filters 全有或全无/alert 帧闭环/心跳与本体分层/frameEpoch 同构等）经审计员与地图双侧逐条对账。
- 测试钉清单写入 M01 §7.8（后端 bar/usecase/hub/observability/trade consume、前端 hub.test/observability）；缺口两条如实：bar 逐行合并语义为私有函数无独立用例、重放×REST 交叠收敛无显式测试。workflow map:test-pins 被 ECONNRESET 打掉，测试钉由主会话 grep 补齐。
- 本轮纯文档，门禁/走查不适用；制图 4/5 agent 成功 + 审计员复核，关键声称（路由表/信封/槽口径）另经主会话一手核读。M01 文档携并行会话 N2-01/N4-02 半场 WIP，提交时须按归属拆账，不可整文件收。

### P2-9 重连缺口时间窗回填（2026-10-19 落地）

- **做法**：`getBars` 加 `BarsWindow { start?, end?, limit? }` 透传（后端零改动——`market.proto:113-123` 的 start/end/limit 自 e6ac3a6 就在，P2-11 记录"需先扩后端接口"的判断证伪已于 :293 更正）；`onResync` 处理器改为 `(await backfillGap()) || (await load())`，ready=false 仍 throw 保持 hub required 语义。回填 = 以末根时刻起窗 `{ start, limit: LIVE_CAP }` 拉新行（1d 末根先按上海日历日截日），`mergeGapRows` 同根替换+新根追加+逐行 `updateLast`（不重套不动视口）。
- **合并纯逻辑抽离** `src/view/market-backfill.ts`：算/判/写分离——先在 `existing.slice()` 副本上合并，`covered && !overflow` 才 splice 写回，失败路径不留脏增量（修复评审 #4/#5）；三形态兜底整载：无基准行 / 首行晚于末根（缺口宽于窗口）/ 合并超 LIVE_CAP。粒度参数 `'ms' | 'day'`：day 模式下同日不同格式（`'2026-10-19'` 与 `'2026-10-19T14:59:00+08:00'`）视为同根，裁掉 existing 中日历日 ≥ 窗口首日的尾部再接 REST 权威行，根治（待用 bar 管线的）1m 污染尾根把当日日线行挡在同根替换外（修复评审 #1）。
- **并发语义**：`frameEpoch` 在 await 期间变化（并发整载接管）不再是"视为恢复完成"，改为 `return load()` 汇进 refresh-queue 跟随其真实结果，不再向 hub 假 ack（修复评审 #2/#8 同源）；disposed 仍直接按完成处理。窗口请求失败 `console.warn` 留痕后转整载（修复 #6，对齐 hub.ts:514 先例）。注释补"待用 bar 管线"限定（修复 #9，呼应 M01 §7.4 已产未路由事实）。
- **评审闭环**：3 维 finder + 逐条敌意核实（18 agent，1.15M token）出 **11 条确认**——3 major（混合粒度丢当日日线行 / epoch 竞态假 ack ×2 同源）、8 nit（原地突变后判 overflow、applied 不喂图分叉、静默 catch、M01 §7 文案行号陈旧、:293 过期判断、注释缺限定、测试标题与分支不符 ×2），全部修复或条款化；**3 条被拒**（不可解析尾根误报恢复——被后端 400→catch→整载链路兜底且 mergeTail 现已加严格守卫；1d 混合 start+空 fetched 误判——前置管线今日零帧不可达；cap≤0 语义——有意不变量且唯一调用方恒传 LIVE_CAP）。
- **已知有界回归（验收口径待定）**：回填成功路径不刷新指标/消息/成交（旧版整载会顺带刷），交由 60s 对账定时器补——页面可见时最长滞后 60s，隐页对账暂停时延到下次 onVisible。已在代码注释明示；是否接受由用户验收时定夺（评审 nit #7）。
- **测试**：新增 `src/view/__tests__/market-backfill.test.ts`（16 例：cmpTime 三分支、空/乱序/够不到/超 cap 不动 existing、day 粒度四例含 #1 原样 repro）+ `src/api/__tests__/market-bars.test.ts`（4 例：默认行数/窗口透传/归一/barTime·bar_time 回退链）；门禁 38 文件 282 用例全绿 + vue-tsc 0 + eslint 0。
- **文档同步**：M01 §7.4/§7.6 resync 语义改为"先窗口回填失败才整载"、§7.3/§7.4 行号指针随本轮 edit 刷新、§7.8 测试钉补上 backfill 套件并销掉"逐行合并无独立用例"缺口。M02 开发进度.md 条目因文件携并行会话未提交 WIP（intraday-alerts）暂缓写入，其提交后补一行指针——决策 4 三件套留痕缺口如实交代。
- 走查：本轮纯行为/数据链，无模板与样式改动，视觉走查不适用；视口保全（不重套不 fitContent）是本设计核心不变量，由 NtKline 非深监听契约 + updateLast 只 `update()` 双重保证。

### P2-10 服务端时钟校准（2026-10-19 落地）

- **做法**：proto-first 新增 `SystemClock/GetServerTime`（`backend/api/admin/v1/time.proto` + 三生成件），admin gateway 落 `GET /api/ops/time`——**字面路由偏离如实交代**：roadmap 原文写的是 `/api/system/time`，实施按 `/api/ops/*` 家族一致性改挂 ops 域（与 environment/observability 同_prefix），文档 :166 条目注记同步。`gateway/time.go` 逐模式镜像 environment.go：`transport.FromServerContext` 拿头 → 仅以 Authorization 重建请求 → `g.principal` 鉴权 → `g.now()` 直读 admin 进程钟（路径写明「无缓存、无转发」——前端要的就是这台机器处理请求那一刻的钟）；`sync.Once` 懒建 `khttp.NewServer`、GET-only 405、`Cache-Control: no-store` 显式钉死（评审 nit）。前端 `web/src/utils/server-clock.ts`：DI 工厂 `createServerClock` + 模块单例 `live` + `main.ts` 显式 `startServerClock()`（不做懒拉取——测试面不发网）。
- **算法语义**（vendor `datafeed.js:304-325` 实为纯透传 getServerTime，以下估算纪律全为 NT 自行实现并如实标注）：offset = 服务端 epoch_ms − 本地中点时刻（RTT/2 口径），不做速率级漂移估计；样本过滤——RTT > 1000ms 视为睡眠唤醒/代理停顿丢弃保旧 offset，epoch_ms 非有限按合同违约拒绝；失败降级照 vendor 纪律 warn 留痕 + 保留旧 offset（未成功过则 offset 0 = 本地钟），不抛给消费方；重校准周期 300s + 页面隐藏跳过 + visibilitychange 补一次。
- **单调钳制（评审 major 收口）**：`serverNow` 不因估算抖动回退——重校准把 offset 往回收（RTT 中点假设的秒级噪声）时读数原地挂起等本地钟追平，保护 45s/90s/+5s/30s 新鲜度闸门不被重校准瞬时误伤；钳制只在校准后生效（未校准 = 纯本地钟原样直报，系统回拨跟随显示，vitest 时钟回放不会被钳住）；**挂回上限 30s**——回拨超限即真实时钟事件（远端发远未来样本/NTP 步进/休眠唤醒），warn 留痕后一次性跟随，绝不无限冻结到刷新页面（无界冻结会让信号永不过期、推送未来守卫误杀、底栏钟与现实脱节）。
- **消费侧重接台账**：凡「与戳比对」的本地钟全切 `serverNow/serverDate`——Signal/Overview/ConditionalPlans/TradeView（有效期倒计时、行动分级、readAt/clockMs 新鲜度对戳、signalDraftBlock）、StatusBar 底栏钟（`serverDate()`）、MonitorView clockMs/nowMs 与 pulseFlow/counterFlow/activity/hole/observeGraph/enqueue 全数戳、load-input sampledAt（与后端同源）、CanvasStage data-pulse（rAF 节拍与 drawAmbient 的 performance.now 不动）、pulses.ts push 戳、detail.ts tokensPerSecond 窗口、datasource-health 陈旧度、account-push 未来守卫、stores account/risk/market checkedAt、alerts toast 时刻。**有意保留**：page-refresh 自容节流、data-frame/quote-flash 动画用 performance.now、ID 唯一性戳（notify-center/brain-monitor/risk-chain/auth token）、GalleryView（dev）、timeband.ts 机器时区（另立技术债条目缓议）；stores/symbol.ts 携并行会话 WIP 未动。另把 `format.ts` validRemainText/isValidExpired 与 `decision-desk.ts` buildDecisionAction 的 `now = Date.now()` 默认值改为**必填**（评审 nit：潜藏默认值防再混本地钟，全量调用点已核均显式传参）。
- **评审闭环**：3 维 finder + 逐条敌意核实（11 agent，618k token）出 **6 条确认、0 被拒**——2 major（无界冻结 → 挂回上限，见上；kratos proto client 再生成把 `backend/openapi.yaml` 耙成 SystemClock 单服务范围 → 已 `git checkout` 恢复 HEAD，全树再生成约定留作后续项）、4 nit（CanvasStage :55 注释仍写 Date.now、format/decision-desk 潜藏默认值、no-store 缺失、stopServerClock/syncServerClock/serverClockHealth 死导出零消费），全部修复。
- **自伤修复（过程记录）**：重写 `web/src/api/system.ts` 时误删既有 `healthz()` 导出（`ws/hub.ts:587` checkHealth 与 LoginView.vue:34 消费），按 HEAD 逐字节恢复后与 `fetchServerTime()` 并存——「新文件」动写过既有导出者，先核 git status/diff 归属再落笔。
- **测试**：`utils/__tests__/server-clock.test.ts` 9 例（未校准直报 / offset 显式算术 / RTT 超限丢样本 / 失败保旧·首败 offset 0 / NaN epoch 拒绝 / ≤30s 回拨冻结等追平 / >30s 跟随+告警 / start-隐藏-stop / 并发去重）；`gateway/time_test.go` 2 例（匿名 401 + epoch 窗口 + RFC3339Nano↔epoch 毫秒对齐 + no-store + POST/DELETE 405；注入 42ms 序列时钟两连读真推进判无缓存）。门禁：vitest 86 文件 774 用例全绿 + vue-tsc 0 + eslint 0 + backend build/vet/gateway 全过。
- **文档同步**：G9 对照行（:133）与条目 10 注记（:166）已于本轮填实；M00/M01/M02 三件套留痕因并行会话持有未提交 WIP 暂缓，待其提交后补指针（同 P2-9 口径）。
- 走查：本轮纯行为/基建（后端读口 + 时间源单例 + 消费侧重接），无模板与样式改动，视觉走查不适用；唯一用户可见变化是底栏钟/倒计时在多端时钟漂移时更准，属语义修正非外观变更。

### P2-12 viz 图形回归（2026-10-20 落地，CI 第四道门）

- **做法**：@playwright/test + 内置 `toHaveScreenshot`（pixelmatch 比对，不引第三方截图链）：用例登记表 `web/src/dev/graphics-cases.ts`（23 个走查 section 各一行），矩阵 = 用例 × 主题(dark/light) × 密度(normal/compact) = **92 goldens**（`web/graphics/golden/`，`snapshotPathTemplate` 去平台后缀——只跑一个后端就不让路径里躺 `-chromium-win32`）；`maxDiffPixelRatio: 0.004`（只吞抗锯齿/次像素抖动，字形与配色漂移必然越线）；viewport 1100×860、dpr 钉 1、zh-CN/Asia/Shanghai、`reducedMotion: 'reduce'`（产品既有约定：ECharts 关动画、画布谐波只画环境帧）；timeout 本机 60s/CI 90s、retries 0（不掩盖失败）、workers CI 2、globalSetup 暖身、webServer `vite dev --host 127.0.0.1:5199 --strictPort`（vite 8 默认只绑 ::1，探活+Playwright 一致走 IPv4）。
- **空画布 golden 根因（P2-12 最大的技术债根治，占评审总消耗一半）**：gallery 内 CanvasStage 系列用例首批 golden 全是空白画布。探针时序追踪锁定：**宿主 `.nt-stage` 在 gallery 里塌到内在高度**（`absolute+inset:0` 只生效于 `.nt-monitor__stage` 下），canvas 默认 `display:inline` 留 ~4px 字形支距 → `measure()` 把 `clientHeight` 回写 `cv.style.height` → 支距让宿主再 +4px → **ResizeObserver 无限自激**（实测 154→158→…→602px，+4px/帧）；且 `cv.width/height` 赋值即使同值也清位图 → 逐帧清空与绘制交替 → 截到透明帧（探针：fillRect 执行 31.4 万次而逐帧采样全透明）。**修复成对**：`measure()` 仅尺寸变化时赋 `cv.width/cv.height`（同尺寸护栏）；`.nt-stage{position:relative;width:100%;height:100%}` + `.nt-stage>canvas{display:block}` 落 `monitor.css` 共址（权重 (0,1,0)，宿主 `.nt-monitor__stage .nt-stage` (0,2,0) 仍胜——级联显式同文件，不依赖注入序）。另修评审裁掉后人工核实为真的第 13 项：2d 上下文获取失败时 canvas v-show 隐藏、宿主在不定高容器折叠 0 高，补 `.nt-stage.is-failed{min-height:120px}` 让失败态有锚点。
- **确定性合同（对抗评审收口后的全套约定）**：① 定格态等宽字形 :root 级钉 `'Cascadia Code', Consolas, monospace`（产品令牌前两顺位 JetBrains/Plex 装机不定——不钉就是机器依赖；:root 级覆盖因为要罩 teleport 出树的 modal 与画布 fillText 运行时取字）；② localStorage 5 键 preset 由用例规格生成；③ 首帧 rAF 双跳定格 + networkidle；④ **golden 唯一权威源 = CI 镜像**：`graphics-update.yml`（workflow_dispatch → windows-2025 全量重刷 → artifact 下发 → 人工 diff 核对后提交），本机 `test:graphics:update` 只当 smoke 不入库——Win10 与 WS2025 系统字体/DirectWrite 不同源，「同平台家族≠同渲染栈」是这类 golden 机制的立项陷阱；⑤ 字体先决条件断言步（pwsh 查注册表缺 Cascadia Code 即 fail-fast，别把环境病送去比像素）；⑥ dev server 专属端口 5199 + `reuseExistingServer:false`（日常 dev 在 5173，禁复用防残留 worktree server 污染）；⑦ `tsconfig.json` include 首次扩到仓库根 TS（`graphics/**/*.ts`、`playwright.config.ts`）让 vue-tsc 门槛罩住；⑧ vitest `test.exclude` 用 `[...configDefaults.exclude, 'graphics/**']`——自定义数组是**替换**默认集，不 spread 会把 dist 等默认排除弄丢。
- **冷编译 flake 实测注记**：webServer 探活只拿到 SPA 回吐的 index.html（`/dev/gallery` 是懒路由），首条用例的 goto 要吃整棵 GalleryView 静态模块图（ui/viz/display/monitor + echarts/depth 深链约 40 组件）的按需编译；CI 清缓存（npm ci）+ 2 workers 下首波可吃满 60s 预算稳定记红、重跑又绿。解法：`graphics/global-setup.ts` 热身一枪 `?case=tokens`（一页即覆盖整模块图），不 import config 自身（循环引用）改读 env 覆盖。
- **评审闭环（15 agent，354k token）**：3 维 finder（flake 面/工程约定/CanvasStage 修复面）出 13 项，12 项敌意核实（workflow 上限裁第 13 项后人工自证为真）→ **9 确认 / 3 驳回**。确认项全修：OS 字体 blocker（上文 ④⑤，最大的立项风险）、JetBrains 顺位 → :root Cascadia 钉死、冷编译暖身 + CI 90s、reuse:false、tsconfig include、四道门头注 ×2（ci.yml 与 CLAUDE.md §6）、CanvasStage style 迁 monitor.css 共址、dpr 变化观察（matchMedia resolution 单发查询逐次重挂——RO 不触发 dpr 变化，混 DPI 多屏（本驾驶舱常态）此前背模糊画布）。驳回留痕：①「GPU 光栅后端差异」——要求实测，探针在受管 GPU 机器上比对 `--disable-gpu` 与否截图 hash 全同（Playwright 托管 headless Chromium 默认即 SwiftShader 软光栅，有 GPU 亦然），驳回；②「settle 与画布清帧存在因果」——固定尺寸+同尺寸护栏下 RO 级联不可能复现，waitUntil 降级属假想解，驳回；③「.gitattributes 会把 PNG 文本化」——`text=auto` 保留二进制嗅探，且全仓没有也不会引入 `* text`，驳回。
- **测试**：graphics 92/92 定稿后连跑全绿（终轮 15.8s，含暖身与 reuse:false 路径）；CanvasStage 修复后 monitor 单元 261 例全过。门禁：vue-tsc 0 错、eslint（自身文件范围）0、CI 由三道门升四道门（web/graphics/tauri-smoke/backend，graphics job 钉 windows-2025 + 镜像浮动注记）。全量 vitest 88 文件 796 过、8 红**全部归属并行会话在途 WIP**——signals.ts P3-10 线 5+3（其 declared 范围外落盘快照）与 c1 名称字典线借 detail.ts/hud.ts 引入 useSecurityStore 致纯 Pinia 测试 2 红（现盘实测通报 novatrader-53 已转其 P4 复核账），均与 P2-12 无涉、不代为修复（并行仓纪律：门禁红先查归属，只交自己文件）。
- **走查**：抽 6 张 golden 目检（kline-light-normal、buttons-dark-normal、heatmap-dark-normal、table-light-compact、monitor-stage-dark-normal、monitor-flow-light-compact）——红涨绿跌、主题 token、密度矩阵、玻璃材质均合 `doc/开发文档/1.视觉需求.md`；监控画布 golden 现为**完整场景**（三列 01 签入/02 研判/03 表决、节点卡、贝塞尔束线、S3 漏斗光带、急停态红环），正是修复有效的目检证据。本轮改动属「恢复本应显示的绘制」的行为修正+基建，无外观规范冲突。

### P2-10 技术债清偿：市场行情纪元钉北京时间（2026-10-20 落地）

- **缘起**：P2-10 服务端钟落地时有意缓议的尾巴（本节 :337 的「timeband.ts 机器时区」条目）：后端戳全是北京墙钟，前端解析与渲染却按设备本地时区——海外/UTC 机上「盘中/盘后」判定、时段带聚焦、时分秒显示整面歪掉；CI web 任务跑在 ubuntu-latest（原生 UTC），必红。
- **做法**（monitor 域全链路：5 生产文件改 + 1 生产文件不动留痕，5 测试文件迁移/补强）：
  1. `timeband.ts` 新增 `shanghaiWall`：固定 +8h 偏移 + UTC 读数拆墙钟字段，**不走 Intl.DateTimeFormat**（域限定等价：上海 1991 年夏令时终止后恒定 UTC+8，输入域为现代交易日瞬时 ≥2019；更早的 1986–1991 UTC+9 与 1940s 战时夏令时按评审纠正后如实注明**在域外**——初版注释「1943 年起全程 UTC+8」为史实误写，本轮一并纠错）。`isTradingDay`/`detectBand`/`bandCountdown` 全改读市场钟。
  2. `freshness.ts` `parseBeijingWall` 收紧：界外分量（月 13/日 40/25 点）在分支内判 null（`Date.UTC` 对越位分量是归一化不是报错）；**历法不存在的日**（'2026-02-31'）按 `daysInMonth` 纯算术判 null——比旧 Date.parse 归一化口径**刻意更严**（数据纪律：没取到≠0，宁缺勿假；Feb-31 非回归的驳回结论仍成立，此收紧属超越 parity 的主动加固并在此申告）；`new Date(0)` + `setUTCFullYear` 构造躲 `Date.UTC` 的 0–99 年映射 1900+y 陷阱；T 分隔按 ISO 从严（小时必须两位，`'T9:30'` 拒绝），空格形态沿袭 Go 布局（小时可单数字）；补可选毫秒位。新增 `WALL_SHAPE` 哨兵：墙钟命形即终判，非法分量**绝不兜底** `Date.parse`——V8 会把 '2026-02-31 10:00:00' 归一化成合法 epoch（新用例首跑实测抓出此洞后补哨兵）。
  3. 裸钟串锚「市场钟的今天」：`parseStamp(text, now?)` 基准钟显式可传、缺省 `serverNow()`（与年龄计算同源——采样钟偏了「今天」跟采样钟走，不跟设备钟走）；`now` 沿 `badgeFor`/`stampFor`/`newestSignalTime`/`newestFillTime`/`cardSilence`/`channelSilence`/`observeGraph` 全链路穿线；`stampDay` 带日期子串**按文本日取**并注释（tradeDate 是市场日历日标签而非瞬时，不能做时区换算）。评审钉死的「同一行数据日级算出 20261340、秒级却 null」分裂就此收口。
  4. 兄弟渲染位同口径：`tape.ts clockText`（时分秒列）与 `MonitorView` 采样时刻角标改上海墙钟读数；`health.ts freshPart` 的 `Date.parse(asOf)` 换 `parseStamp`。**`detail.ts:1067` 不动留痕**：`tokensPerSecond` 吃的是后端 RFC3339 带偏移戳，`Date.parse` 语义已正确，且该文件含并行会话在途编辑——只外科、不越界。
- **评审闭环（11 agent，713k token）**：18 项 → 8 项进入逐条敌意核实 → **5 确认 / 3 驳回 / 10 裁撤**；2 条被裁但自报 blocker 的按评审纪律人工复核**均证实为真并已修**：① `tape.ts clockText` 本地读数——TZ=UTC 下 tape.test.ts 实测 **4 用例红**（CI ubuntu-latest 即原生 UTC，必红 blocker）；② 本人此前对外声称的「TZ=Pacific/Kiritimati +14 全绿」交叉验证是**空证**——Windows Node 只认 `TZ=UTC`，命名时区静默回落 OS 时区（探针 `TZ=Pacific/Kiritimati node -p` 读数 8 而非 14），如实收回并在环境注记记档：Windows 跨时区验证的诚实口径是「本机 CST + TZ=UTC + CI ubuntu 原生 UTC」三点，命名时区不构证。其余确认项：Date.UTC 两位年映射、stampDay/秒级界分裂、裸钟串设备钟「今天」三处均按上文修法落地。
- **CI 阻断实证 → 清除实证**：预修前 TZ=UTC 下 tape.test.ts 4 红（六域归类/时间戳解析/TapeSession 合流）；修复后 monitor 域 18 文件 270 用例在本机（UTC+8）与 TZ=UTC **双跑全绿**（读真实退出码判绿，见下条教训）。
- **门禁勘误与教训**：① `| tail` 截断管道退出码的教训第三轮坐实（本轮 npm-ci 后台任务通知「exit 0」实为 tail 的返回码，日志内 `REAL-EXIT:127` 才是 npm 的真实失败）——凡门禁/后台任务判定必读真实退出码或全文本，已在 P2-12 记录同款注记后列为硬纪律；② 外来红归责法升级：本机 4 簇红经 **HEAD 纯净 worktree 覆盖验证**（junction node_modules + 覆盖本人 4 生产文件，monitor 域 261/261 全绿）证明全部归属并行会话在途链（StatusBar.vue/hud.ts/format.ts 未提交编辑），与本人文件零交集——「worktree 覆盖法」比读调用链更快出铁证。
- **过程环境事故（如实记档）**：交圈门禁阶段 `web/node_modules` 被在途操作写坏（顶层仅 190 项、无 `.bin`、缺整 `@babel` scope；`npx vue-tsc` 全程回落**全局** vue-tsc + TS 6.0.3 抛出 `tsconfig baseUrl TS5101` 伪红——本地 typescript 实为 5.9.3，其弃用表只到 5.0/5.5 era，断无 6.0/7.0 检查项，三次取证锁定全局副本）；本人 npm-ci 抢修又与在途重建撞 EPERM 后主动退出让路；在途重建其后停摆成尸（哨兵 900s 超时实证：顶层 34 项静止、无安装进程存活），本人接手修。**EPERM 锁根取证**：npm 调试日志点名 `@rolldown/binding-win32-x64-msvc/rolldown-binding.win32-x64-msvc.node`，锁主 = 昨日 14:14 常驻至今的 vite dev server（PID 42684，rolldown-vite 原生 .node 映射加载即拒删）。按并行纪律不杀他方进程，采**改名让位法**：`node_modules` 整树改名（Windows 允许重命名含在用文件的目录、仅拒绝删被锁文件本身）→ `npm ci` 全新落地（275 包 8s 热缓存，REAL-EXIT:0；`.bin` 93 项、本地 typescript 5.9.3、@babel/parser 7.29.2 全数归位），僵尸 server 留在已改名旧树上零打扰（残骸树待其退出后清）。**门禁补跑（同日 12:06–12:10，六门全绿，均读真实退出码）**：monitor 域本机 CST 270/270 EXIT 0、TZ=UTC 270/270 EXIT 0（起步戳 04:06 vs 本机 12:06 证 UTC 真生效）、全量 vitest 94 文件 **877/877**（上轮挂靠的 1 红外来 chrome-state 已随并行会话收口转净，本轮零外来红）、eslint（本人 7 文件范围）0 警 0 误、vue-tsc 本地 5.9.3 零输出 0 错（TS5101 伪红随本地铁证落地彻底销案）、graphics 监控子集 7 用例 ×4 格 = **28/28**。
- **测试**：timeband 时区无关性（Z 后缀瞬时跨日界正反四例：UTC 周日深夜 = 上海周一交易日、UTC 周五午后 = 上海周六休市等）；freshness 垃圾拒收全家桶（13 月/40 日/25 点/09:61/'2026-02-31'/'2026-02-29'(2026 非闰)/'2024-02-29' 合法对照）、T-form 从严与空格宽松、毫秒保留、裸钟串「市场钟今天」与跨日界（fake timers）、`now` 穿线同源（显式采样钟锚定）；tape 三常量与 hud 三处构造、GalleryView demoNow 全部钉 `+08:00`（跑测机时区免疫）。
- **走查**：纯行为/时区语义修正，无样式改动；playwright 配置本来就钉 `timezoneId:'Asia/Shanghai'`，golden 渲染文本在修复前后按构造同一（本地 10:24 → 修复后上海墙钟仍 10:24），图形监控子集 28/28 全绿坐实走查证物（golden 按构造免疫本修复：playwright 钉 `timezoneId:'Asia/Shanghai'`，本机 UTC+8 修复前后渲染文义同一；真实受益面在 UTC/海外机与 CI ubuntu 原生 UTC 环境）。
