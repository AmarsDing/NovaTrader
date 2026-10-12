import { describe, expect, it } from 'vitest'
import { bandCountdown, bandSegments, bandSpec, detectBand, hudCells, isTradingDay } from './timeband'

/** 2026-10-12 是周一（交易日）；2026-10-17 周六、2026-10-18 周日。 */
function at(day: number, hour: number, minute: number): Date {
  return new Date(2026, 9, day, hour, minute, 0, 0)
}

describe('isTradingDay', () => {
  it('周一到周五是交易日，周六周日不是', () => {
    expect(isTradingDay(at(12, 10, 0))).toBe(true)
    expect(isTradingDay(at(16, 10, 0))).toBe(true)
    expect(isTradingDay(at(17, 10, 0))).toBe(false)
    expect(isTradingDay(at(18, 10, 0))).toBe(false)
  })
})

describe('detectBand', () => {
  it('交易日各时段落在正确段落', () => {
    expect(detectBand(at(12, 8, 0))).toBe('preopen')
    expect(detectBand(at(12, 8, 45))).toBe('preopen')
    expect(detectBand(at(12, 9, 15))).toBe('auction')
    expect(detectBand(at(12, 9, 20))).toBe('auction')
    expect(detectBand(at(12, 9, 27))).toBe('auction')
    expect(detectBand(at(12, 9, 30))).toBe('morning')
    expect(detectBand(at(12, 10, 30))).toBe('morning')
    expect(detectBand(at(12, 11, 30))).toBe('lunch')
    expect(detectBand(at(12, 12, 30))).toBe('lunch')
    expect(detectBand(at(12, 13, 0))).toBe('afternoon')
    expect(detectBand(at(12, 14, 29))).toBe('afternoon')
    expect(detectBand(at(12, 14, 30))).toBe('closing')
    expect(detectBand(at(12, 14, 59))).toBe('closing')
    expect(detectBand(at(12, 15, 30))).toBe('postmarket')
    expect(detectBand(at(12, 16, 59))).toBe('postmarket')
  })

  it('交易日 8:30 前与 17:00 后分别并入盘前/盘后，不造新段落', () => {
    expect(detectBand(at(12, 6, 0))).toBe('preopen')
    expect(detectBand(at(12, 21, 0))).toBe('postmarket')
  })

  it('非交易日全天是 offday，不看钟点', () => {
    expect(detectBand(at(17, 10, 0))).toBe('offday')
    expect(detectBand(at(18, 15, 0))).toBe('offday')
    expect(detectBand(at(18, 3, 0))).toBe('offday')
  })

  it('交易时段之外的边界分钟稳定', () => {
    expect(detectBand(at(12, 9, 14))).toBe('preopen')
    expect(detectBand(at(12, 9, 29))).toBe('auction')
    expect(detectBand(at(12, 9, 59))).toBe('morning')
    expect(detectBand(at(12, 14, 59))).toBe('closing')
    expect(detectBand(at(12, 17, 0))).toBe('postmarket')
  })
})

describe('bandSpec', () => {
  it('竞价时段提升扫描牌为焦点牌，尾盘提升账户牌', () => {
    expect(bandSpec('auction').promote).toBe('post:scan')
    expect(bandSpec('closing').promote).toBe('book:center')
    expect(bandSpec('morning').promote).toBe('')
    expect(bandSpec('afternoon').promote).toBe('')
  })

  it('午休与盘后压暗画布并停环境流向点', () => {
    expect(bandSpec('lunch')).toMatchObject({ veil: 0.75, calm: true })
    expect(bandSpec('postmarket')).toMatchObject({ veil: 0.5, calm: true })
    expect(bandSpec('offday')).toMatchObject({ calm: true, veil: 1 })
    expect(bandSpec('morning')).toMatchObject({ veil: 1, calm: false })
  })

  it('每个时段都写明页面重点与终端带第一格语义', () => {
    for (const key of ['preopen', 'auction', 'morning', 'lunch', 'afternoon', 'closing', 'postmarket', 'offday'] as const) {
      const spec = bandSpec(key)
      expect(spec.focus.length).toBeGreaterThan(0)
      expect(spec.firstPane.length).toBeGreaterThan(0)
    }
    expect(bandSpec('auction').firstPane).toBe('auction')
    expect(bandSpec('closing').firstPane).toBe('positions')
    expect(bandSpec('offday').firstPane).toBe('dispatch')
  })
})

describe('bandSegments', () => {
  it('当前段实心、已过段半亮、未来段暗', () => {
    const views = bandSegments('afternoon')
    expect(views).toHaveLength(7)
    expect(views.map((item) => item.key)).toEqual(['preopen', 'auction', 'morning', 'lunch', 'afternoon', 'closing', 'postmarket'])
    expect(views.map((item) => item.state)).toEqual(['past', 'past', 'past', 'past', 'now', 'future', 'future'])
  })

  it('非交易日全部段落都是暗的', () => {
    const views = bandSegments('offday')
    expect(views.every((item) => item.state === 'future')).toBe(true)
  })
})

describe('bandCountdown', () => {
  it('各时段给出到下一个锚点的倒计时', () => {
    expect(bandCountdown(at(12, 8, 50), 'preopen')).toBe('距竞价 25 分')
    expect(bandCountdown(at(12, 9, 20), 'auction')).toBe('距开盘 10 分')
    expect(bandCountdown(at(12, 11, 0), 'morning')).toBe('距午休 30 分')
    expect(bandCountdown(at(12, 12, 30), 'lunch')).toBe('距开盘 30 分')
    expect(bandCountdown(at(12, 14, 47), 'closing')).toBe('距收盘 13 分')
    expect(bandCountdown(at(12, 15, 30), 'postmarket')).toBe('')
    expect(bandCountdown(at(17, 10, 0), 'offday')).toBe('')
  })
})

describe('hudCells', () => {
  it('上午与下午是默认十格', () => {
    for (const key of ['morning', 'afternoon'] as const) {
      const cells = hudCells(key)
      expect(cells).toHaveLength(10)
      expect(cells.map((item) => item.label)).toEqual(['阶段', '涨停', '炸板率', '主线', '信号', '漏选', '风控', '大模型', 'GPU', '当日盈亏'])
    }
    expect(hudCells('morning')[9].hero).toBe(true)
    expect(hudCells('morning')[0].select).toBe('post:scan')
    expect(hudCells('morning')[9].select).toBe('book:center')
  })

  it('时段特化只替换前三格，其余保持默认', () => {
    const auction = hudCells('auction')
    expect(auction).toHaveLength(10)
    expect(auction.slice(0, 3).map((item) => item.label)).toEqual(['竞价金额', '竞价涨停', '抢筹前三'])
    expect(auction.slice(3).map((item) => item.label)).toEqual(hudCells('morning').slice(3).map((item) => item.label))

    const closing = hudCells('closing')
    expect(closing.slice(0, 3).map((item) => item.label)).toEqual(['持仓浮盈', '距止损最近', '距收盘'])

    const offday = hudCells('offday')
    expect(offday.slice(0, 3).map((item) => item.label)).toEqual(['回测任务', '数据补全', '库体积'])
  })

  it('缺口格指向现有节点，保证点击弹层有内容', () => {
    for (const key of ['preopen', 'auction', 'lunch', 'closing', 'postmarket', 'offday'] as const) {
      for (const cell of hudCells(key)) {
        expect(cell.select.length).toBeGreaterThan(0)
      }
    }
  })
})
