<script setup lang="ts">
/**
 * TicketDetail（W0-3b / FE-O6）：决策票票体单一事实组件——
 *   决策台右栏票体此前整段写在 OverviewView（页超 1100 行，票体六块+头尾近 130 行），
 *   信号中枢卡片（SignalView）又是另一套裁法——同一票两处口径。
 * 本组件收口票体六块：票头 / 成交说明 / 行动计划（TicketAction）/ 价格（含盈亏比、ATR、
 * 已了结事实）/ 四维投票（含评语）/ 异议 / 理由+证据 / 风控 / 票脚。
 * 「没取到≠0」：价格腿缺失 price() 显「—」，有效期缺失显「无有效期」，分数缺失显「—」。
 * W2 收编：SignalView 卡片全量走本组件（head-extra 槽挂状态章/剩余时效/新鲜度，above 槽挂评分环+雷达卡壳）。
 */
import SymbolName from '@/display/SymbolName.vue'
import TicketAction from '@/display/TicketAction.vue'
import { riskRewardOf, type DecisionAction, type DeskTicket } from '@/view/decision-desk'
import { clock, price, sideLabel, validRemainText } from '@/utils/format'

defineProps<{
  ticket: DeskTicket
  /** 行动计划为 null 时整块不渲（决策台由 buildDecisionAction 计算后下发）。 */
  action: DecisionAction | null
  /** 有效期倒计时的判定钟（P2-10 服务端钟），与宿主视图同源。 */
  nowTick: number
  /** 异议点到的反对维（四维行高亮），焦点态由宿主视图持有。 */
  focusDim?: string
}>()

const emit = defineEmits<{ (e: 'point-dim', key: string): void }>()

defineSlots<{
  /** 票头追加位（SignalView 卡挂状态章/剩余时效/信号时间+新鲜度；决策台不用）。 */
  'head-extra'?(): unknown
  /** 票体上缘追加块（决策台放 ConditionalPlans；信号卡放评分环+雷达）。 */
  above?(): unknown
  /** 票脚按钮区（决策台放 看行情 / 准备模拟盘确认；信号卡放 meta+条件计划+下单钮）。 */
  foot?(): unknown
}>()
</script>

<template>
  <header class="ticket__head">
    <!-- §P0 走查：代码+名称统一 SymbolName(inline)，名称走字典，缺名显「名称未返回」 -->
    <SymbolName :code="ticket.symbol" mode="inline" />
    <em :class="ticket.side === 'buy' ? 'up' : ticket.side === 'sell' ? 'down' : ''">{{
      sideLabel(ticket.side)
    }}</em>
    <span class="date">形成日 {{ ticket.tradeDate || '—' }}</span>
    <span class="date">账本 {{ ticket.book || '未返回' }} · 版本 {{ ticket.strategyVersionId || '未返回' }}</span>
    <span v-if="ticket.degraded" class="tag">规则分 {{ ticket.ruleScore ?? '—' }}</span>
    <b v-else class="score" :class="{ hot: ticket.highValue }">{{ ticket.score ?? '—' }}</b>
    <span v-if="ticket.lane === 'executed'" class="done">{{ ticket.closedNote || '已执行' }}</span>
    <slot name="head-extra" />
  </header>

  <p v-if="ticket.lane === 'executed'" class="nt-note">
    账本已核成交 {{ ticket.executedVolume ?? '—' }} 股 · 成交均价 {{ price(ticket.executedPrice ?? null) }}。部分成交可能仍有余量，请核对原订单。
  </p>

  <TicketAction v-if="action" :action="action" heading="h4">
    <template v-if="action.canOpenTrade" #extra>
      <p class="nt-note">当前仅可准备模拟盘确认，尚未校验最新个股价格、数量与下单风控。</p>
    </template>
  </TicketAction>

  <slot name="above" />

  <div class="ticket__grid">
    <div class="ticket__block">
      <h4>价格</h4>
      <p class="px">
        入场 {{ price(ticket.entryLow) }}–{{ price(ticket.entryHigh) }} · 止损 {{ price(ticket.stop) }} · 止盈
        {{ price(ticket.take) }} · 盈亏比 {{ riskRewardOf(ticket) }}
      </p>
      <!-- 已了结事实与信号中枢卡尾同源（DeskTicket 归一字段）：出场/方式/时点/持有天数一行收拢，
           后验盈亏已由票头 closedNote 承载，此处不重复。缺位显「—」不编。 -->
      <p v-if="ticket.lane === 'executed'" class="px">
        出场 {{ price(ticket.exitPrice) }} · 了结方式 {{ ticket.exitKind || '—' }}<template v-if="ticket.closedAt">
          · {{ clock(ticket.closedAt) }}</template
        ><template v-if="ticket.holdingDays != null"> · 持有 {{ ticket.holdingDays }} 日</template>
      </p>
      <p v-if="ticket.atr != null" class="px">ATR {{ price(ticket.atr) }}</p>
      <p class="valid" :class="{ warn: !ticket.validUntil }">
        {{ ticket.validUntil ? validRemainText(ticket.validUntil, nowTick) || '已过期' : '无有效期' }}
        <template v-if="ticket.risk.requestedPct != null">
          · 请求仓位 {{ (ticket.risk.requestedPct * 100).toFixed(0) }}%</template
        >
      </p>
    </div>

    <div class="ticket__block">
      <h4>四维投票</h4>
      <ul class="votes">
        <li
          v-for="vote in ticket.votes"
          :key="vote.key"
          :class="{ against: vote.against, focus: focusDim === vote.key }"
          @click="vote.against && emit('point-dim', vote.key)"
        >
          <span>{{ vote.label }}</span>
          <b>{{ vote.score ?? '—' }}</b>
          <!-- 四维评语随票归位（原 SignalView 卡私有块）：分数的理由层，无评语不显位 -->
          <i v-if="vote.comment" class="vote-comment">{{ vote.comment }}</i>
        </li>
      </ul>
    </div>
  </div>

  <div v-if="ticket.objections.length" class="ticket__block objections">
    <h4>异议</h4>
    <ul>
      <li v-for="(item, index) in ticket.objections" :key="index">{{ item }}</li>
    </ul>
  </div>

  <div class="ticket__block">
    <h4>理由</h4>
    <p v-if="!ticket.reasons.length" class="nt-empty">
      {{ ticket.degraded ? '规则分：模型降级，没有研判正文' : '未调用' }}
    </p>
    <ol v-else class="reasons">
      <li v-for="(item, index) in ticket.reasons" :key="index">{{ item }}</li>
    </ol>
    <p v-if="ticket.evidence.length" class="evidence">
      <span v-for="(item, index) in ticket.evidence" :key="index" class="chip">{{ item }}</span>
    </p>
  </div>

  <div class="ticket__block">
    <h4>风控</h4>
    <p class="px">
      请求仓位
      {{ ticket.risk.requestedPct != null ? `${(ticket.risk.requestedPct * 100).toFixed(1)}%` : '—' }} · 单股上限估算
      {{ ticket.risk.allowedPct != null ? `${(ticket.risk.allowedPct * 100).toFixed(1)}%` : '—' }}
      <template v-if="ticket.risk.rule"> · {{ ticket.risk.rule }}</template>
    </p>
    <p class="nt-note">{{ ticket.risk.note }}</p>
  </div>

  <footer class="ticket__actions">
    <slot name="foot" />
  </footer>
</template>

<script lang="ts">
export default { name: 'TicketDetail' }
</script>

<style scoped>
.tag {
  font-style: normal;
  font-size: 10px;
  color: var(--nt-warn);
  border: 1px solid var(--nt-warn);
  border-radius: 3px;
  padding: 0 3px;
  white-space: nowrap;
}

.done {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.ticket__head {
  display: flex;
  align-items: baseline;
  gap: 10px;
  flex-wrap: wrap;
}

.ticket__head .up {
  color: var(--nt-up);
  font-style: normal;
}

.ticket__head .down {
  color: var(--nt-down);
  font-style: normal;
}

.ticket__head .date {
  color: var(--nt-text-3);
  font-size: 11px;
}

.ticket__head .score {
  margin-left: auto;
  font-family: var(--nt-font-mono);
  font-size: var(--nt-fs-num-lg); /* 收编自 18px 裸值（§5.3 num-lg 档 17，归并入档；分数是配角不抢 hero） */
  font-weight: 600;
}

.ticket__head .score.hot {
  color: var(--nt-warn); /* 删裸值级联 fallback（--nt-warn 已冻结定义，fallback 是遮羞布） */
}

.ticket__grid {
  display: grid;
  grid-template-columns: repeat(auto-fit, minmax(220px, 1fr));
  gap: 12px;
}

.ticket__block h4 {
  margin: 0 0 6px;
  font-size: 11px;
  font-weight: 600;
  color: var(--nt-text-3);
  letter-spacing: 0.04em;
}

.ticket__block p {
  margin: 0;
}

.votes {
  list-style: none;
  margin: 0;
  padding: 0;
  display: flex;
  flex-direction: column;
  gap: 4px;
}

.votes li {
  display: flex;
  flex-wrap: wrap;
  justify-content: space-between;
  font-size: 12.5px;
  padding: 3px 6px;
  border-radius: var(--nt-radius-s);
  border: 1px solid transparent;
}

.votes li.against {
  color: var(--nt-warn);
  cursor: pointer;
}

.votes li.focus {
  border-color: var(--nt-warn);
}

.votes li b {
  font-family: var(--nt-font-mono);
  font-weight: 500;
}

/* 四维评语：折行整宽排在分数行下，斜体弱一档（理由层不抢读数）。 */
.vote-comment {
  flex-basis: 100%;
  font-style: normal;
  font-size: 11px;
  color: var(--nt-text-3);
  line-height: 1.5;
}

.objections ul {
  margin: 0;
  padding-left: 18px;
  color: var(--nt-warn);
  font-size: 12.5px;
  display: flex;
  flex-direction: column;
  gap: 3px;
}

.reasons {
  margin: 0;
  padding-left: 18px;
  font-size: 12.5px;
  color: var(--nt-text-1);
  display: flex;
  flex-direction: column;
  gap: 4px;
  line-height: 1.5;
}

.evidence {
  margin-top: 6px;
  display: flex;
  gap: 6px;
  flex-wrap: wrap;
}

.chip {
  font-size: 10.5px;
  font-family: var(--nt-font-mono);
  color: var(--nt-text-2);
  border: 1px solid var(--nt-border-1);
  border-radius: 3px;
  padding: 1px 5px;
}

.ticket__actions {
  display: flex;
  gap: 10px;
  justify-content: flex-end;
}

.px {
  color: var(--nt-text-2);
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: 12px;
}

.valid {
  font-size: 11px;
  color: var(--nt-text-3);
  white-space: nowrap;
}

.valid.warn {
  color: var(--nt-warn);
}
</style>
