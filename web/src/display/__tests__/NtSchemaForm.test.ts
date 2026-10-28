/**
 * NtSchemaForm（W7b；R-UI-24/38）合同测试：
 * - schemaError 非空 → 整组缺位横幅，零字段渲出；
 * - grid/cards 按 layout 分流；section 标题只在切段时出现；
 * - 敏感字段走 NtSecretText（已设置）+ 密码输入，清除钮仅 set 可点；
 * - 普通字段走 NtConfigField，update:value / clear 事件上抛。
 */
import { mount } from '@vue/test-utils'
import { describe, expect, it } from 'vitest'
import NtSchemaForm, { type SchemaFormRow } from '../NtSchemaForm.vue'
import NtConfigField from '@/ui/NtConfigField.vue'
import NtSecretText from '@/ui/NtSecretText.vue'
import NtInput from '@/ui/NtInput.vue'
import NtButton from '@/ui/NtButton.vue'

function row(partial: Partial<SchemaFormRow> & Pick<SchemaFormRow, 'id' | 'field'>): SchemaFormRow {
  return { value: '', ...partial }
}

describe('NtSchemaForm R-UI-38', () => {
  it('schemaError 非空时整组缺位，不渲字段', () => {
    const wrapper = mount(NtSchemaForm, {
      props: {
        schemaError: '版本不受支持',
        rows: [row({ id: 'm06.select_score', field: { key: 'm06.select_score', label: '入选分', kind: 'number' }, value: '75' })],
      },
    })
    expect(wrapper.text()).toContain('配置结构已变更或不可达')
    expect(wrapper.text()).toContain('版本不受支持')
    expect(wrapper.findAllComponents(NtConfigField)).toHaveLength(0)
    expect(wrapper.findAll('[data-field]')).toHaveLength(0)
  })
})

describe('NtSchemaForm 普通字段', () => {
  it('grid 布局渲 NtConfigField，切段才出 section 标题', async () => {
    const wrapper = mount(NtSchemaForm, {
      props: {
        layout: 'grid',
        rows: [
          row({ id: 'a', field: { key: 'a', label: '规则权重', kind: 'number', section: '融合' }, value: '0.4' }),
          row({ id: 'b', field: { key: 'b', label: '入选分', kind: 'number', section: '融合' }, value: '75' }),
          row({ id: 'c', field: { key: 'c', label: '冷却', kind: 'int', section: '信号' }, value: '5' }),
        ],
      },
    })
    expect(wrapper.findAllComponents(NtConfigField)).toHaveLength(3)
    const secs = wrapper.findAll('.nt-schema-form__sec').map((el) => el.text())
    expect(secs).toEqual(['融合', '信号'])
    await wrapper.findAllComponents(NtConfigField)[1]!.vm.$emit('update:modelValue', '80')
    expect(wrapper.emitted('update:value')?.[0]).toEqual(['b', '80', expect.objectContaining({ key: 'b' })])
  })

  it('cards 布局按 section 分卡', () => {
    const wrapper = mount(NtSchemaForm, {
      props: {
        layout: 'cards',
        rows: [
          row({ id: 't.on', field: { key: 't.on', label: '启用', kind: 'bool', section: 'Tushare' }, value: 'true' }),
          row({ id: 't.qps', field: { key: 't.qps', label: '每秒', kind: 'int', section: 'Tushare' }, value: '2' }),
          row({ id: 'a.on', field: { key: 'a.on', label: '启用', kind: 'bool', section: 'AkShare' }, value: 'true' }),
        ],
      },
    })
    const titles = wrapper.findAll('.nt-schema-form__card-head h3').map((el) => el.text())
    expect(titles).toEqual(['Tushare', 'AkShare'])
  })
})

describe('NtSchemaForm 敏感字段 R-UI-24', () => {
  it('已设置态走 NtSecretText，清除可点；未设置无 SecretText', async () => {
    const setRow = row({
      id: 'app.ds.tushare.token',
      field: { key: 'app.ds.tushare.token', label: '令牌', kind: 'secret', hint: '优先于环境变量' },
      value: '',
      secretState: 'set',
    })
    const wrapper = mount(NtSchemaForm, { props: { rows: [setRow] } })
    expect(wrapper.findComponent(NtSecretText).exists()).toBe(true)
    expect(wrapper.findComponent(NtSecretText).props('text')).toBe('已保存的密钥')
    expect(wrapper.findAllComponents(NtConfigField)).toHaveLength(0)
    expect(wrapper.findComponent(NtInput).props('type')).toBe('password')
    const clear = wrapper.findAllComponents(NtButton).find((b) => b.text() === '清除')!
    expect(clear.props('disabled')).toBe(false)
    await clear.trigger('click')
    expect(wrapper.emitted('clear')?.[0]).toEqual(['app.ds.tushare.token'])
  })

  it('密文行带生效范围章', () => {
    const wrapper = mount(NtSchemaForm, {
      props: {
        rows: [
          row({
            id: 'app.ds.tushare.token',
            field: { key: 'app.ds.tushare.token', label: '令牌', kind: 'secret' },
            value: '',
            secretState: 'set',
            effective: 'on-restart',
          }),
        ],
      },
    })
    expect(wrapper.text()).toContain('重启后生效')
  })

  it('未设置态不渲 NtSecretText，清除钮禁用', () => {
    const wrapper = mount(NtSchemaForm, {
      props: {
        rows: [
          row({
            id: 'app.ds.tavily.token',
            field: { key: 'app.ds.tavily.token', label: '密钥', kind: 'secret' },
            value: '',
            secretState: 'unset',
          }),
        ],
      },
    })
    expect(wrapper.findComponent(NtSecretText).exists()).toBe(false)
    expect(wrapper.text()).toContain('未设置')
    const clear = wrapper.findAllComponents(NtButton).find((b) => b.text() === '清除')!
    expect(clear.props('disabled')).toBe(true)
  })
})
