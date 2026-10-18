import { expect, it } from 'vitest'
import { parseAllocation } from '../allocation'
import { allocationFixture } from './research-fixtures'
it('保留报价与目标股数未知、精确代际；影子预览不授予权限', () => {
  const r = parseAllocation({ previewJson: JSON.stringify(allocationFixture()) }, 'run-1')
  expect(r.account_generation).toBe('9007199254740993')
  expect(r.rows[0]?.target_quantity).toBeNull()
  expect(r.can_execute).toBe(false)
})
it('拒绝错研究任务、假风控许可和矛盾的可用状态', () => {
  const f = allocationFixture()
  expect(() => parseAllocation({ preview_json: JSON.stringify(f) }, 'another-run')).toThrow()
  for (const patch of [
    { can_execute: true },
    { risk_checked: true },
    { state: 'complete', allocation_ready: true },
    { account_generation: 9007199254740992 },
  ]) {
    expect(() => parseAllocation({ preview_json: JSON.stringify({ ...f, ...patch }) }, 'run-1')).toThrow()
  }
})
