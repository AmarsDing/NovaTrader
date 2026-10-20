<script setup lang="ts">
/**
 * 数据源中心面板（M01 数据源对接与配置方案 §4.4）：三页签。
 * A 源墙：目录 × 健康行按 name 客户端 join——费用档/域徽章/健康灯/开关/凭证；
 * B 域视图：生效降级链（/api/config 覆盖 ?? yaml 默认）+ 候选源上下移编辑；
 * C 健康：listSources + getQuality 两张只读表（写面留在 datasource-health 面板，不开第二入口）。
 *
 * 写面纪律（就绪即灰）：W1 键族能力（app.ds.* / app.domain.*）以 /api/config preview 探测，
 * 服务端判「未登记的参数键」时原文透出并禁用写控件；凭证永不回显明文（secret_set 布尔）。
 * 拖拽排序留 P1（HTML5 draggable 标记位），P0 用「上移/下移」零依赖。
 */
import { computed, onMounted, ref } from 'vue'
import {
  getQuality,
  getSourceCatalog,
  listSources,
  type DataSource,
  type QualityIssue,
  type SourceCatalog,
  type SourceManifest,
} from '@/api/datahub'
import {
  getConfigSnapshot,
  previewConfig,
  saveConfig,
  type ConfigSnapshot,
} from '@/api/config'
import { ApiError } from '@/api/http'
import { useAuthStore } from '@/stores/auth'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { clock, messageOf, ratioPct } from '@/utils/format'
import { serverNow } from '@/utils/server-clock'

type Tab = 'wall' | 'domains' | 'health'

const auth = useAuthStore()

const tab = ref<Tab>('wall')
const tabItems = [
  { value: 'wall', label: '源墙' },
  { value: 'domains', label: '域视图' },
  { value: 'health', label: '健康' },
]

const catalog = ref<SourceCatalog | null>(null)
const sources = ref<DataSource[]>([])
const quality = ref<QualityIssue[]>([])
const snapshot = ref<ConfigSnapshot | null>(null)
const loading = ref(false)
const error = ref('')
const hint = ref('')
const saving = ref('')

/** 键能力探测：W1 键族未落地时服务端判「未登记的参数键」，原文透出、写控件标灰。 */
interface Capability {
  state: 'pending' | 'ok' | 'unavailable'
  message: string
}
const capSwitch = ref<Capability>({ state: 'pending', message: '' })
const capChain = ref<Capability>({ state: 'pending', message: '' })
const capQpsQuota = ref<Capability>({ state: 'pending', message: '' })
const capSecret = ref<Capability>({ state: 'pending', message: '' })

const STALE_MS = 30 * 60 * 1000

/** 与 datasource-health 面板同口径的状态灯（拧成面板内小函数，不改原面板）。 */
function statusOf(s: Pick<DataSource, 'enabled' | 'status' | 'lastAt'>): 'live' | 'ok' | 'stale' | 'down' | 'off' {
  if (!s.enabled) return 'off'
  const state = (s.status || '').toLowerCase()
  if (state === 'open' || state === 'unavailable' || state.includes('down') || state.includes('error')) return 'down'
  if (state === 'half_open') return 'stale'
  if (!s.lastAt) return 'stale'
  const at = new Date(s.lastAt).getTime()
  if (!Number.isFinite(at)) return 'stale'
  return serverNow() - at < STALE_MS ? 'live' : 'stale'
}

/* ------------------------------------------------------------------ */
/* 页签 A：源墙（目录 × 健康按 name join）                               */
/* ------------------------------------------------------------------ */

interface WallCard {
  manifest: SourceManifest
  /** 多域健康行的最差聚合：任一断路/失败 → down/stale；成功率取非空最小；最近成功取最新。 */
  health: Pick<DataSource, 'enabled' | 'status' | 'lastAt' | 'successRate' | 'latencyMs'>
  /** null = 无运行态覆盖键（用运行态开关/健康行为准）。 */
  overrideOn: string | null
  effEnabled: boolean
  secretKey: string | null
  secretSet: boolean
  qps: string | null
  quota: string | null
}

function feeTone(tier: SourceManifest['feeTier']): 'success' | 'warn' | 'neutral' {
  if (tier === 'free') return 'success'
  if (tier === 'freemium') return 'warn'
  return 'neutral'
}

function feeLabel(m: SourceManifest): string {
  if (m.feeTier === 'free') return '免费'
  if (m.feeTier === 'freemium') return '免费+'
  if (m.feeTier === 'paid') return '付费'
  return '—'
}

function requiresLabel(v: SourceManifest['requires']): string {
  if (v === 'token') return '需 token'
  if (v === 'key') return '需密钥'
  if (v === 'sidecar') return '需边车'
  if (v === 'none') return '免凭证'
  return '—'
}

function viaLabel(v: string): string {
  if (v === 'go_direct') return 'Go 直连'
  if (v === 'pyworker') return 'pyworker'
  if (v === 'source_worker') return 'source-worker'
  return v || '—'
}

/** 凭证键：与 pkg/params 既有白名单拼法对齐（tushare/tavily=.token，openbb_fred=.key）。 */
function credentialKey(m: SourceManifest): string | null {
  if (m.requires === 'token') return `app.ds.${m.id}.token`
  if (m.requires === 'key') return `app.ds.${m.id}.key`
  return null
}

function aggregateHealth(name: string, rows: DataSource[]): WallCard['health'] {
  const mine = rows.filter((r) => r.name === name)
  if (!mine.length) return { enabled: false, status: '', lastAt: '', successRate: null, latencyMs: null }
  let status = ''
  let lastAt = ''
  let successRate: number | null = null
  let latencyMs: number | null = null
  for (const r of mine) {
    const st = statusOf(r)
    if (st === 'down') status = 'open'
    else if (st === 'stale' && status !== 'open') status = 'half_open'
    if (r.lastAt && r.lastAt > lastAt) lastAt = r.lastAt
    if (r.successRate != null && (successRate == null || r.successRate < successRate)) successRate = r.successRate
    if (r.latencyMs != null && (latencyMs == null || r.latencyMs > latencyMs)) latencyMs = r.latencyMs
  }
  return { enabled: mine.some((r) => r.enabled), status, lastAt, successRate, latencyMs }
}

const wallCards = computed<WallCard[]>(() => {
  const list = catalog.value?.sources ?? []
  const values = snapshot.value?.values ?? {}
  const secretSet = snapshot.value?.secretSet ?? {}
  return list.map((m) => {
    const health = aggregateHealth(m.id, sources.value)
    const ovKey = `app.ds.${m.id}.on`
    const overrideOn = Object.prototype.hasOwnProperty.call(values, ovKey) ? values[ovKey] : null
    const credKey = credentialKey(m)
    return {
      manifest: m,
      health,
      overrideOn,
      effEnabled: overrideOn === 'true' ? true : overrideOn === 'false' ? false : health.enabled,
      secretKey: credKey,
      secretSet: credKey ? secretSet[credKey] === true : false,
      qps: Object.prototype.hasOwnProperty.call(values, `app.ds.${m.id}.qps`) ? values[`app.ds.${m.id}.qps`] : null,
      quota: Object.prototype.hasOwnProperty.call(values, `app.ds.${m.id}.quota`) ? values[`app.ds.${m.id}.quota`] : null,
    }
  })
})

/** 源墙脏编辑：id → { on?, token?, qps?, quota? }。 */
const wallDraft = ref<Record<string, { on?: boolean; token?: string; qps?: string; quota?: string }>>({})

function draftOf(id: string): { on?: boolean; token?: string; qps?: string; quota?: string } {
  if (!wallDraft.value[id]) wallDraft.value = { ...wallDraft.value, [id]: {} }
  return wallDraft.value[id]
}

function isDirty(card: WallCard): boolean {
  const d = wallDraft.value[card.manifest.id]
  return !!d && (d.on !== undefined || !!d.token || !!d.qps || !!d.quota)
}

function cardOn(card: WallCard): boolean {
  const d = wallDraft.value[card.manifest.id]
  return d?.on !== undefined ? d.on : card.effEnabled
}

function setCardOn(card: WallCard, on: boolean) {
  draftOf(card.manifest.id).on = on
}

function setCardToken(card: WallCard, token: string) {
  draftOf(card.manifest.id).token = token
}

function setCardRate(card: WallCard, field: 'qps' | 'quota', value: string) {
  draftOf(card.manifest.id)[field] = value
}

async function saveCard(card: WallCard) {
  const id = card.manifest.id
  const d = wallDraft.value[id]
  if (!d || auth.isViewer()) return
  const values: Record<string, string> = {}
  if (d.on !== undefined && capSwitch.value.state === 'ok') values[`app.ds.${id}.on`] = String(d.on)
  if (d.token && card.secretKey && capSecret.value.state === 'ok') values[card.secretKey] = d.token
  if (d.qps && capQpsQuota.value.state === 'ok') values[`app.ds.${id}.qps`] = d.qps
  if (d.quota && capQpsQuota.value.state === 'ok') values[`app.ds.${id}.quota`] = d.quota
  if (Object.keys(values).length === 0) return
  await saveWithPreview(values, id, async () => {
    const next = { ...wallDraft.value }
    delete next[id]
    wallDraft.value = next
    sources.value = await listSources()
  })
}

/* ------------------------------------------------------------------ */
/* 页签 B：域视图（生效链 = /api/config 覆盖 ?? yaml 默认）               */
/* ------------------------------------------------------------------ */

interface DomainRow {
  name: string
  defaultChain: string[]
  candidates: string[]
  /** 非空 = 运行态覆盖生效；缺失 = 用 yaml 默认（空覆盖值显「用 yaml 默认」，不显 []）。 */
  override: string[] | null
  effective: string[]
  overridden: boolean
}

const domainRows = computed<DomainRow[]>(() => {
  const list = catalog.value?.domains ?? []
  const values = snapshot.value?.values ?? {}
  return list.map((d) => {
    const key = `app.domain.${d.name}.sources`
    let override: string[] | null = null
    if (Object.prototype.hasOwnProperty.call(values, key) && values[key].trim() !== '') {
      override = parseChain(values[key])
    }
    return {
      name: d.name,
      defaultChain: d.defaultChain,
      candidates: d.candidates,
      override,
      overridden: override != null,
      effective: override ?? d.defaultChain,
    }
  })
})

/** 解析覆盖链 JSON；非法值按缺失处理（不冒配 [] 当生效链）。 */
function parseChain(raw: string): string[] | null {
  try {
    const v = JSON.parse(raw)
    return Array.isArray(v) ? v.filter((x): x is string => typeof x === 'string') : null
  } catch {
    return null
  }
}

/** 域链编辑稿：name → 当前编辑中的链（进入编辑时取生效链快照）。 */
const chainDraft = ref<Record<string, string[]>>({})

function editChain(row: DomainRow): string[] {
  if (!chainDraft.value[row.name]) chainDraft.value = { ...chainDraft.value, [row.name]: [...row.effective] }
  return chainDraft.value[row.name]
}

function chainDirty(row: DomainRow): boolean {
  const draft = chainDraft.value[row.name]
  return !!draft && draft.join('|') !== row.effective.join('|')
}

function moveChain(row: DomainRow, index: number, delta: number) {
  const chain = [...editChain(row)]
  const next = index + delta
  if (next < 0 || next >= chain.length) return
  const [item] = chain.splice(index, 1)
  chain.splice(next, 0, item)
  chainDraft.value = { ...chainDraft.value, [row.name]: chain }
}

function removeChain(row: DomainRow, index: number) {
  const chain = [...editChain(row)]
  chain.splice(index, 1)
  chainDraft.value = { ...chainDraft.value, [row.name]: chain }
}

function addChain(row: DomainRow, source: string | number) {
  const name = String(source)
  const chain = editChain(row)
  if (!name || chain.includes(name)) return
  chainDraft.value = { ...chainDraft.value, [row.name]: [...chain, name] }
}

function addOptions(row: DomainRow) {
  const chain = editChain(row)
  return row.candidates.filter((c) => !chain.includes(c)).map((c) => ({ value: c, label: c }))
}

async function saveChain(row: DomainRow) {
  if (auth.isViewer() || capChain.value.state !== 'ok') return
  const draft = chainDraft.value[row.name]
  if (!draft || draft.length === 0) return
  await saveWithPreview({ [`app.domain.${row.name}.sources`]: JSON.stringify(draft) }, row.name, async () => {
    const next = { ...chainDraft.value }
    delete next[row.name]
    chainDraft.value = next
  })
}

/** 清覆盖回 yaml 默认：空串是 ValidatePatch 的 canonical 清除写法。 */
async function clearChain(row: DomainRow) {
  if (auth.isViewer() || capChain.value.state !== 'ok') return
  await saveWithPreview({ [`app.domain.${row.name}.sources`]: '' }, row.name, async () => {
    const next = { ...chainDraft.value }
    delete next[row.name]
    chainDraft.value = next
  })
}

/* ------------------------------------------------------------------ */
/* 保存与能力探测                                                        */
/* ------------------------------------------------------------------ */

async function saveWithPreview(values: Record<string, string>, tag: string, after?: () => Promise<void>) {
  const base = snapshot.value?.revision
  if (base == null) {
    error.value = '配置快照未就绪，先刷新'
    return
  }
  saving.value = tag
  error.value = ''
  hint.value = ''
  try {
    const preview = await previewConfig(values, base)
    if (preview.invalidatesPlans) {
      error.value = '该改动会使在途计划失效，已拦下（先处理计划再改）'
      return
    }
    const next = await saveConfig(values, base)
    snapshot.value = next
    hint.value = `已保存（revision ${next.revision}），采集侧约 5 秒内生效`
    await after?.()
  } catch (err) {
    if (err instanceof ApiError && err.status === 409) {
      error.value = '配置已被他人改动（revision 冲突），已重读最新快照，请重试'
      try {
        snapshot.value = await getConfigSnapshot()
      } catch {
        /* 快照重读失败由下一轮操作再报 */
      }
    } else {
      error.value = messageOf(err)
    }
  } finally {
    saving.value = ''
  }
}

/** 探测一族键是否已登记（W1 落地探测）：preview 空打不持久化，仅吃服务端校验结论。 */
async function probe(target: typeof capSwitch, values: Record<string, string>) {
  const base = snapshot.value?.revision
  if (base == null) {
    target.value = { state: 'unavailable', message: '配置快照未就绪' }
    return
  }
  try {
    await previewConfig(values, base)
    target.value = { state: 'ok', message: '' }
  } catch (err) {
    target.value = { state: 'unavailable', message: messageOf(err) }
  }
}

async function load() {
  loading.value = true
  error.value = ''
  const settled = await Promise.allSettled([getSourceCatalog(), listSources(), getQuality({ limit: 50 }), getConfigSnapshot()])
  const [cat, src, q, snap] = settled
  if (cat.status === 'fulfilled') catalog.value = cat.value
  if (src.status === 'fulfilled') sources.value = src.value
  if (q.status === 'fulfilled') quality.value = q.value
  if (snap.status === 'fulfilled') snapshot.value = snap.value
  const failures = settled.filter((s) => s.status === 'rejected') as PromiseRejectedResult[]
  if (failures.length) {
    error.value = failures.map((f) => messageOf(f.reason)).filter(Boolean).join('；') || '部分数据没取到'
  } else {
    error.value = ''
  }
  /* 写面能力探测（就绪即灰）：viewer 不必写，跳过探测直接灰。 */
  if (!auth.isViewer() && snapshot.value) {
    const dailyDefault = catalog.value?.domains.find((d) => d.name === 'daily_bar')?.defaultChain ?? ['tushare']
    await Promise.all([
      probe(capSwitch, { 'app.ds.tushare.on': 'true' }),
      probe(capChain, { 'app.domain.daily_bar.sources': JSON.stringify(dailyDefault) }),
      probe(capQpsQuota, { 'app.ds.tushare.qps': '1' }),
      probe(capSecret, { 'app.ds.tushare.token': '' }),
    ])
  }
  loading.value = false
}

/* ------------------------------------------------------------------ */
/* 页签 C：健康（只读，复用既有通路，不开第二个写面入口）                  */
/* ------------------------------------------------------------------ */

type SourceRow = DataSource & Record<string, unknown>
const healthColumns: NtTableColumn<SourceRow>[] = [
  { key: 'name', title: '数据源' },
  { key: 'kind', title: '域', mono: true },
  { key: 'successRate', title: '成功率', align: 'right' },
  { key: 'latencyMs', title: '最近延迟(ms)', align: 'right' },
  { key: 'fails', title: '成功/失败', align: 'right' },
  { key: 'lastAt', title: '最近成功', mono: true },
  { key: 'detail', title: '说明' },
  { key: 'enabled', title: '启停' },
]
const healthRows = computed(() => sources.value as unknown as SourceRow[])

type QualityRow = QualityIssue & Record<string, unknown>
const qualityColumns: NtTableColumn<QualityRow>[] = [
  { key: 'domain', title: '域', mono: true },
  { key: 'checkName', title: '检查项' },
  { key: 'symbol', title: '代码', mono: true },
  { key: 'tradeDate', title: '交易日', mono: true },
  { key: 'severity', title: '严重度' },
  { key: 'source', title: '来源', mono: true },
  { key: 'hits', title: '条数', align: 'right' },
  { key: 'lastSeen', title: '最近出现', mono: true },
  { key: 'message', title: '说明' },
]
const qualityRows = computed(() => quality.value as unknown as QualityRow[])

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">数据源中心</h1>
        <p class="nt-note">
          源目录（谁提供什么）+ 域降级链自助编辑 + 健康总览。
          {{ catalog?.servicesNote || '' }}
        </p>
      </div>
      <NtButton size="sm" :loading="loading" @click="load">刷新</NtButton>
    </header>

    <NtTabs v-model="tab" :items="tabItems" />

    <p v-if="error" class="nt-err">{{ error }}</p>
    <p v-if="hint" class="nt-note">{{ hint }}</p>
    <p v-if="auth.isViewer()" class="nt-note">viewer 只读：开关、凭证与降级链编辑需要 owner。</p>

    <!-- ============================== 页签 A：源墙 ============================== -->
    <section v-if="tab === 'wall'" class="wall">
      <p v-if="capSwitch.state === 'unavailable'" class="nt-note">
        源开关写面未就绪（服务端：{{ capSwitch.message }}），目录仍可只读。
      </p>
      <div v-if="!wallCards.length && !loading" class="nt-empty">目录没取到——点刷新重试</div>
      <div class="wall__grid">
        <article v-for="card in wallCards" :key="card.manifest.id" class="wall__card">
          <header class="wall__card-head">
            <NtStatusDot :status="card.manifest.registered ? statusOf({ enabled: card.effEnabled, status: card.health.status, lastAt: card.health.lastAt }) : 'off'" />
            <span class="wall__name">{{ card.manifest.id }}</span>
            <NtBadge v-if="card.manifest.official" tone="info">官方</NtBadge>
            <NtBadge :tone="feeTone(card.manifest.feeTier)" :title="card.manifest.feeNote || ''">{{ feeLabel(card.manifest) }}</NtBadge>
            <NtBadge v-if="!card.manifest.registered" tone="neutral">未启用</NtBadge>
          </header>
          <div class="wall__domains">
            <NtBadge v-for="d in card.manifest.domains" :key="d" tone="neutral">{{ d }}</NtBadge>
          </div>
          <dl class="wall__facts">
            <div><dt>凭证</dt><dd>{{ requiresLabel(card.manifest.requires) }}</dd></div>
            <div><dt>接入</dt><dd>{{ viaLabel(card.manifest.via) }}</dd></div>
            <div><dt>成功率</dt><dd>{{ card.health.successRate != null ? ratioPct(card.health.successRate, 0) : '—' }}</dd></div>
            <div><dt>最近延迟</dt><dd>{{ card.health.latencyMs != null ? Math.round(card.health.latencyMs) + 'ms' : '—' }}</dd></div>
          </dl>
          <p v-if="card.manifest.feeNote" class="wall__note">{{ card.manifest.feeNote }}</p>
          <p v-if="card.manifest.notes" class="wall__note">{{ card.manifest.notes }}</p>
          <p v-if="card.manifest.riskNote" class="wall__risk">风险：{{ card.manifest.riskNote }}</p>

          <div v-if="!auth.isViewer()" class="wall__write">
            <div class="wall__row">
              <span class="wall__label">启用</span>
              <NtSwitch :model-value="cardOn(card)" :disabled="capSwitch.state !== 'ok'" @update:model-value="setCardOn(card, $event)" />
              <span v-if="card.overrideOn != null" class="wall__hint">已覆盖：{{ card.overrideOn === 'true' ? '开' : '关' }}</span>
            </div>
            <div v-if="card.secretKey" class="wall__row">
              <span class="wall__label">{{ card.manifest.requires === 'key' ? '密钥' : 'token' }}</span>
              <NtInput
                :model-value="wallDraft[card.manifest.id]?.token ?? ''"
                type="password"
                :placeholder="card.secretSet ? '已设置（只写不读）' : '未设置'"
                :disabled="capSecret.state !== 'ok'"
                @update:model-value="setCardToken(card, $event)"
              />
              <NtBadge v-if="card.secretSet" tone="success" dot>已设置</NtBadge>
            </div>
            <div v-if="capQpsQuota.state === 'ok'" class="wall__row">
              <span class="wall__label">QPS/配额</span>
              <NtInput
                :model-value="wallDraft[card.manifest.id]?.qps ?? card.qps ?? ''"
                type="number"
                placeholder="QPS 未覆盖"
                @update:model-value="setCardRate(card, 'qps', $event)"
              />
              <NtInput
                :model-value="wallDraft[card.manifest.id]?.quota ?? card.quota ?? ''"
                type="number"
                placeholder="日配额 0=不限"
                @update:model-value="setCardRate(card, 'quota', $event)"
              />
            </div>
            <div class="wall__row">
              <NtButton size="sm" :disabled="!isDirty(card)" :loading="saving === card.manifest.id" @click="saveCard(card)">
                保存
              </NtButton>
            </div>
          </div>
        </article>
      </div>
    </section>

    <!-- ============================== 页签 B：域视图 ============================== -->
    <section v-else-if="tab === 'domains'" class="chains">
      <p v-if="capChain.state === 'unavailable'" class="nt-note">
        降级链写面未就绪（服务端：{{ capChain.message }}）——显示的是 yaml 默认链，运行态覆盖键落地后此处自动可编辑。
      </p>
      <div v-for="row in domainRows" :key="row.name" class="chain nt-panel">
        <header class="chain__head">
          <span class="chain__name">{{ row.name }}</span>
          <NtBadge :tone="row.overridden ? 'accent' : 'neutral'">{{ row.overridden ? '运行态覆盖生效' : 'yaml 默认' }}</NtBadge>
        </header>
        <ol class="chain__row">
          <li v-for="(src, i) in editChain(row)" :key="src" class="chain__item">
            <span class="chain__src">{{ src }}</span>
            <template v-if="!auth.isViewer() && capChain.state === 'ok'">
              <!-- P1 标记位：HTML5 draggable 原生拖拽替换上下移按钮，键族不动。 -->
              <button type="button" class="chain__btn" :disabled="i === 0" title="上移" @click="moveChain(row, i, -1)">↑</button>
              <button type="button" class="chain__btn" :disabled="i === editChain(row).length - 1" title="下移" @click="moveChain(row, i, 1)">↓</button>
              <button type="button" class="chain__btn" title="移出" @click="removeChain(row, i)">×</button>
            </template>
          </li>
        </ol>
        <div v-if="!auth.isViewer() && capChain.state === 'ok'" class="chain__edit">
          <NtSelect :options="addOptions(row)" placeholder="加挂候选源" @update:model-value="addChain(row, $event)" />
          <NtButton size="sm" :disabled="!chainDirty(row) || editChain(row).length === 0" :loading="saving === row.name" @click="saveChain(row)">
            保存
          </NtButton>
          <NtButton v-if="row.overridden" size="sm" :loading="saving === row.name" @click="clearChain(row)">恢复 yaml 默认</NtButton>
          <span class="wall__hint">链长 1–6，候选必须支持该域；保存后 5 秒内生效。</span>
        </div>
      </div>
    </section>

    <!-- ============================== 页签 C：健康 ============================== -->
    <section v-else class="health">
      <section class="nt-panel">
        <h2 class="nt-panel__title">数据源（只读；启停与手动采集走「数据源治理」面板）</h2>
        <NtTable :columns="healthColumns" :rows="healthRows" row-key="id" dense empty="还没有数据源">
          <template #cell-name="{ row }">
            <NtStatusDot :status="statusOf(row)" :label="row.name || row.id" />
          </template>
          <template #cell-successRate="{ row }">
            <span v-if="row.successRate != null" :class="{ warn: row.successRate < 0.9 }">{{ ratioPct(row.successRate, 0) }}</span>
            <span v-else class="mid" title="统计窗口内无采样，成功率无法计算">—</span>
          </template>
          <template #cell-latencyMs="{ row }">{{ row.latencyMs != null ? Math.round(row.latencyMs) : '—' }}</template>
          <template #cell-fails="{ row }">
            {{ row.successCount != null ? row.successCount : '—' }} / {{ row.failureCount != null ? row.failureCount : '—' }}
          </template>
          <template #cell-lastAt="{ row }">{{ clock(row.lastAt) }}</template>
          <template #cell-enabled="{ row }">{{ row.enabled ? '开' : '关' }}</template>
        </NtTable>
      </section>
      <section class="nt-panel">
        <h2 class="nt-panel__title">质量问题（逐条）</h2>
        <NtTable :columns="qualityColumns" :rows="qualityRows" row-key="id" dense empty="没有质量问题记录">
          <template #cell-severity="{ row }">
            <span :class="{ warn: row.severity === 'high', mid: row.severity === 'medium' }">{{ row.severity || '—' }}</span>
          </template>
          <template #cell-hits="{ row }">{{ row.hits != null ? row.hits : '—' }}</template>
          <template #cell-lastSeen="{ row }">{{ clock(row.lastSeen) }}</template>
        </NtTable>
      </section>
    </section>
  </div>
</template>

<style scoped>
.warn {
  color: var(--nt-warn);
  font-weight: 600;
}
.mid {
  color: var(--nt-text-2);
}

.wall__grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(320px, 1fr));
  gap: 12px;
}
.wall__card {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding: 12px;
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m, 8px);
}
.wall__card-head {
  display: flex;
  align-items: center;
  gap: 8px;
  flex-wrap: wrap;
}
.wall__name {
  font-family: var(--nt-font-mono);
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
}
.wall__domains {
  display: flex;
  flex-wrap: wrap;
  gap: 4px;
}
.wall__facts {
  display: grid;
  grid-template-columns: repeat(4, 1fr);
  gap: 6px;
  margin: 0;
}
.wall__facts dt {
  font-size: 11px;
  color: var(--nt-text-3);
}
.wall__facts dd {
  margin: 0;
  font-size: 12.5px;
  color: var(--nt-text-1);
}
.wall__note {
  margin: 0;
  font-size: 12px;
  color: var(--nt-text-2);
}
.wall__risk {
  margin: 0;
  font-size: 12px;
  color: var(--nt-warn);
}
.wall__write {
  display: flex;
  flex-direction: column;
  gap: 8px;
  padding-top: 8px;
  border-top: 1px solid var(--nt-border-1);
}
.wall__row {
  display: flex;
  align-items: center;
  gap: 8px;
}
.wall__row :deep(.nt-input) {
  flex: 1;
  min-width: 0;
}
.wall__label {
  flex: 0 0 64px;
  font-size: 12px;
  color: var(--nt-text-3);
}
.wall__hint {
  font-size: 11px;
  color: var(--nt-text-3);
}

.chains {
  display: flex;
  flex-direction: column;
  gap: 10px;
}
.chain__head {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-bottom: 8px;
}
.chain__name {
  font-family: var(--nt-font-mono);
  font-weight: 600;
  color: var(--nt-text-1);
}
.chain__row {
  display: flex;
  flex-wrap: wrap;
  gap: 6px;
  margin: 0;
  padding: 0;
  list-style: none;
}
.chain__item {
  display: inline-flex;
  align-items: center;
  gap: 4px;
  padding: 2px 8px;
  background: var(--nt-bg-2);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
}
.chain__src {
  font-family: var(--nt-font-mono);
  font-size: 12px;
  color: var(--nt-text-1);
}
.chain__btn {
  min-width: 20px;
  height: 20px;
  padding: 0;
  font-size: 12px;
  line-height: 18px;
  color: var(--nt-text-2);
  background: transparent;
  border: none;
  border-radius: 3px;
  cursor: pointer;
}
.chain__btn:hover:not(:disabled) {
  color: var(--nt-text-1);
  background: var(--nt-bg-3, var(--nt-bg-1));
}
.chain__btn:disabled {
  opacity: 0.35;
  cursor: not-allowed;
}
.chain__edit {
  display: flex;
  align-items: center;
  gap: 8px;
  margin-top: 8px;
  flex-wrap: wrap;
}
.chain__edit :deep(.nt-select) {
  min-width: 160px;
}

.health {
  display: flex;
  flex-direction: column;
  gap: 12px;
}
</style>
