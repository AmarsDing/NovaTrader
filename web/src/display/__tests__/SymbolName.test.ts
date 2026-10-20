/**
 * SymbolName 组件（方案 §6.1）合同测试：
 * - 四模式渲染：inline 名称+裸六位 / stacked 名称上行代码下行 / compact 只名 / code 只码；
 * - 缺名显弱化「名称未返回」（数据纪律：禁空串冒充、禁拿代码冒充名称占位）；
 * - 点击：默认 setSymbol(规范全码) + openPanel('quote-sidebar')；code 模式默认不可点；
 * - 挂载与换码 ensure 名称字典（normalizeCode 三市场归一）；
 * - 顺带钉住 NtTable kind:'symbol' 快捷列渲 SymbolName。
 */
import { mount } from '@vue/test-utils'
import { createPinia, setActivePinia } from 'pinia'
import { beforeEach, describe, expect, it, vi } from 'vitest'
import SymbolName from '../SymbolName.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import { useSecurityStore } from '@/stores/security'
import { useSymbolStore } from '@/stores/symbol'

const mock = vi.hoisted(() => ({
  openPanel: vi.fn(),
  hasWorkbench: true,
}))

vi.mock('@/workspace/inject', () => ({
  getWorkbench: () => (mock.hasWorkbench ? { openPanel: mock.openPanel } : undefined),
}))

beforeEach(() => {
  setActivePinia(createPinia())
  vi.resetAllMocks()
  mock.hasWorkbench = true
})

describe('SymbolName 四模式渲染', () => {
  it('inline：名称 + 裸六位代码一行', () => {
    useSecurityStore().put([{ code: '300750', name: '宁德时代' }])
    const wrapper = mount(SymbolName, { props: { code: '300750', mode: 'inline' } })
    expect(wrapper.classes()).toContain('sn--inline')
    expect(wrapper.find('.sn__name').text()).toBe('宁德时代')
    expect(wrapper.find('.sn__code').text()).toBe('300750')
  })

  it('stacked：名称行在上（主重）、代码行在下', () => {
    useSecurityStore().put([{ code: '600519.SH', name: '贵州茅台' }])
    const wrapper = mount(SymbolName, { props: { code: '600519', mode: 'stacked' } })
    expect(wrapper.classes()).toContain('sn--stacked')
    const name = wrapper.find('.sn__name')
    const code = wrapper.find('.sn__code')
    expect(name.text()).toBe('贵州茅台')
    expect(code.text()).toBe('600519')
    // 名称在代码之前（文档序）。
    expect(name.element.compareDocumentPosition(code.element) & Node.DOCUMENT_POSITION_FOLLOWING).toBeTruthy()
  })

  it('compact：只显名称，不渲代码槽', () => {
    useSecurityStore().put([{ code: '000001', name: '平安银行' }])
    const wrapper = mount(SymbolName, { props: { code: '000001', mode: 'compact' } })
    expect(wrapper.find('.sn__name').text()).toBe('平安银行')
    expect(wrapper.find('.sn__code').exists()).toBe(false)
  })

  it('code：只显 mono 代码（豁免位），无名称槽', () => {
    useSecurityStore().put([{ code: '688981', name: '中芯国际' }])
    const wrapper = mount(SymbolName, { props: { code: '688981', mode: 'code' } })
    expect(wrapper.find('.sn__name').exists()).toBe(false)
    expect(wrapper.find('.sn__code').text()).toBe('688981')
  })
})

describe('SymbolName 缺名数据纪律', () => {
  it('名称未取到：名称槽显弱化「名称未返回」，不留空白、不拿代码冒充', () => {
    const wrapper = mount(SymbolName, { props: { code: '430047' } })
    expect(wrapper.find('.sn__name').text()).toBe('名称未返回')
    expect(wrapper.classes()).toContain('sn--noname')
    // 北交所代码归一后仍展示裸六位，名称槽绝不回填代码。
    expect(wrapper.find('.sn__code').text()).toBe('430047')
    expect(wrapper.find('.sn__name').text()).not.toContain('430047')
  })

  it('compact 缺名同样显「名称未返回」', () => {
    const wrapper = mount(SymbolName, { props: { code: '830799', mode: 'compact' } })
    expect(wrapper.find('.sn__name').text()).toBe('名称未返回')
    expect(wrapper.classes()).toContain('sn--noname')
  })
})

describe('SymbolName 点击行为', () => {
  it('inline 默认可点：setSymbol 规范全码 + openPanel quote-sidebar', async () => {
    const wrapper = mount(SymbolName, { props: { code: '300750' } })
    await wrapper.trigger('click')
    expect(useSymbolStore().code).toBe('300750.SZ')
    expect(mock.openPanel).toHaveBeenCalledWith('quote-sidebar', { symbol: '300750.SZ' })
  })

  it('code 模式默认不可点：无 role/button 语义，点击不切票不开面板', async () => {
    const wrapper = mount(SymbolName, { props: { code: '600519', mode: 'code' } })
    expect(wrapper.attributes('role')).toBeUndefined()
    expect(wrapper.attributes('tabindex')).toBeUndefined()
    await wrapper.trigger('click')
    expect(useSymbolStore().code).toBe('')
    expect(mock.openPanel).not.toHaveBeenCalled()
  })

  it('clickable=false 钉死不可点；clickable=true 可覆写 code 模式', async () => {
    const off = mount(SymbolName, { props: { code: '600519', clickable: false } })
    await off.trigger('click')
    expect(useSymbolStore().code).toBe('')
    expect(mock.openPanel).not.toHaveBeenCalled()

    const on = mount(SymbolName, { props: { code: '600519', mode: 'code', clickable: true } })
    await on.trigger('click')
    expect(useSymbolStore().code).toBe('600519.SH')
    expect(mock.openPanel).toHaveBeenCalledWith('quote-sidebar', { symbol: '600519.SH' })
  })

  it('工作台不存在时静默跳过 openPanel，切票不受影响', async () => {
    mock.hasWorkbench = false
    const wrapper = mount(SymbolName, { props: { code: '000001' } })
    await wrapper.trigger('click')
    expect(useSymbolStore().code).toBe('000001.SZ')
    expect(mock.openPanel).not.toHaveBeenCalled()
  })
})

describe('SymbolName 名称字典联动', () => {
  it('挂载与换码均 ensure 规范全码', async () => {
    const security = useSecurityStore()
    const spy = vi.spyOn(security, 'ensure')
    const wrapper = mount(SymbolName, { props: { code: '920001' } })
    expect(spy).toHaveBeenCalledWith(['920001.BJ'])
    await wrapper.setProps({ code: '000001' })
    expect(spy).toHaveBeenCalledWith(['000001.SZ'])
  })
})

describe('NtTable kind:"symbol" 快捷列', () => {
  interface Row extends Record<string, unknown> {
    symbol: string
    last: number
  }

  it('cell 值按证券代码渲 SymbolName，其余列行为不变', () => {
    useSecurityStore().put([{ code: '600519.SH', name: '贵州茅台' }])
    const columns: NtTableColumn<Row>[] = [
      { key: 'symbol', title: '证券', kind: 'symbol' },
      { key: 'last', title: '现价', align: 'right' },
    ]
    const wrapper = mount(NtTable<Row>, {
      props: { columns, rows: [{ symbol: '600519', last: 1705 }] },
    })
    const sn = wrapper.find('.sn')
    expect(sn.exists()).toBe(true)
    expect(sn.find('.sn__name').text()).toBe('贵州茅台')
    expect(sn.find('.sn__code').text()).toBe('600519')
    expect(wrapper.text()).toContain('1705')
  })

  it('具名槽仍可覆盖 symbol 列（槽位优先）', () => {
    useSecurityStore().put([{ code: '600519.SH', name: '贵州茅台' }])
    const columns: NtTableColumn<Row>[] = [{ key: 'symbol', title: '证券', kind: 'symbol' }]
    const wrapper = mount(NtTable<Row>, {
      props: { columns, rows: [{ symbol: '600519', last: 0 }] },
      slots: { 'cell-symbol': `<span class="custom-cell">覆盖</span>` },
    })
    expect(wrapper.find('.custom-cell').exists()).toBe(true)
    expect(wrapper.find('.sn').exists()).toBe(false)
  })
})
