/**
 * 会话内采样环形缓冲：HUD sparkline / 卡片活动条（S2）/ 日内权益曲线的数据源。
 * 纯内存、不落库不上传，页面刷新即失（监控业务监控方案 §9）。
 * null 采样保留位置，曲线在缺口处断开，不用 0 冒充。
 */

export class RingSeries {
  private readonly values: (number | null)[] = []

  constructor(readonly size = 30) {}

  push(value: number | null): void {
    this.values.push(value)
    if (this.values.length > this.size) this.values.shift()
  }

  points(): (number | null)[] {
    return [...this.values]
  }
}

/**
 * 把采样点画成 SVG path（polyline）。viewBox 0 0 w×h，null 断段。
 * 恒定序列抬到中线避免贴边；单点只画一个点，不伪造连线。
 */
export function sparkPath(points: (number | null)[], w: number, h: number): string {
  const valid = points.filter((value): value is number => value != null)
  if (valid.length === 0) return ''
  const min = Math.min(...valid)
  const max = Math.max(...valid)
  const span = max - min
  const yOf = (value: number) =>
    span === 0 ? h / 2 : h - 2 - ((value - min) / span) * (h - 4)
  const step = points.length > 1 ? w / (points.length - 1) : 0

  const runs: string[] = []
  let current = ''
  points.forEach((value, index) => {
    if (value == null) {
      if (current) runs.push(current)
      current = ''
      return
    }
    const x = points.length > 1 ? index * step : w / 2
    const y = yOf(value)
    current += current === '' ? `M${round(x)} ${round(y)}` : ` L${round(x)} ${round(y)}`
  })
  if (current) runs.push(current)
  return runs.join('')
}

function round(value: number): number {
  return Math.round(value * 10) / 10
}
