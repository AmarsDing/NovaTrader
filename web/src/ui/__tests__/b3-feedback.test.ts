/**
 * B3 反馈五连件合约测试（R-UI-24/35 配套）。
 * 纪律锁点：
 *  - NtEmpty：三分态文案渲染路径、loading 不冒充真空、missing 档给重试引导；
 *  - NtErrorGuide：error 为空零 DOM、messageOf 归一、重试 emit；
 *  - NtMissing：占位词 + reason 透传（没取到≠0）；
 *  - NtSkeleton：行数夹紧、kind 分支、aria-busy；
 *  - NtConfirm：open 透传、confirm/cancel emit、loading 锁取消钮、danger 换主钮色；
 *  - NtButton disabledReason：禁用+原因才包 span.nt-btn-why，否则 DOM 结构零变化。
 */
import { mount } from '@vue/test-utils'
import { afterEach, describe, expect, it } from 'vitest'
import NtButton from '../NtButton.vue'
import NtConfirm from '../NtConfirm.vue'
import NtEmpty from '../NtEmpty.vue'
import NtErrorGuide from '../NtErrorGuide.vue'
import NtMissing from '../NtMissing.vue'
import NtSkeleton from '../NtSkeleton.vue'

describe('B3 NtEmpty 三分态', () => {
  it('empty 档：主题+引导句都渲染，不给 action 不出按钮', () => {
    const w = mount(NtEmpty, { props: { state: 'empty', title: '还没有候选股', hint: '经策略扫描出入池后在这里展开' } })
    expect(w.get('.nt-empty-box__title').text()).toBe('还没有候选股')
    expect(w.get('.nt-empty-box__hint').text()).toBe('经策略扫描出入池后在这里展开')
    expect(w.findComponent(NtButton).exists()).toBe(false)
    expect(w.find('.nt-empty-box__spin').exists()).toBe(false)
  })

  it('loading 档：渲染 spinning 占位，标题不被替换为「无数据」', () => {
    const w = mount(NtEmpty, { props: { state: 'loading', title: '读取中…' } })
    expect(w.find('.nt-empty-box__spin').exists()).toBe(true)
    expect(w.get('.nt-empty-box__title').text()).toBe('读取中…')
  })

  it('missing 档：warn 章样式 + action 按钮点击 emit', async () => {
    const w = mount(NtEmpty, { props: { state: 'missing', title: '消息未返回', action: '重试' } })
    expect(w.find('.nt-empty-box--missing').exists()).toBe(true)
    await w.findComponent(NtButton).trigger('click')
    expect(w.emitted('action')).toHaveLength(1)
  })
})

describe('B3 NtErrorGuide 错误导引', () => {
  it('error 为空不落任何 DOM（调用面免 v-if）', () => {
    const w = mount(NtErrorGuide, { props: { error: null } })
    expect(w.find('.nt-err-guide').exists()).toBe(false)
  })

  it('Error 实例经 messageOf 归一 + context 前缀', () => {
    const w = mount(NtErrorGuide, { props: { error: new Error('连接超时'), context: '数据源中心读面' } })
    const text = w.get('.nt-err-guide__text').text()
    expect(text).toContain('数据源中心读面：')
    expect(text).toContain('连接超时')
  })

  it('retry 给了才渲染重试钮，点击 emit retry', async () => {
    const w = mount(NtErrorGuide, { props: { error: '未返回', retry: '刷新重试' } })
    await w.findComponent(NtButton).trigger('click')
    expect(w.emitted('retry')).toHaveLength(1)
  })
})

describe('B3 NtMissing 缺席占位', () => {
  it('默认占位词「没取到」，reason 拼接并挂 title', () => {
    const w = mount(NtMissing, { props: { reason: '源未连接' } })
    expect(w.get('.nt-missing-inline').text()).toBe('没取到·源未连接')
    expect(w.get('.nt-missing-inline').attributes('title')).toBe('源未连接')
  })

  it('无 reason 不挂 title，不画 0 不画假数', () => {
    const w = mount(NtMissing)
    expect(w.get('.nt-missing-inline').text()).toBe('没取到')
    expect(w.get('.nt-missing-inline').attributes('title')).toBeUndefined()
  })
})

describe('B3 NtSkeleton 骨架', () => {
  it('rows 文案行数精确，aria-busy 挂根', () => {
    const w = mount(NtSkeleton, { props: { rows: 4 } })
    expect(w.findAll('.nt-skel__row')).toHaveLength(4)
    expect(w.get('.nt-skel').attributes('aria-busy')).toBe('true')
  })

  it('行数下夹 1 上夹 12', () => {
    expect(mount(NtSkeleton, { props: { rows: 0 } }).findAll('.nt-skel__row')).toHaveLength(1)
    expect(mount(NtSkeleton, { props: { rows: 99 } }).findAll('.nt-skel__row')).toHaveLength(12)
  })

  it('kind=table 首列窄块；kind=card 渲三行卡片', () => {
    expect(mount(NtSkeleton, { props: { kind: 'table', rows: 2 } }).find('.nt-skel__row--lead').exists()).toBe(true)
    const card = mount(NtSkeleton, { props: { kind: 'card' } })
    expect(card.find('.nt-skel--card').exists()).toBe(true)
    expect(card.findAll('.nt-skel__row')).toHaveLength(3)
  })
})

describe('B3 NtConfirm 确认壳', () => {
  /** NtModal 走 Teleport 出站 body：挂 attachTo 后用 document 查询，each 用例末清场防串件。 */
  afterEach(() => {
    document.body.innerHTML = ''
  })

  function mountConfirm(props: InstanceType<typeof NtConfirm>['$props']) {
    return mount(NtConfirm, { props, attachTo: document.body })
  }

  it('open=true 渲 NtModal，body 文案进默认 slot', () => {
    mountConfirm({ open: true, title: '删除这条规则？', body: '删除后不可恢复。' })
    expect(document.querySelector('.nt-confirm__body')?.textContent).toBe('删除后不可恢复。')
  })

  it('确认/取消分路 emit：confirm 不关窗（由调用面决定），cancel 关窗', async () => {
    const w = mountConfirm({ open: true, title: '重置？', confirmLabel: '重置', cancelLabel: '再想想' })
    const buttons = [...document.querySelectorAll<HTMLButtonElement>('.nt-btn')]
    const cancelBtn = buttons.find((b) => b.textContent?.includes('再想想'))!
    const confirmBtn = buttons.find((b) => b.textContent?.includes('重置'))!
    confirmBtn.click()
    await w.vm.$nextTick()
    expect(w.emitted('confirm')).toHaveLength(1)
    expect(w.emitted('update:open')).toBeUndefined()
    cancelBtn.click()
    await w.vm.$nextTick()
    expect(w.emitted('cancel')).toHaveLength(1)
    expect(w.emitted('update:open')).toEqual([[false]])
  })

  it('loading 锁取消钮、danger 档主钮换色', () => {
    mountConfirm({ open: true, title: '删除？', tone: 'danger', confirmLabel: '删除', loading: true })
    expect(document.querySelector('.nt-btn--danger')).not.toBeNull()
    const cancelBtn = [...document.querySelectorAll<HTMLButtonElement>('.nt-btn')].find((b) =>
      b.textContent?.includes('取消'),
    )!
    expect(cancelBtn.disabled).toBe(true)
  })
})

describe('B3 NtButton disabledReason 包裹协定', () => {
  it('禁用+原因：包 span.nt-btn-why 挂 title，按钮保持 disabled', () => {
    const w = mount(NtButton, { props: { disabled: true, disabledReason: '先选标的' }, slots: { default: '提交委托' } })
    const why = w.get('.nt-btn-why')
    expect(why.attributes('title')).toBe('先选标的')
    expect(why.get('button').attributes('disabled')).toBeDefined()
  })

  it('未禁用/无原因：不包 span，DOM 结构与既有调用一致', () => {
    const plain = mount(NtButton, { slots: { default: '刷新' } })
    expect(plain.find('.nt-btn-why').exists()).toBe(false)
    const disabledNoReason = mount(NtButton, { props: { disabled: true }, slots: { default: '提交' } })
    expect(disabledNoReason.find('.nt-btn-why').exists()).toBe(false)
    expect(disabledNoReason.get('button').attributes('disabled')).toBeDefined()
  })

  it('lg 档类名落位', () => {
    const w = mount(NtButton, { props: { size: 'lg' }, slots: { default: '提交委托' } })
    expect(w.get('button').classes()).toContain('nt-btn--lg')
  })
})
