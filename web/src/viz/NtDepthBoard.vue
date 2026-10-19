<script setup lang="ts">
/**
 * 五档盘口（买卖各五档，带挂单量柱）。
 * 红涨绿跌：卖盘红、买盘绿。
 * 兜底（P2-3）：双边都无档且给了最近价时，用「最近价 ± tick、封顶涨/跌停」
 * 生成占位阶梯并标「合成·待数据」；只画价格不伪造挂单量，量柱此时不渲染。
 */
import { computed } from 'vue'
import { price } from '@/utils/format'
import { buildSyntheticDepth, type SynthDepth } from './depth-synth'

export interface DepthLevel {
  price: number | null
  volume: number | null
}

const props = withDefaults(
  defineProps<{
    asks?: DepthLevel[]
    bids?: DepthLevel[]
    /** 无档兜底锚点：最近价。缺失/非正 → 不合成，走纯空态。 */
    lastPrice?: number | null
    /** 推断涨跌幅档（板块规则）；空串按主板 10% 档。 */
    symbol?: string
    /** 昨收；缺失时合成档不做涨/跌停封顶。 */
    preClose?: number | null
    /** 最小报价，默认 0.01 元。 */
    tick?: number
  }>(),
  { asks: () => [], bids: () => [], lastPrice: null, symbol: '', preClose: null, tick: 0.01 },
)

/** 任何一侧已有真实档位就不合成——不混真/合，免得半真半假误读。 */
const synth = computed<SynthDepth | null>(() => {
  if (props.asks.length || props.bids.length) return null
  return buildSyntheticDepth(props.lastPrice, {
    symbol: props.symbol,
    preClose: props.preClose,
    tick: props.tick,
  })
})
const synthetic = computed(() => synth.value !== null)
const viewAsks = computed<DepthLevel[]>(() => synth.value?.asks ?? props.asks)
const viewBids = computed<DepthLevel[]>(() => synth.value?.bids ?? props.bids)

/** 价一律走展示层格式（两位小数）；空值显示 '—'。 */
function priceOf(level: DepthLevel): string {
  return level.price == null ? '—' : price(level.price)
}

function widthOf(levels: DepthLevel[], index: number): number {
  const max = Math.max(1, ...levels.map((l) => l.volume ?? 0))
  const v = levels[index]?.volume ?? 0
  return Math.round((v / max) * 100)
}
</script>

<template>
  <div class="nt-depth">
    <div class="nt-depth__side nt-depth__side--ask">
      <div
        v-for="(level, index) in [...viewAsks].reverse()"
        :key="`a${index}`"
        class="nt-depth__row"
        :class="{ 'nt-depth__row--syn': synthetic }"
      >
        <span
          v-if="!synthetic"
          class="nt-depth__bar"
          :style="{ width: `${widthOf(viewAsks, viewAsks.length - 1 - index)}%` }"
        />
        <span class="nt-depth__price up">{{ priceOf(level) }}</span>
        <span class="nt-depth__vol">{{ level.volume ?? '—' }}</span>
      </div>
    </div>
    <div class="nt-depth__mid">
      <template v-if="synthetic">
        <b class="nt-depth__last">{{ price(lastPrice) }}</b>
        <span class="nt-depth__tag">合成·待数据</span>
      </template>
      <template v-else>—</template>
    </div>
    <div class="nt-depth__side">
      <div
        v-for="(level, index) in viewBids"
        :key="`b${index}`"
        class="nt-depth__row"
        :class="{ 'nt-depth__row--syn': synthetic }"
      >
        <span
          v-if="!synthetic"
          class="nt-depth__bar nt-depth__bar--bid"
          :style="{ width: `${widthOf(viewBids, index)}%` }"
        />
        <span class="nt-depth__price down">{{ priceOf(level) }}</span>
        <span class="nt-depth__vol">{{ level.volume ?? '—' }}</span>
      </div>
    </div>
  </div>
</template>

<style scoped>
.nt-depth {
  display: flex;
  flex-direction: column;
  gap: 2px;
  font-size: 12px;
  font-family: var(--nt-font-mono);
}

.nt-depth__row {
  position: relative;
  display: flex;
  justify-content: space-between;
  padding: 1px 6px;
}

.nt-depth__bar {
  position: absolute;
  inset: 0 auto 0 0;
  background: color-mix(in srgb, var(--nt-up) 8%, transparent);
  border-right: 1px solid color-mix(in srgb, var(--nt-up) 20%, transparent);
}

.nt-depth__bar--bid {
  background: color-mix(in srgb, var(--nt-down) 8%, transparent);
  border-right: 1px solid color-mix(in srgb, var(--nt-down) 20%, transparent);
}

.nt-depth__price {
  font-weight: 600;
}

/* 合成档降价不降准：保留卖红/买绿的方向语义，用透明度标明是占位。 */
.nt-depth__row--syn .nt-depth__price {
  opacity: 0.55;
}

.nt-depth__vol {
  color: var(--nt-text-2);
}

.nt-depth__mid {
  text-align: center;
  color: var(--nt-text-3);
  font-size: 10px;
  line-height: 8px;
}

.nt-depth__last {
  color: var(--nt-text-1);
  font-weight: 600;
  margin-right: 6px;
}

/* 占位口径用警示色标注，一眼可辨非真实五档。 */
.nt-depth__tag {
  color: var(--nt-warn);
}
</style>
