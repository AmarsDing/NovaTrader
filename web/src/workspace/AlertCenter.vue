<script setup lang="ts">
/**
 * AlertCenter：极端行情全屏模态锁（发光预算允许的第三处玻璃）。
 * - 触发源：risk.killActive（kill switch / 熔断 / 模式切换 10s 轮询）+ WS alert 主题
 *   level=critical 的帧（risk.alert 的 kind=killswitch/breaker/reject_rate/order_unknown 均为 critical）；
 * - 模态锁：只许确认（走 useOverlay 栈顶，Esc 不静默关，而是确认）；
 * - 声音：WebAudio 双音循环直到确认（设置面板可关，settings.sound）；
 * - 队列：多来源事件排队展示，确认一条看下一条。
 */
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useRiskStore } from '@/stores/risk'
import { useSettingsStore } from '@/stores/settings'
import { useOverlay } from '@/ui/useOverlay'
import { startAlarm, stopAlarm } from '@/utils/sound'
import { clock, pick } from '@/utils/format'
import { onPush } from '@/ws/hub'

interface AlertEvent {
  id: number
  kind: string
  source: string
  title: string
  body: string
  at: string
}

/** 页面级告警入口（如监控 S2 的交易通道静默）。外部只给一条新事件，这里按 id 入队一次。 */
interface ExtraAlert {
  id: string
  kind?: string
  source?: string
  title: string
  body: string
  at: string
}

const props = defineProps<{ extra?: ExtraAlert | null }>()

const risk = useRiskStore()
const settings = useSettingsStore()

let seq = 0
const queue = ref<AlertEvent[]>([])
const current = computed<AlertEvent | null>(() => queue.value[0] ?? null)
/** 防重入：risk 状态每次 refresh 都是同一个 killActive，只在跳变时入队。 */
let killSeen = false

const overlay = useOverlay(() => acknowledge())
const muted = ref(false)
const ringing = ref(false)

function push(kind: string, source: string, title: string, body: string, at: string) {
  queue.value.push({ id: ++seq, kind, source, title, body, at })
  if (!overlay.visible.value) overlay.open()
}

function onPushMessage(msg: { topic: string; data: unknown }) {
  if (msg.topic !== 'alert' || !msg.data || typeof msg.data !== 'object') return
  const record = msg.data as Record<string, unknown>
  const kind = pick<string>(record, 'kind', 'type') || ''
  if (kind !== 'killswitch' && kind !== 'breaker' && kind !== 'reject_rate' && kind !== 'order_unknown') return
  const level = pick<string>(record, 'level', 'severity') || ''
  if (level && level !== 'critical') return
  // kill_active 帧由 risk store 的 watch 统一接管，避免双份。
  if (kind === 'killswitch') return
  const account = pick<string>(record, 'account_type', 'accountType') || ''
  const source = pick<string>(record, 'source') || 'risk'
  const at = pick<string>(record, 'at', 'time') || new Date().toISOString()
  push(kind, account ? `${source} · ${account}` : source, titleOf(kind), bodyCopy(record, kind, at), at)
}

function titleOf(kind: string): string {
  switch (kind) {
    case 'killswitch':
      return 'Kill Switch 已触发'
    case 'breaker':
      return '熔断已锁存'
    case 'reject_rate':
      return '拦截率预警'
    case 'order_unknown':
      return '委托状态未知'
    default:
      return '风控预警'
  }
}

/** §8.2 五段式正文：对象 · 事件 · 条件 · 时点（对象缺省「系统」）。只组文案，模板结构不动。 */
function bodyCopy(record: Record<string, unknown>, kind: string, at: string): string {
  const object = pick<string>(record, 'symbol', 'code') || '系统'
  const event = titleOf(kind)
  const condition = pick<string>(record, 'message', 'reason') || '触发风控预警'
  return [object, event, condition, clock(at)].join(' · ')
}

watch(
  () => risk.killActive,
  (active) => {
    if (active && !killSeen) {
      killSeen = true
      push(
        'killswitch',
        risk.killSource || 'risk',
        'Kill Switch 已触发',
        risk.killReason || '停止新开仓',
        risk.killAt || new Date().toISOString(),
      )
    } else if (!active) {
      killSeen = false
    }
  },
  { immediate: true },
)

watch(
  () => props.extra?.id,
  (id) => {
    const item = props.extra
    if (!id || !item) return
    push(item.kind || 'silence', item.source || 'monitor', item.title, item.body, item.at)
  },
)

watch(current, (item) => {
  if (item && settings.sound && !muted.value) {
    ringing.value = startAlarm()
  } else {
    stopAlarm()
    ringing.value = false
  }
})

watch(
  () => [settings.sound, muted.value],
  () => {
    if (current.value && settings.sound && !muted.value) {
      ringing.value = startAlarm()
    } else {
      stopAlarm()
      ringing.value = false
    }
  },
)

function acknowledge() {
  stopAlarm()
  ringing.value = false
  muted.value = false
  queue.value.shift()
  if (!queue.value.length) overlay.close()
}

const offPush = onPush(onPushMessage)

onBeforeUnmount(() => {
  offPush()
  stopAlarm()
})
</script>

<template>
  <Teleport to="body">
    <div
      v-if="overlay.visible.value && current"
      class="ac-mask"
      :style="{ zIndex: overlay.zIndex.value }"
      role="alertdialog"
      aria-modal="true"
      aria-live="assertive"
      aria-label="极端行情预警"
    >
      <div class="ac-sheet">
        <p class="ac-eyebrow">极端预警 · {{ current.kind }}</p>
        <h2 class="ac-title">{{ current.title }}</h2>
        <p class="ac-body">{{ current.body }}</p>
        <dl class="ac-meta">
          <div>
            <dt>来源</dt>
            <dd>{{ current.source }}</dd>
          </div>
          <div>
            <dt>时间</dt>
            <dd>{{ clock(current.at) }}</dd>
          </div>
          <div v-if="queue.length > 1">
            <dt>队列</dt>
            <dd>还有 {{ queue.length - 1 }} 条</dd>
          </div>
        </dl>
        <div class="ac-actions">
          <button v-if="settings.sound" type="button" class="ac-mute" @click="muted = !muted">
            {{ muted ? '取消静音' : ringing ? '静音' : '静音' }}
          </button>
          <button type="button" class="ac-ack" @click="acknowledge">已知晓，解除锁定</button>
        </div>
      </div>
    </div>
  </Teleport>
</template>

<style scoped>
.ac-mask {
  position: fixed;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 24px;
  background: rgba(10, 4, 4, 0.62);
  backdrop-filter: blur(6px);
}

.ac-sheet {
  width: min(520px, 100%);
  padding: 28px 30px 24px;
  background: var(--nt-bg-glass);
  border: 1px solid var(--nt-danger);
  border-radius: var(--nt-radius-l);
  box-shadow:
    var(--nt-glow-danger),
    0 24px 80px rgba(0, 0, 0, 0.6);
  animation: ac-pulse 1.6s ease-in-out infinite;
}

@keyframes ac-pulse {
  0%,
  100% {
    border-color: var(--nt-danger);
  }
  50% {
    border-color: rgba(255, 58, 58, 0.35);
  }
}

.ac-eyebrow {
  margin: 0 0 6px;
  font-size: 11.5px;
  letter-spacing: 0.12em;
  color: var(--nt-danger);
  font-family: var(--nt-font-mono);
}

.ac-title {
  margin: 0 0 10px;
  font-size: 22px;
  font-weight: 700;
  color: var(--nt-text-1);
}

.ac-body {
  margin: 0 0 16px;
  font-size: 14px;
  line-height: 1.7;
  color: var(--nt-text-1);
  white-space: pre-wrap;
}

.ac-meta {
  display: flex;
  flex-wrap: wrap;
  gap: 8px 22px;
  margin: 0 0 20px;
}

.ac-meta div {
  display: flex;
  gap: 6px;
}

.ac-meta dt {
  font-size: 12px;
  color: var(--nt-text-3);
}

.ac-meta dd {
  margin: 0;
  font-size: 12px;
  color: var(--nt-text-2);
  font-family: var(--nt-font-mono);
}

.ac-actions {
  display: flex;
  justify-content: flex-end;
  gap: 10px;
}

.ac-mute {
  padding: 8px 14px;
  font-size: 13px;
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.ac-ack {
  padding: 8px 18px;
  font-size: 13px;
  font-weight: 600;
  color: #fff;
  background: var(--nt-danger);
  border: none;
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.ac-ack:hover {
  box-shadow: var(--nt-glow-danger);
}
</style>
