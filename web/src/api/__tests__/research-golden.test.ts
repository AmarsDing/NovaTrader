import { expect, it } from 'vitest'
import { parseAllocation } from '../allocation'
import { parseNavExport } from '../nav'
import json from './fixtures/portfolio-preview.v1.json?raw'
import navJson from './fixtures/nav-export.v1.json?raw'
it('真实Go HTTP生产函数返回的合成组合合同能由前端精确解析', () => {
  // Captured from an isolated PostgreSQL+HTTP test, not real market evidence.
  const id = (JSON.parse(json) as { research: { run_id: string } }).research.run_id
  const r = parseAllocation({ preview_json: json }, id)
  expect(r.research.fixture_type).toBe('synthetic_not_market_evidence')
  expect(r.can_execute).toBe(false)
  expect(r.rows.length).toBeGreaterThan(0)
})
it('真实Go HTTP净值合同保留传给QuantStats的完整JSON字符串', () => {
  const raw = JSON.parse(navJson) as { inputJson: string; _fixture_scope: string }
  expect(raw._fixture_scope).toBe('synthetic_test')
  const r = parseNavExport(raw)
  expect(r.inputReady).toBe(true)
  expect(r.inputJson).toBe(raw.inputJson)
  expect(r.report.points[0]?.role).toBe('initial')
  expect(r.report.points.every((p) => p.valuation_complete && p.cash_flow_known)).toBe(true)
})
