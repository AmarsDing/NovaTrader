/**
 * status-map（方案 §7.1 状态字典唯一来源）合同测试：
 * - 链路五态文案一字锁定（live 在线 / ok 正常 / stale 陈旧 / down 中断 / off 关闭）；
 * - 业务状态册首批 20 键全覆盖且文案/tone 逐条锁死（改文案 = 改本测试，防自封文案回流）；
 * - 全册 tone 落在 NtBadge 合法枚举内；册对象为冻结只读。
 */
import { describe, expect, it } from 'vitest'
import { LINK_STATUS, STATUS_MAP, linkStatus, statusOf, type StatusKey, type StatusTone } from '../status-map'

const LEGAL_TONES = new Set<StatusTone>(['neutral', 'accent', 'success', 'warn', 'danger', 'info'])

describe('链路五态（§7.1 文案锁定）', () => {
  it.each([
    ['live', '在线', 'success'],
    ['ok', '正常', 'success'],
    ['stale', '陈旧', 'warn'],
    ['down', '中断', 'danger'],
    ['off', '关闭', 'neutral'],
  ] as const)('%s → %s（%s）', (key, text, tone) => {
    expect(linkStatus(key).text).toBe(text)
    expect(linkStatus(key).tone).toBe(tone)
  })
})

describe('业务状态册（首批，一字不差）', () => {
  const EXPECTED: Record<string, { text: string; tone: StatusTone }> = {
    'alert.armed': { text: '布防中', tone: 'accent' },
    'alert.triggered': { text: '已触发', tone: 'warn' },
    'alert.released': { text: '已解除', tone: 'neutral' },
    'degraded.partial': { text: '部分降级', tone: 'warn' },
    'degraded.model': { text: '模型降级', tone: 'danger' },
    'degraded.data': { text: '数据缺失', tone: 'warn' },
    'degraded.retrieval': { text: '检索降级', tone: 'warn' },
    'auth.unauthorized': { text: '未授权', tone: 'danger' },
    'auth.sim': { text: '模拟SIM', tone: 'accent' },
    'auth.live': { text: '实盘LIVE·授权', tone: 'success' },
    'replay.active': { text: '回看', tone: 'accent' },
    'kb.pending': { text: '待补', tone: 'warn' },
    'kb.ingested': { text: '已入库', tone: 'accent' },
    'evolution.approved': { text: '已批准', tone: 'success' },
    'evolution.rejected': { text: '已拒绝', tone: 'danger' },
    'evolution.pending': { text: '待审', tone: 'warn' },
    'audit.success': { text: '成功', tone: 'success' },
    'audit.failure': { text: '失败', tone: 'danger' },
    'security.suspended': { text: '停牌', tone: 'neutral' },
    'security.st': { text: 'ST', tone: 'warn' },
  }

  it('册键存在性：与首批清单完全一致（不多不少）', () => {
    expect(Object.keys(STATUS_MAP).sort()).toEqual(Object.keys(EXPECTED).sort())
  })

  it.each(Object.entries(EXPECTED))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('全册 tone 合法（NtBadge 枚举内，含链路五态）', () => {
    for (const entry of Object.values(STATUS_MAP)) expect(LEGAL_TONES).toContain(entry.tone)
    for (const entry of Object.values(LINK_STATUS)) expect(LEGAL_TONES).toContain(entry.tone)
  })

  it('全册只读（新增状态必须改源文件入册，禁运行时塞键）', () => {
    expect(Object.isFrozen(STATUS_MAP)).toBe(true)
    expect(Object.isFrozen(LINK_STATUS)).toBe(true)
  })
})
