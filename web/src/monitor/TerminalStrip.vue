<script setup lang="ts">
/**
 * 底部终端带：运行日志 / 任务调度 / 模型链路 / 账本四格。
 * 头部一行是运行状态光点（服务、中间件、磁盘、显存），down 才显示名字。
 * 数据全部来自 loadMonitorInput 已聚合的 24 个接口，本组件不另发请求。
 */
import { computed } from 'vue'
import { amount, intText, ratioPct, signed } from '@/utils/format'
import { dutyOf, isLoud, statusDot } from './duty'
import type { MonitorInput } from './types'

const props = defineProps<{ input: MonitorInput }>()
const emit = defineEmits<{ select: [id: string] }>()

interface DutyDot {
  id: string
  name: string
  status: string
  loud: boolean
}

const duties = computed<DutyDot[]>(() => {
  const ops = props.input.ops
  if (!ops?.ok) return []
  const rows: DutyDot[] = []
  for (const item of ops.services) {
    rows.push(toDot('service', item.name, item.status))
  }
  for (const item of ops.resources ?? []) {
    rows.push(toDot('resource', item.name, item.status))
  }
  return rows
})

function toDot(kind: string, title: string, status: string): DutyDot {
  return {
    id: `${kind}:${title.trim().toLowerCase()}`,
    name: dutyOf(title).name,
    status,
    loud: isLoud(status),
  }
}

const dotClass = statusDot

/* ---- 运行日志：通知 + 审计按时间合并 ---- */

interface LogRow {
  id: string
  time: string
  tag: string
  text: string
  tone: 'up' | 'down' | 'warn' | 'flat'
}

const logs = computed<LogRow[]>(() => {
  const rows: LogRow[] = []
  if (props.input.notices?.ok) {
    for (const item of props.input.notices.items) {
      rows.push({
        id: `n:${item.id}`,
        time: shortTime(item.time),
        tag: item.level || '通知',
        text: item.title,
        tone: item.level === 'critical' || item.level === 'danger' ? 'down' : item.level === 'warn' ? 'warn' : 'flat',
      })
    }
  }
  if (props.input.audit?.ok) {
    for (const item of props.input.audit.items) {
      rows.push({ id: `a:${item.id}`, time: shortTime(item.time), tag: item.action || '审计', text: item.target, tone: 'flat' })
    }
  }
  return rows.slice(0, 14)
})

function shortTime(value: string): string {
  const text = value.trim()
  if (text.length >= 19) return text.slice(11, 19)
  return text || '—'
}

/* ---- 任务调度：回测任务 + 准入 + 影子 ---- */

const tasks = computed(() => (props.input.tasks?.ok ? props.input.tasks.items.slice(0, 4) : []))
const admission = computed(() => props.input.admission ?? null)
const admitPct = computed(() => {
  const admit = admission.value
  if (!admit?.ok || admit.tradingDays == null || !admit.requiredDays) return null
  return Math.min(100, Math.round((admit.tradingDays / admit.requiredDays) * 100))
})
const shadows = computed(() => (props.input.shadow?.ok ? props.input.shadow.items.slice(0, 3) : []))

/* ---- 模型链路 ---- */

const tiers = computed(() => (props.input.brainStats?.ok ? props.input.brainStats.tiers : []))
const brainMissing = computed(() => !props.input.brainStats?.ok)

/* ---- 账本 ---- */

const account = computed(() => props.input.account)
const risk = computed(() => props.input.risk)
const pnlTone = computed(() => {
  const pnl = account.value?.dayPnl
  if (pnl == null || pnl === 0) return 'flat'
  return pnl > 0 ? 'up' : 'down'
})
const openOrders = computed(() => {
  if (!props.input.orders?.ok) return null
  return props.input.orders.items.filter((item) => !['filled', 'cancelled', 'canceled', 'rejected', 'expired'].includes(item.status.toLowerCase())).length
})
</script>

<template>
  <footer class="nt-terms">
    <div class="nt-terms__rail">
      <span class="nt-terms__label">运行状态</span>
      <button
        v-for="dot in duties"
        :key="dot.id"
        type="button"
        class="nt-duty"
        :class="{ 'is-loud': dot.loud }"
        :title="`${dot.name} ${dot.status || '无状态'}`"
        @click="emit('select', dot.id)"
      >
        <i class="dot" :class="dotClass(dot.status)"></i>
        <b v-if="dot.loud">{{ dot.name }}</b>
      </button>
      <span v-if="duties.length === 0" class="nt-terms__gap">运维状态无返回</span>
    </div>
    <div class="nt-terms__grid">
      <section class="nt-term">
        <header>
          <b>运行日志</b>
          <span>RUN LOG</span>
        </header>
        <ul v-if="logs.length">
          <li v-for="row in logs" :key="row.id">
            <em>{{ row.time }}</em>
            <i :class="`is-${row.tone}`">{{ row.tag }}</i>
            <span>{{ row.text }}</span>
          </li>
        </ul>
        <p v-else class="nt-term__empty">通知与审计都没有记录</p>
      </section>

      <section class="nt-term">
        <header>
          <b>任务调度</b>
          <span>DISPATCH</span>
        </header>
        <ul v-if="tasks.length">
          <li v-for="task in tasks" :key="task.id">
            <i class="is-flat">{{ task.status || '—' }}</i>
            <span>{{ task.name || task.id }}</span>
            <em>{{ task.message }}</em>
          </li>
        </ul>
        <p v-else class="nt-term__empty">{{ input.tasks?.ok ? '没有回测任务' : '任务未返回' }}</p>
        <div v-if="admission?.ok" class="nt-term__meter">
          <span>模拟准入</span>
          <div class="nt-meter"><i :style="{ width: `${admitPct ?? 0}%` }"></i></div>
          <b>{{ admission.ready ? '已通过' : `${intText(admission.tradingDays)}/${intText(admission.requiredDays)} 日` }}</b>
        </div>
        <div v-if="shadows.length" class="nt-term__meter">
          <span>影子</span>
          <b v-for="row in shadows" :key="row.name" class="nt-term__chip">{{ row.name }} {{ row.live ?? '—' }}/{{ row.shadow == null ? '—' : ratioPct(row.shadow) }}</b>
        </div>
      </section>

      <section class="nt-term">
        <header>
          <b>模型链路</b>
          <span>BRAIN</span>
        </header>
        <ul v-if="tiers.length">
          <li v-for="tier in tiers" :key="tier.tier || tier.model">
            <i :class="tier.circuitOpen ? 'is-down' : 'is-up'">{{ tier.circuitOpen ? '熔断' : '在跑' }}</i>
            <span>{{ tier.tier }} · {{ tier.model || '未命名' }}</span>
            <em>调用 {{ intText(tier.calls ?? null) }} 失败 {{ intText(tier.failures) }} P95 {{ tier.p95Ms == null ? '—' : `${Math.round(tier.p95Ms)}ms` }}</em>
          </li>
        </ul>
        <p v-if="brainMissing" class="nt-term__empty">模型统计无返回</p>
        <p class="nt-term__note">令牌合计接口未做，花费不折算金额</p>
      </section>

      <section class="nt-term nt-term--ledger">
        <header>
          <b>账本</b>
          <span>LEDGER</span>
        </header>
        <p class="nt-term__pnl" :class="pnlTone">{{ account?.ok ? signed(account.dayPnl) : '账户未返回' }}</p>
        <ul v-if="account?.ok">
          <li>
            <span>权益 {{ amount(account.equity) }} · 现金 {{ amount(account.cash) }}</span>
            <em>持仓 {{ account.positions.length }}<template v-if="openOrders != null"> · 委托 {{ openOrders }}</template></em>
          </li>
          <li v-if="risk?.ok">
            <span>模式 {{ risk.mode || '—' }}</span>
            <em :class="risk.killActive ? 'is-down' : ''">{{ risk.killActive ? `急停 ${risk.killReason || '已触发'}` : '风控守着' }}</em>
          </li>
        </ul>
      </section>
    </div>
  </footer>
</template>
