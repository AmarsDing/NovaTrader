# NovaTrader 项目说明（AI 阅读指引）

> 本文档面向 AI 编码助手：**动手改任何代码之前必须先读完本文**。
> 所有修改必须遵守第 0 节的两条硬性架构决策。

## 0. 硬性架构决策（不可违背）

### 决策 1：前端 = Rust 集成的桌面版
- 前端技术栈：**Vue 3 + Vite**，通过 **Tauri（Rust）** 打包为**桌面应用**（Windows 优先）。
- `web/` 目录即前端 SPA 源码；Tauri Rust 壳位于 `web/src-tauri/`。
- 禁止引入 Electron；禁止把前端部署成纯 Web 服务的方案作为主线。
- 前端与本地 Go 后端通过 HTTP（REST）+ WebSocket 直连，不走浏览器跨域部署模式。

### 决策 2：数据库 = PostgreSQL + ent，自动建库建表
- ORM 统一使用 **entgo.io/ent**（Schema-as-Code），**禁止使用 GORM** 写新业务代码；存量 GORM 代码逐步替换。
- 数据库统一为 **PostgreSQL**（原有 MySQL/SQLite/TDengine 方案仅作历史参考，见 `doc/NovaTrader智脑方案.md`）。
- **自动建库建表**：服务启动时自动完成，禁止手动执行 SQL 脚本建表。
  - 启动流程：连接 postgres 系统库 → 创建业务库 → `CREATE EXTENSION vector` → 建议锁下执行 ent 自动迁移（`client.Schema.Create`）→ 打开 `*ent.Client`。这一整段写在 `backend/pkg/entclient.Open`，各服务 `internal/data` 的 `NewEntClient` / `NewData` 只做配置映射和 Wire 清理。
- **物理位置**：Schema-as-Code 与生成代码在 `backend/ent/`（`schema/` 手写，其余在 `backend/` 下 `go generate ./ent`）。多服务共用这一份包，禁止挪到 `app/*/internal/data/ent`，也禁止按服务复制 schema。
- **逻辑归属**：说「ent 在数据层」指访问权，不是目录。业务读写只走各服务 `internal/data` 的仓储。`*ent.Client` 还可以由横切基础设施持有（`pkg/dbinit`、`pkg/migrate`、`pkg/entclient`，以及 outbox、调度记录、心跳、参数、交易日历同步这类共享表）。`service` / `biz` / `core` 不查询 ent；`core` 使用 `scheduler.Store`、`outbox.Dispatcher` 等端口。
- `sql/init_db.sql` 仅为**历史参考**，其中的表结构需要逐一翻译为 ent Schema，禁止继续维护该 SQL 文件。
- 向量用 PostgreSQL 扩展 **pgvector**，不单独部署 ChromaDB。不引入 TDengine、Neo4j、SQLite 业务库。
- `CREATE DATABASE`、迁移锁、`CREATE EXTENSION vector` 只允许写在 `backend/pkg/dbinit`。业务代码不写裸 SQL。

### 决策 3：行情用通达信，实盘用东方财富文件单
- 通达信（口头常被说成「通信达」）是行情数据源，接法见 `doc/开发文档/M01-数据采集/设计文档.md`。
- 自动下单只走 Windows 上的**东方财富量化终端 CSV 文件单**，由 `broker-gw` 写文件。不点击客户端，不逆向交易接口，不使用 QMT。
- 大模型只产出评分和理由，不直接下单。价格和仓位由规则计算，订单必须先过风控。
- 模拟盘连续满 20 个交易日并通过准入后，才允许半自动实盘。

### 决策 4：文档按模块目录维护
- 入口：`doc/开发文档/README.md`。
- 每个模块三份文件：`需求文档.md`、`设计文档.md`、`开发进度.md`。
- `doc/开发文档/2.需求规格说明书.md`、`3.系统设计说明书.md`、`4.开发进度跟踪.md` 已停更，只保留跳转说明。

## 1. 项目是什么

NovaTrader：部署在 NVIDIA DGX Spark 上的**私有化 A 股超短线量化交易系统**。

- 7×24 无人值守：盘前/盘中/盘后全周期自动数据采集与监控。
- 多源合规数据（通达信 / Tushare / AkShare / Tavily / 东方财富 / 巨潮资讯）。
- 本地大模型四维分析（技术面+消息面+资金面+外围联动），输出标准化交易信号（入场价/止损价/止盈价）。
- 策略回测 + 反馈闭环自主学习；模拟盘与实盘严格隔离。

需求与设计（改对应模块前必读该目录下三份文件）：
- `doc/开发文档/README.md` — 模块索引和已确认决策。
- `doc/开发文档/M00` 至 `M12` — 每个模块的需求、设计、开发进度。
- `doc/开发文档/1.视觉需求.md` — 前端视觉规范。
- `doc/NovaTrader智脑方案.md`、`doc/RAG.md` — 早期材料，与开发文档冲突时以开发文档为准。

## 2. 技术栈总览

| 层 | 技术 | 说明 |
|----|------|------|
| 桌面壳 | Tauri (Rust) | 打包 Vue 前端为桌面应用，`web/src-tauri/` |
| 前端 | Vue 3 + Vite + TS + Pinia + dockview | `web/src/`，渐进 TS（vue-tsc 门槛），纯 JS 存量逐步迁移 |
| 后端 | Go 1.24 + Kratos v2 | `backend/`，module 名 `server` |
| API 定义 | Protobuf (proto3) | proto-first，`backend/api/<app>/v1/*.proto` |
| 依赖注入 | google/wire | `cmd/wire.go` + `wire_gen.go` |
| 数据库 | PostgreSQL + ent + pgvector | 见第 0 节决策 2 |
| 行情边车 | Python `pyworker` | 通达信官方接口、AkShare；只做采集 |
| 交易边车 | Python `broker-gw`（Windows） | 只读写东方财富量化终端文件单 |
| 消息/缓存 | NATS / Redis | 配置见 `configs/config.yaml` |
| 注册中心 | kratos discovery | `backend/app/discovery/`（第三方组件，勿改业务） |

## 3. 仓库目录地图

```
NovaTrader/
├── CLAUDE.md                 ← 本文档
├── backend/                  ← Go 后端（module: server）
│   ├── api/                  ← Protobuf API 定义（admin、demo 两套）
│   │   └── admin/v1/*.proto  ← ⚠ 当前是模板残留示例（airport/ntp/snmp 等），待裁剪
│   ├── app/
│   │   ├── admin/            ← 主服务：cmd/main.go + internal/{biz,core,data,middleware,server,service}
│   │   ├── demo/             ← 示例服务（可参照，业务不依赖）
│   │   └── discovery/        ← 注册中心（第三方独立 module，勿动）
│   ├── conf/conf.proto       ← 配置结构定义（已含 postgres 节）
│   ├── configs/config.yaml   ← 运行配置（端口/中间件/数据源）
│   ├── ent/                  ← 共享 Schema-as-Code（物理位置固定在此，不按服务拆分）
│   ├── pkg/entclient/        ← 建库、pgvector、迁移、打开 *ent.Client
│   ├── library/              ← 通用库（dataformat 等）
│   ├── model/                ← 全局错误码定义
│   ├── utils/                ← 工具包（log/websocket/casbin/aes...）
│   └── third_party/          ← 第三方改造代码
├── web/                      ← Vue3 + TS 前端（dockview 全驾驶舱工作台）
│   ├── src/{api,ws,stores,theme,ui,viz,display,widgets,workspace,panels,view,dev,router,utils,style}
│   ├── popout.html           ← dockview 弹窗宿主页（多页入口，无脚本）
│   ├── eslint.config.js      ← flat config（js/ts/vue + prettier 兼容）
│   └── src-tauri/            ← Tauri 2 Rust 桌面壳（托盘/通知/凭据/popout capability/CSP）
├── sql/init_db.sql           ← 历史 PG 建表脚本（仅参考，已被决策 2 取代）
└── doc/                      ← 需求与设计文档（中文）
```

## 4. 后端分层规范（Kratos）

`backend/app/admin/internal/` 为标准 Kratos 四层，新增业务必须按此分层：

| 目录 | 职责 | 规则 |
|------|------|------|
| `service/` | 协议适配层 | 只做参数校验与转调 biz，不写业务逻辑 |
| `biz/` | 业务编排层 | 用例、领域规则、事务边界 |
| `data/` | 数据访问层 | 业务读写 Postgres 的唯一入口，通过 `backend/ent` 的 Client。禁止裸 SQL、禁止 GORM。schema 包留在 `backend/ent`，不要在 data 目录再放一份 |
| `core/` | 长连接/常驻逻辑 | WebSocket、定时任务、状态维护。不持有 `*ent.Client`；调度与 outbox 走 data/pkg 端口 |

约定：
- **Proto-first**：新增接口先写 `backend/api/admin/v1/*.proto`，再生成 pb 代码，再实现 service。
- 依赖注入走 wire：ProviderSet 改动后重新 `wire ./...`。
- 配置结构改动：先改 `conf/conf.proto` → 重新生成 `conf.pb.go` → 再改 `configs/config.yaml`。

## 5. 数据层落地规范（ent + Postgres）

`backend/ent` 是共享 Schema-as-Code，物理上不属于任何一个 `app`。逻辑上的数据层是各服务 `internal/data`：只有这里（以及第 0 节列出的横切 pkg）可以持有 `*ent.Client`。

```
backend/ent/
├── schema/          ← 手写：每个表一个文件，如 schema/market_data.go
└── ...              ← 生成物：在 backend/ 下执行 go generate ./ent（带 sql/upsert 特性，不要直接调 ent generate）
```

关键约定：
1. Schema 文件头部必须写注释说明表用途（对齐 `doc/NovaTrader智脑方案.md` 第 4 章）。
2. 表已经按业务扩展，超出最初的 `stock_basic`、`market_data`、`news_sentiment`、`trade_signals`、`positions`、`account_snapshots`、`strategy_config`、`agent_decisions`、`morning_briefings`、`daily_reviews`、`strategy_library`、`strategy_feedback`、`strategy_version`。新增表继续放在 `backend/ent/schema/`。
3. 字段类型约定：金额为 `decimal`（ent 用 `field.Float` + schema 注解或 `field.Other` + Numeric），时间统一 `time.Time` / `timestamptz`，JSON 字段用 `field.JSON` / `field.Strings`。
4. 唯一约束、索引用 ent 的 `field.Unique()` / `index.Fields()` 声明，保证自动迁移可重复执行。
5. 客户端由 `pkg/entclient.Open` 创建（建库、vector 扩展、迁移、连接池）。各服务 `internal/data` 保留同名 `NewEntClient`（strategy 为 `NewData`）作为 Wire Provider，函数体只调用 `entclient.Open`。不要在 app 里再写一遍建库和 `Schema.Create`。
6. 迁移只许**追加式演进**（新增字段给默认值），禁止手写回滚 SQL。

## 6. 前端与桌面端规范

- `web/` 是标准 Vite + Vue3 + TS 工程：`npm run dev` 开发；`npm run build` = `vue-tsc --noEmit && eslint . && vite build`（**类型与 lint 是构建门槛**，逃生口 `build:fast`）；`npm run test`（vitest）；`npm run analyze` 出包体报告。
- 组件三层：`ui/`（Nt* 基础控件）/ `viz/`（图表件）/ `display/`（业务展示件），**禁引 store/router**；业务面板在 `panels/<组>/*.panel.vue`（薄壳）+ `workspace/panel-registry.ts`（注册表，21 面板）。组件改动跑 `/dev/gallery`（仅 DEV）走查主题 × 密度矩阵。
- Tauri 壳（已建）：
  - `web/src-tauri/`（Rust，Tauri 2），`tauri.conf.json` 的 `frontendDist` 指向 `../dist`，`security.csp` 已收紧（白名单，见 M12 设计文档 §10）；
  - `npm run tauri dev` 联调，`npm run tauri build` 出安装包；本机 Rust 构建配方：`RUSTUP_TOOLCHAIN=stable-x86_64-pc-windows-msvc CARGO_TARGET_DIR=C:\nt-target`（F:\股票 非 ASCII 路径会损坏 in-tree 产物）；
  - 后端地址运行时可配（设置面板），拼接集中在 `web/src/utils/endpoint.ts`，HTTP 只走 `web/src/api/http.ts`；
  - popout 多屏：`core:webview:allow-create-webview-window` capability + `popout.html` 宿主页 + `POPOUT_ALLOWED` 白名单（trade/settings/ops 禁弹）。
- 视觉规范严格遵循 `doc/开发文档/1.视觉需求.md`：**红涨绿跌**、主题/密度与统一token保持；2026-10-15用户要求更强毛玻璃/半透明/霓虹，使用范围以第七节取代第五节旧限制；材质与完整/节能/减少动态模式已实现，完整视觉/硬件压力仍待验收。保留原监控画布/HUD/终端带与dockview一张票，真实性/对比度/性能共同验收；禁止引入 UI 组件库破坏该风格（element-plus/naive-ui 等默认不要装）。K 线用 lightweight-charts，其余图用 ECharts（`echarts/core` 按需引入）。
- CI 三道门（`.github/workflows/ci.yml`）：web（typecheck/lint/test/build+产物校验）、tauri-smoke（windows `tauri build --no-bundle`）、backend（go build/vet/test）。

## 7. 配置与端口

配置入口：`backend/configs/config.yaml`，结构定义：`backend/conf/conf.proto`（含 `postgres` 节，host/port/user/password/database 齐全）。

| 端口 | 服务 |
|------|------|
| 2001 | admin HTTP（前端 REST 调用） |
| 2002 | admin gRPC |
| 2003 | admin WebSocket（前端实时推送） |
| 2004–2006 | demo 服务（http/grpc/ws） |
| 7171 | discovery 注册中心 |
| 4222 | NATS |
| 5432 | PostgreSQL |
| 14269 | Jaeger 追踪 |

## 8. AI 工作约定

1. 涉及需求/外观的改动：先读 `doc/开发文档/` 里对应模块的三份文档，再动代码。
2. 改码前先画数据流：理清 proto → service → biz → data → ent 的调用链，确认表结构影响面。
3. 小步迭代：一次只聚焦一个明确问题；完成后全局搜索残留引用，不只看当前文件。
4. 新增/修改表结构必须同步：ent Schema + 生成代码 + data 层 Repo，三者一起交付。
5. 不擅自引入新框架/新中间件；不擅自改 `app/discovery/`。

## 9. 已知技术债（现状注意）

- 已清偿（2026-10-18/19 逐一核实，勿再按旧账处理）：
  - `backend/api/admin/v1/` 模板 proto 已全部清除：现仅存 config/environment/observability 三个业务 proto，`api/` 按服务分域（trade/risk/market/intel/brain/strategy/datahub/backtest/notify/broker/platform）；新增接口一律 proto-first，业务路由走 `internal/gateway` in-gate 聚合。
  - `go.mod` 无 GORM/mysql/mongo/mssqld：直接 DB 依赖仅 ent + pgx/v5；全仓（含 third_party、app/discovery）零 gorm/mongo/mssqld 导入；go.sum 保留 go-sql-driver/mysql 校验和属 ent 模块图合法要求，不 prune。
  - web 纯 JS 门面清零：router/index.js、store/confirm.js 迁 TS 并删源文件；零引用门面 utils/request.js 已删；src 下无 .js 业务文件（vite/eslint 工程配置按惯例保留 JS）。
  - admin 服务编译（data.go 模板引用已裁剪）、main.go 头注释、web 登录页+骨架（P1–P6 重构完成，21 面板驾驶舱）。
  - admin 模板死链四件套已删（用户批准后执行，2026-10-19）：`app/admin/internal/core/topic.go` 死主题常量、`app/admin/internal/biz/` 死 NatsCase/TopicCase 链（整包）、`app/admin/internal/data/nats.go` 死 NatsCoreRepo、`backend/utils/natserver/` 孤儿包；删除后 admin 限定 build/vet/test 与隔离PG集成仍绿。

## 10. 一句话总结

**Tauri(Rust) 桌面壳 + Vue3 前端，Go(Kratos) 后端，PostgreSQL + ent 自动建库建表** —— 私有化 A 股超短线量化交易系统。
