<script setup lang="ts">
/**
 * 底栏 chrome（不进 dock）：链路健康、账本、交易模式、Kill Switch、未读通知、WS 主题、上海时段钟。
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import NtStatusDot, { type DotStatus } from '@/ui/NtStatusDot.vue'
import { linkStatus, statusOf } from '@/ui/status-map'
import { useAccountStore } from '@/stores/account'
import { useNotifyStore } from '@/stores/notify'
import { useRiskStore } from '@/stores/risk'
import { clock } from '@/utils/format'
import { serverDate } from '@/utils/server-clock'
import { formatClock, SESSION_TEXT, sessionOf } from '@/utils/time'
import { desiredTopics, isLive, link } from '@/ws/hub'
import { getWorkbench } from '@/workspace/inject'

const risk = useRiskStore()
const account = useAccountStore()
const notify = useNotifyStore()

/** 底栏时段钟走 P2-10 服务端钟（未校准回退本地）：时段文案与时钟同上海历。 */
const now = ref(serverDate())
let timer: ReturnType<typeof setInterval> | undefined

const sessionText = computed(() => SESSION_TEXT[sessionOf(now.value)])
const clockText = computed(() => formatClock(now.value))
/** 链路文案只从链路五态册取（§7.1/§10 全局 chrome，一字锁定）：色调与状态词同源。 */
const healthStatus = computed<DotStatus>(() => (link.health === 'ok' ? 'ok' : link.health === 'down' ? 'down' : 'off'))
/** 推送通道全 live（心跳证据齐）取「在线」呼吸态；open/connecting 未就绪记陈旧；idle 记关闭。 */
const wsStatus = computed<DotStatus>(() =>
  isLive() ? 'live' : link.ws === 'open' || link.ws === 'connecting' ? 'stale' : link.ws === 'idle' ? 'off' : 'down',
)
const recoveryText = computed(() => {
  if (link.sync === 'ready') return '同步已完成'
  if (link.sync === 'syncing') return '权威状态同步中'
  if (link.sync === 'required') return '待恢复 · 未完成核对'
  return '尚未同步'
})
const accountHint = computed(() => account.checkedAt ? `账户最近读取 ${clock(new Date(account.checkedAt))}` : '账户尚未读取')
const riskHint = computed(() => risk.checkedAt ? `风控最近读取 ${clock(new Date(risk.checkedAt))}` : '风控尚未读取')
const riskCurrent = computed(() => risk.ready && isLive())
const killText = computed(() =>
  riskCurrent.value
    ? statusOf(risk.killActive ? 'risk.active' : 'risk.standby').text
    : statusOf(risk.killActive ? 'risk.unverified_active' : 'risk.unverified').text,
)
/** 授权级别徽标（N9 阶梯）：SIM 撮合=青；LIVE 逐单确认=琥珀；LIVE 未验收=红且禁提交。 */
const authzText = computed(() => account.book === 'LIVE' ? 'LIVE · 未授权' : 'SIM 撮合')
const authzClass = computed(() => account.book === 'LIVE' ? 'danger' : 'accent')
const authzHint = computed(() => account.book === 'LIVE'
  ? '实盘执行通道尚未验收，提交保持禁用；切换到 SIM 可继续模拟'
  : '模拟账本撮合授权在位；LIVE 钥匙另行验收（点击开下单条）')
/** 主题健康依赖 hub 的内部计数器，跟着秒针与最后收包时刻重算。 */
const topics = computed(() => {
  void link.lastAt
  void now.value
  return desiredTopics().join(' · ')
})

function openNotify() {
  getWorkbench()?.openPanel('notify-center')
}

/* G3 任务栏未读徽标：无 tauri badge 插件的零依赖替代——窗口标题前缀。
   popout 宿主页不挂本组件，互不抢标题。 */
let baseTitle = ''
const stopTitleWatch = watch(
  () => notify.unread,
  (n) => {
    document.title = n > 0 ? `(${n > 99 ? '99+' : n}) ${baseTitle}` : baseTitle
  },
)

onMounted(() => {
  baseTitle = document.title || 'NovaTrader'
  timer = setInterval(() => {
    now.value = serverDate()
  }, 1000)
  notify.start()
})

onBeforeUnmount(() => {
  clearInterval(timer)
  stopTitleWatch()
  if (baseTitle) document.title = baseTitle
})
</script>

<template>
  <footer class="bar">
    <div class="cell">
      <NtStatusDot :status="healthStatus" />
      admin 2001 · {{ linkStatus(healthStatus).text }}
    </div>
    <div class="cell">
      <NtStatusDot :status="wsStatus" />
      推送通道 2003 · {{ linkStatus(wsStatus).text }}
    </div>
    <div class="cell" :class="{ pending: link.sync !== 'ready' }" :title="link.error || recoveryText">{{ recoveryText }}</div>
    <div class="cell book" :class="{ live: account.book === 'LIVE', pending: !account.accountOk || !isLive() }" :title="accountHint">
      {{ account.book === 'LIVE' ? 'LIVE 实盘' : 'SIM 模拟' }} · {{ account.accountOk && isLive() ? '账户已核对' : '账户待核对' }}
    </div>
    <button
      type="button"
      class="cell authz"
      :class="authzClass"
      :title="authzHint"
      @click="getWorkbench()?.openPanel('trade-bar')"
    >授权 {{ authzText }}</button>
    <div class="cell" :class="{ pending: !riskCurrent }" :title="riskHint">模式 {{ riskCurrent ? risk.mode : '待核对' }}</div>
    <div class="cell" :class="{ warn: risk.killActive, pending: !riskCurrent }" :title="riskHint">Kill Switch {{ killText }}</div>
    <button type="button" class="cell bell" :class="{ has: notify.unread > 0 }" @click="openNotify">
      通知<template v-if="notify.unread > 0"> {{ notify.unread > 99 ? '99+' : notify.unread }}</template>
    </button>
    <div class="cell dim">{{ isLive() ? '数据实时' : '不是实时' }}</div>
    <div class="cell topics" :title="topics">订阅 {{ topics }}</div>
    <div class="cell clock">{{ sessionText }} · {{ clockText }}</div>
    <div v-if="link.error" class="recovery-error" role="status">{{ link.error }}</div>
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
  background: var(--nt-glass-sheen), var(--nt-glass-bg);
  border-top: 1px solid var(--nt-glass-border);
  box-shadow: var(--nt-edge-light);
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

.cell.pending,
.recovery-error {
  color: var(--nt-warn);
}

.recovery-error {
  flex-basis: 100%;
  overflow-wrap: anywhere;
  line-height: 1.5;
  font-size: 11px;
}

.book {
  font-weight: 600;
  color: var(--nt-text-1);
  padding: 2px 7px;
  border: 1px solid var(--nt-glass-border);
  border-radius: var(--nt-radius-s);
  background: var(--nt-data-raised);
}

.book.live {
  color: var(--nt-warn);
}

.authz {
  padding: 2px 7px;
  border: 1px solid var(--nt-glass-border);
  border-radius: var(--nt-radius-s);
  background: var(--nt-data-raised);
  font: inherit;
  font-size: 12px;
  cursor: pointer;
}

.authz:hover {
  text-decoration: none;
  border-color: var(--nt-accent);
}

.authz.accent {
  color: var(--nt-accent);
}

.authz.danger {
  color: var(--nt-danger);
}

.bell {
  padding: 0;
  border: none;
  background: none;
  color: var(--nt-text-3);
  font: inherit;
  cursor: pointer;
}

.bell.has {
  color: var(--nt-accent);
  font-weight: 600;
}

.bell:hover {
  text-decoration: underline;
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
