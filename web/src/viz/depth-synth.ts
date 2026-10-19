/**
 * 无档盘口合成兜底（P2-3）：行情边车还没供上五档时，
 * 用「最近价 ± tick 阶梯、封顶涨/跌停」生成占位档，由组件层标「合成·待数据」。
 * 只生成价格、不伪造挂单量（volume 恒 null，UI 显示 '—'）。
 */
import { limitRatio } from '@/utils/limit'

export interface SynthLevel {
  price: number
  volume: null
}

export interface SynthDepth {
  asks: SynthLevel[]
  bids: SynthLevel[]
}

const round2 = (value: number) => Math.round(value * 100) / 100

/**
 * @param last 最近价；缺失/非正 → null（不猜）
 * @param options.symbol 推断涨跌幅档（复用 limit.ts 板块规则）；preClose 缺失 → 不限涨跌停
 * @param options.tick 最小报价，默认 0.01（A 股股票；基金 0.001 等后续按代码引入）
 * @returns 两侧合计为空返回 null（连占位都画不出，让调用方走空态）
 */
export function buildSyntheticDepth(
  last: number | null | undefined,
  options?: { symbol?: string; preClose?: number | null; tick?: number },
): SynthDepth | null {
  if (last == null || !Number.isFinite(last) || last <= 0) return null
  const tick = options?.tick ?? 0.01
  if (!Number.isFinite(tick) || tick <= 0) return null
  const eps = 1e-9
  let capUp: number | null = null
  let capDown: number | null = null
  const preClose = options?.preClose ?? null
  if (preClose != null && Number.isFinite(preClose) && preClose > 0) {
    const ratio = limitRatio(options?.symbol ?? '')
    capUp = round2(preClose * (1 + ratio))
    capDown = round2(preClose * (1 - ratio))
  }
  const ladder = (dir: 1 | -1, cap: number | null): SynthLevel[] => {
    const out: SynthLevel[] = []
    for (let i = 1; i <= 5; i += 1) {
      const price = round2(last + dir * i * tick)
      if (price <= 0) break
      // 封板价本身可以挂（涨停价排队），越过封板价即停。
      if (cap != null && (dir === 1 ? price > cap + eps : price < cap - eps)) break
      out.push({ price, volume: null })
    }
    return out
  }
  const asks = ladder(1, capUp)
  const bids = ladder(-1, capDown)
  if (!asks.length && !bids.length) return null
  return { asks, bids }
}
