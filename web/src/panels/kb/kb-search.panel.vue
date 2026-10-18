<script setup lang="ts">
/**
 * 知识库检索面板（M04）：全文检索 + 文档列表 + 相似案例。
 * 检索结果可点「相似案例」拉出同源历史案例。
 */
import { onMounted, ref } from 'vue'
import {
  listCases,
  listDocuments,
  searchKb,
  similarCases,
  type KbCase,
  type KbDocument,
  type KbSearchHit,
} from '@/api/kb'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtTabs from '@/ui/NtTabs.vue'
import NtTimeline, { type TimelineEntry } from '@/display/NtTimeline.vue'
import { clock, messageOf } from '@/utils/format'

const tab = ref('search')
const query = ref('')
const searching = ref(false)
const error = ref('')

const hits = ref<KbSearchHit[]>([])
const documents = ref<KbDocument[]>([])
const cases = ref<KbCase[]>([])
const similar = ref<{ id: string; items: KbCase[] } | null>(null)

async function runSearch() {
  const q = query.value.trim()
  if (!q) return
  searching.value = true
  error.value = ''
  try {
    hits.value = await searchKb(q, 30)
  } catch (err) {
    error.value = messageOf(err)
  } finally {
    searching.value = false
  }
}

async function loadDocs() {
  try {
    documents.value = await listDocuments({ limit: 50 })
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function loadCases() {
  try {
    cases.value = await listCases({ limit: 50 })
  } catch (err) {
    error.value = messageOf(err)
  }
}

async function showSimilar(id: string) {
  try {
    similar.value = { id, items: await similarCases(id) }
  } catch (err) {
    error.value = messageOf(err)
  }
}

function hitEntries(): TimelineEntry[] {
  return hits.value.map((h) => ({
    id: h.id || h.documentId,
    time: h.source || '—',
    title: h.title || '（无标题）',
    body: h.snippet,
    tag: h.score != null ? `score ${h.score.toFixed(2)}` : '',
    tone: 'accent',
  }))
}

function docEntries(): TimelineEntry[] {
  return documents.value.map((d) => ({
    id: d.id,
    time: clock(d.createdAt),
    title: d.title,
    tag: d.kind,
    body: d.source,
    tone: 'neutral',
  }))
}

function caseEntries(): TimelineEntry[] {
  return cases.value.map((c) => ({
    id: c.id,
    time: c.tradeDate,
    title: `${c.symbol} · ${c.summary}`,
    body: c.outcome,
    tone: 'neutral',
  }))
}

onMounted(() => {
  void loadDocs()
  void loadCases()
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">知识库</h1>
        <p class="nt-note">历史案例、研报、公告与策略经验。</p>
      </div>
    </header>

    <NtTabs
      v-model="tab"
      :items="[
        { value: 'search', label: '检索' },
        { value: 'docs', label: '文档' },
        { value: 'cases', label: '案例' },
      ]"
    />

    <p v-if="error" class="nt-err">{{ error }}</p>

    <section v-show="tab === 'search'" class="nt-panel">
      <div class="bar">
        <NtInput v-model="query" placeholder="关键词 / 股票代码 / 题材" class="bar__q" @keydown.enter="runSearch" />
        <NtButton size="sm" variant="primary" :loading="searching" :disabled="!query.trim()" @click="runSearch">检索</NtButton>
      </div>
      <NtTimeline :items="hitEntries()" :empty="searching ? '检索中…' : '输入关键词开始检索'" />
      <div v-if="hits.length" class="similar">
        <p class="nt-note">点结果旁的编号查相似案例：</p>
        <button
          v-for="h in hits.slice(0, 8)"
          :key="h.id || h.documentId"
          type="button"
          class="similar__btn"
          @click="showSimilar(h.documentId || h.id)"
        >
          {{ h.title || h.id }}
        </button>
      </div>
    </section>

    <section v-show="tab === 'docs'" class="nt-panel">
      <NtTimeline :items="docEntries()" empty="知识库还没有文档" />
    </section>

    <section v-show="tab === 'cases'" class="nt-panel">
      <NtTimeline :items="caseEntries()" empty="还没有历史案例" />
    </section>

    <section v-if="similar" class="nt-panel">
      <h2 class="nt-panel__title">
        相似案例
        <NtButton size="sm" @click="similar = null">关闭</NtButton>
      </h2>
      <NtTimeline
        :items="
          similar.items.map((c) => ({
            id: c.id,
            time: c.tradeDate,
            title: `${c.symbol} · ${c.summary}`,
            body: c.outcome,
          }))
        "
        empty="没有相似案例"
      />
    </section>
  </div>
</template>

<style scoped>
.bar {
  display: flex;
  gap: 8px;
}

.bar__q {
  flex: 1;
}

.similar {
  margin-top: 10px;
}

.similar__btn {
  margin: 4px 6px 0 0;
  padding: 3px 10px;
  font-size: 11.5px;
  color: var(--nt-accent);
  background: transparent;
  border: 1px solid var(--nt-accent-border);
  border-radius: 999px;
  cursor: pointer;
}

.similar__btn:hover {
  background: var(--nt-accent-soft);
}
</style>
