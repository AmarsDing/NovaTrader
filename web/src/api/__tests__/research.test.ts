import { expect, it } from 'vitest'
import { parseResearchJob } from '../research'
it('preserves a research result without converting missing metrics into zeros', () => {
  const job = parseResearchJob({
    run_id: 'r1',
    kind: 'performance',
    status: 'succeeded',
    revision: '9007199254740993',
    result_json: '{"result":{"metrics":{"sharpe":null,"max_drawdown":0}}}',
  })
  expect(job.revision).toBe('9007199254740993')
  expect(job.result).toEqual({ result: { metrics: { sharpe: null, max_drawdown: 0 } } })
})
it('rejects malformed results and missing identity', () => {
  expect(() => parseResearchJob({ status: 'succeeded' })).toThrow()
  expect(() => parseResearchJob({ run_id: 'r1', status: 'succeeded', result_json: '[]' })).toThrow()
})
