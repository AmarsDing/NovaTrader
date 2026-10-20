<script setup lang="ts">
import { computed, type PropType } from 'vue'
import type { ConfigPreview, ConfigSnapshot } from '@/api/config'
// 运行式 props：dirty 必须保住 undefined 哨兵（type-only 的 Boolean 缺省会被 Vue 铸成 false，
// MonitorView 等不传 dirty 的挂点会误亮条带）；其余 prop 行为与原 type-only 声明一致。
const props = defineProps({
  snapshot: { type: Object as PropType<ConfigSnapshot | null>, default: null },
  preview: { type: Object as PropType<ConfigPreview | null>, default: null },
  compact: { type: Boolean, default: false },
  dirty: { type: Boolean as PropType<boolean | undefined>, default: undefined },
})
defineEmits<{ refresh: []; rollback: [revision: number] }>()
function status(status: string): string { return ({ applied: '已安装到运行态', cache_loaded: '配置缓存已加载', failed: '加载失败' } as Record<string, string>)[status] || '状态未登记' }
// 生效三态（§9.2）：dirty 未传入（MonitorView 挂点不传，禁改）整条带不渲染。
type TripleStage = 'editing' | 'pending' | 'running'
/** 有目标服务尚无当前版本成功回执（或无任何回执观测）→ 保存版仍待生效。 */
const hasPending = computed(() => {
  const s = props.snapshot
  if (!s) return false
  if (!s.targetServices || s.targetServices.length === 0) return s.receipts.length === 0
  return s.targetServices.some(svc => !s.receipts.some(r => r.service === svc && r.revision === s.revision && r.status !== 'failed'))
})
const currentStage = computed<TripleStage | ''>(() => {
  if (props.dirty === undefined) return ''
  if (props.dirty) return 'editing'
  return hasPending.value ? 'pending' : 'running'
})
/** 版本号从 snapshot 现有字段取，取不到就只渲染态名（§9.2）；运行版仅在没有回执缺口时可确证为当前保存版。 */
const savedVersionText = computed(() => (props.snapshot != null ? ` v${props.snapshot.revision}` : ''))
const runningVersionText = computed(() => (!hasPending.value && props.snapshot != null ? ` v${props.snapshot.revision}` : ''))
const stageClass = (stage: TripleStage) => ({ [`stage--${stage}`]: true, 'stage--current': currentStage.value === stage })
</script>
<template>
  <section class="revision-panel" aria-label="配置版本和服务加载回执">
    <header><strong>保存版本 {{ snapshot ? `#${snapshot.revision}` : '未知' }}</strong><button type="button" @click="$emit('refresh')">刷新回执</button></header>
    <!-- 生效三态条带（§9.2）：dirty 未传入（MonitorView 挂点）整条不渲染；当前档高亮，其余淡化。 -->
    <div v-if="dirty !== undefined" class="triple" aria-label="生效三态">
      <span class="stage" :class="stageClass('editing')">编辑中</span><span class="triple__sep" aria-hidden="true">→</span><span class="stage" :class="stageClass('pending')">已保存待生效{{ savedVersionText }}</span><span class="triple__sep" aria-hidden="true">→</span><span class="stage" :class="stageClass('running')">运行生效{{ runningVersionText }}</span>
    </div>
    <p>保存后需等待目标服务回执。缓存加载不代表外部接口已连通；时间为最后观测，离线实例不会伪报成功。</p>
    <p v-if="snapshot?.receiptError" class="warn">{{ snapshot.receiptError }}</p>
    <p v-if="!snapshot?.receipts.length">尚未观测到服务加载回执。</p>
    <p v-for="service in snapshot?.targetServices || []" :key="service">{{ service }}：{{ snapshot?.receipts.some(r => r.service === service && r.revision === snapshot?.revision && r.status !== 'failed') ? '已有当前版本回执，详见实例和观测时间' : '当前版本尚无成功加载回执' }}</p>
    <div v-for="r in snapshot?.receipts || []" :key="`${r.service}:${r.instance}:${r.scope}`" class="receipt">
      <strong>{{ r.service }} · {{ r.scope }}</strong><span>#{{ r.revision }} · {{ status(r.status) }}{{ r.revision !== snapshot?.revision ? ' · 落后于保存版本' : '' }}</span>
      <small>{{ r.observedAt }} · 实例 {{ r.instance.slice(0, 8) }} · {{ r.detail }}</small>
    </div>
    <details v-if="snapshot?.history.length && !compact"><summary>历史版本与回滚</summary><div v-for="v in snapshot.history" :key="v.revision" class="history"><span>#{{ v.revision }} · {{ v.createdAt }}</span><button v-if="v.revision !== snapshot.revision" type="button" @click="$emit('rollback', v.revision)">预览恢复</button></div><p>恢复会创建新版本，不改写历史版本。</p></details>
    <div v-if="preview" class="preview" aria-label="配置影响预览">
      <strong>基于 #{{ preview.baseRevision }} · {{ preview.changes.length }} 项变更</strong>
      <div v-for="c in preview.changes" :key="c.key" class="change"><code>{{ c.key }}</code><span>{{ c.before || '默认/未设置' }} → {{ c.after || '恢复默认' }}</span><small>{{ c.service }} · {{ c.effective }}</small></div>
      <p v-for="s in preview.knownEffects" :key="s">{{ s }}</p><p v-for="s in preview.unknownEffects" :key="s" class="warn">未测影响：{{ s }}</p>
    </div>
  </section>
</template>
<style scoped>
.revision-panel { margin: 12px 0; padding: 12px; border: 1px solid var(--nt-border); border-radius: 8px; background: var(--nt-bg-2); font-size: 12px; }
header,.history { display:flex; align-items:center; justify-content:space-between; gap:8px; }
p,small { color:var(--nt-text-2); line-height:1.6; }
.receipt,.change { display:grid; gap:4px; padding:8px 0; border-bottom:1px solid var(--nt-border); overflow-wrap:anywhere; }
.preview { margin-top:12px; border-top:1px solid var(--nt-accent); padding-top:12px; }
.warn { color:var(--nt-warn); }
button { color:var(--nt-accent); border:1px solid var(--nt-border); border-radius:4px; padding:4px 8px; background:transparent; cursor:pointer; }
summary { cursor:pointer; padding:8px 0; }
/* 生效三态（§9.2）：当前档按其 tone 高亮（编辑中 accent / 待生效 warn / 生效 success），其余淡化。 */
.triple { display:flex; align-items:center; flex-wrap:wrap; gap:6px; margin:8px 0 4px; font-size: var(--nt-fs-caption); }
.triple__sep { color: var(--nt-text-3); }
.stage { color: var(--nt-text-3); }
.stage--current { font-weight: 600; }
.stage--current.stage--editing { color: var(--nt-accent); }
.stage--current.stage--pending { color: var(--nt-warn); }
.stage--current.stage--running { color: var(--nt-success); }
</style>
