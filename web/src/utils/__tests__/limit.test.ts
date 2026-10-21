import { describe, expect, it } from 'vitest'
import { boardTag, limitRatio, limitState } from '../limit'

describe('limitRatio：代码前缀 → 涨跌幅档', () => {
  it('主板代码为 10%（含带后缀与裸码）', () => {
    expect(limitRatio('600519.SH')).toBe(0.1)
    expect(limitRatio('000001.SZ')).toBe(0.1)
    expect(limitRatio('002594.SZ')).toBe(0.1)
  })

  it('创业板 300/301/302 与科创板 68x 为 20%', () => {
    expect(limitRatio('300750.SZ')).toBe(0.2)
    expect(limitRatio('301269.SZ')).toBe(0.2)
    expect(limitRatio('688981.SH')).toBe(0.2)
    expect(limitRatio('689009.SH')).toBe(0.2)
  })

  it('北交所（920 段与老 4/8 段）为 30%', () => {
    expect(limitRatio('920001.BJ')).toBe(0.3)
    expect(limitRatio('430090.BJ')).toBe(0.3)
    expect(limitRatio('830799.BJ')).toBe(0.3)
    expect(limitRatio('871981.BJ')).toBe(0.3)
  })

  it('空串/非数字前缀落到主板默认档 10%', () => {
    expect(limitRatio('')).toBe(0.1)
    expect(limitRatio('HK.00700')).toBe(0.1)
  })
})

describe('limitState：封板判定', () => {
  it('收盘=理论限制价且最高=收盘 → 涨停', () => {
    expect(limitState(11, 11, 10.5, 10, '600519.SH')).toBe('up')
    // 昨收带两位小数导致限制价需取整：3.33×1.1=3.663 → 3.66
    expect(limitState(3.66, 3.66, 3.2, 3.33, '000001.SZ')).toBe('up')
  })

  it('收盘=理论限制价且最低=收盘 → 跌停', () => {
    expect(limitState(9, 9.6, 9, 10, '600519.SH')).toBe('down')
  })

  it('触板未封（最高>收盘）与普通涨幅都不算', () => {
    expect(limitState(10.95, 11, 10.5, 10, '600519.SH')).toBeNull()
    expect(limitState(10.4, 10.5, 10.1, 10, '600519.SH')).toBeNull()
  })

  it('20% 档的股票 +10% 封死不被误亮（按自己档位判定）', () => {
    expect(limitState(11, 11, 10.2, 10, '300750.SZ')).toBeNull()
    expect(limitState(12, 12, 10.8, 10, '688981.SH')).toBe('up')
  })

  it('昨收缺失或数据非法 → null（宁缺勿误）', () => {
    expect(limitState(11, 11, 10.5, null, '600519.SH')).toBeNull()
    expect(limitState(11, 11, 10.5, 0, '600519.SH')).toBeNull()
    expect(limitState(NaN, 11, 10.5, 10, '600519.SH')).toBeNull()
  })
})

describe('boardTag：板块角标册键（status-map board.* 三键）', () => {
  it('创业板 300/301/302 → board.cyb', () => {
    expect(boardTag('300750.SZ')).toBe('board.cyb')
    expect(boardTag('301269.SZ')).toBe('board.cyb')
    expect(boardTag('302132.SZ')).toBe('board.cyb')
  })

  it('科创板 68x（含 689）→ board.kcb', () => {
    expect(boardTag('688981.SH')).toBe('board.kcb')
    expect(boardTag('689009.SH')).toBe('board.kcb')
  })

  it('北交所（920 段与老 4/8 段）→ board.bj', () => {
    expect(boardTag('920001.BJ')).toBe('board.bj')
    expect(boardTag('430090.BJ')).toBe('board.bj')
    expect(boardTag('830799.BJ')).toBe('board.bj')
  })

  it('主板与无法判定一律 null（默认态不打标）', () => {
    expect(boardTag('600519.SH')).toBeNull()
    expect(boardTag('000001.SZ')).toBeNull()
    expect(boardTag('002594.SZ')).toBeNull()
    expect(boardTag('')).toBeNull()
    expect(boardTag('HK.00700')).toBeNull()
  })

  it('与 limitRatio 同前缀口径（单源共存）：非主板档恰好全打标', () => {
    for (const sym of ['600519.SH', '000001.SZ', '300750.SZ', '688981.SH', '689009.SH', '920001.BJ', '430090.BJ', '830799.BJ', '']) {
      expect(boardTag(sym) !== null).toBe(limitRatio(sym) !== 0.1)
    }
  })
})

describe('boardTag：板块角标册键（status-map board.* 三键）', () => {
  it('创业板 300/301/302 → board.cyb', () => {
    expect(boardTag('300750.SZ')).toBe('board.cyb')
    expect(boardTag('301269.SZ')).toBe('board.cyb')
    expect(boardTag('302132.SZ')).toBe('board.cyb')
  })

  it('科创板 68x（含 689）→ board.kcb', () => {
    expect(boardTag('688981.SH')).toBe('board.kcb')
    expect(boardTag('689009.SH')).toBe('board.kcb')
  })

  it('北交所（920 段与老 4/8 段）→ board.bj', () => {
    expect(boardTag('920001.BJ')).toBe('board.bj')
    expect(boardTag('430090.BJ')).toBe('board.bj')
    expect(boardTag('830799.BJ')).toBe('board.bj')
    expect(boardTag('871981.BJ')).toBe('board.bj')
  })

  it('主板与无法判定一律 null（默认态不打标）', () => {
    expect(boardTag('600519.SH')).toBeNull()
    expect(boardTag('000001.SZ')).toBeNull()
    expect(boardTag('002594.SZ')).toBeNull()
    expect(boardTag('')).toBeNull()
    expect(boardTag('HK.00700')).toBeNull()
  })

  it('与 limitRatio 同前缀口径：角标键恰好落在 20%/30% 非常规档', () => {
    for (const sym of ['300750.SZ', '688981.SH', '689009.SH', '920001.BJ', '430090.BJ']) {
      expect(boardTag(sym), sym).not.toBeNull()
      expect(limitRatio(sym), sym).toBeGreaterThan(0.1)
    }
    for (const sym of ['600519.SH', '000001.SZ', '002594.SZ']) {
      expect(boardTag(sym), sym).toBeNull()
      expect(limitRatio(sym), sym).toBe(0.1)
    }
  })
})
