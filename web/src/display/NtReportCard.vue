<script lang="ts">
/** 现行契约版号（与 backend brain biz.SchemaVersion* 对齐；对方只在破形时 +1，前端按同版号守卫）。 */
export const REPORT_SCHEMA_VERSION = 1
</script>

<script setup lang="ts">
// NtReportCard：结构化报告四段卡（W2b；UI 宪法 R-UI-20/23/25）。
// QD P0-2 借鉴：决策面板 / 数据质量 / 情景三卡 / 风险计划 8 格 四段结构一卡成稿；
// 缺口灰显保位（R-UI-23 不蒸发整段）：任一段数据缺省都显 NtMissing 占位而非消失；
// schema_version 缺省/为 0 = 契约落地前旧数据 → 顶部降级提示行（R-UI-25 版号治理，不崩、不硬解自由文本）。
import NtMissing from '@/ui/NtMissing.vue'
import NtRiskPlanGrid, { type RiskPlanSlotInput, type RiskPlanSlotKey } from './NtRiskPlanGrid.vue'

/** 决策面板：结论词与置信度缺省都按缺位显，不伪造 0 置信度。 */
export interface ReportDecision {
  /** 方向判断（如 偏多/偏空/中性）。 */
  bias?: string
  /** 决策结论词。 */
  decision?: string
  /** 置信度，0..1 小数；缺失/非有限按缺位。 */
  confidence?: number | null
  /** 理由正文。 */
  rationale?: string
}

/** 数据质量：overall 0..1 小数；missing 为显式缺失登记（后端 missing 键）。 */
export interface ReportQuality {
  overall?: number | null
  missing?: string[]
}

export interface ReportScenario {
  /** 情景名（如 乐观/基准/悲观）。 */
  name: string
  /** 目标价；缺省显「—」位。 */
  target?: number | null
  trigger?: string
  invalidation?: string
}

/** 报告卡输入：四段各自可缺；schemaVersion 0/缺省 = 旧契约。 */
export interface ReportCardData {
  schemaVersion?: number
  decision?: ReportDecision | null
  quality?: ReportQuality | null
  scenarios?: ReportScenario[] | null
  riskPlan?: Partial<Record<RiskPlanSlotKey, RiskPlanSlotInput | null | undefined>> | null
}

const props = withDefaults(
  defineProps<{
    report?: ReportCardData | null
    title?: string
  }>(),
  { report: null, title: '分析报告' },
)

/** 置信度百分文本；缺省/越界/非有限 → null 走缺位。 */
function confidenceText(d: ReportDecision | null | undefined): string | null {
  const v = d?.confidence
  if (v === null || v === undefined || !Number.isFinite(v) || v < 0 || v > 1) return null
  return `${(v * 100).toFixed(0)}%`
}

/** 质量总分百分文本；同上口径。 */
function qualityText(q: ReportQuality | null | undefined): string | null {
  const v = q?.overall
  if (v === null || v === undefined || !Number.isFinite(v) || v < 0 || v > 1) return null
  return `${(v * 100).toFixed(0)}%`
}

/** 目标价文本；null 走缺位。 */
function targetText(s: ReportScenario): string | null {
  const v = s.target
  if (v === null || v === undefined || !Number.isFinite(v)) return null
  return v.toFixed(2)
}

/** 旧契约判定：缺版号/0 都按旧数据处理。 */
function isLegacy(r: ReportCardData | null): boolean {
  return !r || !r.schemaVersion || r.schemaVersion < 1
}
</script>

<template>
  <section class="nt-report" :data-title="props.title">
    <header class="nt-report__head">
      <h3 class="nt-report__title">{{ props.title }}</h3>
      <span v-if="!isLegacy(props.report)" class="nt-report__ver">v{{ props.report!.schemaVersion }}</span>
    </header>
    <p v-if="isLegacy(props.report)" class="nt-report__legacy">
      旧格式报告（未版注）——仅显示可读段落，缺口按「—」显（v{{ REPORT_SCHEMA_VERSION }} 起为结构化契约）
    </p>

    <!-- 段一：决策面板 -->
    <div class="nt-report__section" data-section="decision">
      <h4 class="nt-report__sec-title">决策</h4>
      <div v-if="props.report?.decision" class="nt-report__decision">
        <span v-if="props.report.decision.decision" class="nt-report__verdict">{{ props.report.decision.decision }}</span>
        <span v-if="props.report.decision.bias" class="nt-report__bias">{{ props.report.decision.bias }}</span>
        <span v-if="confidenceText(props.report.decision) !== null" class="nt-report__conf">置信 {{ confidenceText(props.report.decision) }}</span>
        <NtMissing v-else label="置信度" />
        <p v-if="props.report.decision.rationale" class="nt-report__rationale">{{ props.report.decision.rationale }}</p>
      </div>
      <NtMissing v-else label="决策段" reason="未返回" />
    </div>

    <!-- 段二：数据质量 -->
    <div class="nt-report__section" data-section="quality">
      <h4 class="nt-report__sec-title">数据质量</h4>
      <div v-if="props.report?.quality" class="nt-report__quality">
        <span v-if="qualityText(props.report.quality) !== null" class="nt-report__score">{{ qualityText(props.report.quality) }}</span>
        <NtMissing v-else label="总分" />
        <div v-if="props.report.quality.missing?.length" class="nt-report__missing-list">
          <span class="nt-report__missing-cap">缺失指标：</span>
          <span v-for="m in props.report.quality.missing" :key="m" class="nt-report__missing-chip">{{ m }}</span>
        </div>
        <span v-else class="nt-report__missing-none">无缺失登记</span>
      </div>
      <NtMissing v-else label="质量段" reason="未返回" />
    </div>

    <!-- 段三：情景三卡 -->
    <div class="nt-report__section" data-section="scenarios">
      <h4 class="nt-report__sec-title">情景</h4>
      <div v-if="props.report?.scenarios?.length" class="nt-report__scenarios">
        <div v-for="s in props.report.scenarios" :key="s.name" class="nt-report__scenario">
          <span class="nt-report__scenario-name">{{ s.name }}</span>
          <span v-if="targetText(s) !== null" class="nt-report__scenario-target">{{ targetText(s) }}</span>
          <NtMissing v-else label="目标价" />
          <p v-if="s.trigger" class="nt-report__scenario-line">触发：{{ s.trigger }}</p>
          <p v-if="s.invalidation" class="nt-report__scenario-line">失效：{{ s.invalidation }}</p>
        </div>
      </div>
      <NtMissing v-else label="情景段" reason="未声明" />
    </div>

    <!-- 段四：风险计划 8 格（W2c 件复用，缺槽灰显由该件自治） -->
    <div class="nt-report__section" data-section="riskPlan">
      <NtRiskPlanGrid :plan="props.report?.riskPlan ?? null" title="风险计划" />
    </div>
  </section>
</template>

<style scoped>
.nt-report {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 10px 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

.nt-report__head {
  display: flex;
  align-items: center;
  gap: 8px;
}

.nt-report__title {
  margin: 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-report__ver {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-size: 10px;
  color: var(--nt-text-3);
  border: 1px solid var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  padding: 0 5px;
}

.nt-report__legacy {
  margin: 0;
  font-size: 11px;
  color: var(--nt-warn);
}

.nt-report__section {
  display: flex;
  flex-direction: column;
  gap: 5px;
  padding: 8px 9px;
  background: var(--nt-bg-2, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
}

.nt-report__sec-title {
  margin: 0;
  font-size: 11px;
  font-weight: 600;
  color: var(--nt-text-2);
}

.nt-report__decision {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.nt-report__verdict {
  font-size: 14px;
  font-weight: 700;
  color: var(--nt-text-1);
}

.nt-report__bias {
  font-size: 11px;
  color: var(--nt-text-2);
  border: 1px solid var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  padding: 0 5px;
}

.nt-report__conf {
  font-family: var(--nt-font-mono);
  font-size: 11px;
  color: var(--nt-text-2);
}

.nt-report__rationale {
  margin: 0;
  flex-basis: 100%;
  font-size: 11px;
  line-height: 1.6;
  color: var(--nt-text-2);
  /* W2b.b 接线勘补：理由正文承接晨报大盘判断等多段落文本，换行须保留（pre-line），否则整段糊成一行。 */
  white-space: pre-line;
}

.nt-report__quality {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}

.nt-report__score {
  font-family: var(--nt-font-mono);
  font-size: 16px;
  font-weight: 700;
  color: var(--nt-text-1);
  font-variant-numeric: tabular-nums;
}

.nt-report__missing-list {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 4px;
  font-size: 10px;
  color: var(--nt-text-3);
}

.nt-report__missing-chip {
  border: 1px dashed var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  padding: 0 4px;
  color: var(--nt-text-2);
}

.nt-report__missing-none {
  font-size: 10px;
  color: var(--nt-text-3);
}

.nt-report__scenarios {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 6px;
}

.nt-report__scenario {
  display: flex;
  flex-direction: column;
  gap: 3px;
  padding: 6px 8px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s, 4px);
}

.nt-report__scenario-name {
  font-size: 11px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-report__scenario-target {
  font-family: var(--nt-font-mono);
  font-size: 13px;
  color: var(--nt-text-1);
  font-variant-numeric: tabular-nums;
}

.nt-report__scenario-line {
  margin: 0;
  font-size: 10px;
  line-height: 1.5;
  color: var(--nt-text-3);
}
</style>
