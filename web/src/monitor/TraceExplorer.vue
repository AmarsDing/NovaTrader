<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { getTrace, type TraceSnapshot } from '@/api/traces'
import { clock, messageOf } from '@/utils/format'
import { useOverlay } from '@/ui/useOverlay'
const overlay = useOverlay(() => close())
const query = ref(''),
  snapshot = ref<TraceSnapshot | null>(null),
  busy = ref(false),
  error = ref('')
let generation = 0,
  controller: AbortController | undefined
async function load() {
  const run = ++generation
  controller?.abort()
  controller = new AbortController()
  busy.value = true
  error.value = ''
  snapshot.value = null
  try {
    const out = await getTrace(query.value.trim(), controller.signal)
    if (run === generation) snapshot.value = out
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
</style>
