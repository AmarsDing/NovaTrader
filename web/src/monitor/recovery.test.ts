import { expect, it } from 'vitest'
import { emptyMonitorInput } from './load-input'
import { assertMonitorRecovery } from './recovery'

it('账户、持仓已返回但订单、成交或风险失败时，不确认position游标', () => {
  const input = emptyMonitorInput()
  input.account = { ok: true, book: 'SIM', equity: 100, cash: 100, dayPnl: 0, positions: [], positionsOk: true }
  input.risk = { ok: true, mode: 'L1', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' }
  expect(() => assertMonitorRecovery(input, ['position'], true, true)).toThrow('尚未恢复')
  input.orders = { ok: true, items: [] }
  input.fills = { ok: true, items: [] }
  expect(() => assertMonitorRecovery(input, ['position'], true, true)).not.toThrow()
  input.account.positionsOk = false
  expect(() => assertMonitorRecovery(input, ['position'], true, true)).toThrow('尚未恢复')
})

it('通知缺失不确认告警，取消或上下文失效不确认，即使无关域成功', () => {
  const input = emptyMonitorInput()
  input.risk = { ok: true, mode: 'L1', killActive: false, killReason: '', buysToday: 0, marketBreaker: '' }
  expect(() => assertMonitorRecovery(input, ['alert'], true, true)).toThrow('尚未恢复')
  input.notices = { ok: true, items: [] }
  expect(() => assertMonitorRecovery(input, ['alert'], true, true)).not.toThrow()
  expect(() => assertMonitorRecovery(input, ['alert'], false, true)).toThrow('尚未恢复')
  expect(() => assertMonitorRecovery(input, ['alert'], true, false)).toThrow('已取消')
  const abort = new AbortController()
  abort.abort()
  expect(() => assertMonitorRecovery(input, ['alert'], true, true, abort.signal)).toThrow('已取消')
})
