import { describe, expect, it } from 'vitest'
import { parseEnvironment } from '../environment'
const sample = () => ({
  schemaVersion: 1,
  scope: 'configured_service_processes',
  services: [
    { service: 'risk', state: 'unsupported', observation: { operations: [{ succeeded: 0 }] } },
    {
      service: 'market',
      state: 'observed',
      observation: {
        schemaVersion: 1,
        scope: 'process_consumer_operations',
        service: 'market',
        operations: [{ key: 'refresh', window: { received: '18446744073709551615', succeeded: '0' } }],
      },
    },
  ],
})
describe('环境观测合同', () => {
  it('不支持≠健康零，64位计数保留精确字符串，缺失分位数为未知', () => {
    const r = parseEnvironment(sample())
    expect(r.services[0]?.observation).toBeNull()
    expect(r.services[1]?.observation?.operations[0]).toMatchObject({
      received: '18446744073709551615',
      succeeded: '0',
      p95UpperMs: null,
      attempted: null,
      skipped: null,
    })
  })
  it('规则降级与空轮次的跳过计数可见且不加到成功数', () => {
    const v = sample()
    const op = v.services[1]!.observation!.operations[0]!
    Object.assign(op, { window: { received: '0', succeeded: '0', skipped: '18446744073709551615' } })
    const result = parseEnvironment(v).services[1]?.observation?.operations[0]
    expect(result?.skipped).toBe('18446744073709551615')
    expect(result?.succeeded).toBe('0')
  })
  it('拒绝错服务、重复身份、未知合同版本', () => {
    const wrong = sample()
    wrong.services[1]!.observation!.service = 'intel'
    expect(() => parseEnvironment(wrong)).toThrow()
    const duplicate = sample()
    duplicate.services.push(duplicate.services[0]!)
    expect(() => parseEnvironment(duplicate)).toThrow()
    expect(() => parseEnvironment({ ...sample(), schemaVersion: 2 })).toThrow()
  })
})
