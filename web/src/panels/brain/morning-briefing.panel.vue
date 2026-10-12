<script setup lang="ts">
/**
 * 晨报面板（M05）：盘前必读。
 * 日期切换 + 手动触发生成（owner 写操作）；stale 明确标出。
 */
import { computed, onMounted, ref } from 'vue'
import { getBriefing, runBriefing, type Briefing } from '@/api/brain'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { useAuthStore } from '@/stores/auth'
import { clock, messageOf } from '@/utils/format'

const auth = useAuthStore()

const date = ref(today())
const briefing = ref<Briefing | null>(null)
const loading = ref(false)
const generating = ref(false)
const error = ref('')

function today(): string {
  const d = new Date()
  const y = d.getFullYear()
  const m = String(d.getMonth() + 1).padStart(2, '0')
  const day = String(d.getDate()).padStart(2, '0')
  return `${y}-${m}-${day}`
}

const entries = computed<TimelineEntry[]>(
  () =>
    briefing.value?.items.map((item, index) => ({
      id: `${item.kind}-${index}`,
      time: item.kind || '—',
      title: item.title || item.symbol || '（无标题）',
      body: item.symbol && item.body ? `[${item.symbol}] ${item.body}` : item.body,
      tone: /风险|下跌|预警/i.test(`${item.kind}${item.title}`) ? 'warn' : 'neutral',
      tag: item.symbol || '',
    })) ?? [],
)

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
        <h1 class="nt-title">
          晨报
          <NtBadge v-if="briefing?.stale" tone="warn" dot>不是实时</NtBadge>
        </h1>
        <p class="nt-note">
          盘前必读：隔夜外围、题材催化、关注池要点。
          <span v-if="briefing?.generatedAt">生成于 {{ clock(briefing.generatedAt) }}</span>
        </p>
      </div>
      <div class="nt-head__actions">
        <NtInput v-model="date" placeholder="YYYY-MM-DD" class="date" />
        <NtButton size="sm" :loading="loading" @click="load">查询</NtButton>
        <NtButton v-if="!auth.isViewer()" size="sm" :loading="generating" @click="generate">生成</NtButton>
      </div>
    </header>

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section v-if="briefing?.summary" class="nt-panel">
      <h2 class="nt-panel__title">今日摘要 · {{ briefing.date || date }}</h2>
      <p class="summary">{{ briefing.summary }}</p>
    </section>

    <section class="nt-panel">
      <h2 class="nt-panel__title">条目</h2>
      <NtTimeline :items="entries" :empty="loading ? '加载中…' : '这一天还没有晨报'" />
    </section>
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

.summary {
  margin: 0;
  font-size: 13px;
  line-height: 1.7;
  color: var(--nt-text-1);
  white-space: pre-wrap;
}
</style>
