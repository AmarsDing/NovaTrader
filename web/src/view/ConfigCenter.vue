<script lang="ts">
/**
 * palette「设置 <关键词>」直达挂点（§9.3，S2 批）：
 * nt:open-config 先于实例挂载派发（overlay 路线），模块作用域暂存 detail.category，
 * 实例 onMounted 消费；实例存活期（SettingsView 面板）由实例监听即时定位。
 * 事件合同与 workspace/function-index.ts 的 buildSettingsSpecs 同源。
 */
let pendingConfigCategory: string | null = null

function captureConfigCategory(event: Event): string | null {
  const category = (event as CustomEvent<{ category?: unknown }>).detail?.category
  return typeof category === 'string' && category ? category : null
}

window.addEventListener('nt:open-config', (event) => {
  pendingConfigCategory = captureConfigCategory(event)
})
</script>

<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import {
  getConfigSchema,
  getConfigSnapshot,
  saveConfig,
  previewConfig,
  rollbackConfig,
  type ConfigPreview,
  type ConfigSchema,
  type ConfigSchemaCategory,
  type ConfigSchemaField,
  type ConfigSnapshot,
} from '@/api/config'
import NtSchemaForm, { type SchemaFormRow } from '@/display/NtSchemaForm.vue'
import NtSourceTable from '@/display/NtSourceTable.vue'
import { toOpsSourceRow } from '@/display/source-rows'
import ConfigRevisionPanel from './ConfigRevisionPanel.vue'
import { opsStatus, type OpsNode } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { useSettingsStore, type ColorVisionName, type DensityName, type ThemeName } from '@/stores/settings'
import type { VisualQuality } from '@/theme/visual-quality'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import { getHost, getHttpPort, getWsPort, setHost, setHttpPort, setWsPort } from '@/utils/endpoint'
import { messageOf } from '@/utils/format'
import { inTauri } from '@/utils/tauri'
import { restartLink } from '@/ws/hub'
import { coerceField, fieldId, fileItems, mergeBlob, readBlobField, type ConfigCategory, type ConfigField } from './config-catalog'
import { effectiveScopeOf } from './config-field-meta'

const props = defineProps<{ overlay?: boolean }>()
const emit = defineEmits<{ (e: 'close'): void }>()

const auth = useAuthStore()
const risk = useRiskStore()
const settings = useSettingsStore()

const themeOptions: { value: ThemeName; label: string }[] = [
  { value: 'dark', label: '深色' },
  { value: 'light', label: '亮色' },
]
const densityOptions: { value: DensityName; label: string }[] = [
  { value: 'normal', label: '标准' },
  { value: 'compact', label: '紧凑' },
]
const colorVisionOptions: { value: ColorVisionName; label: string }[] = [
  { value: 'default', label: '红涨绿跌' },
  { value: 'safe', label: '色觉安全' },
]
const qualityOptions: { value: VisualQuality; label: string }[] = [
  { value: 'full', label: '完整' },
  { value: 'eco', label: '节能' },
  { value: 'reduced', label: '减少动态' },
]
const modes = ['L0', 'L1', 'L2', 'L3']
const modeHint: Record<string, string> = {
  L0: '只看',
  L1: '模拟',
  L2: '半自动实盘',
  L3: '按 L2 处理',
}

/** W7b：把后端 schema 字段映射为 catalog 既有的 ConfigField/ConfigCategory 形态（薄壳适配）。 */
function fieldFromSchema(field: ConfigSchemaField): ConfigField {
  return {
    key: field.key,
    label: field.label,
    hint: field.hint,
    kind: field.kind,
    fallback: field.fallback,
    store: field.store,
    section: field.section || undefined,
    jsonKey: field.jsonKey || undefined,
    options: field.options.length ? field.options : undefined,
    wide: field.wide || undefined,
    clearable: field.clearable || undefined,
  }
}
function categoryFromSchema(cat: ConfigSchemaCategory): ConfigCategory {
  return {
    id: cat.id,
    title: cat.title,
    note: cat.note,
    blobKey: cat.blobKey || undefined,
    fields: cat.fields.map(fieldFromSchema),
  }
}
function categoriesFromSchema(schema: ConfigSchema): ConfigCategory[] {
  return schema.categories.map(categoryFromSchema)
}

const schemaCategories = ref<ConfigCategory[] | null>(null)
const schemaError = ref('')
const schemaReady = computed(() => schemaCategories.value !== null)
const configCategories = computed<ConfigCategory[]>(() => schemaCategories.value ?? [])

const nav = computed(() => [
  { id: 'look', title: '外观' },
  { id: 'link', title: '连接与桌面' },
  ...configCategories.value.map((item) => ({ id: item.id, title: item.title })),
  { id: 'files', title: '启动项' },
])

const instanceId = Math.random().toString(36).slice(2)
const currentId = ref('look')
const stored = ref<Record<string, string>>({})
const draft = ref<Record<string, string>>({})
const baseline = ref<Record<string, string>>({})
const host = ref(getHost())
const httpPort = ref(String(getHttpPort()))
const wsPort = ref(String(getWsPort()))
const sources = ref<OpsNode[]>([])
/** W0-3b/FE-D1：链路段数据源清单归一行（状态点/状态词在 NtSourceTable 收口），本页只喂 opsStatus 数据。 */
const opsRows = computed(() => sources.value.map(toOpsSourceRow))
const auto = ref(false)
const desktop = ref(false)
const error = ref('')
const hint = ref('')
const saving = ref(false)
const ready = ref(false)
const configLoaded = ref(false)
const configSnapshot = ref<ConfigSnapshot | null>(null)
const impact = ref<ConfigPreview | null>(null)
const prepared = ref<Record<string, string>>({})
const rollbackTarget = ref<number | null>(null)
const clearSecrets = ref<string[]>([])
let configGeneration = 0

const current = computed(() => configCategories.value.find((item) => item.id === currentId.value))
const dirty = computed(() => {
  const category = current.value
  if (!category) return false
  return category.fields.some((field) => clearSecrets.value.includes(field.key) || draft.value[fieldId(category.blobKey, field)] !== baseline.value[fieldId(category.blobKey, field)])
})

/** 密文三态（§9.1 章口径）：清除待保存=clearing；否则按服务端 secretSet。 */
function secretStateOf(field: ConfigField): 'unset' | 'set' | 'clearing' {
  if (clearSecrets.value.includes(field.key)) return 'clearing'
  return configSnapshot.value?.secretSet[field.key] ? 'set' : 'unset'
}

/** 字段卡回写草稿：bool 归一 'true'/'false' 字串（strategy_config 存字串），其余显式转串。 */
function onSchemaValue(id: string, value: unknown, field: { kind: ConfigField['kind'] }) {
  if (field.kind === 'bool') setDraft(id, value === true ? 'true' : 'false')
  else setDraft(id, value == null ? '' : String(value))
}

/** 当前分类 → NtSchemaForm rows（含生效范围与密文态，display 层不读 store）。 */
const schemaRows = computed<SchemaFormRow[]>(() => {
  const category = current.value
  if (!category) return []
  return category.fields.map((field) => ({
    id: fieldId(category.blobKey, field),
    field: {
      key: field.key,
      label: field.label,
      hint: field.hint || undefined,
      kind: field.kind,
      fallback: field.fallback,
      section: field.section,
      options: field.options,
      wide: field.wide,
      clearable: field.clearable,
    },
    value: draft.value[fieldId(category.blobKey, field)] ?? '',
    secretState: field.kind === 'secret' ? secretStateOf(field) : undefined,
    effective: effectiveScopeOf(field.key),
  }))
})

function pickField(category: ConfigCategory, field: ConfigField): string {
  if (field.kind === 'secret') return ''
  if (field.store === 'risk') return stored.value[field.key] ?? field.fallback
  if (category.blobKey && field.jsonKey) {
    return readBlobField(stored.value[category.blobKey] || '', field.jsonKey, field.fallback)
  }
  const raw = stored.value[field.key]
  return raw != null && raw !== '' ? raw : field.fallback
}

function fill(category?: ConfigCategory) {
  const list = category ? [category] : configCategories.value
  const next = { ...draft.value }
  const base = { ...baseline.value }
  for (const item of list) {
    for (const field of item.fields) {
      const id = fieldId(item.blobKey, field)
      const value = pickField(item, field)
      next[id] = value
      base[id] = value
    }
  }
  draft.value = next
  baseline.value = base
}

async function load() {
  const generation = ++configGeneration
  error.value = ''
  void risk.refresh()
  try {
    const next = await getConfigSnapshot()
    if (generation !== configGeneration) return
    configSnapshot.value = next
    stored.value = configSnapshot.value.values
    impact.value = null
    rollbackTarget.value = null
    clearSecrets.value = []
    configLoaded.value = true
  } catch (err) {
    if (generation !== configGeneration) return
    stored.value = {}
    configLoaded.value = false
    error.value = messageOf(err)
  }
  fill()
  ready.value = true
}

function select(id: string) {
  configGeneration++
  impact.value = null
  rollbackTarget.value = null
  currentId.value = id
  if (!configLoaded.value && !ready.value) void load()
  error.value = ''
  hint.value = ''
}

function setDraft(id: string, value: string) {
  configGeneration++
  impact.value = null
  rollbackTarget.value = null
  clearSecrets.value = clearSecrets.value.filter(key => key !== id)
  draft.value = { ...draft.value, [id]: value }
}

function jumpToCategory(id: unknown) {
  if (typeof id !== 'string' || !id || !nav.value.some((item) => item.id === id)) return
  select(id)
}

/** 存活期直达：实例已挂载时（SettingsView 面板）消费事件并清模块暂存，防下次挂载误跳。 */
function onConfigJump(event: Event) {
  const category = captureConfigCategory(event)
  pendingConfigCategory = null
  if (category) jumpToCategory(category)
}

async function saveHost() {
  error.value = ''
  hint.value = ''
  try {
    host.value = setHost(host.value)
    httpPort.value = String(setHttpPort(httpPort.value))
    wsPort.value = String(setWsPort(wsPort.value))
    restartLink()
    hint.value = `已改为 admin ${host.value} 的 ${httpPort.value} / ${wsPort.value}`
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function switchMode(mode: string) {
  error.value = ''
  hint.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能改模式'
    return
  }
  try {
    await risk.switchMode({ mode, operator: auth.operatorName, reason: '配置页切换运行模式' })
    hint.value = '模式已提交'
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function toggleAuto() {
  error.value = ''
  try {
    const mod = await import('@tauri-apps/plugin-autostart')
    if (auto.value) await mod.disable()
    else await mod.enable()
    auto.value = await mod.isEnabled()
  } catch (err) {
    error.value = messageOf(err)
  }
}

function payloadOf(field: ConfigField, raw: string): string {
  const text = raw.trim()
  if (field.kind === 'bool') return text === 'true' ? 'true' : 'false'
  if (field.kind === 'json') {
    if (!text) throw new Error('请填写 JSON')
    const parsed = JSON.parse(text) as unknown
    if (!parsed || typeof parsed !== 'object' || Array.isArray(parsed)) throw new Error('系数要是 JSON 对象')
    return text
  }
  if (field.kind === 'select') {
    if (!field.options?.some((opt) => opt.value === text)) throw new Error('请从列表里选择')
    return text
  }
  if (field.kind === 'text' || field.kind === 'secret') {
    if (!text) throw new Error('请填写内容')
    return text
  }
  coerceField(field.kind, text)
  return text
}

async function refreshReceipts() {
  try { const next = await getConfigSnapshot(); if (configSnapshot.value?.revision !== next.revision) { hint.value = '保存版本已改变，请重新读取并核对草稿'; return }; configSnapshot.value = next } catch (err) { error.value = messageOf(err) }
}
function clearSecret(key: string) { configGeneration++; impact.value = null; rollbackTarget.value = null; clearSecrets.value = [...new Set([...clearSecrets.value, key])]; draft.value[key] = '' }
function currentValues(): Record<string, string> {
  const category = current.value
  if (!category) return {}
  const values: Record<string, string> = {}
  const patch: Record<string, string | number> = {}
  for (const field of category.fields) {
    const id = fieldId(category.blobKey, field)
    if (clearSecrets.value.includes(field.key)) { values[field.key] = ''; continue }
    if (draft.value[id] === baseline.value[id]) continue
    if (category.blobKey) patch[field.jsonKey || field.key] = coerceField(field.kind, draft.value[id] || '')
    else if (field.clearable && !(draft.value[id] || '').trim()) values[field.key] = ''
    else values[field.key] = payloadOf(field, draft.value[id] || '')
  }
  if (category.blobKey && Object.keys(patch).length) values[category.blobKey] = mergeBlob(stored.value[category.blobKey] || '', patch)
  return values
}
async function previewRollback(target: number) {
  if (!configSnapshot.value || auth.isViewer()) return
  const generation = ++configGeneration
  try { error.value = ''; const next = await previewConfig({}, configSnapshot.value.revision, target); if (generation !== configGeneration) return; impact.value = next; rollbackTarget.value = target; prepared.value = {} } catch (err) { error.value = messageOf(err) }
}
async function saveCurrent() {
  if (!current.value || saving.value || auth.isViewer()) return
  if (schemaError.value) { error.value = '配置 schema 不可达，不能保存（R-UI-38）'; return }
  if (!configLoaded.value || !configSnapshot.value) { error.value = '还没读到版本化配置，无法保存'; return }
  error.value = ''; hint.value = ''; saving.value = true
  const generation = configGeneration
  try {
    if (!impact.value) {
      prepared.value = currentValues()
      if (!Object.keys(prepared.value).length) { hint.value = '这一类没有改动'; return }
      const next = await previewConfig(prepared.value, configSnapshot.value.revision)
      // FE-N5：会使在途计划失效的改动统一拦下（对齐 datasource-center 既有做法）
      if (next.invalidatesPlans) { error.value = '该改动会使在途计划失效，已拦下（先处理计划再改）'; return }
      if (generation === configGeneration) impact.value = next
      return
    }
    const saved = rollbackTarget.value == null ? await saveConfig(prepared.value, impact.value.baseRevision) : await rollbackConfig(impact.value.baseRevision, rollbackTarget.value)
    if (generation !== configGeneration) { hint.value = `版本 #${saved.revision} 已保存，当前新草稿已保留，请重新读取核对`; return }
    configSnapshot.value = saved; stored.value = saved.values; impact.value = null; rollbackTarget.value = null; clearSecrets.value = []
    fill()
    hint.value = `已保存版本 #${saved.revision}，请查看目标服务加载回执`
    window.dispatchEvent(new CustomEvent('nt:config-saved', { detail: instanceId }))
    void risk.refresh()
  } catch (err) { error.value = err instanceof SyntaxError ? 'JSON 格式不对' : messageOf(err) }
  finally { saving.value = false }
}

function onSaved(event: Event) {
  if ((event as CustomEvent<string>).detail === instanceId) return
  if (dirty.value) { hint.value = '另一窗口修改了配置；草稿已保留，保存时会校验版本'; return }
  void load()
}

onMounted(async () => {
  window.addEventListener('nt:config-saved', onSaved)
  window.addEventListener('nt:open-config', onConfigJump)
  desktop.value = inTauri()
  // W7b：先把 schema 拉上来再决定渲染面。schema 不可达时表单整组缺位（R-UI-38）——
  // 不静默回退到旧硬编码目录（漂移口径会误导用户去保存不存在的字段）。
  // 深链跳转必须等 schema 入 nav 之后，否则 jumpToCategory 找不到分类。
  try {
    const schema = await getConfigSchema()
    schemaCategories.value = categoriesFromSchema(schema)
    schemaError.value = ''
  } catch (err) {
    schemaCategories.value = []
    schemaError.value = messageOf(err) || '配置 schema 不可达'
  }
  if (pendingConfigCategory) {
    const category = pendingConfigCategory
    pendingConfigCategory = null
    jumpToCategory(category)
  }
  void load()
  try {
    const data = await opsStatus()
    sources.value = data.sources
  } catch {
    sources.value = []
  }
  if (!desktop.value) return
  try {
    const { isEnabled } = await import('@tauri-apps/plugin-autostart')
    auto.value = await isEnabled()
  } catch {
    auto.value = false
  }
})

onBeforeUnmount(() => {
  window.removeEventListener('nt:config-saved', onSaved)
  window.removeEventListener('nt:open-config', onConfigJump)
})
</script>

<template>
  <div class="cfg" :class="{ overlay }">
    <header class="cfg__head">
      <div>
        <h1 class="nt-title">配置</h1>
        <p class="nt-note">按分类修改运行参数。保存成功表示配置已存储，各服务是否加载仍需核验。外观和连接保存在这台电脑。</p>
      </div>
      <NtButton v-if="props.overlay" size="sm" @click="emit('close')">关闭</NtButton>
    </header>

    <div class="cfg__body">
      <nav class="cfg__nav" aria-label="配置分类">
        <button
          v-for="item in nav"
          :key="item.id"
          type="button"
          :class="{ on: currentId === item.id }"
          @click="select(item.id)"
        >
          {{ item.title }}
        </button>
      </nav>

      <div class="cfg__main">
        <p v-if="schemaError" class="cfg-schema-error" role="alert" data-testid="schema-error">
          配置结构已变更或不可达：{{ schemaError }}。字段目录由后端下发，本次不能保存任何配置分类（R-UI-38）。
        </p>
        <section v-if="currentId === 'look'" class="block">
          <p class="nt-note">改完立即作用在这个窗口。</p>
          <div class="pref">
            <span>主题</span>
            <NtSegmented
              :model-value="settings.theme"
              :options="themeOptions"
              @update:model-value="settings.setTheme($event as ThemeName)"
            />
          </div>
          <div class="pref">
            <span>密度</span>
            <NtSegmented
              :model-value="settings.density"
              :options="densityOptions"
              @update:model-value="settings.setDensity($event as DensityName)"
            />
          </div>
          <div class="pref">
            <span>色觉</span>
            <NtSegmented
              :model-value="settings.colorVision"
              :options="colorVisionOptions"
              @update:model-value="settings.setColorVision($event as ColorVisionName)"
            />
          </div>
          <p class="nt-note quality-note">
            {{ settings.colorVision === 'safe' ? '色觉安全：涨跌与成败改为朱红/钴蓝暖冷对，红绿色盲下仍可分辨；A 股红涨绿跌约定以暖冷方式保留。' : '红涨绿跌：A 股惯例配色，正常视觉下区分度最佳。' }}
          </p>
          <div class="pref">
            <span>显示质量</span>
            <NtSegmented
              :model-value="settings.visualQuality"
              :options="qualityOptions"
              @update:model-value="settings.setVisualQuality($event as VisualQuality)"
            />
          </div>
          <p class="nt-note quality-note">
            {{ settings.visualQuality === 'eco' ? '节能：使用实体背板，关闭玻璃模糊、光晕与装饰运动。' : settings.visualQuality === 'reduced' ? '减少动态：保留玻璃和静态光轨，停止装饰运动。' : '完整：分层玻璃、局部霓虹与真实事件反馈。' }}
            <template v-if="settings.systemReducedMotion">系统已要求减少动态，动态效果已关闭。</template>
            数据更新、告警与交易规则保持运行；选择会自动保存。
          </p>
          <div class="material-preview" aria-hidden="true">
            <span class="material-preview__rail"></span>
            <div><b>深海霓虹玻璃</b><small>壳层透光 · 数据清晰 · 状态可辨</small></div>
          </div>
          <div class="pref">
            <span>预警声</span>
            <NtSwitch
              :model-value="settings.sound"
              :label="settings.sound ? '极端预警时循环响铃' : '已静音'"
              @update:model-value="settings.setSound($event)"
            />
          </div>
        </section>

        <section v-else-if="currentId === 'link'" class="block">
          <p class="nt-note">填 admin 主机与端口。默认 HTTP 2001、WebSocket 2003；共享机部署可改。关闭窗口会缩进托盘。</p>
          <form class="nt-form" @submit.prevent="saveHost">
            <label class="grow"><span>admin 主机</span><NtInput v-model="host" mono /></label>
            <label><span>HTTP</span><NtInput v-model="httpPort" mono /></label>
            <label><span>WS</span><NtInput v-model="wsPort" mono /></label>
            <NtButton variant="primary" @click="saveHost">保存</NtButton>
          </form>
          <NtButton v-if="desktop" @click="toggleAuto">开机自启：{{ auto ? '已开' : '已关' }}</NtButton>
          <p v-else class="nt-empty">开机自启和系统通知只在桌面壳里可用。令牌放在系统凭据库。</p>
          <!-- W0-3b/FE-D1：数据源清单=NtSourceTable(ops) 单一事实组件，与 OpsView 数据源块同源。
               健康状态（灯/成功率/新鲜度）在数据源面板，此处只回答「连没连上」。 -->
          <NtSourceTable variant="ops" :ops="opsRows" empty="数据源清单未返回。健康状态在数据源面板。" />
        </section>

        <section v-else-if="currentId === 'mode'" class="block">
          <p class="nt-note">当前 {{ risk.mode || '—' }}。切到 L2 / L3 需要实盘准入已经打开。</p>
          <div class="modes">
            <NtButton
              v-for="mode in modes"
              :key="mode"
              :variant="risk.mode === mode ? 'primary' : 'ghost'"
              :disabled="auth.isViewer()"
              @click="switchMode(mode)"
            >
              {{ mode }} {{ modeHint[mode] }}
            </NtButton>
            <NtButton :disabled="saving" @click="load">重新读取配置</NtButton>
          </div>
        </section>

        <section v-else-if="current" class="block">
          <p class="nt-note">{{ current.note }}</p>
          <NtSchemaForm
            v-if="ready"
            :rows="schemaRows"
            :layout="current.id === 'datasource' ? 'cards' : 'grid'"
            :disabled="auth.isViewer() || !!schemaError"
            :schema-error="schemaError"
            @update:value="onSchemaValue"
            @clear="clearSecret"
          />
          <p v-else class="nt-empty">正在读取当前值。</p>
          <template v-if="!schemaError">
            <ConfigRevisionPanel :snapshot="configSnapshot" :preview="impact" :dirty="dirty" @refresh="refreshReceipts" @rollback="previewRollback" />
            <div class="savebar">
              <NtButton variant="primary" :disabled="auth.isViewer() || saving || (!dirty && !impact)" @click="saveCurrent">
                {{ impact ? (rollbackTarget == null ? '确认保存新版本' : '确认回滚为新版本') : '预览本类变更' }}
              </NtButton>
              <NtButton :disabled="saving" @click="load">重新读取配置</NtButton>
            </div>
          </template>
        </section>
        <section v-else-if="!schemaReady" class="block">
          <p class="nt-empty">正在读取字段目录。</p>
        </section>

        <section v-else class="block">
          <p class="nt-note">
            下面这些仍由服务在启动时从 backend/configs/config.yaml 读取，改完需要重启对应服务。数据接口、模型、智能体和通知在左边的分类里。
          </p>
          <ul class="files">
            <li v-for="item in fileItems" :key="item.name">
              <strong>{{ item.name }}</strong>
              <span>{{ item.where }}</span>
            </li>
          </ul>
        </section>

        <NtErrorGuide :error="error" />
        <p v-if="hint" class="nt-note">{{ hint }}</p>
      </div>
    </div>
  </div>
</template>

<style scoped>
.cfg {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-height: 0;
  height: 100%;
}

.cfg.overlay {
  width: min(1080px, 100%);
  height: min(860px, calc(100vh - 48px));
  padding: 16px 18px;
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  backdrop-filter: var(--nt-glass-filter);
  border: 1px solid var(--nt-glass-border);
  border-radius: var(--nt-radius-m);
  box-shadow: var(--nt-edge-light), var(--nt-shadow-depth), var(--nt-glow-soft);
}

.cfg__head {
  display: flex;
  justify-content: space-between;
  gap: 12px;
  align-items: flex-start;
}

.cfg-schema-error {
  margin: 0;
  padding: 10px 12px;
  color: var(--nt-warn);
  background: color-mix(in srgb, var(--nt-warn) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--nt-warn) 35%, transparent);
  border-radius: var(--nt-radius-s);
  font-size: var(--nt-fs-caption);
}

.cfg__body {
  display: grid;
  grid-template-columns: 168px minmax(0, 1fr);
  gap: 16px;
  min-height: 0;
  flex: 1;
}

.cfg__nav {
  display: flex;
  flex-direction: column;
  gap: 2px;
  align-self: start;
  position: sticky;
  top: 0;
}

.cfg__nav button {
  height: 30px;
  padding: 0 10px;
  text-align: left;
  color: var(--nt-text-2);
  background: transparent;
  border: none;
  border-radius: var(--nt-radius-s);
  font-size: 12.5px;
}

.cfg__nav button.on,
.cfg__nav button:hover {
  color: var(--nt-text-1);
  background: var(--nt-accent-soft);
}

.cfg__nav button.on {
  box-shadow: inset 2px 0 var(--nt-accent), var(--nt-glow-soft);
}

.cfg__main {
  min-width: 0;
  overflow: auto;
  padding-right: 4px;
}

.block {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.pref {
  display: flex;
  align-items: center;
  gap: 16px;
  flex-wrap: wrap;
}

.pref > span {
  width: 60px;
  color: var(--nt-text-2);
  font-size: 12.5px;
}

.quality-note {
  max-width: 620px;
}

.material-preview {
  display: flex;
  align-items: center;
  gap: 12px;
  max-width: 620px;
  padding: 16px;
  border: 1px solid var(--nt-glass-border);
  border-radius: var(--nt-radius-m);
  background: var(--nt-glass-sheen), var(--nt-glass-raised);
  box-shadow: var(--nt-edge-light), var(--nt-glow-soft);
}

.material-preview__rail {
  width: 3px;
  height: 32px;
  background: var(--nt-accent);
  box-shadow: var(--nt-glow-accent);
}

.material-preview b,
.material-preview small {
  display: block;
  font-size: 12px;
  font-weight: 500;
}

.material-preview small {
  margin-top: 5px;
  color: var(--nt-text-2);
  font-weight: 400;
}

.modes {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
}

.grid {
  display: grid;
  grid-template-columns: repeat(auto-fill, minmax(220px, 1fr));
  gap: 12px 14px;
}

.src-cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.src-card {
  display: flex;
  flex-direction: column;
  gap: 10px;
  padding: 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-0);
}

.src-card__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.src-card__head h3 {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.src-card__body {
  display: grid;
  grid-template-columns: 1fr 1fr;
  gap: 10px 14px;
}

.src-card__body .field.wide {
  grid-column: 1 / -1;
}

.sec {
  grid-column: 1 / -1;
  margin: 4px 0 0;
  font-size: 12px;
  font-weight: 600;
  color: var(--nt-text-2);
}

.field {
  display: flex;
  flex-direction: column;
  gap: 4px;
  min-width: 0;
}

.field.wide {
  grid-column: 1 / -1;
}

.savebar {
  display: flex;
  align-items: center;
  gap: 10px;
}

/* W0-3b：数据源清单改走 NtSourceTable(ops)，.src 列表样式随 ul 一并退役；.files 保留。 */
.files {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.files li {
  display: flex;
  flex-direction: column;
  gap: 2px;
  padding-bottom: 6px;
  border-bottom: 1px solid var(--nt-border-1);
}

.files strong {
  font-size: 12.5px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.files span {
  color: var(--nt-text-2);
  font-size: 12px;
}

@media (max-width: 720px) {
  .cfg__body {
    grid-template-columns: 1fr;
  }

  .cfg__nav {
    flex-direction: row;
    flex-wrap: wrap;
    position: static;
  }
}
</style>
