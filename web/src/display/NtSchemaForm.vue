<script setup lang="ts">
/**
 * NtSchemaForm —— 配置 schema 驱动表单（W7b；R-UI-24/38）。
 * 字段目录由宿主从 `/api/config/schema` 拉下后组装成 rows；本件不碰 store/router、
 * 不硬编码字段表。敏感字段走 NtSecretText（遮蔽槽）+ 密码输入（替换槽），与「—」缺位分家。
 * schemaError 非空时整表降级横幅（R-UI-38），禁止静默丢字段或回退本地目录。
 */
import { computed } from 'vue'
import NtBadge, { type BadgeTone } from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtConfigField, { type ConfigFieldLite, type EffectiveScope, type SecretState } from '@/ui/NtConfigField.vue'
import NtInput from '@/ui/NtInput.vue'
import NtSecretText from '@/ui/NtSecretText.vue'

const EFFECTIVE_META: Record<EffectiveScope, { text: string; tone: BadgeTone }> = {
  immediate: { text: '立即生效', tone: 'success' },
  'on-save': { text: '保存后生效', tone: 'neutral' },
  'on-restart': { text: '重启后生效', tone: 'warn' },
}

export interface SchemaFormField {
  key: string
  label: string
  hint?: string
  kind: ConfigFieldLite['kind']
  fallback?: string
  section?: string
  options?: { value: string; label: string }[]
  wide?: boolean
  clearable?: boolean
}

export interface SchemaFormRow {
  /** 草稿读写键（blob 字段为 `blobKey.jsonKey`，其余为 field.key）。 */
  id: string
  field: SchemaFormField
  value: string
  secretState?: SecretState
  effective?: EffectiveScope
}

const props = withDefaults(
  defineProps<{
    rows: SchemaFormRow[]
    layout?: 'grid' | 'cards'
    disabled?: boolean
    /** R-UI-38：schema 不可达或不兼容时整组缺位，不渲染字段。 */
    schemaError?: string
  }>(),
  { layout: 'grid', disabled: false, schemaError: '' },
)

const emit = defineEmits<{
  (e: 'update:value', id: string, value: unknown, field: SchemaFormField): void
  (e: 'clear', key: string): void
}>()

const blocks = computed(() => {
  if (props.layout !== 'cards') return null
  const out: { title: string; rows: SchemaFormRow[] }[] = []
  for (const row of props.rows) {
    const title = row.field.section || '其他'
    const last = out[out.length - 1]
    if (last && last.title === title) last.rows.push(row)
    else out.push({ title, rows: [row] })
  }
  return out
})

function showSection(index: number): boolean {
  const section = props.rows[index]?.field.section
  if (!section) return false
  return index === 0 || props.rows[index - 1]?.field.section !== section
}

function liteOf(field: SchemaFormField): ConfigFieldLite {
  return {
    key: field.key,
    label: field.label,
    kind: field.kind,
    hint: field.hint || undefined,
    options: field.options,
    placeholder: field.clearable ? '留空跟随配置文件' : undefined,
  }
}

function onFieldValue(row: SchemaFormRow, value: unknown) {
  emit('update:value', row.id, value, row.field)
}

function wideOf(row: SchemaFormRow): boolean {
  if (props.layout === 'cards') return row.field.kind !== 'bool'
  return row.field.kind === 'json' || row.field.wide === true
}
</script>

<template>
  <div class="nt-schema-form" data-testid="nt-schema-form">
    <p v-if="schemaError" class="nt-schema-form__error" role="alert">
      配置结构已变更或不可达：{{ schemaError }}。字段目录由后端下发，本次不能编辑任何配置（R-UI-38）。
    </p>
    <div v-else-if="layout === 'cards' && blocks" class="nt-schema-form__cards">
      <article v-for="block in blocks" :key="block.title" class="nt-schema-form__card">
        <header class="nt-schema-form__card-head">
          <h3>{{ block.title }}</h3>
        </header>
        <div class="nt-schema-form__card-body">
          <div
            v-for="row in block.rows"
            :key="row.id"
            class="nt-schema-form__field"
            :class="{ wide: wideOf(row) }"
            :data-field="row.field.key"
          >
            <template v-if="row.field.kind === 'secret'">
              <div class="nt-schema-form__secret-head">
                <span class="nt-schema-form__label">{{ row.field.label }}</span>
                <span v-if="row.field.hint" class="nt-schema-form__hint">{{ row.field.hint }}</span>
              </div>
              <div class="nt-schema-form__secret-row">
                <NtSecretText
                  v-if="row.secretState === 'set' || row.secretState === 'clearing'"
                  :text="row.secretState === 'clearing' ? '清除待保存' : '已保存的密钥'"
                  :copyable="false"
                />
                <NtBadge v-else tone="neutral" dot>未设置</NtBadge>
                <NtBadge v-if="row.secretState === 'clearing'" tone="warn">清除待保存</NtBadge>
                <NtBadge v-else-if="row.secretState === 'set'" tone="accent" dot>已设置</NtBadge>
                <NtBadge v-if="row.effective" :tone="EFFECTIVE_META[row.effective].tone">{{ EFFECTIVE_META[row.effective].text }}</NtBadge>
                <NtButton
                  class="nt-schema-form__clear"
                  size="sm"
                  variant="quiet"
                  :disabled="disabled || row.secretState !== 'set'"
                  @click="emit('clear', row.field.key)"
                >
                  清除
                </NtButton>
              </div>
              <NtInput
                type="password"
                :model-value="row.value"
                :placeholder="row.secretState === 'set' ? '留空保留，输入替换' : '填写后保存生效'"
                :disabled="disabled"
                @update:model-value="onFieldValue(row, $event)"
              />
            </template>
            <NtConfigField
              v-else
              :field="liteOf(row.field)"
              :model-value="row.value"
              :default-value="row.field.fallback"
              :disabled="disabled"
              :effective="row.effective"
              @update:model-value="onFieldValue(row, $event)"
            />
          </div>
        </div>
      </article>
    </div>
    <div v-else class="nt-schema-form__grid">
      <template v-for="(row, index) in rows" :key="row.id">
        <h3 v-if="showSection(index)" class="nt-schema-form__sec">{{ row.field.section }}</h3>
        <div class="nt-schema-form__field" :class="{ wide: wideOf(row) }" :data-field="row.field.key">
          <template v-if="row.field.kind === 'secret'">
            <div class="nt-schema-form__secret-head">
              <span class="nt-schema-form__label">{{ row.field.label }}</span>
              <span v-if="row.field.hint" class="nt-schema-form__hint">{{ row.field.hint }}</span>
            </div>
            <div class="nt-schema-form__secret-row">
              <NtSecretText
                v-if="row.secretState === 'set' || row.secretState === 'clearing'"
                :text="row.secretState === 'clearing' ? '清除待保存' : '已保存的密钥'"
                :copyable="false"
              />
              <NtBadge v-else tone="neutral" dot>未设置</NtBadge>
              <NtBadge v-if="row.secretState === 'clearing'" tone="warn">清除待保存</NtBadge>
              <NtBadge v-else-if="row.secretState === 'set'" tone="accent" dot>已设置</NtBadge>
              <NtBadge v-if="row.effective" :tone="EFFECTIVE_META[row.effective].tone">{{ EFFECTIVE_META[row.effective].text }}</NtBadge>
              <NtButton
                class="nt-schema-form__clear"
                size="sm"
                variant="quiet"
                :disabled="disabled || row.secretState !== 'set'"
                @click="emit('clear', row.field.key)"
              >
                清除
              </NtButton>
            </div>
            <NtInput
              type="password"
              :model-value="row.value"
              :placeholder="row.secretState === 'set' ? '留空保留，输入替换' : '填写后保存生效'"
              :disabled="disabled"
              @update:model-value="onFieldValue(row, $event)"
            />
          </template>
          <NtConfigField
            v-else
            :field="liteOf(row.field)"
            :model-value="row.value"
            :default-value="row.field.fallback"
            :disabled="disabled"
            :effective="row.effective"
            @update:model-value="onFieldValue(row, $event)"
          />
        </div>
      </template>
    </div>
  </div>
</template>

<style scoped>
.nt-schema-form {
  display: flex;
  flex-direction: column;
  gap: 12px;
  min-width: 0;
}

.nt-schema-form__error {
  margin: 0;
  padding: 10px 12px;
  color: var(--nt-warn);
  background: color-mix(in srgb, var(--nt-warn) 12%, transparent);
  border: 1px solid color-mix(in srgb, var(--nt-warn) 35%, transparent);
  border-radius: var(--nt-radius-s);
  font-size: var(--nt-fs-caption);
}

.nt-schema-form__grid {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}

.nt-schema-form__sec {
  grid-column: 1 / -1;
  margin: 4px 0 0;
  font-size: var(--nt-fs-caption);
  font-weight: 600;
  color: var(--nt-text-2);
}

.nt-schema-form__field {
  min-width: 0;
  display: flex;
  flex-direction: column;
  gap: 6px;
}

.nt-schema-form__field.wide {
  grid-column: 1 / -1;
}

.nt-schema-form__cards {
  display: flex;
  flex-direction: column;
  gap: 12px;
}

.nt-schema-form__card {
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: color-mix(in srgb, var(--nt-glass-bg) 70%, transparent);
  padding: 12px;
}

.nt-schema-form__card-head h3 {
  margin: 0 0 8px;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-1);
}

.nt-schema-form__card-body {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 12px 16px;
}

.nt-schema-form__secret-head {
  display: flex;
  align-items: baseline;
  gap: 8px;
  min-width: 0;
}

.nt-schema-form__label {
  flex: 0 0 auto;
  font-size: var(--nt-fs-body);
  color: var(--nt-text-2);
}

.nt-schema-form__hint {
  min-width: 0;
  font-size: var(--nt-fs-caption);
  color: var(--nt-text-3);
}

.nt-schema-form__secret-row {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
}
</style>
