<script setup lang="ts">
/**
 * NtFreshness（方案 §7.3）：面板头右上「数据新鲜度」徽注。
 * 合同：「{label} {相对年龄}」（超阈染 warn）/「断 · 最后 HH:mm:ss」（warn）/
 * 「{label} v{n}·已生效」（version 优先生效，但不掩盖 cut）/「{label} 时点未知」（text-3 弱化）。
 * 数据纪律：无时间证据绝不冒充 0s/当前时刻；未来时间钳到 0s（时钟微偏不算未知）。
 * 读秒用 serverNow（漂移补偿后的交易时刻口径），1s 自刷新让超阈即时显形。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { serverNow } from '@/utils/server-clock'
import { formatClock, formatDate, formatDateTime, formatMinute } from '@/utils/time'

const props = withDefaults(
  defineProps<{
    /** 类别名：行情 / 情报 / 配置… */
    label: string
    /** 数据时点（epoch 毫秒）；null/缺失 = 无时间证据。 */
    at?: number | null
    /** 新鲜度阈值（毫秒），年龄超过即染 warn。 */
    staleAfterMs?: number
    /** 链路已断：显「断 · 最后 HH:mm:ss」，优先级最高。 */
    cut?: boolean
    /** 配置类版本号：显「v{n}·已生效」，优先于年龄分支但不掩盖 cut。 */
    version?: string | number | null
  }>(),
  { at: null, staleAfterMs: 30000, cut: false, version: null },
)

const now = ref(serverNow())
let timer: ReturnType<typeof setInterval> | undefined
onMounted(() => {
  timer = setInterval(() => {
    now.value = serverNow()
  }, 1000)
})
onBeforeUnmount(() => {
  if (timer) clearInterval(timer)
})

/** 合法时间证据：有限数值；其余一律视为缺失（数据纪律：没取到 ≠ 0）。 */
const atMs = computed(() => (typeof props.at === 'number' && Number.isFinite(props.at) ? props.at : null))

interface FreshView {
  label: string
  value: string
  tone: 'normal' | 'warn' | 'muted'
}

const view = computed<FreshView>(() => {
  const at = atMs.value
  if (props.cut) {
    return { label: '断 ·', value: at == null ? '时点未知' : `最后 ${formatClock(at)}`, tone: 'warn' }
  }
  if (props.version !== undefined && props.version !== null && props.version !== '') {
    return { label: props.label, value: `v${props.version}·已生效`, tone: 'muted' }
  }
  if (at == null) return { label: props.label, value: '时点未知', tone: 'muted' }
  const ageMs = Math.max(0, now.value - at)
  const ageSec = Math.floor(ageMs / 1000)
  let ageText: string
  if (ageSec < 60) ageText = `${ageSec}s`
  else if (ageSec < 3600) ageText = `${Math.floor(ageSec / 60)}min`
  else if (ageSec < 86400) ageText = `${Math.floor(ageSec / 3600)}h·${formatMinute(at)}`
  else ageText = `${Math.floor(ageSec / 86400)}d·${formatDate(at).slice(5)}`
  return { label: props.label, value: ageText, tone: ageMs > props.staleAfterMs ? 'warn' : 'normal' }
})

const titleText = computed(() => (atMs.value == null ? '' : formatDateTime(atMs.value)))
</script>

<template>
  <span
    class="nf"
    :class="{ 'nf--warn': view.tone === 'warn', 'nf--muted': view.tone === 'muted' }"
    :title="titleText"
  >
    <span class="nf__label">{{ view.label }}</span> <span class="nf__value">{{ view.value }}</span>
  </span>
</template>

<style scoped>
.nf {
  font-size: 0.92em;
  line-height: 1.4;
  color: var(--nt-text-2);
  white-space: nowrap;
}

.nf--warn {
  color: var(--nt-warn);
}

.nf--muted {
  color: var(--nt-text-3);
}

.nf__value {
  font-variant-numeric: tabular-nums;
}
</style>
