import { describe, expect, it, vi } from 'vitest'
import {
  createQuoteFlash,
  QUOTE_FLASH_CALM_MS,
  QUOTE_FLASH_MS,
  type FlashSample,
  type QuoteFlashClock,
} from '@/utils/quote-flash'

/** 手动时钟：defer 只保留当前一条定时。 */
function makeClock() {
  let now = 0
  let timer: { run: () => void; delay: number } | null = null
  const clock: QuoteFlashClock = {
    now: () => now,
    defer: (run, delay) => {
      timer = { run, delay }
      return () => {
        if (timer?.run === run) timer = null
      }
    },
  }
  return {
    clock,
    /** 推进毫秒并触发到点定时（链式直到无定时）。 */
    advance(ms: number) {
      now += ms
      let guard = 8
      while (timer && guard-- > 0) {
        const t = timer
        timer = null
        t.run()
      }
    },
  }
}

describe('quote-flash 闪动节律', () => {
  it('mark 后闪动 FLASH_MS，冷静期后条目清除', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    flash.mark('600519', 'up')
    expect(flash.get('600519')).toMatchObject({ direction: 'up', flashing: true })

    env.advance(QUOTE_FLASH_MS) // 闪动结束但仍在冷静期
    expect(flash.get('600519')).toMatchObject({ direction: 'up', flashing: false })

    env.advance(QUOTE_FLASH_CALM_MS) // 冷静期结束，条目清除
    expect(flash.get('600519')).toBeUndefined()
    flash.dispose()
  })

  it('闪动窗口内同方向再变：顺延不重计数；快照不变不派发', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    const listener = vi.fn()
    flash.onChange(listener)
    flash.mark('600519', 'up')
    expect(listener).toHaveBeenCalledTimes(1)

    env.advance(200) // 仍在闪动内
    flash.mark('600519', 'up')
    expect(listener).toHaveBeenCalledTimes(1) // 内容未变，不再派发
    expect(flash.get('600519')).toMatchObject({ flashing: true })
    flash.dispose()
  })

  it('冷静期内同向变动不重闪；方向反转立即重闪', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    flash.mark('600519', 'up')
    env.advance(QUOTE_FLASH_MS + 100) // 进入冷静期
    expect(flash.get('600519')?.flashing).toBe(false)

    flash.mark('600519', 'up') // 冷静期内同向：不闪
    expect(flash.get('600519')?.flashing).toBe(false)

    flash.mark('600519', 'down') // 反转：立即重闪
    expect(flash.get('600519')).toMatchObject({ direction: 'down', flashing: true })
    flash.dispose()
  })

  it('多 symbol 独立计时', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    flash.mark('600519', 'up')
    env.advance(200)
    flash.mark('000001', 'down')
    env.advance(QUOTE_FLASH_MS - 200) // 600519 闪完，000001 仍在闪
    expect(flash.get('600519')?.flashing).toBe(false)
    expect(flash.get('000001')?.flashing).toBe(true)
    flash.dispose()
  })

  it('快照引用稳定：无变化时同一 Map 对象', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    const snapshots: ReadonlyMap<string, FlashSample>[] = []
    flash.onChange((snap) => snapshots.push(snap))
    flash.mark('600519', 'up')
    flash.mark('000001', 'up')
    expect(snapshots).toHaveLength(2)
    expect(snapshots[0]).not.toBe(snapshots[1])
    // 同方向闪动内再 mark 不产新快照
    const before = flash.snapshot()
    flash.mark('600519', 'up')
    expect(flash.snapshot()).toBe(before)
    flash.dispose()
  })

  it('dispose 后不再派发、计时清空', () => {
    const env = makeClock()
    const flash = createQuoteFlash(env.clock)
    const listener = vi.fn()
    flash.onChange(listener)
    flash.mark('600519', 'up')
    flash.dispose()
    env.advance(1000)
    expect(listener).toHaveBeenCalledTimes(1)
    expect(flash.get('600519')).toBeUndefined()
  })
})
