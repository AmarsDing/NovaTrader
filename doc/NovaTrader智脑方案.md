# 📄 DGX Spark 超短线量化交易系统

**更新日期**：2026年3月24日  
**适配硬件**：NVIDIA DGX Spark（GB10 Blackwell，128GB 统一内存，4TB NVMe SSD）

---

## 1. 项目概述

### 1.1 建设目标
在 DGX Spark 上构建一套**私有化、全自动化、可自主学习、合规可控**的A股超短线量化交易系统，实现：
- **7×24小时无人值守**：覆盖盘前、盘中、盘后全周期，自动数据采集与监控。
- **多源合规数据实时采集**：行情、新闻、公告、研报、外围市场、宏观数据，主备冗余。
- **本地大模型深度分析**：四维分析（技术面+消息面+资金面+外围联动）。
- **超短线选股信号输出**：固化可量化规则，输出标准化交易信号（含入场价、止损价、止盈价）。
- **可视化看板与即时通知**：本地 Vue3 前端 + 飞书。
- **策略回测与自主进化**：完整回测体系，通过反馈闭环持续优化选股逻辑与模型参数。
- **模拟与实盘交易隔离**：先模拟验证，后合规对接券商 API，严格风控。

### 1.2 核心验收指标
| 指标 | 要求 |
|------|------|
| 行情延迟 |  
| 新闻延迟 | ≤1min |
| 全市场选股扫描耗时 | ≤3分钟（5000+标的） |
| 模型推理单条延迟 | 
| 系统可用性 | ≥99.5%（交易日交易时段无中断） |
| 回测要求 | 年化≥30%，最大回撤≤20%，胜率≥55%，盈亏比≥1.5 |


## 2. 系统总体架构（含数据流与端口规范）

### 2.1 分层架构总图
```
┌─────────────────────────────────────────────────────────────────────────────────────┐
│                              数据源层（合规多源冗余）                                │
│  ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐ ┌──────────┐                 │
│  │ Tushare  │ │ AkShare  │ │ Tavily   │ │ 东方财富 │ │ 巨潮资讯 │                 │
│  │ REST API │ │ Python库 │ │ REST API │ │          │ │ 公告API  │                 │
│  └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘ └────┬─────┘                 │
│       └────────────┴────────────┴────────────┴────────────┘                        │
│                                    │                                                │
│                                    ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │                    【后端服务层】golang                                      │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ 数据聚合器                                                          │   │   │
│  │  │ • 定时拉取+实时订阅，主备自动切换                                     │   │   │
│  │  │ • Redis 缓存（TTL=10s），去重清洗（MinHash）                         │   │   │
│  │  │ • 知识图谱关联（Neo4j）提升消息-个股匹配精度                          │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ RAG检索模块（优化）                                                  │   │   │
│  │  │ • 向量化模型： （"Qwen3.5-122B-A10B-GPTQ-Int4）                      │   │   
│  │  │ • 存储：ChromaDB，按月清理过期向量                                    │   │   │
│  │  │ • 情感评分："Qwen3.5-122B-A10B-GPTQ-Int4                            │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ 策略回测引擎（向量化计算）                                           │   │   │
│  │  │ • 分钟级回测，支持参数优化、蒙特卡洛模拟                              │   │   │
│  │  │ • 样本外验证、过度拟合检测                                            │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ API接口层（REST+WebSocket）                                         │   │   │
│  │  │ • 行情/新闻/回测接口 • 交易指令接口 • 健康检查                        │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                    │                                                │
│                                    ▼                                                │
│  ┌─────────────────────────────────────────────────────────────────────────────┐   │
│  │              【OpenClaw Gateway】统一消息中枢 (端口：18789)                  │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ Channel 层：飞书/HTTP/WebSocket                                      │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ PiAgent 层：意图理解、任务拆解、上下文管理                            │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  │  ┌─────────────────────────────────────────────────────────────────────┐   │   │
│  │  │ Skills 层：                                                         │   │   │
│  │  │ • 技术分析Skill（调用"Qwen3.5-122B-A10B-GPTQ-Int4）                 │   │   │
│  │  │ • 消息解读Skill（调用 "Qwen3.5-122B-A10B-GPTQ-Int4 + RAG + 知识图谱）│   │   │
│  │  │ • 选股决策Skill（"Qwen3.5-122B-A10B-GPTQ-Int4）                     │   │   │
│  │  │ • 回测优化Skill（调用后端回测引擎）                                   │   │   │
│  │  │ • 风控校验Skill（动态ATR止损、仓位管理）                              │   │   │
│  │  │ • 自省进化Skill（反馈分析、参数优化、模型微调）                       │   │   │
│  │  └─────────────────────────────────────────────────────────────────────┘   │   │
│  └─────────────────────────────────────────────────────────────────────────────┘   │
│                                    │                                                │
│         ┌──────────────────────────┼──────────────────────────┐                    │
│         ▼                          ▼                          ▼                    │
│  ┌─────────────────┐    ┌─────────────────┐    ┌─────────────────┐                │
│  │ 模型推理层      │    │ 本地前端看板     │    │ 通知推送系统     │                │
│  │ (端口：11434)   │    │ (端口：8080)     │    │ (飞书/钉钉)      │                │
│  │ • vLLM          │    │ • Vue3交易看板   │    │ • 信号推送        │                │
│  │ • 模型按需加载   │    │ • Grafana监控    │    │ • 风险预警        │                │
│  │ • 量化+LoRA微调 │    │ • 离线访问        │    │ • 复盘报告        │                │
│  └─────────────────┘    └─────────────────┘    └─────────────────┘                │
└─────────────────────────────────────────────────────────────────────────────────────┘
```

### 2.2 核心端口规范与访问说明
| 端口 | 服务名称 | 访问地址规范 | 常见报错解决方案 |
|------|----------|--------------|------------------|

### 2.3 数据流与指令下发核心流程
1. **数据上行**：多源数据 → 后端清洗/存储 → Redis缓存/WebSocket推送 → OpenClaw通过API/订阅获取 → Skill调用模型 → 生成信号。
2. **指令下行**：用户/定时任务 → Channel → PiAgent拆解 → 调度Skill至本地Node → 调用模型/后端API → 结果回传 → 推送前端/通知/交易执行。

---

## 3. 数据采集与处理

### 3.1 合规数据源清单（多源冗余）
| 数据类别 | 主数据源 | 备用数据源 | 获取方式 | 更新频率 | 用途 |
|----------|----------|------------|----------|----------|------|
| A股基础行情 | Tushare Pro | AkShare | REST API/Python库 | 实时/日终 | 日线、分钟线、资金流向、财务数据 |
| A股高级行情 | 东方财富Level-2 | 券商QMT/PTrade | 券商API | 毫秒级 | 逐笔成交、十档盘口 |
| 财经新闻/快讯 | Tavily API | 新浪财经RSS | REST API/爬虫 | 实时 | 新闻聚合、情感分析 |
| 上市公司公告 | 巨潮资讯网API | 东方财富网 | 官方API/爬虫 | 每日/实时 | 定期报告、临时公告 |
| 行业研报 | 东方财富研报 | 慧博投研 | 爬虫 | 每日 | 券商观点、盈利预测 |
| 外围市场数据 | 新浪美股/英为财情 | AkShare | 爬虫/Python库 | 实时 | 美股、汇率、大宗商品 |
| 宏观数据 | 国家统计局/央行 | Wind免费版 | 官方API/爬虫 | 月度/实时 | CPI、PMI、利率 |
| 市场舆情 | 股吧/财经论坛 | 同花顺舆情 | 合规爬虫 | 实时 | 情绪、热词 |

### 3.2 数据处理流程
1. **采集**：定时+实时订阅，主源失败自动切换备用，重试3次后告警。
2. **清洗**：
   - 行情：完整性校验、前复权处理、停牌/ST过滤。
   - 文本：MinHash去重、敏感词过滤、格式标准化。
3. **存储**：
   - 时序行情：TDengine。
   - 关系数据和实时热数据：PostgreSQL。
   - 文本向量：ChromaDB
4. **知识图谱**：Neo4j存储行业-概念-个股映射，新闻通过实体识别关联至具体标的。
5. **情感评分**：Qwen3.5-122B-A10B-GPTQ-Int4模型对新闻进行情感打分（0~5）。

---

## 4. 数据存储设计

### 4.1 PostgreSQL 表结构

#### 4.1.1 股票基础信息表 `stock_basic`
```sql
CREATE TABLE stock_basic (
    stock_code VARCHAR(10) PRIMARY KEY,
    stock_name VARCHAR(20) NOT NULL,
    market VARCHAR(5) NOT NULL,
    list_date DATE,
    delist_date DATE,
    industry VARCHAR(50),
    concept TEXT,                         -- 概念板块（逗号分隔）
    total_share BIGINT,
    free_share BIGINT,
    circ_mv DECIMAL(15,2),
    pe_ttm DECIMAL(10,2),
    pb DECIMAL(10,2),
    st_flag BOOLEAN DEFAULT FALSE,
    suspend_flag BOOLEAN DEFAULT FALSE,
    update_time TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_stock_market ON stock_basic(market);
CREATE INDEX idx_stock_industry ON stock_basic(industry);
```

#### 4.1.2 交易信号表 `trade_signals`
```sql
CREATE TABLE trade_signals (
    id BIGSERIAL PRIMARY KEY,
    signal_time TIMESTAMPTZ NOT NULL,
    stock_code VARCHAR(10) NOT NULL,
    stock_name VARCHAR(20) NOT NULL,
    signal_type VARCHAR(10) NOT NULL CHECK (signal_type IN ('buy','sell','hold')),
    entry_price DECIMAL(10,2) NOT NULL,
    stop_loss DECIMAL(10,2) NOT NULL,
    take_profit DECIMAL(10,2) NOT NULL,
    signal_reasoning TEXT NOT NULL,
    confidence_score DECIMAL(5,4) NOT NULL,
    status VARCHAR(10) DEFAULT 'pending',
    executed_price DECIMAL(10,2),
    executed_volume INT,
    created_at TIMESTAMPTZ DEFAULT NOW(),
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
CREATE INDEX idx_signals_time ON trade_signals(signal_time);
CREATE INDEX idx_signals_code ON trade_signals(stock_code);
CREATE INDEX idx_signals_status ON trade_signals(status);
```

#### 4.1.3 账户持仓快照表 `account_snapshots`（10秒粒度）
```sql
CREATE TABLE account_snapshots (
    id BIGSERIAL PRIMARY KEY,
    snapshot_time TIMESTAMPTZ NOT NULL,
    total_equity DECIMAL(15,2) NOT NULL,
    available_funds DECIMAL(15,2) NOT NULL,
    unrealized_pnl DECIMAL(15,2) NOT NULL,
    realized_pnl DECIMAL(15,2) NOT NULL,
    margin_used DECIMAL(15,2) DEFAULT 0,
    risk_ratio DECIMAL(5,4) NOT NULL,
    position_count INT NOT NULL
);
CREATE INDEX idx_account_snapshots_time ON account_snapshots(snapshot_time);
```

#### 4.1.4 策略配置表 `strategy_config`（动态参数）
```sql
CREATE TABLE strategy_config (
    id SERIAL PRIMARY KEY,
    config_key VARCHAR(50) UNIQUE NOT NULL,
    config_value TEXT NOT NULL,
    value_type VARCHAR(20) DEFAULT 'string',
    description TEXT,
    updated_at TIMESTAMPTZ DEFAULT NOW()
);
-- 初始化参数
INSERT INTO strategy_config (config_key, config_value, value_type, description) VALUES
('rsi_lower', '30', 'int', 'RSI下阈值'),
('rsi_upper', '70', 'int', 'RSI上阈值'),
('turnover_min', '3', 'int', '换手率下限(%)'),
('turnover_max', '15', 'int', '换手率上限(%)'),
('amount_min', '50000000', 'bigint', '最小成交额(元)'),
('stop_loss_atr_mult', '2', 'float', 'ATR止损倍数'),
('take_profit_atr_mult', '3', 'float', 'ATR止盈倍数'),
('position_per_stock', '0.05', 'float', '单票最大仓位'),
('max_daily_loss', '0.02', 'float', '单日最大亏损比例');
```

### 4.2 TDengine 超级表（行情）
```sql
CREATE DATABASE quant KEEP 365;
USE quant;
CREATE STABLE market_quotes (
    ts TIMESTAMP,
    open FLOAT, high FLOAT, low FLOAT, close FLOAT,
    volume BIGINT, amount FLOAT, turnover_rate FLOAT,
    buy_volume BIGINT, sell_volume BIGINT,
    rsi_6 FLOAT, macd FLOAT, macd_signal FLOAT,
    atr_20 FLOAT                         -- 动态止损指标
) TAGS (stock_code VARCHAR(10), stock_name VARCHAR(20), market VARCHAR(10));
```

### 4.3 SQLite 自主学习表

#### 4.3.1 策略反馈表 `strategy_feedback`
```sql
CREATE TABLE strategy_feedback (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    trade_date TEXT NOT NULL,
    stock_code TEXT NOT NULL,
    signal_id INTEGER,
    predicted_return REAL NOT NULL,
    actual_return REAL NOT NULL,
    signal_correct BOOLEAN,
    false_negative BOOLEAN DEFAULT 0,     -- 是否错失机会
    reasoning TEXT,
    error_analysis TEXT,
    update_time TEXT DEFAULT CURRENT_TIMESTAMP
);
CREATE INDEX idx_feedback_date ON strategy_feedback(trade_date);
```

#### 4.3.2 策略版本表 `strategy_version`
```sql
CREATE TABLE strategy_version (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    version_name TEXT NOT NULL,
    config_snapshot TEXT NOT NULL,        -- JSON格式全量配置
    backtest_report TEXT,
    created_at TEXT DEFAULT CURRENT_TIMESTAMP,
    is_active BOOLEAN DEFAULT 0
);
```

---

## 5. AI模型部署与优化

Qwen3.5-122B-A10B-GPTQ-Int4  负责除交易外全部
Qwen2.5-7B-Instruct-GPTQ-Int8 负责交易

---

## 6. 知识库与自主学习能力

### 6.1 知识库增强
- **Embedding 模型**：。
- **知识图谱**：
- **情感评分**：
- **向量库维护**：

### 6.2 自主学习闭环
#### 6.2.1 反馈数据记录
- 每笔信号写入 postgres `strategy_feedback`，记录预测收益、实际收益、正确性。
- 新增 `false_negative` 字段：记录满足所有规则但未被选入的股票后续表现，用于检测“错失机会”。

#### 6.2.2 自省 Agent 功能（每日15:30执行）
- 统计近30天胜率、收益、回撤。
- 分析错误信号原因（指标失效、消息误判、流动性风险等），归类统计。
- 动态调整参数：
  - 若胜率连续5天<40%，触发立即优化：调整 RSI 阈值、换手率范围等。
  - 若错失机会率>20%，放宽选股规则（如降低量比要求）。
- 调用后端回测引擎验证新参数（样本外数据），通过后更新 `strategy_config` 表，并记录新版本。
- 生成自省报告推送至飞书。

#### 6.2.3 动态风控
- 止损/止盈基于 ATR(20)：  
  `stop_loss = entry_price - ATR × 2`  
  `take_profit = entry_price + ATR × 3`  
  自动适应市场波动。
- 仓位管理根据风险度动态调整：  
  `position_size = total_equity × position_per_stock × (1 - risk_ratio)`

---

## 7. 后端服务（golang kratosv2）

### 7.1 核心功能模块
| 模块 | 关键接口示例 |
|------|--------------|
| 数据聚合 | `GET /api/data/quote/{code}`<br>`POST /api/data/fetch/manual` |
| RAG检索 | `GET /api/rag/search?q=...&limit=10`<br>`POST /api/rag/refresh` |
| 策略回测 | `POST /api/backtest/run`<br>`GET /api/backtest/report/{task_id}` |
| 交易接口 | `POST /api/trade/order`<br>`GET /api/trade/account` |
| 系统监控 | `GET /api/health`<br>`GET /api/monitor/status` |

### 7.2 高可用设计
- 异步框架（golang kratosv2 ）。
- 任务队列（nats）。
- 日志结构化，异常自动告警。
- 健康检查与自启动（systemd）。

---

## 8. 前端看板（Vue3）模块

| 看板 | 核心内容 | 刷新频率 |
|------|----------|----------|
| 交易信号看板 | 推荐卡片（股票、逻辑、风险、止损止盈）、历史信号回溯 | 实时+5秒轮询 |
| 持仓与绩效看板 | 权益曲线、持仓明细、风险度仪表盘、回撤曲线 | 10秒 |
| 热点舆情看板 | 热词云图、情感指数、突发新闻、板块热度 | 实时推送 |
| 策略回测看板 | 回测任务管理、收益曲线、参数优化结果 | 手动刷新 |
| AI预测与复盘看板 | 预测曲线、准确率趋势、模型版本 | 盘后更新 |
| 系统运维看板 |agents运行、任务完成情况、 硬件资源、服务状态、错误日志 | 30秒 |

---

## 9. 通知系统（飞书）

| 通知类型 | 触发条件 | 优先级 |
|----------|----------|--------|
| 盘中实时交易信号 | 触发选股规则 | 最高 |
| 风险预警 | 止损/止盈/风险度超标 | 最高 |
| 系统异常告警 | 数据源中断、服务崩溃 | 最高 |
| 开盘前信号 | 盘前扫描完成 | 高 |
| 尾盘信号 | 尾盘扫描完成 | 高 |
| 每日复盘报告 | 收盘后统计 | 中 |
| 每周策略报告 | 周日 | 中 |

---

## 10. 自动交易与风控

### 10.1 模拟交易模式


### 10.2 风控规则
| 风控类型 | 实现规则 | 参数 |
|----------|----------|------|
| ATR动态止损 | 入场价 - ATR×2 | ATR(20) |
| ATR动态止盈 | 入场价 + ATR×3 | ATR(20) |
| 仓位控制 | 单票≤5%，总仓≤80% | 动态调整 |
| 单日熔断 | 日亏损≥2%暂停交易 | 熔断至收盘 |
| 涨跌停限制 | 涨停不买，跌停不卖 | 永久 |
| 流动性校验 | 盘口挂单≥2倍下单量 | 实时检查 |

---

## 11. 部署步骤

---

## 13. 故障排查与运维指南


### 13.2 模型相关
| 现象 | 解决 |
|------|------|
| 推理超时 | 降低并发，等待首次加载完成 |

### 13.3 数据源相关
| 现象 | 解决 |
|------|------|
| 连接失败 | 检查网络、API token，自动切换备用源 |
| 数据缺失 | 手动补拉，检查除权除息，过滤停牌标的 |

### 13.4 日常运维
- 每日巡检：开盘前检查服务状态、硬件资源。
- 每周维护：清理缓存、镜像，备份数据。
- 每月复盘：策略效果、系统稳定性。
- 定期备份：核心数据库每日备份，模型文件每周备份。

---

---

## 15. 实施优先级与时间规划

| 优先级 | 模块 | 工期 | 交付物 |
|--------|------|------|--------|
| P0 | 数据源对接与存储 | 2天 | 数据库正常运行，表结构创建 |
| P0 | OpenClaw部署与模型加载 | 3天 | Gateway服务正常，模型可推理 |
| P0 | 选股Skill开发 | 3天 | 可输出标准化信号 |
| P1 | 后端框架 | 2天 | API可用，定时任务执行 |
| P1 | 通知集成 | 1天 | 信号推送正常 |
| P1 | 回测引擎 | 3天 | 可生成回测报告 |
| P2 | 风控+模拟交易 | 3天 | 模拟下单，风控生效 |
| P2 | 前端看板 | 5天 | 交易看板可用 |
| P3 | RAG+时序预测 | 4天 | 语义检索，预测上线 |
| P3 | 自主学习 | 3天 | 自省Agent闭环 |
| P3 | 实盘对接 | 2天 | 券商API集成 |
| P3 | 运维监控 | 2天 | 监控看板，故障排查 |

**总工期**：约28个工作日（不含模型微调、策略深度调优）


**文档结束**