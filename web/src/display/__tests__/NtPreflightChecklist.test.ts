import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtPreflightChecklist, { type PreflightItem } from '../NtPreflightChecklist.vue'

const items: PreflightItem[] = [
  { key: 'a', label: '连续模拟运行证据', state: 'warn', why: '天数未满', progress: { done: 12, total: 20 } },
  { key: 'b', label: '文件单通道', state: 'ok', blocking: false },
  { key: 'c', label: '数据源', state: 'down', blocking: false, deeplink: '/datahub#sources' },
]

describe('NtPreflightChecklist（W3c 启用模态清单件）', () => {
  it('汇总只计必堵缺口；提示项再差不进缺口', () => {
    const w = mount(NtPreflightChecklist, { props: { items } })
    expect(w.text()).toContain('还有 1 项未达')
    expect(w.text()).toContain('12/20')
    expect(w.findAll('.nt-preflight__row')).toHaveLength(3)
  })

  it('全就位汇总归零；空清单给显式说明（不静默空白）', () => {
    const ok = items.map((i) => ({ ...i, state: 'ok' as const }))
    const w = mount(NtPreflightChecklist, { props: { items: ok } })
    expect(w.text()).toContain('前置检查全部就位')
    const empty = mount(NtPreflightChecklist, { props: { items: [] } })
    expect(empty.text()).toContain('本动作无需前置检查项')
  })

  it('unknown 显「未证实」暗点不显绿；total 缺省不画进度条（缺数不伪造 0%）', () => {
    const w = mount(NtPreflightChecklist, {
      props: { items: [{ key: 'x', label: 'x', state: 'unknown', progress: { done: 3, total: 0 } }] },
    })
    expect(w.text()).toContain('未证实')
    expect(w.find('.nt-preflight__progress').exists()).toBe(false)
  })

  it('「去处理」原样外抛 deeplink 给宿主（组件三层纪律：不碰 router/store）', async () => {
    const w = mount(NtPreflightChecklist, { props: { items } })
    await w.find('.nt-preflight__go').trigger('click')
    expect(w.emitted('navigate')).toEqual([['/datahub#sources']])
  })
})
