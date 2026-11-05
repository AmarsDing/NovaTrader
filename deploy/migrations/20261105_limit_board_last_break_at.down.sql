-- 回滚：移除涨停最后开板时刻列。回滚后旧代码不再读取该列，数据不可恢复（未做备份时）。
ALTER TABLE limit_board DROP COLUMN IF EXISTS last_break_at;
