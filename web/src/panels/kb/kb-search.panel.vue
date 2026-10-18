<script setup lang="ts">
/**
 * 知识库面板（M04 × M12）：检索 / 文档管理 / 案例 / 上传。
 * 口径见 api/kb.ts：Search 与 SimilarCases 走 POST；案例列表 = 情形留空的 SimilarCases。
 * 嵌入服务未接入时检索降级关键词，界面照实挂 degraded 徽标，不装作向量命中。
 */
import { computed, onMounted, reactive, ref } from 'vue'
import {
  deleteDocument,
  getDocument,
  ingestDocument,
  listDocuments,
  searchKb,
  similarCases,
  type KbCaseHit,
  type KbDocumentDetail,
  type KbIngestReply,
  type KbSearchResult,
} from '@/api/kb'
import NtBadge from '@/ui/NtBadge.vue'
import NtButton from '@/ui/NtButton.vue'
import NtInput from '@/ui/NtInput.vue'
import NtModal from '@/ui/NtModal.vue'
import NtSelect from '@/ui/NtSelect.vue'
import NtTable, { type NtTableColumn } from '@/ui/NtTable.vue'
import NtTabs from '@/ui/NtTabs.vue'
import { clock, messageOf, pctText, phaseName, pnlClass, price } from '@/utils/format'
import {
  attributionLabel,
  BOOK_OPTIONS,
  buildIngest,
  bytesToBase64,
  categoryLabel,
  CATEGORY_OPTIONS,
  EMOTION_OPTIONS,
  embedTone,
  finalStatusLabel,
  formatSize,
  outcomeLabel,
  SOURCE_TYPE_OPTIONS,
  sourceLabel,
  STATUS_OPTIONS,
  statusTone,
  toDocRow,
  type DocRow,
  type IngestForm,
} from './kb'

const PAGE = 20

const tab = ref('search')
const err = ref('')

/* ---------- 检索 ---------- */

const sq = ref('')
const sCategory = ref('')
const sStock = ref('')
const searching = ref(false)
const searchRes = ref<KbSearchResult | null>(null)
const searchHits = computed(() => searchRes.value?.hits ?? [])

async function runSearch() {
  const q = sq.value.trim()
  if (!q) return
  searching.value = true
  err.value = ''
  try {
    searchRes.value = await searchKb({
      query: q,
      topK: 30,
      categories: sCategory.value ? [sCategory.value] : undefined,
      stockCode: sStock.value.trim() || undefined,
    })
  } catch (e) {
    err.value = messageOf(e)
  } finally {
    searching.value = false
  }
}

function degradedText(): string {
  return '向量路未接入：当前只做关键词检索（DGX 嵌入服务未部署，见 M04 待办）'
}

/* ---------- 文档 ---------- */

const dCategory = ref('')
const dStatus = ref('')
const docs = ref<DocRow[]>([])
const dTotal = ref(0)
const dOffset = ref(0)
const dBusy = ref(false)
const deleting = ref('')

const docColumns: NtTableColumn<DocRow>[] = [
  { key: 'title', title: '标题' },
  { key: 'category', title: '分类', width: '64px' },
  { key: 'format', title: '格式', width: '56px' },
  { key: 'size', title: '大小', align: 'right', width: '76px' },
  { key: 'status', title: '状态', width: '72px' },
  { key: 'embed', title: '向量', width: '88px' },
  { key: 'chunks', title: '分块', align: 'right', width: '56px' },
  { key: 'version', title: '版本', align: 'right', width: '52px' },
  { key: 'created', title: '入库时间', width: '140px' },
  { key: 'action', title: '操作', width: '128px' },
]

const pageText = computed(() => {
  if (!dTotal.value) return '0 / 0'
  const from = dOffset.value + 1
  const to = dOffset.value + docs.value.length
  return `${from}–${to} / ${dTotal.value}`
})

async function loadDocs(offset: number) {
  dBusy.value = true
  err.value = ''
  try {
    const res = await listDocuments({
      category: dCategory.value || undefined,
      status: dStatus.value || undefined,
      offset,
      limit: PAGE,
    })
    docs.value = res.documents.map(toDocRow)
    dTotal.value = res.total
    dOffset.value = offset
  } catch (e) {
    err.value = messageOf(e)
  } finally {
    dBusy.value = false
  }
}

function pageDocs(step: number) {
  const next = dOffset.value + step * PAGE
  if (next < 0 || (next >= dTotal.value && step > 0)) return
  void loadDocs(next)
}

async function removeDoc(id: string) {
  deleting.value = id
  err.value = ''
  try {
    await deleteDocument(id)
    await loadDocs(dOffset.value)
  } catch (e) {
    err.value = messageOf(e)
  } finally {
    deleting.value = ''
  }
}

/* ---------- 文档详情 ---------- */

const detailOpen = ref(false)
const detailBusy = ref(false)
const detail = ref<KbDocumentDetail | null>(null)

async function openDetail(row: DocRow) {
  detailOpen.value = true
  detailBusy.value = true
  try {
    detail.value = await getDocument(row.id)
  } catch (e) {
    err.value = messageOf(e)
    detailOpen.value = false
  } finally {
    detailBusy.value = false
  }
}

/* ---------- 案例 ---------- */

const csForm = reactive({ situation: '', pattern: '', emotion: '', stock: '', book: '' })
const csBusy = ref(false)
const csRes = ref<{ hits: KbCaseHit[]; degraded: boolean } | null>(null)
const csHits = computed(() => csRes.value?.hits ?? [])

async function runCases() {
  csBusy.value = true
  err.value = ''
  try {
    csRes.value = await similarCases({
      situation: csForm.situation,
      pattern: csForm.pattern,
      emotionPhase: csForm.emotion || undefined,
      stockCode: csForm.stock,
      book: csForm.book || undefined,
      topK: 20,
    })
  } catch (e) {
    err.value = messageOf(e)
  } finally {
    csBusy.value = false
  }
}

/** 从检索命中钻到案例：拿命中的标题+正文当情形描述。 */
function drillSimilar(docId: string) {
  const hit = searchHits.value.find((h) => h.docId === docId)
  csForm.situation = [hit?.title, hit?.heading, hit?.content].filter(Boolean).join(' ').slice(0, 200)
  tab.value = 'cases'
  void runCases()
}

/* ---------- 上传 ---------- */

const up = reactive<IngestForm>({
  category: '',
  sourceType: 'file',
  title: '',
  source: '',
  author: '',
  fileName: '',
  fileSize: null,
  text: '',
  stockCodesText: '',
  tagsText: '',
  expiresLocal: '',
})
const upBase64 = ref('')
const upErr = ref('')
const upMsg = ref('')
const upBusy = ref(false)

const upFileText = computed(() =>
  up.fileName ? `${up.fileName}（${formatSize(up.fileSize)}）` : '没选文件',
)

async function onPick(event: Event) {
  const file = (event.target as HTMLInputElement).files?.[0]
  upErr.value = ''
  upMsg.value = ''
  if (!file) return
  up.fileName = file.name
  up.fileSize = file.size
  up.title = up.title || file.name.replace(/\.[^.]+$/, '')
  if (up.sourceType === 'file' && !up.source.trim()) up.source = file.name
  try {
    upBase64.value = bytesToBase64(new Uint8Array(await file.arrayBuffer()))
  } catch {
    upBase64.value = ''
    upErr.value = '文件读取失败'
  }
}

function onModeChange() {
  upErr.value = ''
  upMsg.value = ''
  // 切走文件模式时把已读书节和文件名清掉，manual/url 不带旧后缀，避免后端按旧 file_name 判错格式。
  up.fileName = ''
  up.fileSize = null
  upBase64.value = ''
}

async function submitIngest() {
  upBusy.value = true
  upErr.value = ''
  upMsg.value = ''
  try {
    const built = buildIngest(up, up.sourceType === 'file' ? upBase64.value : '')
    if (!built.ok) {
      upErr.value = built.reason
      return
    }
    const res: KbIngestReply = await ingestDocument(built.params)
    if (res.duplicate) {
      upMsg.value = `内容与已有文档重复：已指向原文档 «${res.document?.title || res.document?.fileName || ''}»（v${res.document?.version ?? '?'}），没有重复入库`
    } else if (res.embedded) {
      upMsg.value = '已入库，向量已写入，可混合检索'
    } else {
      upMsg.value = '已入库；嵌入服务未接入，向量待补嵌入任务写入（当前检索仅走关键词）'
    }
    up.title = ''
    up.stockCodesText = ''
    up.tagsText = ''
    up.expiresLocal = ''
    up.text = ''
    if (up.sourceType === 'file') {
      up.fileName = ''
      up.fileSize = null
      upBase64.value = ''
    }
    await loadDocs(0)
  } catch (e) {
    upErr.value = messageOf(e)
  } finally {
    upBusy.value = false
  }
}

onMounted(() => {
  void loadDocs(0)
})
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">知识库</h1>
        <p class="nt-note">上传、检索与历史案例复盘（M04）。命中内容喂给大模型时必须带出出处。</p>
      </div>
    </header>

    <NtTabs
      v-model="tab"
      :items="[
        { value: 'search', label: '检索' },
        { value: 'docs', label: '文档' },
        { value: 'cases', label: '案例' },
        { value: 'upload', label: '上传' },
      ]"
    />

    <p v-if="err" class="nt-err">{{ err }}</p>

    <section v-show="tab === 'search'" class="nt-panel">
      <div class="kb-bar">
        <NtInput v-model="sq" class="grow" placeholder="关键词 / 题材 / 事件" @keydown.enter="runSearch" />
        <NtSelect
          v-model="sCategory"
          class="w140"
          :options="[{ value: '', label: '全部分类' }, ...CATEGORY_OPTIONS]"
        />
        <NtInput v-model="sStock" class="w140" placeholder="股票代码（可空）" mono @keydown.enter="runSearch" />
        <NtButton size="sm" variant="primary" :loading="searching" :disabled="!sq.trim()" @click="runSearch">
          检索
        </NtButton>
      </div>
      <div v-if="searchRes" class="kb-note-line">
        <span>{{ searchHits.length }} 条命中</span>
        <NtBadge v-if="searchRes.degraded" tone="warn" dot>{{ degradedText() }}</NtBadge>
      </div>
      <ul v-if="searchHits.length" class="kb-hits">
        <li v-for="h in searchHits" :key="`${h.docId}:${h.chunkId}`" class="kb-hits__item">
          <header class="kb-hits__head">
            <strong>{{ h.title || '（无标题）' }}</strong>
            <NtBadge v-if="h.heading" tone="neutral">{{ h.heading }}</NtBadge>
            <NtBadge tone="accent">{{ categoryLabel(h.category) }}</NtBadge>
            <span class="kb-hits__ranks">
              <template v-if="h.keywordRank">关键词 #{{ h.keywordRank }}</template>
              <template v-if="h.vectorRank">{{ h.keywordRank ? ' · ' : '' }}向量 #{{ h.vectorRank }}</template>
            </span>
          </header>
          <p class="kb-hits__src">来源：{{ h.source || '—' }}（{{ sourceLabel(h.sourceType) }}）</p>
          <p class="kb-hits__body">{{ h.content }}</p>
          <div class="kb-actions">
            <NtButton size="sm" variant="quiet" @click="drillSimilar(h.docId)">据此查相似案例</NtButton>
          </div>
        </li>
      </ul>
      <p v-else class="nt-note">{{ searching ? '检索中…' : searchRes ? '没有命中' : '输入关键词开始检索' }}</p>
    </section>
    <section v-show="tab === 'docs'" class="nt-panel">
      <div class="kb-bar">
        <NtSelect
          v-model="dCategory"
          class="w140"
          :options="[{ value: '', label: '全部分类' }, ...CATEGORY_OPTIONS]"
          @update:model-value="loadDocs(0)"
        />
        <NtSelect
          v-model="dStatus"
          class="w140"
          :options="[{ value: '', label: '全部状态' }, ...STATUS_OPTIONS]"
          @update:model-value="loadDocs(0)"
        />
        <NtButton size="sm" :loading="dBusy" @click="loadDocs(0)">刷新</NtButton>
        <span class="grow" />
        <NtButton size="sm" :disabled="dOffset <= 0 || dBusy" @click="pageDocs(-1)">上一页</NtButton>
        <span class="kb-page">{{ pageText }}</span>
        <NtButton size="sm" :disabled="dOffset + PAGE >= dTotal || dBusy" @click="pageDocs(1)">下一页</NtButton>
      </div>
      <NtTable
        :columns="docColumns"
        :rows="docs"
        row-key="id"
        dense
        clickable
        empty="知识库还没有文档，去「上传」页传一份"
        @row-click="openDetail"
      >
        <template #cell-status="{ row }">
          <NtBadge :tone="statusTone(row.doc.status)">{{ row.status }}</NtBadge>
        </template>
        <template #cell-embed="{ row }">
          <NtBadge :tone="embedTone(row.doc.embedStatus)">{{ row.embed }}</NtBadge>
        </template>
        <template #cell-created="{ row }">{{ clock(row.created) }}</template>
        <template #cell-action="{ row }">
          <div class="kb-actions" @click.stop>
            <NtButton size="sm" variant="quiet" @click="openDetail(row)">详情</NtButton>
            <NtButton size="sm" variant="danger" :loading="deleting === row.id" @click="removeDoc(row.id)">
              删除
            </NtButton>
          </div>
        </template>
      </NtTable>
    </section>

    <NtModal v-model:open="detailOpen" :title="detail?.document?.title || '文档详情'" width="720px">
      <p v-if="detailBusy" class="nt-note">读取中…</p>
      <div v-else-if="detail?.document" class="kb-detail">
        <dl class="kb-detail__meta">
          <div><dt>来源</dt><dd>{{ detail.document.source || '—' }}（{{ sourceLabel(detail.document.sourceType) }}）</dd></div>
          <div><dt>分类</dt><dd>{{ categoryLabel(detail.document.category) }}</dd></div>
          <div><dt>作者</dt><dd>{{ detail.document.author || '—' }}</dd></div>
          <div><dt>格式</dt><dd>{{ detail.document.format || '—' }} · {{ detail.document.fileName || '—' }}</dd></div>
          <div><dt>大小</dt><dd>{{ formatSize(detail.document.sizeBytes) }}</dd></div>
          <div>
            <dt>状态</dt>
            <dd>
              <NtBadge :tone="statusTone(detail.document.status)">{{ detail.document.status }}</NtBadge>
              <NtBadge :tone="embedTone(detail.document.embedStatus)">{{ detail.document.embedStatus }}</NtBadge>
            </dd>
          </div>
          <div v-if="detail.document.stockCodes.length"><dt>关联股票</dt><dd class="mono">{{ detail.document.stockCodes.join('，') }}</dd></div>
          <div v-if="detail.document.tags.length"><dt>标签</dt><dd>{{ detail.document.tags.join('，') }}</dd></div>
          <div><dt>有效期至</dt><dd>{{ detail.document.expiresAt ? clock(detail.document.expiresAt) : '不过期' }}</dd></div>
          <div><dt>入库时间</dt><dd>{{ clock(detail.document.createdAt) }}</dd></div>
          <div><dt>版本</dt><dd>v{{ detail.document.version ?? '—' }}</dd></div>
        </dl>
        <h3 class="kb-detail__sub">分块（{{ detail.chunks.length }}）</h3>
        <ul class="kb-chunks">
          <li v-for="c in detail.chunks" :key="c.id">
            <header>
              <span class="mono">#{{ c.seq }}</span>
              <strong>{{ c.heading || '（无题头）' }}</strong>
              <NtBadge :tone="c.embedded ? 'accent' : 'warn'">{{ c.embedded ? '已向量' : '待补' }}</NtBadge>
              <span class="nt-note">{{ c.tokenCount ?? '—' }} token</span>
            </header>
            <p>{{ c.content }}</p>
          </li>
        </ul>
      </div>
    </NtModal>
    <section v-show="tab === 'cases'" class="nt-panel">
      <div class="kb-bar">
        <NtInput
          v-model="csForm.situation"
          class="grow"
          placeholder="情形描述（留空 = 最近结束的案例倒序）"
          @keydown.enter="runCases"
        />
        <NtButton size="sm" variant="primary" :loading="csBusy" @click="runCases">查询</NtButton>
      </div>
      <div class="kb-bar">
        <NtInput v-model="csForm.pattern" class="w160" placeholder="形态（可空）" @keydown.enter="runCases" />
        <NtSelect
          v-model="csForm.emotion"
          class="w140"
          :options="[{ value: '', label: '全部情绪' }, ...EMOTION_OPTIONS]"
          @update:model-value="runCases"
        />
        <NtInput v-model="csForm.stock" class="w140" placeholder="股票代码（可空）" mono @keydown.enter="runCases" />
        <NtSelect
          v-model="csForm.book"
          class="w140"
          :options="[{ value: '', label: '全部账本' }, ...BOOK_OPTIONS]"
          @update:model-value="runCases"
        />
      </div>
      <div v-if="csRes" class="kb-note-line">
        <span>{{ csHits.length }} 条案例</span>
        <NtBadge v-if="csRes.degraded" tone="warn" dot>{{ degradedText() }}</NtBadge>
      </div>
      <ul v-if="csHits.length" class="kb-cases">
        <li v-for="hit in csHits" :key="hit.kbCase.id || hit.chunkId" class="kb-cases__item">
          <header class="kb-cases__head">
            <strong>{{ hit.kbCase.stockName || '—' }}</strong>
            <span class="mono">{{ hit.kbCase.stockCode }}</span>
            <NtBadge tone="neutral">{{ outcomeLabel(hit.kbCase.outcome) }}</NtBadge>
            <NtBadge tone="neutral">{{ finalStatusLabel(hit.kbCase.finalStatus) }}</NtBadge>
            <span
              class="kb-cases__pnl"
              :class="pnlClass(hit.kbCase.pnlPct)"
              :title="
                hit.kbCase.pnlPct != null
                  ? `入场 ${price(hit.kbCase.entryPrice)} → 出场 ${price(hit.kbCase.exitPrice)}，持有 ${hit.kbCase.holdingDays ?? '—'} 天`
                  : ''
              "
            >
              {{ pctText(hit.kbCase.pnlPct) }}
            </span>
          </header>
          <p class="kb-cases__meta">
            <template v-if="hit.kbCase.pattern">{{ hit.kbCase.pattern }} · </template>
            情绪 {{ phaseName(hit.kbCase.emotionPhase) }}<template v-if="hit.kbCase.strategy"> · {{ hit.kbCase.strategy }}</template>
            <template v-if="hit.kbCase.book"> · {{ hit.kbCase.book === 'paper' ? '模拟' : hit.kbCase.book === 'live' ? '实盘' : hit.kbCase.book }}</template>
            <template v-if="attributionLabel(hit.kbCase.attribution)"> · {{ attributionLabel(hit.kbCase.attribution) }}</template>
          </p>
          <p class="kb-cases__time">
            信号 {{ clock(hit.kbCase.signalTime) || '—' }} → 结束 {{ clock(hit.kbCase.closedAt) || '—' }}
            <span v-if="hit.score != null"> · 相似分 {{ hit.score.toFixed(3) }}</span>
          </p>
        </li>
      </ul>
      <p v-else class="nt-note">
        {{ csBusy ? '查询中…' : csRes ? '没有符合条件的案例' : '情形留空直接查询 = 最近结束的案例' }}
      </p>
    </section>

    <section v-show="tab === 'upload'" class="nt-panel">
      <div class="kb-form">
        <div class="kb-form__row">
          <NtSelect v-model="up.sourceType" label="来源类型" :options="SOURCE_TYPE_OPTIONS" @update:model-value="onModeChange" />
          <NtSelect v-model="up.category" label="分类（必选）" placeholder="选择分类" :options="CATEGORY_OPTIONS" />
        </div>

        <div v-if="up.sourceType === 'file'" class="kb-form__row kb-form__row--col">
          <label class="kb-file">
            <span class="nt-input__label">文件（md / txt / docx / xlsx / pdf，≤ 20 MB）</span>
            <input type="file" accept=".md,.txt,.docx,.xlsx,.pdf" @change="onPick" />
            <span class="mono">{{ upFileText }}</span>
          </label>
          <p class="kb-hint">旧版 .doc/.xls 与扫描 PDF 不收；重复内容会直接指向原文档，不会重复占库。</p>
          <NtInput v-model="up.source" label="出处（可空，默认用文件名）" placeholder="例如：研报 / 公告 / 笔记路径" />
        </div>

        <div v-else-if="up.sourceType === 'url'" class="kb-form__row kb-form__row--col">
          <NtInput v-model="up.source" label="链接（http/https）" placeholder="https://…" mono />
          <p class="kb-hint">界面不抓取网页：把网页正文贴到下面，链接只作出处入库。</p>
        </div>

        <div v-else class="kb-form__row kb-form__row--col">
          <div class="kb-form__row">
            <NtInput v-model="up.author" label="作者 / 录入人（必填）" class="grow" />
            <NtInput v-model="up.source" label="出处说明（必填）" placeholder="例如：个人复盘笔记 2026-10" class="grow" />
          </div>
        </div>

        <label v-if="up.sourceType !== 'file'" class="kb-ta">
          <span class="nt-input__label">内容文本</span>
          <textarea v-model="up.text" rows="7" placeholder="粘贴要入库的正文；按文件内容分块入向量" />
        </label>

        <div class="kb-form__row">
          <NtInput v-model="up.title" label="标题（空 = 取自文件/链接）" class="grow" />
        </div>
        <div class="kb-form__row">
          <NtInput v-model="up.stockCodesText" label="关联股票代码（逗号/空格分隔）" mono class="grow" placeholder="600519, 300750" />
          <NtInput v-model="up.tagsText" label="标签（逗号/空格分隔）" class="grow" />
        </div>
        <div class="kb-form__row">
          <label class="kb-dt">
            <span class="nt-input__label">有效期至（可空）</span>
            <input v-model="up.expiresLocal" type="datetime-local" />
          </label>
        </div>

        <p v-if="upErr" class="nt-err">{{ upErr }}</p>
        <p v-if="upMsg" class="kb-ok">{{ upMsg }}</p>

        <div class="kb-actions kb-actions--end">
          <NtButton
            size="sm"
            variant="primary"
            :loading="upBusy"
            :disabled="up.sourceType === 'file' ? !upBase64 : false"
            @click="submitIngest"
          >
            入库
          </NtButton>
        </div>
      </div>
    </section>
  </div>
</template>

<style scoped>
.kb-bar {
  display: flex;
  align-items: flex-end;
  gap: 8px;
  margin-bottom: 8px;
}

.grow {
  flex: 1;
  min-width: 0;
}

.w140 {
  width: 140px;
}

.w160 {
  width: 160px;
}

.kb-note-line {
  display: flex;
  align-items: center;
  gap: 10px;
  margin: 2px 0 8px;
  font-size: 12px;
  color: var(--nt-text-3);
}

.kb-page {
  align-self: center;
  font-size: 12px;
  color: var(--nt-text-3);
}

.kb-hits,
.kb-cases {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kb-hits__item,
.kb-cases__item {
  padding: 8px 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}

.kb-hits__head,
.kb-cases__head {
  display: flex;
  align-items: center;
  flex-wrap: wrap;
  gap: 8px;
  font-size: 13px;
  color: var(--nt-text-1);
}

.kb-hits__ranks {
  margin-left: auto;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.kb-hits__src {
  margin: 4px 0 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.kb-hits__body {
  margin: 6px 0 6px;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--nt-text-2);
  display: -webkit-box;
  -webkit-box-orient: vertical;
  -webkit-line-clamp: 3;
  overflow: hidden;
}

.kb-cases__pnl {
  margin-left: auto;
  font-size: 13px;
  font-weight: 600;
}

.kb-cases__meta,
.kb-cases__time {
  margin: 4px 0 0;
  font-size: 12px;
  color: var(--nt-text-2);
}

.kb-cases__time {
  color: var(--nt-text-3);
  font-size: 11.5px;
}

.kb-actions {
  display: flex;
  gap: 6px;
  margin-top: 4px;
}

.kb-actions--end {
  justify-content: flex-end;
  margin-top: 2px;
}

.kb-detail {
  max-height: 64vh;
  overflow: auto;
  padding-right: 4px;
}

.kb-detail__meta {
  display: grid;
  grid-template-columns: repeat(2, minmax(0, 1fr));
  gap: 6px 16px;
  margin: 0 0 10px;
}

.kb-detail__meta dt {
  font-size: 11px;
  color: var(--nt-text-3);
}

.kb-detail__meta dd {
  display: flex;
  align-items: center;
  gap: 6px;
  margin: 0;
  font-size: 12.5px;
  color: var(--nt-text-1);
}

.kb-detail__sub {
  margin: 10px 0 6px;
  font-size: 12.5px;
  font-weight: 600;
  color: var(--nt-text-2);
}

.kb-chunks {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 8px;
}

.kb-chunks li {
  padding: 8px 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}

.kb-chunks header {
  display: flex;
  align-items: center;
  gap: 8px;
  font-size: 12px;
  color: var(--nt-text-1);
}

.kb-chunks p {
  margin: 6px 0 0;
  font-size: 12.5px;
  line-height: 1.6;
  color: var(--nt-text-2);
  white-space: pre-wrap;
}

.kb-form {
  display: flex;
  flex-direction: column;
  gap: 10px;
  max-width: 720px;
}

.kb-form__row {
  display: flex;
  gap: 8px;
}

.kb-form__row--col {
  flex-direction: column;
}

.kb-file {
  display: flex;
  flex-direction: column;
  gap: 6px;
  font-size: 12px;
  color: var(--nt-text-2);
}

.kb-file input[type='file'] {
  width: 100%;
  box-sizing: border-box;
  padding: 6px 10px;
  font-size: 12.5px;
  color: var(--nt-text-2);
  background: var(--nt-bg-0);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
}

.kb-file input[type='file']::file-selector-button {
  margin-right: 8px;
  padding: 2px 10px;
  font-size: 12px;
  color: var(--nt-text-1);
  background: var(--nt-bg-3);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.kb-hint {
  margin: 0;
  font-size: 11.5px;
  color: var(--nt-text-3);
}

.kb-ta {
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.kb-ta textarea,
.kb-dt input {
  width: 100%;
  box-sizing: border-box;
  padding: 6px 10px;
  font-size: 13px;
  color: var(--nt-text-1);
  background: var(--nt-bg-0);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  outline: none;
  resize: vertical;
}

.kb-ta textarea:focus,
.kb-dt input:focus {
  border-color: var(--nt-accent);
}

.kb-dt {
  display: flex;
  flex-direction: column;
  gap: 4px;
  width: 240px;
}

.kb-ok {
  margin: 0;
  font-size: 12.5px;
  color: var(--nt-success);
}

.mono {
  font-variant-numeric: tabular-nums;
  font-family: var(--nt-font-mono);
}
</style>
