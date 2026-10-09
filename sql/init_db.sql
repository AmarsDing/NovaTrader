-- 创建数据库（如果不存在）
CREATE DATABASE IF NOT EXISTS stock_db;
\c stock_db;

-- 行情数据表
CREATE TABLE market_data (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(20) NOT NULL,
    date DATE NOT NULL,
    open DECIMAL(10,4),
    high DECIMAL(10,4),
    low DECIMAL(10,4),
    close DECIMAL(10,4),
    volume BIGINT,
    amount DECIMAL(16,4),
    UNIQUE(symbol, date)
);

-- 新闻舆情表
CREATE TABLE news_sentiment (
    id BIGSERIAL PRIMARY KEY,
    news_title TEXT,
    content TEXT,
    source VARCHAR(100),
    publish_time TIMESTAMP,
    sentiment_score DECIMAL(5,4),  -- -1 to 1
    stock_related VARCHAR(20)
);

-- Agent 决策日志表
CREATE TABLE agent_decisions (
    id BIGSERIAL PRIMARY KEY,
    agent_name VARCHAR(50),
    decision_type VARCHAR(50),
    content JSONB,
    confidence DECIMAL(5,4),
    created_at TIMESTAMP DEFAULT NOW()
);

-- 晨会简报表
CREATE TABLE morning_briefings (
    id BIGSERIAL PRIMARY KEY,
    date DATE NOT NULL,
    summary JSONB,
    created_at TIMESTAMP DEFAULT NOW()
);

-- 复盘报告表
CREATE TABLE daily_reviews (
    id BIGSERIAL PRIMARY KEY,
    date DATE NOT NULL,
    review_content TEXT,
    accuracy_stats JSONB,
    lessons_learned TEXT[],
    created_at TIMESTAMP DEFAULT NOW()
);

-- 策略库表
CREATE TABLE strategy_library (
    id BIGSERIAL PRIMARY KEY,
    name VARCHAR(100) UNIQUE,
    description TEXT,
    code TEXT,
    backtest_result JSONB,
    status VARCHAR(20) DEFAULT 'pending' -- pending, active, deprecated
);

-- 持仓记录表
CREATE TABLE positions (
    id BIGSERIAL PRIMARY KEY,
    symbol VARCHAR(20),
    quantity INT,
    avg_cost DECIMAL(10,4),
    current_price DECIMAL(10,4),
    pnl DECIMAL(12,4),
    updated_at TIMESTAMP DEFAULT NOW()
);

-- 创建索引
CREATE INDEX idx_market_symbol_date ON market_data(symbol, date);
CREATE INDEX idx_news_time ON news_sentiment(publish_time);
CREATE INDEX idx_agent_time ON agent_decisions(created_at);