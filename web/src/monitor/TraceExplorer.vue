<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import {
  DISPOSITION_ACTIONS,
  dispositionLabel,
  getTrace,
  listDispositions,
  writeDisposition,
  type Disposition,
  type TraceSnapshot,
} from '@/api/traces'
import { useAuthStore } from '@/stores/auth'
import { clock, messageOf } from '@/utils/format'
import { useOverlay } from '@/ui/useOverlay'
const overlay = useOverlay(() => close())
const auth = useAuthStore()
const isOwner = computed(() => auth.user?.role === 'owner')
const query = ref(''),
  snapshot = ref<TraceSnapshot | null>(null),
  busy = ref(false),
  error = ref(''),
  dispositions = ref<Disposition[]>([]),
  dispositionError = ref('')
let generation = 0,
  controller: AbortController | undefined
async function load() {
  const run = ++generation
  controller?.abort()
  controller = new AbortController()
  busy.value = true
  error.value = ''
  snapshot.value = null
  dispositions.value = []
  dispositionError.value = ''
  try {
    const out = await getTrace(query.value.trim(), controller.signal)
    if (run !== generation) return
    snapshot.value = out
    if (out.events.length && !out.events.some((e) => e.eventId === draft.value.eventId)) {
      draft.value.eventId = out.events[0].eventId
    }
    const trace = query.value.trim()
    if (trace && out.events.length) {
      try {
        const rows = await listDispositions({ traceId: trace }, controller.signal)
        if (run === generation) dispositions.value = rows
      } catch (e) {
        if (run === generation) dispositionError.value = messageOf(e)
      }
    }
  } catch (e) {
    if (run === generation) error.value = messageOf(e)
  } finally {
    if (run === generation) busy.value = false
  }
}
function open() {
  if (!overlay.visible.value) overlay.open()
  void load()
}
function close() {
  generation++
  controller?.abort()
  busy.value = false
  overlay.close()
}
onBeforeUnmount(() => {
  generation++
  controller?.abort()
})
const states: Record<string, string> = {
  pending: '待投递',
  published: '总线投递成功',
  leased: '正在投递',
  retry_wait: '退避等待',
  dead_letter: '死信待处置',
}

/* 处置留痕（N2-02 处置回执）：只标注，不改投递状态、不回放事件。仅 Owner 可写。 */
const draft = ref({ eventId: '', action: 'acknowledged' as string, note: '' }),
  draftBusy = ref(false),
  draftError = ref('')
async function send() {
  if (!draft.value.eventId || draftBusy.value) return
  draftBusy.value = true
  draftError.value = ''
  try {
    await writeDisposition({
      eventId: draft.value.eventId,
      traceId: query.value.trim(),
      action: draft.value.action,
      note: draft.value.note.trim(),
    })
    draft.value.note = ''
    dispositions.value = await listDispositions({ traceId: query.value.trim() })
  } catch (e) {
    draftError.value = messageOf(e)
  } finally {
    draftBusy.value = false
  }
}
function shortEvent(id: string): string {
  return id.length > 20 ? `${id.slice(0, 20)}…` : id
}
</script>
<template>
  <button type="button" class="nt-link" @click="open">事件追踪</button>
  <Teleport to="body"
    ><div
      v-if="overlay.visible.value"
      class="nt-pop"
      :style="{ zIndex: overlay.zIndex.value }"
      role="dialog"
      aria-modal="true"
      aria-label="持久事件追踪"
      @click.self="close"
    >
      <section class="nt-pop__sheet nt-pop__sheet--deep trace-sheet">
        <header>
          <div>
            <h2>事件追踪与投递状态</h2>
            <p>跨服务持久事件 · 原身份 · 直接父事件</p>
          </div>
          <button type="button" @click="close">关闭</button>
        </header>
        <form @submit.prevent="load">
          <input
            v-model="query"
            aria-label="精确trace_id"
            placeholder="输入精确 trace_id，留空查看投递概况"
            maxlength="128"
          /><button type="submit" :disabled="busy">{{ busy ? '查询中' : '查询' }}</button>
        </form>
        <p v-if="error" class="nt-err">{{ error }}</p>
        <p v-if="!snapshot?.available">持久事件仓储尚未核验，数量未知。</p>
        <template v-else
          ><dl>
            <div>
              <dt>待投递（含租约）</dt>
              <dd>{{ snapshot.pending ?? '—' }}</dd>
            </div>
            <div>
              <dt>正在投递</dt>
              <dd>{{ snapshot.leased ?? '—' }}</dd>
            </div>
            <div>
              <dt>死信</dt>
              <dd>{{ snapshot.deadLetters ?? '—' }}</dd>
            </div>
            <div>
              <dt>最早待投递形成时间</dt>
              <dd>{{ clock(snapshot.oldestPendingAt) || '—' }}</dd>
            </div>
          </dl>
          <p class="note">{{ snapshot.note }} 截至 {{ clock(snapshot.asOf) }}。</p>
          <p v-if="snapshot.truncated" class="warn">结果超过200条，当前仅展示部分链路。</p>
          <p v-if="snapshot.missingParents.length" class="warn">
            本次未观测到 {{ snapshot.missingParents.length }} 个直接父事件，关联链条不完整。
          </p>
          <p v-if="query && !snapshot.events.length">该范围没有持久事件记录；不能据此推断业务从未发生。</p>
          <section v-if="query && snapshot.events.length" class="receipts" aria-label="处置留痕">
            <h3>处置留痕</h3>
            <p class="note">留痕只标注，不改投递状态、不回放事件；死信重放与处置审批另需实现。</p>
            <p v-if="dispositionError" class="nt-err">留痕读取失败：{{ dispositionError }}</p>
            <p v-if="!dispositions.length && !dispositionError" class="note">本链路暂无处置留痕。</p>
            <ul>
              <li v-for="d in dispositions" :key="d.id">
                {{ dispositionLabel(d.action) }} · {{ d.actor }} · {{ clock(d.createdAt) || '时间未知' }}<template
                  v-if="d.note"
                >
                  — {{ d.note }}</template
                >
              </li>
            </ul>
            <form v-if="isOwner" class="receipt-form" @submit.prevent="send">
              <select v-model="draft.eventId" aria-label="留痕目标事件">
                <option v-for="e in snapshot.events" :key="e.eventId" :value="e.eventId">
                  {{ e.subject }} · {{ shortEvent(e.eventId) }}
                </option>
              </select>
              <select v-model="draft.action" aria-label="处置动作">
                <option v-for="[v, l] in DISPOSITION_ACTIONS" :key="v" :value="v">{{ l }}</option>
              </select>
              <input v-model="draft.note" maxlength="280" placeholder="备注（≤280字，可空）" aria-label="处置备注" />
              <button type="submit" :disabled="draftBusy">{{ draftBusy ? '提交中' : '留痕' }}</button>
            </form>
            <p v-else class="note">新增留痕需要 Owner 角色。</p>
            <p v-if="draftError" class="nt-err">{{ draftError }}</p>
          </section>
          <ol>
            <li v-for="e in snapshot.events" :key="e.eventId" :class="{ failed: e.state === 'dead_letter' }">
              <div>
                <strong>{{ e.source }} · {{ e.subject }}</strong
                ><span>{{ states[e.state] || '状态未知' }}</span>
              </div>
              <p>
                形成 {{ clock(e.formedAt) }} · 源 {{ e.sourceTime ? clock(e.sourceTime) : '未知' }} · 投递
                {{ e.publishedAt ? clock(e.publishedAt) : '尚无成功记录' }} · 尝试 {{ e.attempts ?? '—' }}
              </p>
              <code>{{ e.eventId }}</code>
              <p>直接父事件 {{ e.causationId || '未记录' }} · 实体 {{ e.entityId || '未记录' }}</p>
              <p v-if="e.errorClass" class="warn">{{ e.errorClass }}</p>
            </li>
          </ol>
        </template>
      </section>
    </div></Teleport
  >
</template>
<style scoped>
.trace-sheet {
  max-width: 900px;
}
form {
  display: flex;
  gap: 8px;
  margin: 14px 0;
}
input {
  flex: 1;
  min-width: 0;
  padding: 9px;
  color: var(--nt-text-1);
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
button {
  cursor: pointer;
}
dl {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(140px, 1fr));
  gap: 12px;
}
dt,
.note {
  font-size: 11px;
  color: var(--nt-text-2);
}
dd {
  margin: 5px 0;
  font: 18px var(--nt-font-mono);
  color: var(--nt-accent);
}
ol {
  list-style: none;
  padding: 0;
  border-left: 1px solid var(--nt-accent-border);
}
li {
  margin: 12px 0 12px 10px;
  padding: 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
li > div {
  display: flex;
  justify-content: space-between;
  gap: 10px;
}
li p {
  font-size: 11px;
  color: var(--nt-text-2);
}
code {
  font-size: 10px;
  overflow-wrap: anywhere;
}
.warn {
  color: var(--nt-warn) !important;
}
.failed {
  border-color: var(--nt-warn);
}
.receipts {
  margin: 12px 0;
  padding: 12px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
.receipts h3 {
  margin: 0 8px 6px 0;
  font-size: 12px;
  color: var(--nt-text-1);
}
.receipts ul {
  margin: 8px 0;
  padding: 0;
  list-style: none;
}
.receipts ul li {
  margin: 6px 0;
  padding: 6px 8px;
  font-size: 12px;
  color: var(--nt-text-1);
  background: transparent;
  border: none;
  border-bottom: 1px dashed var(--nt-border-1);
  border-radius: 0;
}
.receipt-form {
  align-items: center;
  margin: 10px 0 0;
}
.receipt-form select {
  padding: 8px;
  max-width: 200px;
  color: var(--nt-text-1);
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
}
</style>
