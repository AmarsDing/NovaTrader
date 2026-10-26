<script setup lang="ts">
// NtTrustBar：回测结果页信任条 12 判据位展示件（W8b；UI 宪法 R-UI-04/20/23）。
// 契约锚 backend api/backtest/v1 TrustBar（W8a）+ biz computeTrustBar（W8c）：
// - 12 槽固定槽序全出（R-UI-23 缺位灰显保位，不蒸发整槽）；
// - 缺位徽注词全部取自状态册（R-UI-04 唯一出处）：没取到/未声明/未验证/样本不足/已验证，
//   面板不自封文案；
// - 零值语义=未验证/未声明/未统计 → NtMissing 灰显，绝不拿零值画绿勾；
//   「已验证」success 绿只在 verified=true 时渲染（判据真通过才配绿）；
// - 冻结凭据（数据集/哈希）mono 截断 + title 全文，供核对样本未换源。
import type { TrustBar } from '@/api/types/backtest'
import NtMissing from '@/ui/NtMissing.vue'
import { statusOf } from '@/ui/status-map'

const props = withDefaults(
  defineProps<{
    /** 信任条判据位；null=详情没取到 → 全槽缺位灰显（缺步灰显，不崩）。 */
    bar?: TrustBar | null
    title?: string
  }>(),
  { bar: null, title: '信任条' },
)

/** 缺位/通过词唯一出处（R-UI-04）：从状态册取词，不自封。 */
const words = {
  data: statusOf('missing.data').text,
  undeclared: statusOf('missing.undeclared').text,
  unverified: statusOf('missing.unverified').text,
  insufficient: statusOf('missing.insufficient').text,
  pass: statusOf('verify.pass').text,
} as const

type SlotKind = 'count' | 'bps' | 'mono' | 'text' | 'verify'

interface SlotDef {
  key: keyof TrustBar
  label: string
  kind: SlotKind
  unit?: string
  /** 缺位徽注词（来自状态册）。 */
  miss: string
}

/** 槽定义唯一出处（键序即槽序；改文案须同步改测试）。 */
const SLOT_DEFS: readonly SlotDef[] = [
  { key: 'barsTotal', label: 'K 线样本', kind: 'count', unit: '根', miss: words.data },
  { key: 'tradingDays', label: '交易日', kind: 'count', unit: '天', miss: words.data },
  // 契约：trade_count 0 笔=「样本不足」结论位，不是 0% 胜率——缺位词取 insufficient。
  { key: 'tradeCount', label: '成交笔数', kind: 'count', unit: '笔', miss: words.insufficient },
  { key: 'windowStart', label: '窗口起', kind: 'mono', miss: words.undeclared },
  { key: 'windowEnd', label: '窗口止', kind: 'mono', miss: words.undeclared },
  { key: 'costModel', label: '成本口径', kind: 'text', miss: words.undeclared },
  { key: 'feeBps', label: '佣金', kind: 'bps', unit: 'bps', miss: words.undeclared },
  { key: 'slippageBps', label: '滑点', kind: 'bps', unit: 'bps', miss: words.undeclared },
  { key: 'blindVerified', label: '盲测判据', kind: 'verify', miss: words.unverified },
  { key: 'pboVerified', label: '过拟合判据', kind: 'verify', miss: words.unverified },
  { key: 'datasetManifestId', label: '冻结数据集', kind: 'mono', miss: words.undeclared },
  { key: 'datasetHash', label: '数据哈希', kind: 'mono', miss: words.undeclared },
]

/** 判据位通过：只在 verified=true 才给绿（零值/缺 bar 一律 false）。 */
function isVerified(def: SlotDef): boolean {
  return def.kind === 'verify' && props.bar?.[def.key] === true
}

/** 非判据槽可显文本：null = 缺位（交 NtMissing）；0/空串/非有限一律缺位。 */
function slotText(def: SlotDef): string | null {
  if (def.kind === 'verify') return null
  const bar = props.bar
  if (!bar) return null
  const v = bar[def.key]
  if (def.kind === 'count') return typeof v === 'number' && v > 0 ? String(v) : null
  if (def.kind === 'bps') return typeof v === 'number' && Number.isFinite(v) ? v.toFixed(1) : null
  return typeof v === 'string' && v !== '' ? v : null
}

/** mono 槽 hover 给全文（哈希截断后仍可对账）。 */
function slotTitle(def: SlotDef): string | undefined {
  return def.kind === 'mono' ? slotText(def) ?? undefined : undefined
}
</script>

<template>
  <section class="nt-trust" :data-title="props.title">
    <header class="nt-trust__head">
      <h3 class="nt-trust__title">{{ props.title }}</h3>
      <span class="nt-trust__legend">零值即未验证——绿勾只发真判据</span>
    </header>
    <div class="nt-trust__grid">
      <div v-for="def in SLOT_DEFS" :key="def.key" class="nt-trust__slot" :data-slot="def.key">
        <span class="nt-trust__label">{{ def.label }}</span>
        <span v-if="isVerified(def)" class="nt-trust__pass">{{ words.pass }}</span>
        <span
          v-else-if="slotText(def) !== null"
          class="nt-trust__value"
          :class="{ 'nt-trust__value--text': def.kind === 'text', 'nt-trust__value--clip': def.kind === 'mono' }"
          :title="slotTitle(def)"
        >{{ slotText(def) }}</span>
        <NtMissing v-else :label="def.miss" />
        <span v-if="def.unit" class="nt-trust__unit">{{ def.unit }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.nt-trust {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-trust__head {
  display: flex;
  align-items: baseline;
  gap: 8px;
}

.nt-trust__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-trust__legend {
  font-size: 10px;
  color: var(--nt-text-3);
}

.nt-trust__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 6px;
}

.nt-trust__slot {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 7px 9px;
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
  min-width: 0;
}

.nt-trust__label {
  font-size: 10px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.nt-trust__value {
  font-family: var(--nt-font-mono);
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
  font-variant-numeric: tabular-nums;
  min-width: 0;
}

.nt-trust__value--text {
  font-family: var(--nt-font-sans);
  font-size: 11px;
  font-weight: 400;
  line-height: 1.5;
  color: var(--nt-text-2);
}

.nt-trust__value--clip {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
  font-weight: 400;
  font-size: 11px;
}

.nt-trust__pass {
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-success);
}

.nt-trust__unit {
  font-size: 10px;
  color: var(--nt-text-3);
}
</style>
