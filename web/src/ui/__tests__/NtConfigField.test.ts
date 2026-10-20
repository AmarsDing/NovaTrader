/**
 * NtConfigField（§9.1 字段卡五件套）+ ConfigRevisionPanel·生效三态（§9.2）合同测试：
 * - 7 kind 渲染分支 ×（默认 / 已修改 / secret 三态）的呈现断言（章存在与否、mono class、掩码、清除 emit、update:modelValue emit）；
 * - mono 合同锁定：number / int / json / secret 恒 mono，text / select / bool 恒非 mono；
 * - 默认值徽注：与默认不同显「已修改」accent 点；相同或无 defaultValue 不显（未收录不编造默认）；
 * - 生效范围章：立即=success / 保存后=neutral / 重启=warn，未给 effective 不显章；
 * - secret：掩码「••••••」恒显不回显明文；已设置=accent 章+清除可点，未设置 / clearing=neutral 章+清除禁用；
 * - ConfigRevisionPanel dirty gating：dirty 未传入（MonitorView 挂点，禁改）整条带不渲染；
 *   dirty=true 高亮「编辑中」，dirty=false 按回执是否到位高亮「已保存待生效 v{n} / 运行生效 v{n}」。
 */
import { mount, type VueWrapper } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtConfigField, { type ConfigFieldLite } from '../NtConfigField.vue'
import NtBadge from '../NtBadge.vue'
import NtInput from '../NtInput.vue'
import NtSelect from '../NtSelect.vue'
import NtSwitch from '../NtSwitch.vue'
import ConfigRevisionPanel from '@/view/ConfigRevisionPanel.vue'
import type { ConfigSnapshot } from '@/api/config'

const lite = (field: Partial<ConfigFieldLite> & Pick<ConfigFieldLite, 'key' | 'kind'>): ConfigFieldLite => ({ label: field.key, ...field })
const badgeTexts = (w: VueWrapper): string[] => w.findAllComponents(NtBadge).map(b => b.text())

describe('控件分支（7 kind）', () => {
  it('text → NtInput 文本，恒非 mono', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.site.name', kind: 'text', hint: '一句人话说明' }), modelValue: 'NovaTrader' } })
    const input = w.findComponent(NtInput)
    expect(input.exists()).toBe(true)
    expect(input.props('type')).toBe('text')
    expect(input.props('mono')).toBe(false)
    expect(w.find('.nt-config-field__label').text()).toBe('app.site.name')
    expect(w.find('.nt-config-field__hint').text()).toBe('一句人话说明')
  })

  it('number → NtInput number，恒 mono；unit 徽注随行', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'm06.select_score', kind: 'number', unit: '分' }), modelValue: '75' } })
    const input = w.findComponent(NtInput)
    expect(input.props('type')).toBe('number')
    expect(input.props('mono')).toBe(true)
    expect(w.find('.nt-config-field__unit').text()).toBe('分')
  })

  it('int → NtInput number + mono（整数约束由上层 coerce 校验）', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.ds.tushare.qps', kind: 'int' }), modelValue: '2' } })
    const input = w.findComponent(NtInput)
    expect(input.props('type')).toBe('number')
    expect(input.props('mono')).toBe(true)
  })

  it('bool → NtSwitch 开关（兼容后端字串态），恒非 mono', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.ds.tushare.on', kind: 'bool' }), modelValue: 'true' } })
    expect(w.findComponent(NtSwitch).props('modelValue')).toBe(true)
    expect(w.findComponent(NtInput).exists()).toBe(false)
    expect(w.find('.nt-input__field--mono').exists()).toBe(false)
  })

  it('select → NtSelect 且 options 直通', () => {
    const options = [
      { value: 'small', label: '小模型' },
      { value: 'large', label: '大模型' },
    ]
    const w = mount(NtConfigField, { props: { field: lite({ key: 'm06.model_tier', kind: 'select', options }), modelValue: 'small' } })
    const select = w.findComponent(NtSelect)
    expect(select.exists()).toBe(true)
    expect(select.props('options')).toHaveLength(2)
    expect(w.findComponent(NtInput).exists()).toBe(false)
  })

  it('json → 多行 textarea，恒 mono（专属类）', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'market.sector.weights', kind: 'json' }), modelValue: '{"pct":1}' } })
    const ta = w.find('textarea.nt-config-field__json')
    expect(ta.exists()).toBe(true)
    expect((ta.element as HTMLTextAreaElement).value).toBe('{"pct":1}')
  })

  it('json 非字串值：JSON 化显式呈现，不拿空串冒充', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'market.sector.weights', kind: 'json' }), modelValue: { pct: 1 } } })
    expect((w.find('textarea').element as HTMLTextAreaElement).value).toContain('"pct": 1')
  })

  it('secret → 只读掩码「••••••」，不回显明文', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.ds.tushare.token', kind: 'secret' }), modelValue: 'should-never-show', secretState: 'set' } })
    expect(w.find('.nt-config-field__mask').text()).toBe('••••••')
    expect(w.text()).not.toContain('should-never-show')
  })
})

describe('默认值徽注（§9.1 第二件）', () => {
  it('与默认不同 → 「已修改」accent 点 + title 提示默认值原文', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'm06.select_score', kind: 'number' }), modelValue: '80', defaultValue: '75' } })
    const chip = w.findAllComponents(NtBadge).find(b => b.text() === '已修改')
    expect(chip).toBeDefined()
    expect(chip!.props('tone')).toBe('accent')
    expect(chip!.props('dot')).toBe(true)
    expect(chip!.attributes('title')).toContain('默认值：75')
  })

  it('与默认相同 → 不显徽注', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'm06.select_score', kind: 'number' }), modelValue: '75', defaultValue: '75' } })
    expect(badgeTexts(w)).not.toContain('已修改')
  })

  it('无 defaultValue → 不显徽注（未收录不编造默认）', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'm06.select_score', kind: 'number' }), modelValue: '80' } })
    expect(badgeTexts(w)).not.toContain('已修改')
  })
})

describe('生效范围章（§9.1 第三件）', () => {
  it.each([
    ['immediate', '立即生效', 'success'],
    ['on-save', '保存后生效', 'neutral'],
    ['on-restart', '重启后生效', 'warn'],
  ] as const)('effective=%s → 「%s」%s 章', (effective, text, tone) => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.site.name', kind: 'text' }), modelValue: '', effective } })
    const chip = w.findAllComponents(NtBadge).find(b => b.text() === text)
    expect(chip).toBeDefined()
    expect(chip!.props('tone')).toBe(tone)
  })

  it('未给 effective → 不显生效章', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.site.name', kind: 'text' }), modelValue: '' } })
    expect(w.findAllComponents(NtBadge)).toHaveLength(0)
  })
})

describe('secret 三态（已设置 / 未设置 / clearing）', () => {
  const field = lite({ key: 'app.ds.tushare.token', kind: 'secret' })

  it('set → 「已设置」accent 章，清除可点并 emit clear', async () => {
    const w = mount(NtConfigField, { props: { field, modelValue: '', secretState: 'set' } })
    expect(w.findAllComponents(NtBadge).find(b => b.text() === '已设置')?.props('tone')).toBe('accent')
    const btn = w.find('button.nt-config-field__clear')
    expect(btn.attributes('disabled')).toBeUndefined()
    await btn.trigger('click')
    expect(w.emitted('clear')).toHaveLength(1)
  })

  it('unset → 「未设置」neutral 章，清除禁用', () => {
    const w = mount(NtConfigField, { props: { field, modelValue: '', secretState: 'unset' } })
    expect(w.findAllComponents(NtBadge).find(b => b.text() === '未设置')?.props('tone')).toBe('neutral')
    expect(w.find('button.nt-config-field__clear').attributes('disabled')).toBeDefined()
  })

  it('clearing → 「未设置」neutral 章，清除禁用（不可重复触发）', () => {
    const w = mount(NtConfigField, { props: { field, modelValue: '', secretState: 'clearing' } })
    expect(w.findAllComponents(NtBadge).find(b => b.text() === '未设置')?.props('tone')).toBe('neutral')
    expect(w.find('button.nt-config-field__clear').attributes('disabled')).toBeDefined()
  })

  it('disabled=true 时即便已设置，清除也禁用', () => {
    const w = mount(NtConfigField, { props: { field, modelValue: '', secretState: 'set', disabled: true } })
    expect(w.find('button.nt-config-field__clear').attributes('disabled')).toBeDefined()
  })
})

describe('update:modelValue 透出', () => {
  it('text 原样回字串', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.site.name', kind: 'text' }), modelValue: '' } })
    w.findComponent(NtInput).vm.$emit('update:modelValue', '新名字')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['新名字'])
  })

  it('bool 回布尔', () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'app.ds.tushare.on', kind: 'bool' }), modelValue: false } })
    w.findComponent(NtSwitch).vm.$emit('update:modelValue', true)
    expect(w.emitted('update:modelValue')?.[0]).toEqual([true])
  })

  it('select 回选中值', () => {
    const w = mount(NtConfigField, {
      props: { field: lite({ key: 'm06.model_tier', kind: 'select', options: [{ value: 'small', label: '小模型' }] }), modelValue: '' },
    })
    w.findComponent(NtSelect).vm.$emit('update:modelValue', 'small')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['small'])
  })

  it('json textarea 回文本', async () => {
    const w = mount(NtConfigField, { props: { field: lite({ key: 'market.sector.weights', kind: 'json' }), modelValue: '' } })
    await w.find('textarea').setValue('{"pct":2}')
    expect(w.emitted('update:modelValue')?.[0]).toEqual(['{"pct":2}'])
  })
})

describe('ConfigRevisionPanel · 生效三态（§9.2，dirty gating）', () => {
  const snap = (over: Partial<ConfigSnapshot> = {}): ConfigSnapshot => ({
    revision: 7,
    values: {},
    secretSet: {},
    receipts: [],
    history: [],
    createdAt: '',
    note: '',
    receiptError: '',
    ...over,
  })

  it('dirty 未传入（MonitorView 挂点，禁改）→ 整条带不渲染', () => {
    const w = mount(ConfigRevisionPanel, { props: { snapshot: snap() } })
    expect(w.find('.triple').exists()).toBe(false)
  })

  it('dirty=true → 高亮「编辑中」，其余档淡化', () => {
    const w = mount(ConfigRevisionPanel, { props: { snapshot: snap(), dirty: true } })
    expect(w.findAll('.stage')).toHaveLength(3)
    expect(w.find('.stage--current').text()).toContain('编辑中')
    expect(w.find('.stage--pending.stage--current').exists()).toBe(false)
    expect(w.find('.stage--running.stage--current').exists()).toBe(false)
  })

  it('dirty=false 且目标服务无当前版回执 → 高亮「已保存待生效 v7」', () => {
    const w = mount(ConfigRevisionPanel, { props: { snapshot: snap({ targetServices: ['strategy'] }), dirty: false } })
    const current = w.find('.stage--current')
    expect(current.text()).toContain('已保存待生效')
    expect(current.text()).toContain('v7')
  })

  it('dirty=false 且回执全到位 → 高亮「运行生效 v7」', () => {
    const receipt = { service: 'strategy', instance: 'abcd1234', scope: 'live', revision: 7, status: 'applied', detail: '已回执', observedAt: '2026-10-21 09:30:00' }
    const w = mount(ConfigRevisionPanel, { props: { snapshot: snap({ targetServices: ['strategy'], receipts: [receipt] }), dirty: false } })
    const current = w.find('.stage--current')
    expect(current.text()).toContain('运行生效')
    expect(current.text()).toContain('v7')
  })

  it('snapshot 为 null → 只渲染态名不渲染版本号（版本取自 snapshot，取不到不编造）', () => {
    const w = mount(ConfigRevisionPanel, { props: { snapshot: null, dirty: true } })
    expect(w.findAll('.stage')).toHaveLength(3)
    expect(w.find('.triple').text()).not.toMatch(/v\d+/)
  })
})
