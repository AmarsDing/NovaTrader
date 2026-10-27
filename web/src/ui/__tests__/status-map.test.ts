/**
 * status-map（方案 §7.1 状态字典唯一来源）合同测试：
 * - 链路五态文案一字锁定（live 在线 / ok 正常 / stale 陈旧 / down 中断 / off 关闭）；
 * - 业务状态册首批 20 键 + 第二批 36 键 + 第三批 board 域 3 键 + 第四批 ops 域 1 键 + 第五批 brain 域 1 键 + 第六批 ai/monitor 域 5 键 + 第七批 move 域 3 键 + 第八批 missing 域 5 键 + 第九批 verify 域 1 键 + 第十批 depth 域 1 键 + 第十一批 role 域 4 键 + 第十二批 ds 域 12 键 + 第十三批 notify 域 1 键 + 第十四批 notify.kind 域 18 键全覆盖且文案/tone 逐条锁死（改文案 = 改本测试，防自封文案回流）；
 * - 全册 tone 落在 NtBadge 合法枚举内；册对象为冻结只读。
 */
import { describe, expect, it } from 'vitest'
import { LINK_STATUS, STATUS_MAP, linkStatus, notifyKindEntry, statusOf, type StatusKey, type StatusTone } from '../status-map'

/* B2 词汇表治理：'ai' 为 AI 紫词（B2 入册）；info 词产物面已零调用，B1.5 随 golden 重刷删词时从本集合除名。 */
const LEGAL_TONES = new Set<StatusTone>(['neutral', 'accent', 'success', 'warn', 'danger', 'info', 'ai', 'up', 'down'])

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

  it('首批键全在册内（第二批扩册不吞旧键，逐条锁死）', () => {
    expect(Object.keys(EXPECTED).length).toBe(20)
    for (const key of Object.keys(EXPECTED)) expect(STATUS_MAP).toHaveProperty(key)
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

describe('业务状态册（第二批 36 键，一字不差）', () => {
  const EXPECTED_BATCH2: Record<string, { text: string; tone: StatusTone }> = {
    // risk 风控域
    'risk.killswitch': { text: '已触发', tone: 'danger' },
    'risk.breaker': { text: '行情熔断', tone: 'warn' },
    'risk.precheck.pass': { text: '预检通过', tone: 'success' },
    'risk.precheck.blocked': { text: '预检拦截', tone: 'danger' },
    'risk.active': { text: '生效中', tone: 'danger' },
    'risk.standby': { text: '未触发', tone: 'neutral' },
    'risk.unverified': { text: '状态待核对', tone: 'warn' },
    'risk.unverified_active': { text: '待核对·上次已触发', tone: 'warn' },
    // evolution 策略演化域
    'evolution.admitted': { text: '已达准入', tone: 'success' },
    // doc 文档生命周期域
    'doc.active': { text: '有效', tone: 'success' },
    'doc.superseded': { text: '被替换', tone: 'warn' },
    'doc.expired': { text: '已过期', tone: 'warn' },
    'doc.deleted': { text: '已删除', tone: 'danger' },
    // quality 数据质量域
    'quality.high': { text: '高', tone: 'warn' },
    'quality.medium': { text: '中', tone: 'neutral' },
    'quality.low': { text: '低', tone: 'neutral' },
    // quote 行情域（quote.suspended 文案同为「停牌」但为 danger 紧急档，security.suspended 为档案域 neutral 档）
    'quote.suspended': { text: '停牌', tone: 'danger' },
    'quote.nolimit': { text: '无涨跌幅', tone: 'accent' },
    'quote.stale': { text: '已过期', tone: 'warn' },
    // signal 交易信号域
    'signal.pending': { text: '待审核', tone: 'warn' },
    'signal.risk_checking': { text: '风控审核', tone: 'warn' },
    'signal.approved': { text: '已通过', tone: 'success' },
    'signal.rejected': { text: '已拦截', tone: 'danger' },
    'signal.executing': { text: '执行中', tone: 'accent' },
    'signal.done': { text: '已有成交', tone: 'success' },
    'signal.closed': { text: '已平仓', tone: 'neutral' },
    'signal.expired': { text: '已过期', tone: 'warn' },
    'signal.cancelled': { text: '已取消', tone: 'neutral' },
    // order 订单域
    'order.pending': { text: '待报', tone: 'neutral' },
    'order.submitted': { text: '已报', tone: 'accent' },
    'order.pending_confirm': { text: '待人工确认', tone: 'warn' },
    'order.partial': { text: '部成', tone: 'warn' },
    'order.filled': { text: '已成', tone: 'success' },
    'order.cancelled': { text: '已撤', tone: 'neutral' },
    'order.rejected': { text: '已拒绝', tone: 'danger' },
    'order.expired': { text: '已过期', tone: 'warn' },
  }

  it('第二批 36 键全在册内（不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH2).length).toBe(36)
    for (const key of Object.keys(EXPECTED_BATCH2)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH2))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('回归抽样：首批旧键未被扩册改动（抽 5 键）', () => {
    expect(statusOf('alert.armed')).toEqual({ text: '布防中', tone: 'accent' })
    expect(statusOf('degraded.model')).toEqual({ text: '模型降级', tone: 'danger' })
    expect(statusOf('auth.live')).toEqual({ text: '实盘LIVE·授权', tone: 'success' })
    expect(statusOf('evolution.pending')).toEqual({ text: '待审', tone: 'warn' })
    expect(statusOf('security.st')).toEqual({ text: 'ST', tone: 'warn' })
  })

  it('同文案双键分级：quote.suspended=行情域 danger，security.suspended=档案域 neutral', () => {
    expect(statusOf('quote.suspended').tone).toBe('danger')
    expect(statusOf('security.suspended').tone).toBe('neutral')
  })
})

describe('业务状态册（第三批 board 板块标识域 3 键，一字不差）', () => {
  const EXPECTED_BATCH3: Record<string, { text: string; tone: StatusTone }> = {
    'board.cyb': { text: '创业板', tone: 'accent' },
    'board.kcb': { text: '科创板', tone: 'accent' },
    'board.bj': { text: '北交所', tone: 'warn' },
  }

  it('第三批 3 键全在册内（不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH3).length).toBe(3)
    for (const key of Object.keys(EXPECTED_BATCH3)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH3))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('tone 即波动档语义：20% 档（创/科）accent、30% 档（北交所）warn；主板不入册不打标', () => {
    expect(statusOf('board.cyb').tone).toBe('accent')
    expect(statusOf('board.kcb').tone).toBe('accent')
    expect(statusOf('board.bj').tone).toBe('warn')
    expect(STATUS_MAP).not.toHaveProperty('board.main')
  })

  it('回归抽样：第二批旧键未被扩册改动（抽 3 键）', () => {
    expect(statusOf('risk.killswitch')).toEqual({ text: '已触发', tone: 'danger' })
    expect(statusOf('quote.stale')).toEqual({ text: '已过期', tone: 'warn' })
    expect(statusOf('order.filled')).toEqual({ text: '已成', tone: 'success' })
  })
})

describe('业务状态册（第四批 ops 运维域 1 键，一字不差）', () => {
  it('ops.unknown 入册且锁定：unknown=未观测/未配置/不采集类结构性缺省，中性不冒充「关闭」', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('ops.')).length).toBe(1)
    expect(statusOf('ops.unknown')).toEqual({ text: '未知', tone: 'neutral' })
  })

  it('回归抽样：第三批旧键未被扩册改动（抽 2 键）', () => {
    expect(statusOf('board.cyb')).toEqual({ text: '创业板', tone: 'accent' })
    expect(statusOf('board.bj')).toEqual({ text: '北交所', tone: 'warn' })
  })
})

describe('业务状态册（第五批 brain 晨报域 1 键，一字不差）', () => {
  it('brain.scrub.magnitude 入册且锁定：量级畸变按 omitted 计数，danger 档与同级词一致', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('brain.')).length).toBe(1)
    expect(statusOf('brain.scrub.magnitude')).toEqual({ text: '量级畸变', tone: 'danger' })
  })

  it('回归抽样：第四批旧键未被扩册改动（ops.unknown）', () => {
    expect(statusOf('ops.unknown')).toEqual({ text: '未知', tone: 'neutral' })
  })
})

describe('业务状态册（第六批 B2 词汇表治理：ai 域 2 键 + monitor 巡检域 3 键）', () => {
  const EXPECTED_BATCH6: Record<string, { text: string; tone: StatusTone }> = {
    'ai.segments': { text: 'AI 分段复盘', tone: 'ai' },
    'ai.weekly': { text: 'AI 周报', tone: 'ai' },
    'monitor.idle': { text: '空闲', tone: 'neutral' },
    'monitor.gap': { text: '未返回', tone: 'warn' },
    'monitor.attention': { text: '要注意', tone: 'warn' },
  }

  it('第六批 5 键全在册内（不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH6).length).toBe(5)
    for (const key of Object.keys(EXPECTED_BATCH6)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH6))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('AI 紫词成族：ai 域全键 tone=ai（--nt-ai，直觉2 第五个词）', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('ai.')).length).toBe(2)
    expect(statusOf('ai.segments').tone).toBe('ai')
    expect(statusOf('ai.weekly').tone).toBe('ai')
  })

  it('monitor 巡检域不重复立词：live/down 复用链路五态 linkStatus', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('monitor.')).length).toBe(3)
    expect(STATUS_MAP).not.toHaveProperty('monitor.live')
    expect(STATUS_MAP).not.toHaveProperty('monitor.down')
    expect(linkStatus('live').text).toBe('在线')
    expect(linkStatus('down').text).toBe('中断')
  })

  it('回归抽样：第五批旧键未被扩册改动（brain.scrub.magnitude）', () => {
    expect(statusOf('brain.scrub.magnitude')).toEqual({ text: '量级畸变', tone: 'danger' })
  })
})

describe('业务状态册（第七批 move 盘中异动域 3 键，一字不差）', () => {
  const EXPECTED_BATCH7: Record<string, { text: string; tone: StatusTone }> = {
    'move.surge': { text: '急拉', tone: 'warn' },
    'move.limit_up': { text: '封板', tone: 'up' },
    'move.break': { text: '开板', tone: 'neutral' },
  }

  it('第七批 3 键全在册内（不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH7).length).toBe(3)
    for (const key of Object.keys(EXPECTED_BATCH7)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH7))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('封板换轨涨跌词：limit_up=up 红（涨停方向语义），不再借 danger 危险词', () => {
    expect(statusOf('move.limit_up').tone).toBe('up')
    expect(statusOf('move.limit_up').tone).not.toBe('danger')
  })

  it('回归抽样：第六批旧键未被扩册改动（ai.weekly / monitor.gap）', () => {
    expect(statusOf('ai.weekly')).toEqual({ text: 'AI 周报', tone: 'ai' })
    expect(statusOf('monitor.gap')).toEqual({ text: '未返回', tone: 'warn' })
  })
})

describe('业务状态册（第八批 missing 缺位徽注域 5 键；W1b，R-UI-04/20/23）', () => {
  const EXPECTED_BATCH8: Record<string, { text: string; tone: StatusTone }> = {
    'missing.data': { text: '没取到', tone: 'neutral' },
    'missing.source': { text: '源未连接', tone: 'warn' },
    'missing.unverified': { text: '未验证', tone: 'neutral' },
    'missing.undeclared': { text: '未声明', tone: 'neutral' },
    'missing.insufficient': { text: '样本不足', tone: 'warn' },
  }

  it('第八批 5 键全在册内（不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH8).length).toBe(5)
    for (const key of Object.keys(EXPECTED_BATCH8)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH8))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('missing 全族只此 5 键，且与 monitor.gap 分工不替（巡检域词不进本族）', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('missing.')).length).toBe(5)
    expect(STATUS_MAP).not.toHaveProperty('missing.gap')
    expect(statusOf('monitor.gap').text).toBe('未返回')
  })

  it('tone 语义：缺位/零值是事实（neutral 不冒充负面；未验证绝不伪造绿），可操作缺陷才 warn', () => {
    expect(statusOf('missing.data').tone).toBe('neutral')
    expect(statusOf('missing.unverified').tone).toBe('neutral')
    expect(statusOf('missing.undeclared').tone).toBe('neutral')
    expect(statusOf('missing.source').tone).toBe('warn')
    expect(statusOf('missing.insufficient').tone).toBe('warn')
    for (const key of Object.keys(EXPECTED_BATCH8)) expect(statusOf(key as StatusKey).tone).not.toBe('success')
  })

  it('回归抽样：第七批旧键未被扩册改动（move.limit_up / move.break）', () => {
    expect(statusOf('move.limit_up')).toEqual({ text: '封板', tone: 'up' })
    expect(statusOf('move.break')).toEqual({ text: '开板', tone: 'neutral' })
  })
})

describe('业务状态册（第九批 verify 判据域 1 键；W8b，R-UI-04/20/23）', () => {
  it('verify.pass 入册且锁定：判据真通过才配绿（success 不与灰显词同族）', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('verify.')).length).toBe(1)
    expect(statusOf('verify.pass')).toEqual({ text: '已验证', tone: 'success' })
  })

  it('判据反面不另立词：无 verify.fail——走 missing.unverified 灰显，绝不伪造绿也不冒充 danger', () => {
    expect(STATUS_MAP).not.toHaveProperty('verify.fail')
    expect(statusOf('missing.unverified')).toEqual({ text: '未验证', tone: 'neutral' })
  })

  it('回归抽样：第八批旧键未被扩册改动（missing.data / missing.insufficient）', () => {
    expect(statusOf('missing.data')).toEqual({ text: '没取到', tone: 'neutral' })
    expect(statusOf('missing.insufficient')).toEqual({ text: '样本不足', tone: 'warn' })
  })
})

describe('业务状态册（第十批 depth 盘口域 1 键；B3 走查收编）', () => {
  it('depth.synth 入册且锁定：合成占位徽注 warn 档，不冒充真实亦不归 danger 故障', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('depth.')).length).toBe(1)
    expect(statusOf('depth.synth')).toEqual({ text: '合成·待数据', tone: 'warn' })
  })

  it('回归抽样：第九批旧键未被扩册改动（verify.pass / missing.unverified）', () => {
    expect(statusOf('verify.pass')).toEqual({ text: '已验证', tone: 'success' })
    expect(statusOf('missing.unverified')).toEqual({ text: '未验证', tone: 'neutral' })
  })
})

describe('业务状态册（第十一批 audit 角色域 4 键；W4b 审计三段式收编）', () => {
  const EXPECTED_BATCH11: Record<string, { text: string; tone: StatusTone }> = {
    'role.owner': { text: '属主', tone: 'accent' },
    'role.viewer': { text: '查看', tone: 'neutral' },
    'role.operator': { text: '操作员', tone: 'neutral' },
    'role.unknown': { text: '未知', tone: 'neutral' },
  }

  it('第十一批 4 键全在册内（不多不少）', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('role.')).length).toBe(4)
    for (const key of Object.keys(EXPECTED_BATCH11)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH11))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('角色是权限事实不是警报：owner accent 点出、viewer/operator/unknown 中性不冒绿不冒红', () => {
    expect(statusOf('role.owner').tone).toBe('accent')
    expect(statusOf('role.viewer').tone).toBe('neutral')
    expect(statusOf('role.operator').tone).toBe('neutral')
    expect(statusOf('role.unknown').tone).toBe('neutral')
    for (const key of Object.keys(EXPECTED_BATCH11)) {
      expect(statusOf(key as StatusKey).tone).not.toBe('success')
      expect(statusOf(key as StatusKey).tone).not.toBe('danger')
    }
  })

  it('回归抽样：第十批旧键未被扩册改动（depth.synth）', () => {
    expect(statusOf('depth.synth')).toEqual({ text: '合成·待数据', tone: 'warn' })
  })
})

describe('业务状态册（第十二批 datasource 源目录域 12 键；W4b 数据源中心裸码族入册）', () => {
  const EXPECTED_BATCH12: Record<string, { text: string; tone: StatusTone }> = {
    'ds.fee.free': { text: '免费', tone: 'neutral' },
    'ds.fee.freemium': { text: '免费+', tone: 'warn' },
    'ds.fee.paid': { text: '付费', tone: 'neutral' },
    'ds.req.token': { text: '需 token', tone: 'warn' },
    'ds.req.key': { text: '需密钥', tone: 'warn' },
    'ds.req.sidecar': { text: '需边车', tone: 'warn' },
    'ds.req.none': { text: '免凭证', tone: 'neutral' },
    'ds.src.official': { text: '官方', tone: 'accent' },
    'ds.src.unregistered': { text: '未启用', tone: 'neutral' },
    'ds.override.active': { text: '运行态覆盖生效', tone: 'accent' },
    'ds.override.default': { text: 'yaml 默认', tone: 'neutral' },
    'ds.undeclared': { text: '—', tone: 'neutral' },
  }

  it('第十二批 13 键全在册内（ds.fee 3 + ds.req 4 + ds.src 2 + ds.override 2 + ds.cred 2 + ds.undeclared 1）', () => {
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('ds.')).length).toBe(14)
    for (const key of Object.keys(EXPECTED_BATCH12)) expect(STATUS_MAP).toHaveProperty(key)
    expect(STATUS_MAP).toHaveProperty('ds.cred.set')
    expect(STATUS_MAP).toHaveProperty('ds.cred.unset')
  })

  it.each(Object.entries(EXPECTED_BATCH12))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('ds.cred 凭证写面两键锁定：已设置=success 正面回执，未设置=neutral 待配置不是故障', () => {
    expect(statusOf('ds.cred.set')).toEqual({ text: '已设置', tone: 'success' })
    expect(statusOf('ds.cred.unset')).toEqual({ text: '未设置', tone: 'neutral' })
  })

  it('费用档语义：免费/付费=中性事实不冒绿不冒红；免费+有付费边界=warn 提示位', () => {
    expect(statusOf('ds.fee.free').tone).toBe('neutral')
    expect(statusOf('ds.fee.paid').tone).toBe('neutral')
    expect(statusOf('ds.fee.freemium').tone).toBe('warn')
  })

  it('回归抽样：第十一批旧键未被扩册改动（role.owner / role.unknown）', () => {
    expect(statusOf('role.owner')).toEqual({ text: '属主', tone: 'accent' })
    expect(statusOf('role.unknown')).toEqual({ text: '未知', tone: 'neutral' })
  })
})

describe('业务状态册（第十三批 notify 通知域 1 键；W4b 静默注记入册）', () => {
  it('notify.silenced 入册且锁定：静默=漏斗判定不扰民只落账，neutral 事实不是警报', () => {
    /* W4c 起 notify.kind.* 同域共存——本批计数须排除第十四批类别键。 */
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('notify.') && !k.startsWith('notify.kind.')).length).toBe(1)
    expect(statusOf('notify.silenced')).toEqual({ text: '静默', tone: 'neutral' })
  })

  it('回归抽样：第十二批旧键未被扩册改动（ds.src.official / ds.cred.set）', () => {
    expect(statusOf('ds.src.official')).toEqual({ text: '官方', tone: 'accent' })
    expect(statusOf('ds.cred.set')).toEqual({ text: '已设置', tone: 'success' })
  })
})

describe('业务状态册（第十四批 notify.kind 通知类别域 18 键；W4c 通知二维分级）', () => {
  const EXPECTED_BATCH14: Record<string, { text: string; tone: StatusTone }> = {
    // REST 7 键（api/notify normalizeMessage 由 kind||type||category 推导）
    'notify.kind.signal': { text: '信号', tone: 'accent' },
    'notify.kind.risk': { text: '风控', tone: 'warn' },
    'notify.kind.fill': { text: '成交', tone: 'success' },
    'notify.kind.briefing': { text: '晨报', tone: 'ai' },
    'notify.kind.review': { text: '复盘', tone: 'ai' },
    'notify.kind.intel': { text: '情报', tone: 'neutral' },
    'notify.kind.market': { text: '行情', tone: 'neutral' },
    // WS 告警 11 键（alert-levels L4/L3 kind 白名单 + scan_zero_hit + onPushMessage 缺省 alert）
    'notify.kind.killswitch': { text: '急停', tone: 'danger' },
    'notify.kind.breaker': { text: '熔断', tone: 'warn' },
    'notify.kind.reject_rate': { text: '拒单飙升', tone: 'warn' },
    'notify.kind.order_unknown': { text: '订单未知', tone: 'warn' },
    'notify.kind.alert-rule': { text: '规则命中', tone: 'warn' },
    'notify.kind.surge': { text: '急拉', tone: 'warn' },
    'notify.kind.limit_up': { text: '封板', tone: 'up' },
    'notify.kind.break': { text: '开板', tone: 'neutral' },
    'notify.kind.drawdown': { text: '回撤', tone: 'warn' },
    'notify.kind.scan_zero_hit': { text: '零命中', tone: 'warn' },
    'notify.kind.alert': { text: '告警', tone: 'neutral' },
  }

  it('第十四批 18 键全在册内（notify.kind 前缀恰 18，不多不少）', () => {
    expect(Object.keys(EXPECTED_BATCH14).length).toBe(18)
    expect(Object.keys(STATUS_MAP).filter((k) => k.startsWith('notify.kind.')).length).toBe(18)
    for (const key of Object.keys(EXPECTED_BATCH14)) expect(STATUS_MAP).toHaveProperty(key)
  })

  it.each(Object.entries(EXPECTED_BATCH14))('%s 入册条目一字锁定', (key, entry) => {
    expect(statusOf(key as StatusKey)).toEqual(entry)
  })

  it('二维分工：类型章除 killswitch 外不碰 danger——等级由 NtTimeline LEVEL_BADGE 独担，不双重编码', () => {
    for (const [key, entry] of Object.entries(EXPECTED_BATCH14)) {
      if (key !== 'notify.kind.killswitch') expect(entry.tone).not.toBe('danger')
    }
  })

  it('族对齐：move 三态同文同调、fill=成交回执、briefing/review 走 AI 紫词', () => {
    expect(statusOf('notify.kind.surge')).toEqual(statusOf('move.surge'))
    expect(statusOf('notify.kind.limit_up')).toEqual(statusOf('move.limit_up'))
    expect(statusOf('notify.kind.break')).toEqual(statusOf('move.break'))
    expect(statusOf('notify.kind.fill').tone).toBe('success')
    expect(statusOf('notify.kind.briefing').tone).toBe('ai')
    expect(statusOf('notify.kind.review').tone).toBe('ai')
  })

  it('notifyKindEntry 出口：册内命中返回条目，册外/空值返回 null（调用处原样回显不自封）', () => {
    expect(notifyKindEntry('risk')).toEqual({ text: '风控', tone: 'warn' })
    expect(notifyKindEntry('alert-rule')).toEqual({ text: '规则命中', tone: 'warn' })
    expect(notifyKindEntry('scan_zero_hit')).toEqual({ text: '零命中', tone: 'warn' })
    expect(notifyKindEntry('not-a-kind')).toBeNull()
    expect(notifyKindEntry('')).toBeNull()
    expect(notifyKindEntry(null)).toBeNull()
    expect(notifyKindEntry(undefined)).toBeNull()
    /* 原型链词不得冒名命中（hasOwnProperty 守门，索引重构后仍安全） */
    expect(notifyKindEntry('toString')).toBeNull()
    expect(notifyKindEntry('constructor')).toBeNull()
  })

  it('回归抽样：第十三批旧键未被扩册改动（notify.silenced）', () => {
    expect(statusOf('notify.silenced')).toEqual({ text: '静默', tone: 'neutral' })
  })
})
