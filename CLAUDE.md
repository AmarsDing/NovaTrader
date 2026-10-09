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
  - 启动流程：连接 postgres 系统库 → `CREATE DATABASE IF NOT EXISTS novatrader` → 连接业务库 → 执行 ent 自动迁移（`client.Schema.Create`）。
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
| 前端 | Vue 3 + Vite + vue-router | `web/src/`，纯 JS（jsconfig，无 TS） |
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
│   ├── ent/                  ← 【待建】ent Schema 与生成代码
│   ├── library/              ← 通用库（dataformat 等）
│   ├── model/                ← 全局错误码定义
│   ├── utils/                ← 工具包（log/websocket/casbin/aes...）
│   └── third_party/          ← 第三方改造代码
├── web/                      ← Vue3 前端 SPA
│   ├── src/{api,router,store,view,layout,component,utils,style}
│   └── src-tauri/            ← 【待建】Tauri Rust 桌面壳
├── sql/init_db.sql           ← 历史 PG 建表脚本（仅参考，已被决策 2 取代）
└── doc/                      ← 需求与设计文档（中文）
```

## 4. 后端分层规范（Kratos）

`backend/app/admin/internal/` 为标准 Kratos 四层，新增业务必须按此分层：

| 目录 | 职责 | 规则 |
|------|------|------|
| `service/` | 协议适配层 | 只做参数校验与转调 biz，不写业务逻辑 |
| `biz/` | 业务编排层 | 用例、领域规则、事务边界 |
| `data/` | 数据访问层 | **只许通过 ent Client 访问 Postgres**，禁止裸 SQL、禁止 GORM |
| `core/` | 长连接/常驻逻辑 | WebSocket、定时任务、状态维护 |

约定：
- **Proto-first**：新增接口先写 `backend/api/admin/v1/*.proto`，再生成 pb 代码，再实现 service。
- 依赖注入走 wire：ProviderSet 改动后重新 `wire ./...`。
- 配置结构改动：先改 `conf/conf.proto` → 重新生成 `conf.pb.go` → 再改 `configs/config.yaml`。

## 5. 数据层落地规范（ent + Postgres）

目标结构（尚未创建，按此执行）：

```
backend/ent/
├── schema/          ← 手写：每个表一个文件，如 schema/market_data.go
└── ...              ← 生成物：在 backend/ 下执行 go generate ./ent（带 sql/upsert 特性，不要直接调 ent generate）
```

关键约定：
1. Schema 文件头部必须写注释说明表用途（对齐 `doc/NovaTrader智脑方案.md` 第 4 章）。
2. 先生成的表（首批）：`stock_basic`、`market_data`、`news_sentiment`、`trade_signals`、`positions`、`account_snapshots`、`strategy_config`、`agent_decisions`、`morning_briefings`、`daily_reviews`、`strategy_library`、`strategy_feedback`、`strategy_version`。
3. 字段类型约定：金额为 `decimal`（ent 用 `field.Float` + schema 注解或 `field.Other` + Numeric），时间统一 `time.Time` / `timestamptz`，JSON 字段用 `field.JSON` / `field.Strings`。
4. 唯一约束、索引用 ent 的 `field.Unique()` / `index.Fields()` 声明，保证自动迁移可重复执行。
5. 数据层初始化放在 `internal/data/`：`ent.Open("postgres", dsn)` → `client.Schema.Create(ctx)`，由 wire 注入各 Repo。
6. 迁移只许**追加式演进**（新增字段给默认值），禁止手写回滚 SQL。

## 6. 前端与桌面端规范

- `web/` 是标准 Vite + Vue3 工程：`npm run dev` 开发，`npm run build` 产出 `web/dist/`。
- Tauri 壳（待建）要点：
  - `web/src-tauri/`（Rust），`tauri.conf.json` 的 `frontendDist` 指向 `../dist`；
  - `npm run tauri dev` 联调，`npm run tauri build` 出安装包；
  - 后端地址按环境区分：开发态直连本机 Go 服务（见第 7 节端口），地址集中配置在 `web/src/utils/request.js`。
- 视觉规范严格遵循 `doc/开发文档/1.视觉需求.md`：深空黑 `#0A0E17` 基底、极光青 `#00E5FF` 主色、磨砂玻璃拟态面板；禁止引入 UI 组件库破坏该风格（element-plus/naive-ui 等默认不要装）。K 线可用 lightweight-charts，其余图可用 ECharts。

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

- `backend/app/admin/internal/data/data.go` 引用了 `server/api/asset|cfgcenter|logcenter|monitoralarm/v1` 等**不存在的包**（模板残留），admin 服务当前无法编译，动手时需先裁剪这些模板代码。
- `backend/api/admin/v1/` 下的 proto（airportinfo/ntp/snmp/dataForward/serviceMonitor 等）均为模板示例，与股票业务无关，待清理后重建业务 proto。
- `web/` 仅有登录页+首页骨架，看板各模块（信号/持仓/舆情/回测/复盘/运维）均未实现。
- `go.mod` 中 GORM/mysql、mongo、mssqld 等依赖待随业务替换逐步清理。
- `backend/app/admin/cmd/main.go` 头部注释仍写着模板项目名 `weatherMaster-server`，属历史遗留。

## 10. 一句话总结

**Tauri(Rust) 桌面壳 + Vue3 前端，Go(Kratos) 后端，PostgreSQL + ent 自动建库建表** —— 私有化 A 股超短线量化交易系统。
