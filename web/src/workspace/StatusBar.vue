<script setup lang="ts">
/**
 * 底栏 chrome（不进 dock）：链路健康、交易模式、Kill Switch、WS 主题、上海时段钟。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import NtStatusDot from '@/ui/NtStatusDot.vue'
import { useRiskStore } from '@/stores/risk'
import { formatClock, SESSION_TEXT, sessionOf } from '@/utils/time'
import { desiredTopics, isLive, link } from '@/ws/hub'

const risk = useRiskStore()

const now = ref(new Date())
let timer: ReturnType<typeof setInterval> | undefined

const sessionText = computed(() => SESSION_TEXT[sessionOf(now.value)])
const clockText = computed(() => formatClock(now.value))
/** 主题健康依赖 hub 的内部计数器，跟着秒针与最后收包时刻重算。 */
const topics = computed(() => {
  void link.lastAt
  void now.value
  return desiredTopics().join(' · ')
})

onMounted(() => {
  timer = setInterval(() => {
    now.value = new Date()
  }, 1000)
})

onBeforeUnmount(() => clearInterval(timer))
</script>

<template>
  <footer class="bar">
    <div class="cell">
      <NtStatusDot :status="link.health === 'ok' ? 'live' : 'down'" />
      admin 2001 · {{ link.health === 'ok' ? '可达' : '中断' }}
    </div>
    <div class="cell">
      <NtStatusDot :status="link.ws === 'open' ? 'live' : 'down'" />
      行情通道 2003 · {{ link.ws === 'open' ? '已连接' : '中断' }}
    </div>
    <div class="cell">模式 {{ risk.mode || '—' }}</div>
    <div class="cell" :class="{ warn: risk.killActive }">
      Kill Switch {{ risk.killActive ? '已触发' : '未触发' }}
    </div>
    <div class="cell dim">{{ isLive() ? '数据实时' : '不是实时' }}</div>
    <div class="cell topics" :title="topics">订阅 {{ topics }}</div>
    <div class="cell clock">
      {{ sessionText }} · {{ clockText }}
    </div>
  </footer>
</template>

<style scoped>
.bar {
  display: flex;
  align-items: center;
  gap: 8px 18px;
  padding: 6px 14px;
  flex-wrap: wrap;
  font-size: 12px;
  color: var(--nt-text-2);
  background: var(--nt-bg-1);
  border-top: 1px solid var(--nt-border-1);
}

.cell {
  display: flex;
  align-items: center;
  gap: 6px;
  white-space: nowrap;
}

.cell.warn {
  color: var(--nt-danger);
}

.cell.dim {
  margin-left: auto;
}

.topics {
  overflow: hidden;
  text-overflow: ellipsis;
  max-width: 40vw;
  color: var(--nt-text-3);
}

.clock {
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  color: var(--nt-text-2);
}
</style>
