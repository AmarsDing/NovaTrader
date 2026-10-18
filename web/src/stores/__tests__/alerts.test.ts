/**
 * G3 toast 栈纪律：硬上限 MAX_TOASTS 挤最旧，dismiss 语义不变。
 */
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it } from 'vitest'
import { MAX_TOASTS, useAlertsStore } from '../alerts'

beforeEach(() => setActivePinia(createPinia()))

describe('pushToast 栈上限', () => {
  it('超过 MAX_TOASTS 挤掉最旧，最新保留', () => {
    const alerts = useAlertsStore()
    for (let i = 1; i <= MAX_TOASTS + 2; i += 1) alerts.pushToast('info', `t${i}`)
    expect(alerts.toasts).toHaveLength(MAX_TOASTS)
    expect(alerts.toasts[0].title).toBe('t3')
    expect(alerts.toasts.at(-1)?.title).toBe(`t${MAX_TOASTS + 2}`)
  })

  it('dismissToast 精准移除', () => {
    const alerts = useAlertsStore()
    const a = alerts.pushToast('warn', 'a')
    alerts.pushToast('warn', 'b')
    alerts.dismissToast(a)
    expect(alerts.toasts.map((t) => t.title)).toEqual(['b'])
  })
})
