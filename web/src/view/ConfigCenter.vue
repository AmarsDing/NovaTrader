<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { getConfigValues, saveConfigValues } from '@/api/config'
import { opsStatus, type OpsNode } from '@/api/ops'
import { useAuthStore } from '@/stores/auth'
import { useRiskStore } from '@/stores/risk'
import { useSettingsStore, type DensityName, type ThemeName } from '@/stores/settings'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSegmented from '@/ui/NtSegmented.vue'
import NtSelect from '@/ui/NtSelect.vue'
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

const current = computed(() => configCategories.find((item) => item.id === currentId.value))
const dirty = computed(() => {
  const category = current.value
  if (!category) return false
  return category.fields.some((field) => draft.value[fieldId(category.blobKey, field)] !== baseline.value[fieldId(category.blobKey, field)])
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

function headerSwitch(fields: ConfigField[]): ConfigField | undefined {
  const bools = fields.filter((field) => field.kind === 'bool')
  if (bools.length !== 1 || fields.length === 1) return undefined
  return bools[0]
}

function bodyFields(fields: ConfigField[]): ConfigField[] {
  const head = headerSwitch(fields)
  if (!head) return fields
  return fields.filter((field) => field !== head)
}

function pickRisk(field: ConfigField): string {
  const raw = risk.params[field.key]
  if (typeof raw === 'string') return raw
  if (typeof raw === 'number' || typeof raw === 'boolean') return String(raw)
  return field.fallback
}

function pickField(category: ConfigCategory, field: ConfigField): string {
  if (field.store === 'risk') return pickRisk(field)
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
  error.value = ''
  await risk.refresh()
  try {
    stored.value = await getConfigValues()
    configLoaded.value = true
  } catch (err) {
    stored.value = {}
    configLoaded.value = false
    error.value = messageOf(err)
  }
  fill()
  ready.value = true
}

function select(id: string) {
  currentId.value = id
  error.value = ''
  hint.value = ''
}

function setDraft(id: string, value: string) {
  draft.value = { ...draft.value, [id]: value }
}

function checked(id: string): boolean {
  return draft.value[id] === 'true'
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

async function saveCurrent() {
  const category = current.value
  if (!category || saving.value) return
  error.value = ''
  hint.value = ''
  if (auth.isViewer()) {
    error.value = '当前是查看角色，不能改配置'
    return
  }
  const riskReady = Object.keys(risk.params).length > 0
  if (category.fields[0]?.store === 'risk' && !riskReady) {
    error.value = '还没读到风控参数，先别保存'
    return
  }
  if (category.fields[0]?.store === 'config' && !configLoaded.value) {
    error.value = '还没读到参数表，先别保存'
    return
  }
  saving.value = true
  try {
    if (category.blobKey) {
      const patch: Record<string, string | number> = {}
      for (const field of category.fields) {
        const id = fieldId(category.blobKey, field)
        if (draft.value[id] === baseline.value[id]) continue
        patch[field.jsonKey || field.key] = coerceField(field.kind, draft.value[id] || '')
      }
      if (!Object.keys(patch).length) {
        hint.value = '这一类没有改动'
        return
      }
      stored.value = await saveConfigValues({ [category.blobKey]: mergeBlob(stored.value[category.blobKey] || '', patch) })
    } else if (category.fields[0]?.store === 'risk') {
      for (const field of category.fields) {
        const id = fieldId(category.blobKey, field)
        if (draft.value[id] === baseline.value[id]) continue
        const value = payloadOf(field, draft.value[id] || '')
        await risk.saveParam({ key: field.key, value, operator: auth.operatorName })
      }
    } else {
      const values: Record<string, string> = {}
      for (const field of category.fields) {
        const id = fieldId(category.blobKey, field)
        if (draft.value[id] === baseline.value[id]) continue
        values[field.key] = payloadOf(field, draft.value[id] || '')
      }
      if (!Object.keys(values).length) {
        hint.value = '这一类没有改动'
        return
      }
      stored.value = await saveConfigValues(values)
    }
    fill(category)
    hint.value = category.fields[0]?.store === 'risk' ? '已生效' : '已写入参数表'
    window.dispatchEvent(new CustomEvent('nt:config-saved', { detail: instanceId }))
  } catch (err) {
    error.value = err instanceof SyntaxError ? 'JSON 格式不对' : messageOf(err)
  } finally {
    saving.value = false
  }
}

function onSaved(event: Event) {
  if ((event as CustomEvent<string>).detail === instanceId) return
  void load()
}

onMounted(async () => {
  window.addEventListener('nt:config-saved', onSaved)
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
})
</script>

<template>
  <div class="cfg" :class="{ overlay }">
    <header class="cfg__head">
      <div>
        <h1 class="nt-title">配置</h1>
        <p class="nt-note">按分类修改。数据接口、模型、智能体、通知、选股和风控写入参数表，几秒内盖过配置文件。外观和连接保存在这台电脑。</p>
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
          </div>
        </section>

        <section v-else-if="current" class="block">
          <p class="nt-note">{{ current.note }}</p>
          <div v-if="ready && current.id === 'datasource'" class="src-cards">
            <article v-for="block in sectionBlocks(current)" :key="block.title" class="src-card">
              <header class="src-card__head">
                <div>
                  <h3>{{ block.title }}</h3>
                  <p v-if="headerSwitch(block.fields)?.hint" class="field__hint">{{ headerSwitch(block.fields)?.hint }}</p>
                </div>
                <NtSwitch
                  v-if="headerSwitch(block.fields)"
                  :model-value="checked(fieldId(current.blobKey, headerSwitch(block.fields)!))"
                  :disabled="auth.isViewer()"
                  @update:model-value="setDraft(fieldId(current.blobKey, headerSwitch(block.fields)!), $event ? 'true' : 'false')"
                />
              </header>
              <div class="src-card__body">
                <div
                  v-for="field in bodyFields(block.fields)"
                  :key="fieldId(current.blobKey, field)"
                  class="field"
                  :class="{ wide: field.kind !== 'bool', toggle: field.kind === 'bool' }"
                >
                  <NtSwitch
                    v-if="field.kind === 'bool'"
                    :model-value="checked(fieldId(current.blobKey, field))"
                    :label="field.label"
                    :disabled="auth.isViewer()"
                    @update:model-value="setDraft(fieldId(current.blobKey, field), $event ? 'true' : 'false')"
                  />
                  <label v-else-if="field.kind === 'text' && field.wide" class="area">
                    <span>{{ field.label }}</span>
                    <textarea
                      :value="draft[fieldId(current.blobKey, field)]"
                      spellcheck="false"
                      :disabled="auth.isViewer()"
                      @input="setDraft(fieldId(current.blobKey, field), ($event.target as HTMLTextAreaElement).value)"
                    />
                  </label>
                  <NtInput
                    v-else
                    :label="field.label"
                    :model-value="draft[fieldId(current.blobKey, field)] || ''"
                    :type="field.kind === 'secret' ? 'password' : field.kind === 'text' ? 'text' : 'number'"
                    :mono="field.kind === 'text'"
                    :disabled="auth.isViewer()"
                    @update:model-value="setDraft(fieldId(current.blobKey, field), $event)"
                  />
                  <p v-if="field.hint" class="field__hint">{{ field.hint }}</p>
                </div>
              </div>
            </article>
          </div>
          <div v-else-if="ready" class="grid">
            <template v-for="(field, index) in current.fields" :key="fieldId(current.blobKey, field)">
              <h3 v-if="showSection(current, index)" class="sec">{{ field.section }}</h3>
              <div class="field" :class="{ wide: field.kind === 'json' }">
                <NtSwitch
                  v-if="field.kind === 'bool'"
                  :model-value="checked(fieldId(current.blobKey, field))"
                  :label="field.label"
                  :disabled="auth.isViewer()"
                  @update:model-value="setDraft(fieldId(current.blobKey, field), $event ? 'true' : 'false')"
                />
                <label v-else-if="field.kind === 'json'" class="area">
                  <span>{{ field.label }}</span>
                  <textarea
                    :value="draft[fieldId(current.blobKey, field)]"
                    spellcheck="false"
                    :disabled="auth.isViewer()"
                    @input="setDraft(fieldId(current.blobKey, field), ($event.target as HTMLTextAreaElement).value)"
                  />
                </label>
                <NtSelect
                  v-else-if="field.kind === 'select'"
                  :label="field.label"
                  :model-value="draft[fieldId(current.blobKey, field)] || ''"
                  :options="field.options || []"
                  :disabled="auth.isViewer()"
                  @update:model-value="setDraft(fieldId(current.blobKey, field), String($event))"
                />
                <NtInput
                  v-else
                  :label="field.label"
                  :model-value="draft[fieldId(current.blobKey, field)] || ''"
                  :type="field.kind === 'secret' ? 'password' : field.kind === 'text' ? 'text' : 'number'"
                  :mono="field.kind !== 'text' && field.kind !== 'secret'"
                  :disabled="auth.isViewer()"
                  @update:model-value="setDraft(fieldId(current.blobKey, field), $event)"
                />
                <p v-if="field.hint" class="field__hint">{{ field.hint }}</p>
              </div>
            </template>
          </div>
          <p v-else class="nt-empty">正在读取当前值。</p>
          <div class="savebar">
            <NtButton variant="primary" :disabled="auth.isViewer() || saving || !dirty" @click="saveCurrent">
              保存本类
            </NtButton>
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

        <p v-if="error" class="nt-err">{{ error }}</p>
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
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-m);
  box-shadow: 0 24px 64px rgba(0, 0, 0, 0.45);
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
  background: rgba(0, 229, 255, 0.08);
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
}

.pref > span {
  width: 60px;
  color: var(--nt-text-2);
  font-size: 12.5px;
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

.field__hint {
  margin: 0;
  color: var(--nt-text-3);
  font-size: 11px;
  line-height: 1.4;
}

.area {
  display: flex;
  flex-direction: column;
  gap: 4px;
  color: var(--nt-text-2);
  font-size: 12px;
}

.area textarea {
  min-height: 72px;
  padding: 8px;
  resize: vertical;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-0);
  color: var(--nt-text-1);
  font-family: ui-monospace, SFMono-Regular, Menlo, Consolas, monospace;
  font-size: 12px;
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
