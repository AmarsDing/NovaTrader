import { describe, expect, it } from 'vitest'
import { blockingGaps, gateItems, LIVE_GATE_ADVISORY, PLAN_AUTH_GATES } from '../preflight'

describe('W3c 启用模态前置检查构造器', () => {
  it('指定闸缺报也成一行未证实（没取到≠0，不静默当就位），且 blocking 徽随族位走', () => {
    const items = gateItems([{ gate: 'risk_engine', state: 'ok' }], PLAN_AUTH_GATES)
    expect(items).toHaveLength(5)
    const hit = items.find((i) => i.key === 'risk_engine')
    expect(hit?.state).toBe('ok')
    expect(hit?.blocking).toBe(true)
    const missing = items.find((i) => i.key === 'risk_config')
    expect(missing?.state).toBe('unknown')
    expect(missing?.blocking).toBe(true)
    expect(missing?.why).toContain('未证实')
    const advisory = items.find((i) => i.key === 'llm_config')
    expect(advisory?.blocking).toBe(false)
  })

  it('必堵口径：blocking 项非 ok 全锁；advisory 项再差也不锁', () => {
    const gaps = blockingGaps(
      gateItems(
        [
          { gate: 'risk_engine', state: 'ok' },
          { gate: 'risk_config', state: 'ok' },
          { gate: 'risk_killswitch', state: 'warn', why: '急停已触发' },
          { gate: 'datahub_sources', state: 'down' },
          { gate: 'llm_config', state: 'down' },
        ],
        PLAN_AUTH_GATES,
      ),
    )
    expect(gaps.map((i) => i.key)).toEqual(['risk_killswitch'])
  })

  it('实盘授权谱必堵为空——授权只校验准入证据，执行链三闸全提示不锁提交（通道行走 admission 信封，不入谱）', () => {
    const items = gateItems(
      [
        { gate: 'risk_engine', state: 'down' },
        { gate: 'broker_gw', state: 'down' },
      ],
      LIVE_GATE_ADVISORY,
    )
    expect(items).toHaveLength(3)
    expect(items.find((i) => i.key === 'broker_gw')).toBeUndefined()
    expect(items.every((i) => i.blocking === false)).toBe(true)
    expect(blockingGaps(items)).toHaveLength(0)
  })
})
