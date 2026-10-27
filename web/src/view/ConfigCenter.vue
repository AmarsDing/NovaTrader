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
import { getConfigSnapshot, saveConfig, previewConfig, rollbackConfig, type ConfigSnapshot, type ConfigPreview } from '@/api/config'
import ConfigRevisionPanel from './ConfigRevisionPanel.vue'
import { opsStatus, type OpsNode } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { useSettingsStore, type ColorVisionName, type DensityName, type ThemeName } from '@/stores/settings'
import type { VisualQuality } from '@/theme/visual-quality'
import NtButton from '@/ui/NtButton.vue'
import NtConfigField, { type ConfigFieldLite } from '@/ui/NtConfigField.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtSwitch from '@/ui/NtSwitch.vue'
import { getHost, setHost } from '@/utils/endpoint'
import { messageOf } from '@/utils/format'
import { inTauri } from '@/utils/tauri'
import { restartLink } from '@/ws/hub'
import {
  coerceField,
  configCategories,
  fieldId,
  fileItems,
  mergeBlob,
  readBlobField,
  type ConfigCategory,
  type ConfigField,
} from './config-catalog'
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

const nav = [
  { id: 'look', title: '外观' },
  { id: 'link', title: '连接与桌面' },
  ...configCategories.map((item) => ({ id: item.id, title: item.title })),
  { id: 'files', title: '启动项' },
]

const instanceId = Math.random().toString(36).slice(2)
const currentId = ref('look')
const stored = ref<Record<string, string>>({})
const draft = ref<Record<string, string>>({})
const baseline = ref<Record<string, string>>({})
const host = ref(getHost())
const sources = ref<OpsNode[]>([])
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

const current = computed(() => configCategories.find((item) => item.id === currentId.value))
const dirty = computed(() => {
  const category = current.value
  if (!category) return false
  return category.fields.some((field) => clearSecrets.value.includes(field.key) || draft.value[fieldId(category.blobKey, field)] !== baseline.value[fieldId(category.blobKey, field)])
})

function showSection(category: ConfigCategory, index: number): boolean {
  const section = category.fields[index]?.section
  if (!section) return false
  return index === 0 || category.fields[index - 1]?.section !== section
}

function sectionBlocks(category: ConfigCategory): { title: string; fields: ConfigField[] }[] {
  const blocks: { title: string; fields: ConfigField[] }[] = []
  for (const field of category.fields) {
    const title = field.section || '其他'
    const last = blocks[blocks.length - 1]
    if (last && last.title === title) last.fields.push(field)
    else blocks.push({ title, fields: [field] })
  }
  return blocks
}

/** 五件套映射（§9.1 第一件）：catalog 字段 → 字段卡 props；clearable 留空提示进 placeholder。 */
function liteOf(field: ConfigField): ConfigFieldLite {
  return {
    key: field.key,
    label: field.label,
    kind: field.kind,
    hint: field.hint || undefined,
    options: field.options,
    placeholder: field.clearable ? '留空跟随配置文件' : undefined,
  }
}

/** 密文三态（§9.1 章口径）：清除待保存=clearing；否则按服务端 secretSet。 */
function secretStateOf(field: ConfigField): 'unset' | 'set' | 'clearing' {
  if (clearSecrets.value.includes(field.key)) return 'clearing'
  return configSnapshot.value?.secretSet[field.key] ? 'set' : 'unset'
}

/** 字段卡回写草稿：bool 归一 'true'/'false' 字串（strategy_config 存字串），其余显式转串。 */
function onFieldValue(field: ConfigField, id: string, value: unknown) {
  if (field.kind === 'bool') setDraft(id, value === true ? 'true' : 'false')
  else setDraft(id, value == null ? '' : String(value))
}

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
  const list = category ? [category] : configCategories
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
  if (typeof id !== 'string' || !id || !nav.some((item) => item.id === id)) return
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
    restartLink()
    hint.value = '已改为 admin ' + host.value + ' 的 2001 / 2003'
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
  if (!configLoaded.value || !configSnapshot.value) { error.value = '还没读到版本化配置，无法保存'; return }
  error.value = ''; hint.value = ''; saving.value = true
  const generation = configGeneration
  try {
    if (!impact.value) {
      prepared.value = currentValues()
      if (!Object.keys(prepared.value).length) { hint.value = '这一类没有改动'; return }
      const next = await previewConfig(prepared.value, configSnapshot.value.revision)
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
  // overlay 路线：事件先于挂载派发，模块暂存在这里消费（无暂存时落在默认分组）。
  if (pendingConfigCategory) {
    const category = pendingConfigCategory
    pendingConfigCategory = null
    jumpToCategory(category)
  }
  desktop.value = inTauri()
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
          <p class="nt-note">只填 admin 主机。HTTP 固定 2001，WebSocket 固定 2003。关闭窗口会缩进托盘。</p>
          <form class="nt-form" @submit.prevent="saveHost">
            <label class="grow"><span>admin 主机</span><NtInput v-model="host" mono /></label>
            <NtButton variant="primary" @click="saveHost">保存</NtButton>
          </form>
          <NtButton v-if="desktop" @click="toggleAuto">开机自启：{{ auto ? '已开' : '已关' }}</NtButton>
          <p v-else class="nt-empty">开机自启和系统通知只在桌面壳里可用。令牌放在系统凭据库。</p>
          <ul class="src">
            <li v-for="row in sources" :key="row.name">{{ row.name }} · {{ row.status || '—' }} · {{ row.detail }}</li>
          </ul>
          <p v-if="!sources.length" class="nt-empty">数据源清单未返回。健康状态在数据源面板。</p>
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
          <div v-if="ready && current.id === 'datasource'" class="src-cards">
            <article v-for="block in sectionBlocks(current)" :key="block.title" class="src-card">
              <header class="src-card__head">
                <div>
                  <h3>{{ block.title }}</h3>
                </div>
              </header>
              <div class="src-card__body">
                <div
                  v-for="field in block.fields"
                  :key="fieldId(current.blobKey, field)"
                  class="field"
                  :class="{ wide: field.kind !== 'bool' }"
                >
                  <NtConfigField
                    :field="liteOf(field)"
                    :model-value="field.kind === 'secret' ? '' : (draft[fieldId(current.blobKey, field)] ?? '')"
                    :default-value="field.kind === 'secret' ? undefined : field.fallback"
                    :disabled="auth.isViewer()"
                    :secret-state="field.kind === 'secret' ? secretStateOf(field) : undefined"
                    :effective="effectiveScopeOf(field.key)"
                    @update:model-value="onFieldValue(field, fieldId(current.blobKey, field), $event)"
                    @clear="clearSecret(field.key)"
                  />
                  <NtInput
                    v-if="field.kind === 'secret'"
                    type="password"
                    :model-value="draft[fieldId(current.blobKey, field)] || ''"
                    :placeholder="configSnapshot?.secretSet[field.key] ? '留空保留，输入替换' : '填写后保存生效'"
                    :disabled="auth.isViewer()"
                    @update:model-value="setDraft(fieldId(current.blobKey, field), $event)"
                  />
                </div>
              </div>
            </article>
          </div>
          <div v-else-if="ready" class="grid">
            <template v-for="(field, index) in current.fields" :key="fieldId(current.blobKey, field)">
              <h3 v-if="showSection(current, index)" class="sec">{{ field.section }}</h3>
              <div class="field" :class="{ wide: field.kind === 'json' }">
                <NtConfigField
                  :field="liteOf(field)"
                  :model-value="field.kind === 'secret' ? '' : (draft[fieldId(current.blobKey, field)] ?? '')"
                  :default-value="field.kind === 'secret' ? undefined : field.fallback"
                  :disabled="auth.isViewer()"
                  :secret-state="field.kind === 'secret' ? secretStateOf(field) : undefined"
                  :effective="effectiveScopeOf(field.key)"
                  @update:model-value="onFieldValue(field, fieldId(current.blobKey, field), $event)"
                  @clear="clearSecret(field.key)"
                />
                <NtInput
                  v-if="field.kind === 'secret'"
                  type="password"
                  :model-value="draft[fieldId(current.blobKey, field)] || ''"
                  :placeholder="configSnapshot?.secretSet[field.key] ? '留空保留，输入替换' : '填写后保存生效'"
                  :disabled="auth.isViewer()"
                  @update:model-value="setDraft(fieldId(current.blobKey, field), $event)"
                />
              </div>
            </template>
          </div>
          <p v-else class="nt-empty">正在读取当前值。</p>
          <ConfigRevisionPanel :snapshot="configSnapshot" :preview="impact" :dirty="dirty" @refresh="refreshReceipts" @rollback="previewRollback" />
          <div class="savebar">
            <NtButton variant="primary" :disabled="auth.isViewer() || saving || (!dirty && !impact)" @click="saveCurrent">
              {{ impact ? (rollbackTarget == null ? '确认保存新版本' : '确认回滚为新版本') : '预览本类变更' }}
            </NtButton>
            <NtButton :disabled="saving" @click="load">重新读取配置</NtButton>
          </div>
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

.src,
.files {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.src {
  color: var(--nt-text-2);
  font-size: 12.5px;
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
