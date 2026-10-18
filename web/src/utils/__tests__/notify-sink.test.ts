/**
 * G3 三 sink 分流纯函数测试：活跃感知路由 + severity → toast 色调映射。
 * deskNotify 编排壳不测（Tauri/pinia 集成面），逻辑全部压在这两个纯函数里。
 */
import { describe, expect, it } from 'vitest'
import { pickNotifySink, toastKindOf } from '../notify'

describe('pickNotifySink 活跃感知分流', () => {
  it('活跃（聚焦且可见）→ 仅应用内 toast，无壳与否都不走原生', () => {
    expect(pickNotifySink(true, true)).toBe('inapp')
    expect(pickNotifySink(true, false)).toBe('inapp')
  })

  it('不活跃（最小化/失焦/锁屏）→ 有桌面壳走原生', () => {
    expect(pickNotifySink(false, true)).toBe('native')
  })

  it('不活跃且无壳（浏览器开发态）→ 应用内兜底', () => {
    expect(pickNotifySink(false, false)).toBe('inapp')
  })
})

describe('toastKindOf severity 映射', () => {
  it('致命/错误系 → error', () => {
    for (const raw of ['critical', 'CRITICAL', 'fatal', 'error', 'danger', 'live-danger']) {
      expect(toastKindOf(raw)).toBe('error')
    }
  })

  it('警告/告警系 → warn', () => {
    for (const raw of ['warn', 'warning', 'alert', 'WARN']) {
      expect(toastKindOf(raw)).toBe('warn')
    }
  })

  it('成功/上行系 → success', () => {
    for (const raw of ['success', 'up', 'ok']) {
      expect(toastKindOf(raw)).toBe('success')
    }
  })

  it('空与不可识别 → info', () => {
    expect(toastKindOf('')).toBe('info')
    expect(toastKindOf('notify')).toBe('info')
    expect(toastKindOf('whatever')).toBe('info')
  })
})
