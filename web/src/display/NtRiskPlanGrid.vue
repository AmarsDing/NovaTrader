<script lang="ts">
/** 8 槽键集（固定槽序的键名出处；键改名会破坏下游测试与接线）。 */
export const RISK_PLAN_KEYS = [
  'entry',
  'stopLoss',
  'takeProfit',
  'grossR',
  'netR',
  'riskBudgetPct',
  'positionPct',
  'costBps',
] as const

export type RiskPlanSlotKey = (typeof RISK_PLAN_KEYS)[number]
</script>

<script setup lang="ts">
// NtRiskPlanGrid：risk_plan 固定 8 槽位展示件（W2c；UI 宪法 R-UI-20/23）。
// QD P0-2 借鉴：入场/止损/止盈/毛R/净R/风险预算/建议仓位/往返成本 8 格固定槽序全出，
// 缺槽走 NtMissing「没取到≠0」（不显 0、不蒸发整格）；值之缺失与非有限同待（来源缺失不冒充）。
// 口径契约：价格类按单价小数展示两位；R 倍数带「x」后缀两位；百分位输入为小数（0.02=2%）；
// 往返成本 bps 一位小数。分量单位也在格内常驻（§显示合同 附录C 单位不省略反模式）。
import NtMissing from '@/ui/NtMissing.vue'

/** 单槽输入：value 缺失/非有限按缺位显；reason 为缺位原因徽注（如「源未连接」）。 */
export interface RiskPlanSlotInput {
  value?: number | null
  reason?: string
}

const props = withDefaults(
  defineProps<{
    /** 各槽数据；键缺整槽按缺位显。 */
    plan?: Partial<Record<RiskPlanSlotKey, RiskPlanSlotInput | null | undefined>> | null
    /** 卡片标题。 */
    title?: string
  }>(),
  { plan: null, title: '风险计划' },
)

interface SlotDef {
  key: RiskPlanSlotKey
  label: string
  unit: string
  format: (v: number) => string
}

/** 槽定义唯一出处（标签/单位/格式化与键序绑定；改文案须同步改测试）。 */
const SLOT_DEFS: readonly SlotDef[] = [
  { key: 'entry', label: '入场价', unit: '元', format: (v) => v.toFixed(2) },
  { key: 'stopLoss', label: '止损价', unit: '元', format: (v) => v.toFixed(2) },
  { key: 'takeProfit', label: '止盈价', unit: '元', format: (v) => v.toFixed(2) },
  { key: 'grossR', label: '毛 R 倍数', unit: 'R', format: (v) => `${v.toFixed(2)}x` },
  { key: 'netR', label: '净 R 倍数', unit: 'R', format: (v) => `${v.toFixed(2)}x` },
  { key: 'riskBudgetPct', label: '风险预算', unit: '%', format: (v) => `${(v * 100).toFixed(1)}%` },
  { key: 'positionPct', label: '建议仓位', unit: '%', format: (v) => `${(v * 100).toFixed(1)}%` },
  { key: 'costBps', label: '往返成本', unit: 'bps', format: (v) => `${v.toFixed(1)}` },
]

/** 取槽位可显文本：null = 缺位（交 NtMissing），string = 已格式化值。 */
function slotText(def: SlotDef): string | null {
  const inSlot = props.plan?.[def.key]
  const v = inSlot?.value
  if (v === null || v === undefined || !Number.isFinite(v)) return null
  return def.format(v)
}

function slotReason(def: SlotDef): string {
  return props.plan?.[def.key]?.reason ?? ''
}
</script>

<template>
  <section class="nt-riskplan" :data-title="props.title">
    <h3 v-if="props.title" class="nt-riskplan__title">{{ props.title }}</h3>
    <div class="nt-riskplan__grid">
      <div v-for="def in SLOT_DEFS" :key="def.key" class="nt-riskplan__slot" :data-slot="def.key">
        <span class="nt-riskplan__label">{{ def.label }}</span>
        <span v-if="slotText(def) !== null" class="nt-riskplan__value">{{ slotText(def) }}</span>
        <NtMissing v-else :reason="slotReason(def)" />
        <span class="nt-riskplan__unit">{{ def.unit }}</span>
      </div>
    </div>
  </section>
</template>

<style scoped>
.nt-riskplan {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-riskplan__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-riskplan__grid {
  display: grid;
  grid-template-columns: repeat(4, minmax(0, 1fr));
  gap: 6px;
}

.nt-riskplan__slot {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 7px 9px;
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
  min-width: 0;
}

.nt-riskplan__label {
  font-size: 10px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.nt-riskplan__value {
  font-family: var(--nt-font-mono);
  font-size: 14px;
  font-weight: 600;
  color: var(--nt-text-1);
  font-variant-numeric: tabular-nums;
}

.nt-riskplan__unit {
  font-size: 10px;
  color: var(--nt-text-3);
}
</style>
