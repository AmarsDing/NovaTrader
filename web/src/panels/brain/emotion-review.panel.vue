<script setup lang="ts">
/**
 * 情绪复盘面板（M05 P0 + P1-7 + P1-7b）：收盘定稿读数 / 明日验证对账 / 程序版复盘 / 分段式 AI 复盘（P2-8 六段）/ AI 周报。
 * 「没取到 ≠ 0」：undefined/null 一律显示「没取到」，不拼 0。
 */
import { computed, onMounted, ref, watch } from 'vue'
import {
  generateAIReview,
  getFrozenEmotion,
  getProgramReview,
  listPredictionChecks,
  runPredictionCheck,
  type FrozenEmotion,
  type PredictionCheck,
} from '@/api/brain'
import { getEmotionPath, type EmotionPath } from '@/api/market'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import SymbolName from '@/display/SymbolName.vue'
import { useAuthStore } from '@/stores/auth'
import { statusOf } from '@/ui/status-map'
import { amount, messageOf, pctText } from '@/utils/format'
import { loadPanelPrefs, savePanelPrefs } from '@/utils/panel-prefs'

const auth = useAuthStore()

type Tab = 'frozen' | 'check' | 'review'
type ReviewKindUI = 'program' | 'ai' | 'ai_week'
// W3-2 面板偏好持久化：键版勿改，旧档自然失效。
const PREFS_KEY = 'nt.prefs.emotion-review.v1'
const PREFS_ALLOW = {
  tab: ['frozen', 'check', 'review'],
  review_kind: ['program', 'ai', 'ai_week'],
} as const
const prefs = loadPanelPrefs(PREFS_KEY, PREFS_ALLOW)
const tab = ref<Tab>((prefs.tab as Tab | undefined) ?? 'frozen')
const reviewKind = ref<ReviewKindUI>((prefs.review_kind as ReviewKindUI | undefined) ?? 'program')
watch([tab, reviewKind], () =>
  savePanelPrefs(PREFS_KEY, PREFS_ALLOW, { tab: tab.value, review_kind: reviewKind.value }),
)
const date = ref(today())

const frozen = ref<FrozenEmotion | null>(null)
const path = ref<EmotionPath | null>(null)
const checks = ref<PredictionCheck[]>([])
const reviewContent = ref('')
const reviewStats = ref('')
const aiContent = ref('')
const aiLessons = ref<string[]>([])
const aiModel = ref('')
const wkContent = ref('')
const wkLessons = ref<string[]>([])
const wkModel = ref('')
const loading = ref(false)
const running = ref(false)
const generating = ref(false)
const error = ref('')

interface AISegment {
  text?: string
  points?: string[]
  evidence?: string[]
  degraded?: boolean
}
interface AIReviewStats {
  segments?: Record<string, AISegment>
  evidence?: { field?: string; span?: string; verdict?: string; match?: string }[]
  omitted_count?: number
  sealed_at?: string
  partial?: boolean
  degraded?: boolean
  degraded_segments?: number
  missing?: string[]
}
/** P1-7b 周报 stats：无 segments，改为逐日读数 + 周窗口。 */
interface WeeklyStats {
  window_start?: string
  window_end?: string
  days?: string[]
  day_count?: number
  expected_days?: number
  usable_days?: number
  evidence?: { field?: string; span?: string; verdict?: string; match?: string }[]
  omitted_count?: number
  sealed_at?: string
  partial?: boolean
  degraded?: boolean
  missing?: string[]
}

/** 六段展示顺序与中文名（对齐后端 aiSegment；P2-8 加筹码供给）。 */
const SEG_META: { key: string; name: string }[] = [
  { key: 'emotion', name: '情绪' },
  { key: 'capital', name: '资金' },
  { key: 'theme', name: '题材' },
  { key: 'lhb', name: '龙虎榜' },
  { key: 'leader', name: '龙头' },
  { key: 'supply', name: '筹码供给' },
]

const aiStatsRaw = ref('')
const parsedAIStats = computed<AIReviewStats | null>(() => parseJSON<AIReviewStats>(aiStatsRaw.value))
const omittedCount = computed<number | null>(() => parsedAIStats.value?.omitted_count ?? null)
const aiMissing = computed(() => parsedAIStats.value?.missing ?? [])
const aiSealedAt = computed(() => parsedAIStats.value?.sealed_at ?? '')

const wkStatsRaw = ref('')
const parsedWKStats = computed<WeeklyStats | null>(() => parseJSON<WeeklyStats>(wkStatsRaw.value))
const wkOmitted = computed<number | null>(() => parsedWKStats.value?.omitted_count ?? null)
const wkMissing = computed(() => parsedWKStats.value?.missing ?? [])
const wkSealedAt = computed(() => parsedWKStats.value?.sealed_at ?? '')
const wkDays = computed(() => parsedWKStats.value?.days ?? [])

function today(): string {
  const d = new Date()
  return `${d.getFullYear()}-${String(d.getMonth() + 1).padStart(2, '0')}-${String(d.getDate()).padStart(2, '0')}`
}

const ladder = computed<Record<string, { symbol: string; name: string; firstSealAt?: string }[]>>(() => parseJSON(frozen.value?.ladderJson) ?? {})
const themes = computed<{ reason: string; count: number; maxHeight: number }[]>(() => parseJSON(frozen.value?.themeTreeJson) ?? [])
const prevTier = computed<{ symbol: string; name: string; pct: number; relimit: boolean }[]>(() => parseJSON(frozen.value?.prevTierJson) ?? [])

function parseJSON<T>(raw: string | undefined): T | null {
  if (!raw) return null
  try {
    return JSON.parse(raw) as T
  } catch {
    return null
  }
}

function na(v: number | null | undefined, suffix = ''): string {
  return v == null ? '没取到' : `${v}${suffix}`
}

async function load() {
  loading.value = true
  error.value = ''
  frozen.value = null
  path.value = null
  reviewContent.value = ''
  reviewStats.value = ''
  aiContent.value = ''
  aiStatsRaw.value = ''
  aiLessons.value = []
  aiModel.value = ''
  wkContent.value = ''
  wkStatsRaw.value = ''
  wkLessons.value = []
  wkModel.value = ''
  try {
    const [f, p, c, r, a, w] = await Promise.allSettled([
      getFrozenEmotion(date.value),
      getEmotionPath(date.value),
      listPredictionChecks(20),
      getProgramReview(date.value),
      getProgramReview(date.value, 'ai'),
      getProgramReview(date.value, 'ai_week'),
    ])
    if (f.status === 'fulfilled') frozen.value = f.value
    if (p.status === 'fulfilled') path.value = p.value
    if (c.status === 'fulfilled') checks.value = c.value
    if (r.status === 'fulfilled') {
      reviewContent.value = r.value.content
      reviewStats.value = r.value.statsJson
    }
    if (a.status === 'fulfilled') {
      aiContent.value = a.value.content
      aiStatsRaw.value = a.value.statsJson
      aiLessons.value = a.value.lessons
      aiModel.value = a.value.model
    }
    if (w.status === 'fulfilled') {
      wkContent.value = w.value.content
      wkStatsRaw.value = w.value.statsJson
      wkLessons.value = w.value.lessons
      wkModel.value = w.value.model
    }
    const allBad = [f, p, c, r, a, w].every((s) => s.status === 'rejected')
    if (allBad) {
      error.value = `${date.value} 还没有复盘数据（15:35 后自动生成）`
    }
  } finally {
    loading.value = false
  }
}

async function runCheck() {
  if (auth.isViewer()) return
  running.value = true
  error.value = ''
  try {
    const c = await runPredictionCheck(date.value)
    const idx = checks.value.findIndex((x) => x.checkDate === c.checkDate && x.source === c.source)
    if (idx >= 0) checks.value[idx] = c
    else checks.value.unshift(c)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    running.value = false
  }
}

/** 手动生成/重新生成：AI 分段日复盘或 AI 周报（随当前子切换；同日同 kind 覆盖）。 */
async function genAIReview() {
  if (auth.isViewer()) return
  generating.value = true
  error.value = ''
  try {
    const kind = reviewKind.value === 'ai_week' ? 'ai_week' : 'ai'
    const a = await generateAIReview(date.value, kind)
    if (kind === 'ai_week') {
      wkContent.value = a.content
      wkStatsRaw.value = a.statsJson
      wkLessons.value = a.lessons
      wkModel.value = a.model
    } else {
      aiContent.value = a.content
      aiStatsRaw.value = a.statsJson
      aiLessons.value = a.lessons
      aiModel.value = a.model
    }
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
        <h1 class="nt-title">
          情绪复盘
          <NtBadge v-if="frozen?.partial" tone="warn" dot>有缺失</NtBadge>
        </h1>
        <p class="nt-sub">收盘定稿 · 明日验证 · 复盘与 AI 周报（15:35 起自动生成）</p>
      </div>
      <div class="nt-actions">
        <NtInput v-model="date" placeholder="YYYY-MM-DD" style="width: 9em" @keyup.enter="load" />
        <NtButton :loading="loading" @click="load">查询</NtButton>
        <NtButton v-if="!auth.isViewer()" :loading="running" @click="runCheck">补跑对账</NtButton>
        <div class="nt-tabs">
          <button type="button" :class="{ on: tab === 'frozen' }" @click="tab = 'frozen'">定稿</button>
          <button type="button" :class="{ on: tab === 'check' }" @click="tab = 'check'">明日验证</button>
          <button type="button" :class="{ on: tab === 'review' }" @click="tab = 'review'">复盘</button>
        </div>
      </div>
    </header>

    <p v-if="error" class="nt-error">{{ error }}</p>

    <!-- 定稿 -->
    <section v-if="tab === 'frozen'" class="nt-body">
      <template v-if="frozen">
        <div class="nt-grid metric">
          <div class="cell"><span>涨停</span><strong :class="frozen.upCount ? 'up' : ''">{{ na(frozen.upCount) }}</strong></div>
          <div class="cell"><span>跌停</span><strong :class="frozen.downCount ? 'down' : ''">{{ na(frozen.downCount) }}</strong></div>
          <div class="cell"><span>炸板</span><strong>{{ na(frozen.brokenCount) }}</strong></div>
          <div class="cell"><span>封板率</span><strong>{{ na(frozen.sealRate, '%') }}</strong></div>
          <div class="cell"><span>最高板</span><strong>{{ na(frozen.maxHeight) }}</strong></div>
          <div class="cell"><span>1进2</span><strong>{{ na(frozen.promo12, '%') }}</strong></div>
          <div class="cell"><span>2进3</span><strong>{{ na(frozen.promo23, '%') }}</strong></div>
          <div class="cell"><span>高位晋级</span><strong>{{ na(frozen.promoHigh, '%') }}</strong></div>
        </div>
        <div class="nt-grid metric">
          <div class="cell"><span>昨梯队均涨</span><strong>{{ na(frozen.tierAvg, '%') }}</strong></div>
          <div class="cell"><span>昨梯队中位</span><strong>{{ na(frozen.tierMedian, '%') }}</strong></div>
          <div class="cell"><span>翻红率</span><strong>{{ na(frozen.tierRedRate, '%') }}</strong></div>
          <div class="cell"><span>再涨停率</span><strong>{{ na(frozen.tierRelimit, '%') }}</strong></div>
          <div v-if="frozen.structureGaps.length" class="cell wide"><span>梯队断层</span><strong class="up">{{ frozen.structureGaps.map((g) => `${g}板`).join('、') }}</strong></div>
        </div>

        <h2 class="sec">盘中情绪路径</h2>
        <template v-if="path">
          <div class="nt-grid metric">
            <div class="cell">
              <span>竞价总额</span>
              <strong>{{ path.auctionFound ? amount(path.auctionTotalAmount) : '没取到' }}</strong>
            </div>
            <div class="cell">
              <span>竞价涨停</span>
              <strong>{{ path.auctionFound ? `${path.auctionLimitUpCount} 家` : '没取到' }}</strong>
            </div>
            <div class="cell">
              <span>昨涨停竞价强度</span>
              <strong :class="path.prevTierAuctionPct != null && path.prevTierAuctionPct > 0 ? 'up' : path.prevTierAuctionPct != null && path.prevTierAuctionPct < 0 ? 'down' : ''">
                {{ na(path.prevTierAuctionPct, '%') }}
              </strong>
            </div>
            <div class="cell">
              <span>强度样本</span>
              <strong>{{ path.prevTierAuctionSample || '没取到' }}</strong>
            </div>
          </div>
          <div v-if="path.points.length" class="nt-table-wrap"><table>
            <thead>
              <tr>
                <th>时点</th><th class="r">涨停</th><th class="r">跌停</th><th class="r">炸板</th>
                <th class="r">炸板率</th><th class="r">最高板</th><th class="r">上涨/下跌</th><th>阶段</th>
              </tr>
            </thead>
            <tbody>
              <tr v-for="p in path.points" :key="p.time + (p.auction ? '-auc' : '')" :class="{ partial: p.stale }">
                <td class="mono">{{ p.time }}<NtBadge v-if="p.auction" tone="neutral" dot>竞价</NtBadge></td>
                <td class="r" :class="{ up: !p.auction && p.up > 0 }">{{ p.auction ? '—' : p.up }}</td>
                <td class="r" :class="{ down: !p.auction && p.down > 0 }">{{ p.auction ? '—' : p.down }}</td>
                <td class="r">{{ p.auction ? '—' : p.broken }}</td>
                <td class="r">{{ p.auction ? '—' : `${p.brokenRate}%` }}</td>
                <td class="r">{{ p.auction ? '—' : p.maxHeight }}</td>
                <td class="r">{{ p.auction ? '—' : `${p.advance}/${p.decline}` }}</td>
                <td>{{ p.auction ? '—' : p.phase || '—' }}</td>
              </tr>
            </tbody>
          </table></div>
          <p v-else class="nt-empty">没取到盘中时点截面</p>
          <p v-if="path.missing.length" class="nt-missing">没取到的块：{{ path.missing.join('、') }}</p>
        </template>
        <p v-else class="nt-empty">没取到盘中情绪路径</p>

        <h2 class="sec">连板天梯</h2>
        <div class="ladder">
          <div v-for="h in Object.keys(ladder).map(Number).sort((a, b) => b - a)" :key="h" class="row">
            <span class="h">{{ h }}板</span>
            <span v-for="item in ladder[String(h)] || []" :key="item.symbol" class="chip" :title="item.firstSealAt ? `首封 ${item.firstSealAt}` : ''">
              {{ item.name || item.symbol }}
            </span>
          </div>
          <p v-if="!Object.keys(ladder).length" class="nt-empty">没取到天梯数据</p>
        </div>

        <h2 class="sec">题材归并 Top</h2>
        <div v-if="themes.length" class="nt-table-wrap"><table>
          <thead><tr><th>题材</th><th class="r">只数</th><th class="r">最高板</th></tr></thead>
          <tbody>
            <tr v-for="t in themes.slice(0, 10)" :key="t.reason">
              <td>{{ t.reason }}</td>
              <td class="r">{{ t.count }}</td>
              <td class="r">{{ t.maxHeight }}板</td>
            </tr>
          </tbody>
        </table></div>
        <p v-else class="nt-empty">没取到题材数据</p>

        <h2 class="sec">昨日梯队今日表现</h2>
        <div v-if="prevTier.length" class="nt-table-wrap"><table>
          <thead><tr><th>名称</th><th class="r">涨幅</th><th class="r">再涨停</th></tr></thead>
          <tbody>
            <tr v-for="r in prevTier.slice(0, 20)" :key="r.symbol">
              <td>{{ r.name || r.symbol }}</td>
              <td class="r" :class="r.pct > 0 ? 'up' : r.pct < 0 ? 'down' : ''">{{ pctText(r.pct) }}</td>
              <td class="r"><NtBadge v-if="r.relimit" tone="danger" dot>涨停</NtBadge><span v-else>—</span></td>
            </tr>
          </tbody>
        </table></div>
        <p v-else class="nt-empty">没取到昨日梯队表现</p>

        <p v-if="frozen.missing.length" class="nt-missing">没取到的块：{{ frozen.missing.join('、') }}</p>
        <p class="nt-meta">来源 {{ frozen.source || '—' }} · 冻结于 {{ frozen.capturedAt || '—' }}</p>
      </template>
      <p v-else-if="!loading" class="nt-empty">{{ date }} 无收盘定稿（15:35 自动生成，或当时数据源缺失）</p>
    </section>

    <!-- 明日验证 -->
    <section v-else-if="tab === 'check'" class="nt-body">
      <div v-if="checks.length" class="nt-table-wrap"><table>
        <thead>
          <tr>
            <th>对账日</th><th>信号日</th><th class="r">候选</th><th class="r">命中(收涨)</th>
            <th class="r">命中率</th><th class="r">均幅</th><th class="r">最高</th><th class="r">再涨停</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="c in checks" :key="`${c.checkDate}-${c.source}`" :class="{ partial: c.partial }">
            <td>{{ c.checkDate }}</td>
            <td>{{ c.signalDate }}</td>
            <td class="r">{{ na(c.candidateCount) }}</td>
            <td class="r">{{ na(c.hitCount) }}</td>
            <td class="r" :class="c.hitRate != null && c.hitRate >= 50 ? 'up' : ''">{{ na(c.hitRate, '%') }}</td>
            <td class="r" :class="c.avgPct != null && c.avgPct > 0 ? 'up' : c.avgPct != null && c.avgPct < 0 ? 'down' : ''">{{ na(c.avgPct, '%') }}</td>
            <td class="r">{{ na(c.maxPct, '%') }}</td>
            <td class="r">{{ na(c.relimitCount) }}</td>
          </tr>
        </tbody>
      </table></div>
      <p v-else class="nt-empty">暂无对账记录（15:45 起按日生成）</p>
      <details v-for="c in checks.filter((x) => x.results.length)" :key="`d-${c.checkDate}`" class="nt-detail">
        <summary>{{ c.checkDate }} 明细（{{ c.results.length }} 只）</summary>
        <div class="nt-table-wrap"><table>
          <thead><tr><th>代码</th><th>入选逻辑</th><th class="r">次日涨幅</th><th class="r">结果</th></tr></thead>
          <tbody>
            <tr v-for="r in c.results" :key="r.symbol">
              <td><SymbolName v-if="r.symbol" :code="r.symbol" mode="inline" /><template v-else>—</template></td>
              <td class="muted">{{ c.candidates.find((x) => x.symbol === r.symbol)?.text || c.candidates.find((x) => x.symbol === r.symbol)?.reason || '—' }}</td>
              <td class="r" :class="r.pct != null && r.pct > 0 ? 'up' : r.pct != null && r.pct < 0 ? 'down' : ''">{{ r.pct == null ? '没取到' : pctText(r.pct) }}</td>
              <td class="r">
                <NtBadge v-if="r.relimit" tone="danger" dot>涨停</NtBadge>
                <NtBadge v-else-if="r.hit" tone="neutral">收涨</NtBadge>
                <span v-else class="down">走弱</span>
              </td>
            </tr>
          </tbody>
        </table></div>
      </details>
    </section>

    <!-- 复盘：程序版八格 / 分段式 AI 复盘（P1-7）/ AI 周报（P1-7b） -->
    <section v-else class="nt-body">
      <div class="review-head">
        <div class="nt-tabs">
          <button type="button" :class="{ on: reviewKind === 'program' }" @click="reviewKind = 'program'">程序版</button>
          <button type="button" :class="{ on: reviewKind === 'ai' }" @click="reviewKind = 'ai'">AI 复盘</button>
          <button type="button" :class="{ on: reviewKind === 'ai_week' }" @click="reviewKind = 'ai_week'">AI 周报</button>
        </div>
        <NtButton
          v-if="reviewKind !== 'program' && !auth.isViewer()"
          :loading="generating" @click="genAIReview">{{ reviewKind === 'ai_week' ? '生成/重跑 AI 周报' : '生成/重跑 AI 复盘' }}</NtButton>
      </div>

      <!-- 程序版八格 -->
      <template v-if="reviewKind === 'program'">
        <article v-if="reviewContent" class="nt-article" v-text="reviewContent" />
        <p v-else class="nt-empty">{{ date }} 无程序版复盘（15:40 自动生成）</p>
        <details v-if="reviewStats" class="nt-detail">
          <summary>八格读数 JSON</summary>
          <pre class="nt-pre">{{ reviewStats }}</pre>
        </details>
      </template>

      <!-- 分段式 AI 复盘 -->
      <template v-else-if="reviewKind === 'ai'">
        <p v-if="!aiContent" class="nt-empty">{{ date }} 无 AI 复盘（15:50 自动生成，或点右上方手动生成）</p>
        <template v-else>
          <div class="review-head">
            <div>
              <NtBadge v-if="parsedAIStats?.partial" tone="warn" dot>部分降级</NtBadge>
              <NtBadge
                v-if="omittedCount == null || omittedCount > 0"
                :tone="omittedCount == null ? 'neutral' : 'warn'"
                dot
                >{{ omittedCount == null ? '数字核对信息缺失' : `略 ${omittedCount} 个无出处数字` }}</NtBadge
              >
              <NtBadge :tone="statusOf('ai.segments').tone" dot>{{ statusOf('ai.segments').text }}（{{ SEG_META.length }} 段）</NtBadge>
            </div>
            <span class="nt-meta">封盘 {{ aiSealedAt || '—' }} · 模型 {{ aiModel || '—' }}</span>
          </div>

          <article class="nt-article" v-text="aiContent" />

          <h2 class="sec">给明天的教训</h2>
          <ul v-if="aiLessons.length" class="lessons">
            <li v-for="(l, i) in aiLessons" :key="i">{{ l }}</li>
          </ul>
          <p v-else class="nt-empty">没取到教训要点</p>

          <h2 class="sec">分段解读</h2>
          <div v-for="s in SEG_META" :key="s.key" class="seg">
            <div class="seg-head">
              <strong>{{ s.name }}</strong>
              <NtBadge v-if="parsedAIStats?.segments?.[s.key]?.degraded" tone="warn" dot>模型未参与</NtBadge>
            </div>
            <template v-if="parsedAIStats?.segments?.[s.key]">
              <p class="seg-text">{{ parsedAIStats.segments[s.key].text }}</p>
              <ul v-if="parsedAIStats.segments[s.key].points?.length" class="lessons">
                <li v-for="(p, i) in parsedAIStats.segments[s.key].points" :key="i">{{ p }}</li>
              </ul>
              <p v-if="parsedAIStats.segments[s.key].evidence?.length" class="nt-meta">证据：{{ parsedAIStats.segments[s.key].evidence?.join('、') }}</p>
            </template>
            <p v-else class="nt-empty">没取到本段</p>
          </div>

          <details v-if="parsedAIStats?.evidence?.length" class="nt-detail">
            <summary>数字核对明细（{{ parsedAIStats.evidence.length }} 条）</summary>
            <div class="nt-table-wrap"><table>
              <thead><tr><th>字段</th><th class="r">数字</th><th>判定</th><th>依据</th></tr></thead>
              <tbody>
                <tr v-for="(e, i) in parsedAIStats.evidence" :key="i">
                  <td class="mono">{{ e.field }}</td>
                  <td class="r mono">{{ e.span }}</td>
                  <td>
                    <NtBadge v-if="e.verdict === 'sourced'" tone="success">有出处</NtBadge>
                    <NtBadge v-else-if="e.verdict === 'recomputed'" tone="success">重算通过</NtBadge>
                    <NtBadge v-else tone="danger">已略※</NtBadge>
                  </td>
                  <td class="muted">{{ e.match || '无出处，正文已替换为（略※）' }}</td>
                </tr>
              </tbody>
            </table></div>
          </details>

          <p v-if="aiMissing.length" class="nt-missing">没取到的块：{{ aiMissing.join('、') }}</p>
        </template>
      </template>

      <!-- AI 周报（P1-7b）：窗口/逐日读数 + 总评 + 给下周的纪律，行落在本周最后交易日 -->
      <template v-else>
        <p v-if="!wkContent" class="nt-empty">{{ date }} 所在周暂无 AI 周报（本周最后交易日 15:55 自动生成，或点右上方手动生成）</p>
        <template v-else>
          <div class="review-head">
            <div>
              <NtBadge v-if="parsedWKStats?.degraded" tone="warn" dot>模型未参与，展示兜底</NtBadge>
              <NtBadge v-else-if="parsedWKStats?.partial" tone="warn" dot>有缺失</NtBadge>
              <NtBadge
                v-if="!parsedWKStats?.degraded && (wkOmitted == null || wkOmitted > 0)"
                :tone="wkOmitted == null ? 'neutral' : 'warn'"
                dot
                >{{ wkOmitted == null ? '数字核对信息缺失' : `略 ${wkOmitted} 个无出处数字` }}</NtBadge
              >
              <NtBadge :tone="statusOf('ai.weekly').tone" dot>{{ statusOf('ai.weekly').text }}</NtBadge>
            </div>
            <span class="nt-meta">窗口 {{ parsedWKStats?.window_start || '—' }} ~ {{ parsedWKStats?.window_end || '—' }} · 覆盖 {{ parsedWKStats?.usable_days ?? '—' }}/{{ parsedWKStats?.expected_days ?? '—' }} 个交易日 · 封盘 {{ wkSealedAt || '—' }} · 模型 {{ wkModel || '—' }}</span>
          </div>

          <article class="nt-article" v-text="wkContent" />

          <h2 class="sec">给下周的纪律</h2>
          <ul v-if="wkLessons.length" class="lessons">
            <li v-for="(l, i) in wkLessons" :key="i">{{ l }}</li>
          </ul>
          <p v-else class="nt-empty">没取到教训要点</p>

          <h2 class="sec">周内逐日读数</h2>
          <ul v-if="wkDays.length" class="lessons">
            <li v-for="(d, i) in wkDays" :key="i" class="mono day-line">{{ d }}</li>
          </ul>
          <p v-else class="nt-empty">没取到逐日读数</p>

          <details v-if="parsedWKStats?.evidence?.length" class="nt-detail">
            <summary>数字核对明细（{{ parsedWKStats.evidence.length }} 条）</summary>
            <div class="nt-table-wrap"><table>
              <thead><tr><th>字段</th><th class="r">数字</th><th>判定</th><th>依据</th></tr></thead>
              <tbody>
                <tr v-for="(e, i) in parsedWKStats.evidence" :key="i">
                  <td class="mono">{{ e.field }}</td>
                  <td class="r mono">{{ e.span }}</td>
                  <td>
                    <NtBadge v-if="e.verdict === 'sourced'" tone="success">有出处</NtBadge>
                    <NtBadge v-else-if="e.verdict === 'recomputed'" tone="success">重算通过</NtBadge>
                    <NtBadge v-else tone="danger">已略※</NtBadge>
                  </td>
                  <td class="muted">{{ e.match || '无出处，正文已替换为（略※）' }}</td>
                </tr>
              </tbody>
            </table></div>
          </details>

          <p v-if="wkMissing.length" class="nt-missing">没取到的块：{{ wkMissing.join('、') }}</p>
        </template>
      </template>
    </section>
  </div>
</template>

<style scoped>
.sec { margin: 0; font-size: 13px; font-weight: 600; color: var(--nt-text-2); border-left: 3px solid var(--nt-accent); padding-left: 9px; }
.nt-detail { border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); padding: 8px 10px; }
.nt-detail > summary { cursor: pointer; font-size: 12px; color: var(--nt-text-2); margin-bottom: 8px; }
.nt-pre { margin: 0; font-family: var(--nt-font-mono); font-size: 11px; color: var(--nt-text-2); white-space: pre-wrap; word-break: break-all; }
.nt-actions { display: flex; align-items: center; gap: 8px; flex-wrap: wrap; }
.nt-tabs { display: inline-flex; gap: 4px; padding: 3px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); }
.nt-tabs button { padding: 4px 12px; border: 0; border-radius: var(--nt-radius-s); background: transparent; color: var(--nt-text-2); font-size: 12px; cursor: pointer; }
.nt-tabs button.on { background: var(--nt-accent-soft); color: var(--nt-accent); }
.nt-body { display: flex; flex-direction: column; gap: 12px; }
.metric { display: grid; grid-template-columns: repeat(4, 1fr); gap: 8px; margin: 0; padding: 0; }
.metric .cell { display: flex; flex-direction: column; gap: 2px; padding: 8px 10px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); }
.metric .cell.wide { grid-column: span 2; }
.metric .cell span { font-size: 11px; color: var(--nt-text-3); }
.metric .cell strong { font-size: 16px; font-family: var(--nt-font-mono); font-weight: 600; color: var(--nt-text-1); }
.up { color: var(--nt-up); }
.down { color: var(--nt-down); }
.ladder .row { display: flex; align-items: center; gap: 8px; padding: 4px 0; }
.ladder .h { min-width: 3em; font-family: var(--nt-font-mono); color: var(--nt-up); font-weight: 600; }
.ladder .chip { padding: 2px 10px; border-radius: 999px; border: 1px solid var(--nt-border-1); font-size: 12px; color: var(--nt-text-1); }
.nt-missing { font-size: 12px; color: var(--nt-warn); }
.nt-meta { font-size: 11px; color: var(--nt-text-3); }
.nt-article { white-space: pre-wrap; line-height: 1.7; font-size: 13px; color: var(--nt-text-1); padding: 12px; border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); }
tr.partial td { opacity: 0.6; }
.mono { font-family: var(--nt-font-mono); }
.muted { color: var(--nt-text-3); font-size: 12px; }
.review-head { display: flex; align-items: center; justify-content: space-between; gap: 8px; flex-wrap: wrap; }
.lessons { margin: 0; padding-left: 18px; display: flex; flex-direction: column; gap: 4px; }
.lessons li { font-size: 13px; color: var(--nt-text-1); line-height: 1.6; }
.seg { border: 1px solid var(--nt-border-1); border-radius: var(--nt-radius-m); background: var(--nt-data-raised); padding: 10px 12px; display: flex; flex-direction: column; gap: 6px; }
.seg-head { display: flex; align-items: center; gap: 8px; }
.seg-head strong { font-size: 13px; color: var(--nt-text-1); }
.seg-text { margin: 0; font-size: 12px; color: var(--nt-text-2); line-height: 1.6; }
.day-line { font-size: 12px; color: var(--nt-text-2); }
</style>
