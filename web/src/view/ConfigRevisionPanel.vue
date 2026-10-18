<script setup lang="ts">
import type { ConfigPreview, ConfigSnapshot } from '@/api/config'
defineProps<{ snapshot: ConfigSnapshot | null; preview?: ConfigPreview | null; compact?: boolean }>()
defineEmits<{ refresh: []; rollback: [revision: number] }>()
function status(status: string): string { return ({ applied: '已安装到运行态', cache_loaded: '配置缓存已加载', failed: '加载失败' } as Record<string, string>)[status] || '状态未登记' }
</script>
<template>
  <section class="revision-panel" aria-label="配置版本和服务加载回执">
    <header><strong>保存版本 {{ snapshot ? `#${snapshot.revision}` : '未知' }}</strong><button type="button" @click="$emit('refresh')">刷新回执</button></header>
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
</style>
