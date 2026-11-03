/**
 * BE-05 板块归属反查（GetSectorMembership，只读）。
 * 独立文件而非并入 market.ts：该文件有他人未提交改动，避免提交纠缠。
 */
import { request } from '@/api/http'
import { asList, pick, type RawRecord } from '@/utils/format'

export interface SectorRefView {
  code: string
  name: string
}

export interface SectorMembershipItem {
  symbol: string
  /** 空数组 = 当日无有效归属（「未归属」），不是错误。 */
  sectors: SectorRefView[]
}

export interface SectorMembershipReply {
  /** 实际生效的归属窗口日（YYYY-MM-DD）。 */
  tradeDate: string
  /** 按请求 symbols 顺序回包（含零归属券）。 */
  items: SectorMembershipItem[]
}

/** 板块归属批量反查：GET /api/market/sectors/membership。
 * repeated query 手工拼（?symbols=A&symbols=B，同 fetchSecurityBasics 模式——request 的 query 不支持 repeated）；
 * symbols 为空直接回空结果不发请求；后端上限截断（超出部分不回包，属缺失非错误）。 */
export async function sectorMembership(symbols: string[]): Promise<SectorMembershipReply> {
  if (!symbols.length) return { tradeDate: '', items: [] }
  const suffix = symbols.map((s) => `symbols=${encodeURIComponent(s)}`).join('&')
  const data = await request(`/api/market/sectors/membership?${suffix}`)
  const raw = (data ?? {}) as RawRecord
  return {
    tradeDate: pick<string>(raw, 'tradeDate', 'trade_date') || '',
    items: asList<RawRecord>(raw, 'items').map((row) => ({
      symbol: pick<string>(row, 'symbol') || '',
      sectors: asList<RawRecord>(row, 'sectors')
        .map((sec) => ({
          code: pick<string>(sec, 'code') || '',
          name: pick<string>(sec, 'name') || pick<string>(sec, 'code') || '',
        }))
        .filter((sec) => sec.code !== ''),
    })),
  }
}
