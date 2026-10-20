/**
 * NtFreshness（方案 §7.3）合同测试：
 * - 四分支：年龄「Ns / Nmin / Nh·HH:mm / Nd·MM-DD」、超阈染 warn、「时点未知」弱化、断流「断 · 最后 HH:mm:ss」；
 * - 数据纪律：at 缺失显「时点未知」，绝不拿 0s/当前时刻冒充；
 * - version 分支「v{n}·已生效」优先于年龄显示，但不掩盖 cut。
 */
import { mount } from '@vue/test-utils'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import NtFreshness from '../NtFreshness.vue'
import { formatClock, formatMinute } from '@/utils/time'

const BASE = new Date('2026-10-20T09:30:00+08:00').getTime()

beforeEach(() => {
  vi.useFakeTimers()
  vi.setSystemTime(BASE)
})

afterEach(() => {
  vi.useRealTimers()
})

describe('年龄分支', () => {
  it('60s 内显 Ns', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 12_000 } })
    expect(wrapper.text()).toBe('行情 12s')
  })

  it('60min 内显 Nmin', () => {
    const wrapper = mount(NtFreshness, { props: { label: '情报', at: BASE - 3 * 60_000 } })
    expect(wrapper.text()).toBe('情报 3min')
  })

  it('24h 内显 Nh·HH:mm', () => {
    const wrapper = mount(NtFreshness, { props: { label: '情报', at: BASE - 2 * 3_600_000 } })
    expect(wrapper.text()).toBe(`情报 2h·${formatMinute(BASE - 2 * 3_600_000)}`)
  })

  it('24h 及以上显 Nd·MM-DD', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 2 * 86_400_000 } })
    expect(wrapper.text()).toBe('行情 2d·10-18')
  })

  it('未超阈不染 warn', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 12_000 } })
    expect(wrapper.classes()).not.toContain('nf--warn')
  })
})

describe('超阈与缺失纪律', () => {
  it('年龄超过 staleAfterMs 染 warn（默认 30s）', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 45_000 } })
    expect(wrapper.text()).toBe('行情 45s')
    expect(wrapper.classes()).toContain('nf--warn')
  })

  it('自定义阈值生效', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 5_000, staleAfterMs: 3_000 } })
    expect(wrapper.classes()).toContain('nf--warn')
  })

  it('时点未知：at 缺失显弱化「时点未知」，不冒充 0s', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: null } })
    expect(wrapper.text()).toBe('行情 时点未知')
    expect(wrapper.classes()).toContain('nf--muted')
    expect(wrapper.text()).not.toContain('0s')
  })
})

describe('断流分支', () => {
  it('cut=true 显「断 · 最后 HH:mm:ss」（warn）', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: BASE - 60_000, cut: true } })
    expect(wrapper.text()).toBe(`断 · 最后 ${formatClock(BASE - 60_000)}`)
    expect(wrapper.classes()).toContain('nf--warn')
  })

  it('cut 且时点缺失：显「断 · 时点未知」，不拿当前时刻冒充', () => {
    const wrapper = mount(NtFreshness, { props: { label: '行情', at: null, cut: true } })
    expect(wrapper.text()).toBe('断 · 时点未知')
    expect(wrapper.classes()).toContain('nf--warn')
  })
})

describe('version 优先级', () => {
  it('version 有值显「v{n}·已生效」，优先于年龄分支（muted）', () => {
    const wrapper = mount(NtFreshness, { props: { label: '配置', at: BASE - 5_000, version: 42 } })
    expect(wrapper.text()).toBe('配置 v42·已生效')
    expect(wrapper.classes()).toContain('nf--muted')
    expect(wrapper.text()).not.toContain('5s')
  })

  it('version 不掩盖 cut：断流仍显「断 · 最后 HH:mm:ss」（warn）', () => {
    const wrapper = mount(NtFreshness, { props: { label: '配置', at: BASE - 60_000, cut: true, version: 42 } })
    expect(wrapper.text()).toBe(`断 · 最后 ${formatClock(BASE - 60_000)}`)
    expect(wrapper.classes()).toContain('nf--warn')
    expect(wrapper.text()).not.toContain('v42')
  })
})
