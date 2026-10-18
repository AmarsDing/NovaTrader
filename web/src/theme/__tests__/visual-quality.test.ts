import { describe, expect, it, vi } from 'vitest'
import { createVisualFrameLoop, reduceVisualMotion, visualQualityOf } from '../visual-quality'

describe('显示质量与系统动态偏好', () => {
  it('旧偏好与未知值恢复为完整模式，节能和减少动态都禁运动', () => {
    expect(visualQualityOf(undefined)).toBe('full')
    expect(visualQualityOf('bad')).toBe('full')
    expect(visualQualityOf('eco')).toBe('eco')
    expect(visualQualityOf('reduced')).toBe('reduced')
    expect(reduceVisualMotion('full', false)).toBe(false)
    expect(reduceVisualMotion('full', true)).toBe(true)
    expect(reduceVisualMotion('eco', false)).toBe(true)
    expect(reduceVisualMotion('reduced', false)).toBe(true)
  })
})

function fixture() {
  let visible = true
  let moving = true
  let next = 0
  const frames = new Map<number, FrameRequestCallback>()
  const draw = vi.fn()
  const cancel = vi.fn((id: number) => frames.delete(id))
  const loop = createVisualFrameLoop({
    draw,
    canRender: () => visible,
    animate: () => moving,
    request: (callback) => { frames.set(++next, callback); return next },
    cancel,
  })
  function flush() {
    const queued = [...frames.entries()]
    frames.clear()
    for (const [, callback] of queued) callback(0)
  }
  return { loop, draw, cancel, frames, flush,
    visible: (value: boolean) => { visible = value },
    moving: (value: boolean) => { moving = value },
  }
}

describe('画布按可见性和真实状态调度', () => {
  it('重复失效最多一帧，动态态才续帧', () => {
    const f = fixture()
    f.loop.invalidate()
    f.loop.invalidate()
    expect(f.frames.size).toBe(1)
    f.flush()
    expect(f.draw).toHaveBeenCalledTimes(1)
    expect(f.frames.size).toBe(1)
    f.loop.dispose()
  })

  it('后台立即取消帧，后台新数据不续RAF，恢复显示只安排一帧', () => {
    const f = fixture()
    f.loop.invalidate()
    f.visible(false)
    f.loop.refresh()
    expect(f.cancel).toHaveBeenCalledTimes(1)
    expect(f.frames.size).toBe(0)
    f.loop.invalidate()
    f.flush()
    expect(f.draw).not.toHaveBeenCalled()
    f.visible(true)
    f.loop.refresh()
    expect(f.frames.size).toBe(1)
    f.flush()
    expect(f.draw).toHaveBeenCalledTimes(1)
    f.loop.dispose()
  })

  it('减少动态或无动态内容时只因数据/交互失效重绘', () => {
    const f = fixture()
    f.moving(false)
    f.loop.invalidate()
    f.flush()
    expect(f.draw).toHaveBeenCalledTimes(1)
    expect(f.frames.size).toBe(0)
    f.loop.invalidate()
    f.flush()
    expect(f.draw).toHaveBeenCalledTimes(2)
    expect(f.frames.size).toBe(0)
  })

  it('排队后隐藏也不绘制，销毁后无新帧', () => {
    const f = fixture()
    f.loop.invalidate()
    f.visible(false)
    f.flush()
    expect(f.draw).not.toHaveBeenCalled()
    f.loop.dispose()
    f.visible(true)
    f.loop.refresh()
    f.loop.invalidate()
    expect(f.frames.size).toBe(0)
  })
})
