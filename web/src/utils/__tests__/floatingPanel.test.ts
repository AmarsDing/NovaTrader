import { describe, expect, it } from 'vitest'
import { clamp, clampToViewport, snap } from '../floatingPanel'

describe('floatingPanel', () => {
  it('snap 按步长取整', () => {
    expect(snap(23, 22)).toBe(22)
    expect(snap(33, 22)).toBe(44)
    expect(snap(0, 22)).toBe(0)
    expect(snap(5, 0)).toBe(5)
  })

  it('clamp 区间', () => {
    expect(clamp(5, 0, 10)).toBe(5)
    expect(clamp(-3, 0, 10)).toBe(0)
    expect(clamp(15, 0, 10)).toBe(10)
    expect(clamp(5, 10, 0)).toBe(10)
  })

  it('clampToViewport 不越界', () => {
    const viewport = { width: 1280, height: 800 }
    const leftTop = clampToViewport({ x: -50, y: -20, width: 300, height: 200 }, viewport)
    expect(leftTop.x).toBe(10)
    expect(leftTop.y).toBe(10)
    const rightBottom = clampToViewport({ x: 1200, y: 700, width: 300, height: 200 }, viewport)
    expect(rightBottom.x).toBe(970)
    expect(rightBottom.y).toBe(590)
  })

  it('面板大过视口时贴左上', () => {
    const result = clampToViewport({ x: 400, y: 400, width: 2000, height: 2000 }, { width: 800, height: 600 })
    expect(result.x).toBe(10)
    expect(result.y).toBe(10)
  })
})
