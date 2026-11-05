-- 涨停最后开板时刻（可空列，独立于代码上线；历史数据不回填）。
-- 部署顺序：先执行本文件，再切换包含 LastBreakAt 的 market 服务代码。
ALTER TABLE limit_board ADD COLUMN IF NOT EXISTS last_break_at timestamptz NULL;
COMMENT ON COLUMN limit_board.last_break_at IS '当日最后一次真实开板（封住→打开）时刻；未采集为空，禁止用 as_of 冒充';
