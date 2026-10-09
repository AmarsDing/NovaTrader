<script setup lang="ts">
import { onMounted, ref } from 'vue'
import { listSignals, type SignalItem } from '@/api/signals'
import NtButton from '@/ui/NtButton.vue'
import NtProgressRing from '@/ui/NtProgressRing.vue'
import NtRadar from '@/viz/NtRadar.vue'
import { dimValue, messageOf, price } from '@/utils/format'
import { useWorkbench } from '@/workspace/inject'

const workbench = useWorkbench()
const rows = ref<SignalItem[]>([])
const error = ref('')
const loading = ref(false)
const labels = ['技术面', '消息面', '资金面', '外围']

async function load() {
  loading.value = true
  error.value = ''
  try {
    rows.value = await listSignals()
  } catch (err) {
    rows.value = []
    error.value = messageOf(err)
  } finally {
    loading.value = false
  }
}

function openMarket(symbol: string) {
  workbench?.openPanel('market', { symbol })
}

function openTrade(item: SignalItem) {
  workbench?.openPanel('trade', { symbol: item.symbol, side: item.side, price: item.entry || '' })
}

function dimsOf(item: SignalItem) {
  return labels.map((label) => ({ label, value: dimValue(item.dims, label) }))
}

onMounted(load)
</script>

<template>
  <div class="nt-page">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">信号中枢</h1>
        <p class="nt-note">综合分、四维、入场止损止盈和证据。85 分以上高亮。</p>
      </div>
      <NtButton :loading="loading" @click="load">刷新</NtButton>
    </header>
    <p v-if="loading" class="nt-empty">读取中…</p>
    <p v-else-if="error" class="nt-err">{{ error }}</p>
    <p v-else-if="!rows.length" class="nt-empty">今日没有信号。</p>
    <div v-else class="grid">
      <article v-for="item in rows" :key="item.id || item.symbol + item.time" class="card" :class="{ hot: item.highValue }">
        <header>
          <button type="button" class="sym" @click="openMarket(item.symbol)">{{ item.symbol }}</button>
          <span>{{ item.name }}</span>
          <!-- 红涨绿跌：买入红、卖出绿 -->
          <em :class="item.side === 'buy' ? 'up' : item.side === 'sell' ? 'down' : ''">
            {{ item.side === 'buy' ? '买' : item.side === 'sell' ? '卖' : item.side }}
          </em>
        </header>
        <div class="body">
          <NtProgressRing :value="item.score" :hot="item.highValue" :size="56" />
          <NtRadar :dims="dimsOf(item)" :size="84" />
          <ul>
            <li v-for="label in labels" :key="label">
              <span>{{ label }}</span>
              <b>{{ dimValue(item.dims, label) ?? '—' }}</b>
            </li>
          </ul>
        </div>
        <dl>
          <div><dt>入场</dt><dd>{{ price(item.entryLow) }} – {{ price(item.entryHigh) }}</dd></div>
          <div><dt>止损</dt><dd class="down">{{ price(item.stop) }}</dd></div>
          <div><dt>止盈</dt><dd class="up">{{ price(item.take) }}</dd></div>
        </dl>
        <p class="meta">
          规则 {{ item.ruleScore ?? '—' }} · AI {{ item.aiScore ?? '—' }}{{ item.degraded ? ' · 模型降级' : '' }} ·
          {{ item.status || item.strategy }}
        </p>
        <p v-if="item.reason" class="reason">{{ item.reason }}</p>
        <ul v-if="item.evidence.length" class="evidence">
          <li v-for="(line, index) in item.evidence" :key="index">{{ line }}</li>
        </ul>
        <button type="button" class="go" @click="openTrade(item)">去下单</button>
      </article>
    </div>
  </div>
</template>

<style scoped>
.grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(320px, 1fr));
  gap: 12px;
}

.card {
  padding: var(--nt-pad-panel);
  display: flex;
  flex-direction: column;
  gap: 10px;
  background: var(--nt-bg-1);
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
}

/* AI 信号 ≥85：发光预算允许的高亮。 */
.card.hot {
  border-color: rgba(0, 229, 255, 0.45);
  box-shadow: 0 0 24px rgba(0, 229, 255, 0.14);
}

header {
  display: flex;
  gap: 8px;
  align-items: baseline;
}

.sym {
  background: none;
  border: none;
  color: var(--nt-accent);
  padding: 0;
  min-height: 0;
  font-size: 14px;
  font-weight: 600;
  cursor: pointer;
}

.sym:hover {
  text-decoration: underline;
}

em {
  margin-left: auto;
  font-style: normal;
  font-weight: 600;
}

.body {
  display: grid;
  grid-template-columns: 56px 84px 1fr;
  gap: 10px;
  align-items: center;
}

ul {
  list-style: none;
  margin: 0;
  padding: 0;
}

.body li,
dl {
  display: flex;
  justify-content: space-between;
  gap: 8px;
  font-size: 12px;
}

dl {
  display: grid;
  gap: 4px;
  margin: 0;
  font-family: var(--nt-font-mono);
}

dt {
  color: var(--nt-text-3);
}

dd {
  margin: 0;
}

.meta,
.reason {
  margin: 0;
  color: var(--nt-text-2);
  font-size: 11.5px;
}

.evidence {
  color: var(--nt-text-2);
  font-size: 11.5px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.go {
  align-self: flex-start;
  padding: 5px 12px;
  font-size: 12px;
  font-weight: 600;
  font-family: inherit;
  color: var(--nt-accent);
  background: rgba(0, 229, 255, 0.06);
  border: 1px solid rgba(0, 229, 255, 0.32);
  border-radius: var(--nt-radius-s);
  cursor: pointer;
}

.go:hover {
  background: rgba(0, 229, 255, 0.12);
}
</style>
