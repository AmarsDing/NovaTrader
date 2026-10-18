import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import { flushPromises, shallowMount, type VueWrapper } from '@vue/test-utils'
import ConfigCenter from '../ConfigCenter.vue'
import ConfigRevisionPanel from '../ConfigRevisionPanel.vue'
import NtInput from '@/ui/NtInput.vue'
import NtButton from '@/ui/NtButton.vue'
const m = vi.hoisted(() => ({ get: vi.fn(), save: vi.fn(), preview: vi.fn(), rollback: vi.fn() }))
vi.mock('@/api/config', () => ({ getConfigSnapshot: m.get, saveConfig: m.save, previewConfig: m.preview, rollbackConfig: m.rollback }))
vi.mock('@/api/ops', () => ({ opsStatus: async () => ({ sources: [] }) }))
vi.mock('@/stores/auth', () => ({ useAuthStore: () => ({ isViewer: () => false, operatorName: 'owner' }) }))
vi.mock('@/stores/risk', () => ({ useRiskStore: () => ({ params: {}, refresh: async () => true }) }))
vi.mock('@/stores/settings', () => ({ useSettingsStore: () => ({ theme: 'dark', density: 'normal', visualQuality: 'full' }) }))
vi.mock('@/utils/tauri', () => ({ inTauri: () => false }))
vi.mock('@/ws/hub', () => ({ restartLink: vi.fn() }))
let w: VueWrapper | undefined
const snapshot = (revision = 7) => ({ revision, values: { 'm06.select_score': '75' }, secretSet: {}, receipts: [], history: [], createdAt: '', note: '', receiptError: '' })
const preview = () => ({ baseRevision: 7, changes: [{ key: 'm06.select_score', before: '75', after: '80', secret: false, service: 'strategy', effective: '下次扫描' }], knownEffects: [], unknownEffects: ['收益未测'], services: ['strategy'], invalidatesPlans: true })
beforeEach(() => { vi.resetAllMocks(); m.get.mockResolvedValue(snapshot()); m.preview.mockResolvedValue(preview()); m.save.mockResolvedValue(snapshot(8)) })
afterEach(() => { w?.unmount(); w = undefined })
async function open() {
  w = shallowMount(ConfigCenter, { global: { renderStubDefaultSlot: true } }); await flushPromises()
  await w.findAll('nav button').find(b => b.text() === '评分与信号')!.trigger('click')
}
async function edit(value: string) { w!.findAllComponents(NtInput).find(c => c.props('label') === '入选分')!.vm.$emit('update:modelValue', value); await flushPromises() }
async function action() { w!.findAllComponents(NtButton).find(c => /预览本类|确认保存/.test(c.text()))!.vm.$emit('click'); await flushPromises() }
describe('configuration save workflow', () => {
  it('previews frozen edits then saves with the exact read revision; never labels save as loaded', async () => {
    await open(); await edit('80'); await action()
    expect(m.save).not.toHaveBeenCalled(); expect(m.preview).toHaveBeenCalledWith({ 'm06.select_score': '80' }, 7)
    await action(); expect(m.save).toHaveBeenCalledWith({ 'm06.select_score': '80' }, 7)
    expect(w!.text()).toContain('已保存版本 #8，请查看目标服务加载回执')
  })
  it('preserves a draft when a newer revision is observed; cannot silently relabel it', async () => {
    await open(); await edit('80'); m.get.mockResolvedValueOnce(snapshot(9))
    w!.findComponent(ConfigRevisionPanel).vm.$emit('refresh'); await flushPromises()
    expect(w!.text()).toContain('保存版本已改变'); await action()
    expect(m.preview).toHaveBeenCalledWith({ 'm06.select_score': '80' }, 7)
  })
  it('does not accept a late preview after the user edits the draft again', async () => {
    await open(); await edit('80')
    let resolve!: (value: ReturnType<typeof preview>) => void
    m.preview.mockReturnValueOnce(new Promise(r => { resolve = r }))
    await action(); await edit('81'); resolve(preview()); await flushPromises()
    expect(w!.findComponent(ConfigRevisionPanel).props('preview')).toBeNull()
    await action(); expect(m.save).not.toHaveBeenCalled(); expect(m.preview).toHaveBeenLastCalledWith({ 'm06.select_score': '81' }, 7)
  })
})
