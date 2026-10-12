import { describe, expect, it } from 'vitest'
import { RingSeries, sparkPath } from './sampler'

describe('RingSeries', () => {
  it('保留最近 size 个点，旧点被挤出', () => {
    const ring = new RingSeries(3)
    ring.push(1)
    ring.push(2)
    ring.push(3)
    ring.push(4)
    expect(ring.points()).toEqual([2, 3, 4])
  })

  it('null 也占一个采样位（缺口留在曲线里），长度不足时按现有长度返回', () => {
    const ring = new RingSeries(4)
    ring.push(1)
    ring.push(null)
    ring.push(3)
    expect(ring.points()).toEqual([1, null, 3])
    ring.push(4)
    ring.push(5)
    expect(ring.points()).toEqual([null, 3, 4, 5])
  })

  it('空序列返回空数组', () => {
    expect(new RingSeries(30).points()).toEqual([])
  })
})

describe('sparkPath', () => {
  it('把点归一化进 w×h 视口并连成折线', () => {
    const path = sparkPath([0, 5, 10], 100, 14)
    expect(path.startsWith('M')).toBe(true)
    expect(path).toContain('L')
    expect(path).toMatch(/^M[\d.]+ [\d.]+( L[\d.]+ [\d.]+)+$/)
  })

  it('null 把曲线断成多段，全 null 返回空串', () => {
    const broken = sparkPath([1, null, 3], 100, 14)
    const runs = broken.split('M').filter(Boolean)
    expect(runs.length).toBe(2)
    expect(sparkPath([null, null], 100, 14)).toBe('')
    expect(sparkPath([], 100, 14)).toBe('')
  })

  it('单点只画一个末点圆（M 后无 L）', () => {
    const path = sparkPath([7], 100, 14)
    expect(path.startsWith('M')).toBe(true)
    expect(path).not.toContain(' L')
  })

  it('恒定序列落在中线，不会贴边', () => {
    const path = sparkPath([5, 5, 5], 100, 14)
    const coords = path
      .slice(1)
      .split(' L')
      .map((item) => Number(item.split(' ')[1]))
    for (const y of coords) {
      expect(y).toBeGreaterThan(0)
      expect(y).toBeLessThan(14)
    }
  })
})
