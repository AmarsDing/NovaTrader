import { expect, it } from 'vitest'
import { parseNavExport } from '../nav'
import { navFixture } from './research-fixtures'
it('净值缺失和未证实保持null，不生成可用于研究的输入', () => {
  const f = navFixture()
  const r = parseNavExport({ report_json: JSON.stringify(f.report), input_ready: false })
  expect(r.report.points[0]?.equity).toBeNull()
  expect(r.inputJson).toBe('')
  expect(() =>
    parseNavExport({
      report_json: JSON.stringify(f.report),
      input_ready: true,
      input_json: '{}',
      input_sha256: 'a'.repeat(64),
    }),
  ).toThrow()
})
it('拒绝错账本和未完整估值，精确输入字符串不重序列化', () => {
  const f = navFixture()
  expect(() =>
    parseNavExport({ reportJson: JSON.stringify({ ...f.report, account_type: 'LIVE' }), inputReady: false }),
  ).toThrow()
  f.report.missing_dates = []
  f.report.reasons = []
  f.report.points = [
    {
      ...f.report.points[0]!,
      generation: '9007199254740993',
      equity: 1000,
      valuation_complete: true,
      cash_flow_known: true,
      external_flow: 0,
      reasons: [],
    },
  ]
  const input = '{ "kind": "account_nav", "generation": 9007199254740993 }'
  const r = parseNavExport({
    reportJson: JSON.stringify(f.report),
    inputReady: true,
    inputJson: input,
    inputSha256: 'b'.repeat(64),
  })
  expect(r.inputJson).toBe(input)
  expect(r.report.points[0]?.generation).toBe('9007199254740993')
})
