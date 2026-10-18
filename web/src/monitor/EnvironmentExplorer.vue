<script setup lang="ts">
import { onBeforeUnmount, ref } from 'vue'
import { getEnvironment, type EnvironmentSnapshot } from '@/api/environment'
import { useOverlay } from '@/ui/useOverlay'
import { clock, messageOf } from '@/utils/format'
import { environmentLabel as label } from './environment-labels'
const overlay = useOverlay(() => close())
const data = ref<EnvironmentSnapshot | null>(null),
  busy = ref(false),
  error = ref('')
let generation = 0,
  controller: AbortController | undefined,
  timer: ReturnType<typeof setInterval> | undefined
const states: Record<string, string> = {
  observed: '已采样',
  offline: '探针未连通',
  unsupported: '未接操作指标',
  unconfigured: '未配置',
  invalid: '合同无效',
  unknown: '未知',
}
const kinds: Record<string, string> = { notification: '本地通知', business: '业务操作', infrastructure: '基础任务' }
function stop() {
  generation++
  controller?.abort()
  if (timer) clearInterval(timer)
  timer = undefined
  busy.value = false
}
async function load() {
  if (busy.value) return
  const run = ++generation
  controller = new AbortController()
  busy.value = true
  try {
    const d = await getEnvironment(controller.signal)
    if (run === generation) {
      data.value = d
      error.value = ''
    }
  } catch (e) {
    if (run === generation) {
      data.value = null
      error.value = messageOf(e)
    }
  } finally {
    if (run === generation) busy.value = false
  }
}
function open() {
  if (overlay.visible.value) return
  overlay.open()
  data.value = null
  void load()
  timer = setInterval(() => {
    if (!document.hidden) void load()
  }, 5000)
}
function close() {
  stop()
  overlay.close()
}
onBeforeUnmount(stop)
</script>
<template>
  <button type="button" class="nt-link" @click="open">环境与消费者</button>
  <Teleport to="body"
    ><div
      v-if="overlay.visible.value"
      class="nt-pop"
      :style="{ zIndex: overlay.zIndex.value }"
      role="dialog"
      aria-modal="true"
      aria-label="环境与消费者观测"
      @click.self="close"
    >
      <section class="nt-pop__sheet nt-pop__sheet--deep environment-sheet">
        <header>
          <div>
            <span class="eyebrow">ENVIRONMENT / CONSUMER TELEMETRY</span>
            <h2>环境与消费者 · 实际处理进度</h2>
          </div>
          <button type="button" @click="close">关闭</button>
        </header>
        <p class="note">
          计数按本实例最近窗口采样。收到通知、进入队列、处理返回分别展示；模型规则降级计入跳过，输出丢弃计入拒绝。完成范围以每项声明为准。
        </p>
        <button type="button" :disabled="busy" @click="load">{{ busy ? '采样中' : '重新采样' }}</button>
        <p v-if="error" class="nt-err">{{ error }}</p>
        <p v-if="!data" class="note">进程与消费者指标尚未核验，数量未知。</p>
        <template v-else
          ><p class="note">
            网关采样 {{ clock(data.sampledAt) }} · {{ data.cached ? '使用共享缓存' : '本批探针' }} · {{ data.note }}
          </p>
          <article
            v-for="s in data.services"
            :key="s.service"
            class="service"
            :class="{ observed: s.state === 'observed' }"
          >
            <h3>
              {{ label(s.service) }} <span>{{ states[s.state] }}</span>
            </h3>
            <p class="note">
              {{ label(s.note) }} · 探针往返
              {{ s.probeDurationMs == null ? '未知' : `${s.probeDurationMs.toFixed(1)} ms` }}（HTTP，不代表业务延迟）
            </p>
            <template v-if="s.observation"
              ><p class="meta">
                {{ s.observation.host || '主机未知' }} · {{ s.observation.os }}/{{ s.observation.arch }} · 实例
                {{ s.observation.instanceId }} · 进程采样 {{ clock(s.observation.sampledAt) }}
              </p>
              <div class="facts">
                <span>协程 {{ s.observation.goroutines ?? '—' }}</span
                ><span>堆内存 {{ s.observation.heapBytes ?? '—' }} B</span
                ><span
                  >窗口 {{ s.observation.observedSeconds?.toFixed(1) ?? '—' }}/{{
                    s.observation.windowSeconds ?? '—'
                  }}
                  s · {{ s.observation.windowComplete ? '完整' : '尚未攒满' }}</span
                >
              </div>
              <div class="table-wrap">
                <table>
                  <thead>
                    <tr>
                      <th>操作 / 输入</th>
                      <th>收到 / 入队 / 合并</th>
                      <th>开始 / 成功返回</th>
                      <th>失败 / 拒绝 / 取消 / 跳过</th>
                      <th>在途</th>
                      <th>P95 耗时上界</th>
                    </tr>
                  </thead>
                  <tbody>
                    <tr v-for="op in s.observation.operations" :key="op.key">
                      <td>
                        <b>{{ label(op.key) }}</b
                        ><small>{{ kinds[op.kind] || op.kind }} · {{ op.input }}</small
                        ><small>完成范围：{{ label(op.completion) || '未知' }}</small>
                      </td>
                      <td>{{ op.received ?? '—' }} / {{ op.queued ?? '—' }} / {{ op.coalesced ?? '—' }}</td>
                      <td>{{ op.attempted ?? '—' }} / {{ op.succeeded ?? '—' }}</td>
                      <td>
                        {{ op.failed ?? '—' }} / {{ op.rejected ?? '—' }} / {{ op.cancelled ?? '—' }} /
                        {{ op.skipped ?? '—' }}
                      </td>
                      <td>{{ op.inflight ?? '—' }}</td>
                      <td>
                        {{ op.p95UpperMs == null ? '未知 / 未收集' : `≤ ${op.p95UpperMs} ms`
                        }}<small>最近完成 {{ clock(op.lastCompletedAt) || '—' }}</small>
                      </td>
                    </tr>
                  </tbody>
                </table>
              </div>
              <details>
                <summary>该进程尚不支持的指标</summary>
                <p>{{ s.observation.unsupported.map(label).join(' · ') || '未声明' }}</p>
                <p>时间口径：{{ s.observation.windowBasis }}</p>
              </details>
            </template>
          </article>
          <p class="note">环境覆盖边界：{{ data.unsupported.map(label).join(' · ') || '未声明' }}</p>
        </template>
      </section>
    </div></Teleport
  >
</template>
<style scoped>
.environment-sheet {
  width: min(1180px, 96vw);
}
header {
  display: flex;
  justify-content: space-between;
  gap: 12px;
}
.eyebrow {
  font: 10px var(--nt-font-mono);
  letter-spacing: 0.16em;
  color: var(--nt-accent);
}
h2 {
  margin: 5px 0 10px;
  font-size: 17px;
}
h3 {
  margin: 0;
  font-size: 14px;
}
h3 span {
  margin-left: 12px;
  font-size: 11px;
  color: var(--nt-warn);
}
.observed h3 span {
  color: var(--nt-accent);
}
.service {
  margin-top: 12px;
  padding: 12px;
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
}
.note,
.meta,
details {
  font-size: 11px;
  color: var(--nt-text-2);
  line-height: 1.7;
}
.meta {
  overflow-wrap: anywhere;
}
.facts {
  display: flex;
  flex-wrap: wrap;
  gap: 16px;
  font: 11px var(--nt-font-mono);
  color: var(--nt-text-1);
  padding: 8px 0;
}
.table-wrap {
  overflow-x: auto;
}
table {
  width: 100%;
  border-collapse: collapse;
  font: 11px var(--nt-font-mono);
}
th,
td {
  padding: 8px;
  text-align: left;
  border-bottom: 1px solid var(--nt-border-1);
  vertical-align: top;
}
th {
  color: var(--nt-text-2);
}
small {
  display: block;
  margin-top: 5px;
  color: var(--nt-text-2);
  max-width: 300px;
  line-height: 1.5;
}
summary {
  cursor: pointer;
}
</style>
