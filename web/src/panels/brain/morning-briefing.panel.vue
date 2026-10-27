<script setup lang="ts">
/**
 * 晨报面板（M05）：盘前必读。
 * 日期切换 + 手动触发生成（owner 写操作）。
 * P1-5 数字三档核对：略掉无出处数字时给出警告徽标 + 逐条留痕回查明细。
 */
import { computed, onMounted, ref } from 'vue'
import { getBriefing, runBriefing, type Briefing, type EvidenceItem } from '@/api/brain'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtInput from '@/ui/NtInput.vue'
import NtReportCard, { type ReportCardData } from '@/display/NtReportCard.vue'
import SymbolName from '@/display/SymbolName.vue'
import { statusOf, type StatusTone } from '@/ui/status-map'
import { useAuthStore } from '@/stores/auth'
import { clock, messageOf } from '@/utils/format'

const auth = useAuthStore()

const date = ref(today())
const briefing = ref<Briefing | null>(null)
const loading = ref(false)
const generating = ref(false)
const showEvidence = ref(false)
const error = ref('')

/** 降级章文案/tone 出自状态册（§7.1，一字锁定）：degraded.model = 模型降级(danger)。 */
const degradedModel = statusOf('degraded.model')
/** 数字核对 magnitude 档文案/tone 出自状态册（brain.scrub.magnitude = 量级畸变(danger)，按 omitted 计数）。 */
const scrubMagnitude = statusOf('brain.scrub.magnitude')

/** 晨报是一份/日的产物：超过 30h 还没新晨报才染 warn（§7.3 年龄超阈）。 */
const DAILY_FRESH_MS = 30 * 3600 * 1000
/** 面板头徽注（§7.2-2）：时间证据取晨报生成时刻；缺失传 null 显「时点未知」（不自封）。 */
const briefingAt = computed<number | null>(() => {
  const raw = briefing.value?.createdAt
  if (!raw) return null
  const t = Date.parse(String(raw))
  return Number.isFinite(t) ? t : null
})

/** W2b.b 报告卡接线（晨报是简报四段真宿主，QD P0-2 映射族）：决策=头版结论+大盘判断理由（真字段改落位，
 *  头版/大盘判断两节并入本卡不再单独成节）；置信度/质量总分晨报无出处→缺位灰显，不伪造 0（数据纪律）；
 *  质量缺失指标=三档核对 omitted 字段名去重登记（真源=evidence 留痕）；情景/风险计划晨报未声明→整段缺位
 *  （R-UI-23 缺口灰显不蒸发、不硬塞风险清单冒充情景）。schemaVersion 缺链=未版注旧契约，卡片自带提示位。 */
const briefingReport = computed<ReportCardData | null>(() => {
  const b = briefing.value
  if (!b) return null
  const missing = [...new Set(b.evidence.filter((e) => e.verdict === 'omitted').map((e) => e.field).filter(Boolean))]
  return {
    schemaVersion: b.schemaVersion ?? undefined,
    decision: b.headline || b.marketView
      ? { decision: b.headline || undefined, rationale: b.marketView || undefined }
      : null,
    quality: { overall: null, missing },
    scenarios: null,
    riskPlan: null,
  }
})

const VERDICTS: { v: string; t: string; tone: StatusTone }[] = [
  { v: 'sourced', t: '有出处', tone: 'success' },
  { v: 'recomputed', t: '重算通过', tone: 'success' },
  { v: 'omitted', t: '已略※', tone: 'danger' },
  { v: 'magnitude', t: scrubMagnitude.text, tone: scrubMagnitude.tone },
]

function verdictOf(e: EvidenceItem) {
  return VERDICTS.find((c) => c.v === e.verdict) ?? { v: e.verdict, t: e.verdict || '—', tone: 'neutral' as const }
}

function today(): string {
  const d = new Date()
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

async function load() {
  loading.value = true
  error.value = ''
  try {
    briefing.value = await getBriefing(date.value.trim())
  } catch (err) {
    briefing.value = null
    const status = (err as { status?: number }).status
    error.value = status === 404 ? '' : messageOf(err)
  } finally {
    loading.value = false
  }
}

async function generate() {
  if (auth.isViewer()) return
  generating.value = true
  error.value = ''
  try {
    briefing.value = await runBriefing(date.value.trim() ? { date: date.value.trim() } : {})
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    generating.value = false
  }
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">晨报</h1>
        <p class="nt-note">
          盘前必读：隔夜外围、题材催化、关注池要点。数字经三档核对：有出处 / 算式重算 / 无出处替换为「（略※）」。
          <span v-if="briefing?.createdAt">生成于 {{ clock(briefing.createdAt) }}</span>
        </p>
      </div>
      <div class="nt-head__actions">
        <!-- 状态徽注区（§7.2-2）：降级/缺失章 + 新鲜度，固定右上。 -->
        <NtBadge v-if="briefing?.degraded" :tone="degradedModel.tone" dot>{{ degradedModel.text }}</NtBadge>
        <NtBadge
          v-if="briefing && (briefing.omittedCount == null || briefing.omittedCount > 0)"
          :tone="briefing.omittedCount == null ? 'neutral' : 'warn'"
          dot
          @click="showEvidence = !showEvidence"
          >{{
            briefing.omittedCount == null ? '数字核对信息缺失' : `略 ${briefing.omittedCount} 个无出处数字`
          }}</NtBadge
        >
        <NtFreshness label="晨报" :at="briefingAt" :stale-after-ms="DAILY_FRESH_MS" />
        <NtInput v-model="date" placeholder="YYYY-MM-DD" class="date" />
        <NtButton size="sm" :loading="loading" @click="load">查询</NtButton>
        <NtButton v-if="!auth.isViewer()" size="sm" :loading="generating" @click="generate">生成</NtButton>
      </div>
    </header>

    <NtErrorGuide :error="error" />
    <p v-if="briefing?.degraded && briefing.reason" class="nt-note">降级原因：{{ briefing.reason }}</p>

    <template v-if="briefing">
      <!-- W2b.b：头版+大盘判断并入报告卡决策段（原两节不重复渲染）；缺口段灰显由卡自治。 -->
      <NtReportCard :report="briefingReport" :title="`晨报决策 · ${briefing.date || date}`" />

      <section v-if="briefing.risks.length" class="nt-panel">
        <h2 class="nt-panel__title">风险</h2>
        <ul class="plain">
          <li v-for="(r, i) in briefing.risks" :key="i">{{ r }}</li>
        </ul>
      </section>

      <section v-if="briefing.watchlist.length" class="nt-panel">
        <h2 class="nt-panel__title">关注池</h2>
        <div class="nt-table-wrap"><table>
          <thead><tr><th>代码</th><th>理由</th><th>证据编号</th></tr></thead>
          <tbody>
            <tr v-for="w in briefing.watchlist" :key="w.symbol">
              <td><SymbolName v-if="w.symbol" :code="w.symbol" mode="inline" /><template v-else>—</template></td>
              <td>{{ w.reason || '—' }}</td>
              <td class="mono ev">{{ w.evidence.join('、') || '—' }}</td>
            </tr>
          </tbody>
        </table></div>
      </section>

      <section v-if="briefing.positions.length" class="nt-panel">
        <h2 class="nt-panel__title">持仓看点</h2>
        <div class="nt-table-wrap"><table>
          <thead><tr><th>代码</th><th>看法</th><th>证据编号</th></tr></thead>
          <tbody>
            <tr v-for="p in briefing.positions" :key="p.symbol">
              <td><SymbolName v-if="p.symbol" :code="p.symbol" mode="inline" /><template v-else>—</template></td>
              <td>{{ p.view || '—' }}</td>
              <td class="mono ev">{{ p.evidence.join('、') || '—' }}</td>
            </tr>
          </tbody>
        </table></div>
      </section>

      <section v-if="briefing.news.length" class="nt-panel">
        <h2 class="nt-panel__title">原始情报</h2>
        <div class="nt-table-wrap"><table>
          <thead><tr><th>标题</th><th class="r">重要度</th><th>代码</th></tr></thead>
          <tbody>
            <tr v-for="(n, i) in briefing.news" :key="n.id || i">
              <td>{{ n.title }}</td>
              <td class="r mono">{{ n.importance ?? '—' }}</td>
              <td><SymbolName v-if="n.symbol" :code="n.symbol" mode="inline" /><template v-else>—</template></td>
            </tr>
          </tbody>
        </table></div>
      </section>

      <section v-if="briefing.evidence.length" class="nt-panel">
        <h2 class="nt-panel__title">
          数字核对明细
          <NtButton size="sm" @click="showEvidence = !showEvidence">{{ showEvidence ? '收起' : '展开' }}</NtButton>
        </h2>
        <div v-if="showEvidence" class="nt-table-wrap"><table>
          <thead><tr><th>字段</th><th>数字</th><th>判定</th><th>依据</th></tr></thead>
          <tbody>
            <tr v-for="(e, i) in briefing.evidence" :key="i">
              <td class="mono">{{ e.field }}</td>
              <td class="mono">{{ e.span }}</td>
              <td><NtBadge :tone="verdictOf(e).tone" dot>{{ verdictOf(e).t }}</NtBadge></td>
              <td class="ev">{{ e.match || '无出处，正文已替换为「（略※）」' }}</td>
            </tr>
          </tbody>
        </table></div>
      </section>
    </template>

    <!-- 直觉5：error 与「还没有晨报」不得同屏——非 404 失败时真空态必须让位错误引导。 -->
    <p v-else-if="!loading && !error" class="nt-empty">这一天还没有晨报</p>
  </div>
</template>

<style scoped>
.nt-head__actions {
  display: flex;
  gap: 8px;
  align-items: center;
}

.date {
  width: 130px;
}

.plain {
  margin: 0;
  padding-left: 18px;
  font-size: 13px;
  line-height: 1.8;
  color: var(--nt-text-1);
}

.mono {
  font-family: var(--nt-font-mono);
}

.ev {
  font-size: 11px;
  color: var(--nt-text-2);
}
</style>
