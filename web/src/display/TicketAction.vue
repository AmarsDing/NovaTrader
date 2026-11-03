<script setup lang="ts">
/**
 * TicketAction（W0-3b / FE-O6）：行动计划块的单一事实组件——
 *   决策台票体（h4 标目）与信号中枢卡片（h3 标目）此前各写一份近乎逐字相同的
 *   action-plan 区块（什么时候行动/失效条件/主要阻断/先看反证），改一处漏一处。
 * 本组件收口：行动计划渲染唯一出处；标题层级由 heading 指定（票体 h4、卡片 h3），
 * 决策台特有的「仅可准备模拟盘」提示经 extra 槽下发，不内置业务文案。
 */
import { computed } from 'vue'
import type { DecisionAction } from '@/view/decision-desk'

const props = withDefaults(
  defineProps<{
    action: DecisionAction
    /** 标目层级：desk=票体内嵌用 h4，card=信号卡内嵌用 h3（语义随容器走）。 */
    heading?: 'h3' | 'h4'
  }>(),
  { heading: 'h4' },
)

/* 卡片变体与标目层级同轴：h3=信号卡（弱化面板框、密字排），h4=决策台票体（面板块）。 */
const cardClass = computed(() => (props.heading === 'h3' ? 'action-plan--card' : ''))
</script>

<template>
  <section class="action-plan" :class="[action.state, cardClass]" aria-label="当前行动计划">
    <h4 v-if="heading === 'h4'">当前动作：{{ action.title }}</h4>
    <h3 v-else>当前动作：{{ action.title }}</h3>
    <p><strong>什么时候行动</strong>{{ action.timing }}</p>
    <p><strong>失效条件</strong>{{ action.invalidation }}</p>
    <p v-if="action.blocker" class="action-plan__blocker">
      <strong>主要阻断</strong>{{ action.blocker }}
    </p>
    <p class="action-plan__caution"><strong>先看反证</strong>{{ action.caution }}</p>
    <slot name="extra" />
  </section>
</template>

<style scoped>
.action-plan {
  padding: 10px 12px;
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-s);
  background: var(--nt-bg-1);
}

/* 卡片内嵌变体（信号中枢）：弱化卡片边距、标目档位随卡内层级——
   票体变体（决策台）保留上面的面板块样式。 */
.action-plan--card {
  padding: 8px 0;
  border: none;
  border-top: 1px solid var(--nt-border-1);
  border-bottom: 1px solid var(--nt-border-1);
  border-radius: 0;
  background: none;
}

.action-plan h4 {
  margin: 0 0 8px;
  color: var(--nt-text-1);
  font-size: 13px;
}

.action-plan h3 {
  margin: 0 0 6px;
  font-size: 12.5px;
  color: var(--nt-text-1);
}

.action-plan p {
  margin: 5px 0;
  color: var(--nt-text-2);
  font-size: 12px;
  line-height: 1.6;
}

.action-plan--card.action-plan p {
  margin: 4px 0;
  font-size: 11.5px;
  line-height: 1.5;
}

.action-plan strong {
  margin-right: 8px;
  font-weight: 500;
  color: var(--nt-text-3);
}

.action-plan--card.action-plan strong {
  margin-right: 6px;
}

.action-plan .action-plan__blocker,
.action-plan .action-plan__caution {
  color: var(--nt-warn);
}
</style>
