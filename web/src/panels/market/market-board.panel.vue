<script setup lang="ts">
/**
 * 市场看板（market-board）：TSP·A股智能量化工作台看板的信息架构在 NT 的重述。
 * 实施依据：docs/NovaTrader/plan/2026-11-08-market-board-implementation-plan.md（v2）。
 *
 * 九卡：指数卡墙 / KPI 六格 / 涨跌广度 / 情绪路径 / 趋势强度 / 涨停梯队 / 板块热度 / 异动流 / 龙头榜。
 * 布局：CSS Grid 显式 grid-template-areas + 容器查询四档（≥1080 12 列 / 780–1079 8 列 /
 * 540–779 6 列 / <540 单列，320px 可用）；DOM 序 = 视觉阅读序；空数据保留卡位不塌格。
 * 面板内不做拖拽网格（布局自定义归 dockview）；零裸色；--nt-text-0 零使用（禁 hero）。
 */
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import type { IntradayAlert, SectorRow } from '@/api/market'
import NtAlertFeed from '@/display/NtAlertFeed.vue'
import NtBreadthBar from '@/display/NtBreadthBar.vue'
import NtIndexCards from '@/display/NtIndexCards.vue'
import NtKpiRow from '@/display/NtKpiRow.vue'
import NtLadderMini from '@/display/NtLadderMini.vue'
import NtLeaderBoard from '@/display/NtLeaderBoard.vue'
import NtSectorRank from '@/display/NtSectorRank.vue'
import NtTrendStrength from '@/display/NtTrendStrength.vue'
import NtButton from '@/ui/NtButton.vue'
import NtErrorGuide from '@/ui/NtErrorGuide.vue'
import NtFreshness from '@/ui/NtFreshness.vue'
import NtEmotionPath from '@/viz/NtEmotionPath.vue'
import { useSymbolStore } from '@/stores/symbol'
import { getWorkbench } from '@/workspace/inject'
import { SESSION_TEXT, sessionOf } from '@/utils/time'
import { serverNow } from '@/utils/server-clock'
import { useMarketBoard } from './useMarketBoard'

const {
  sentiment,
  sentimentReady,
  sentimentFailed,
  indices,
  indicesReady,
  indicesFailed,
  alerts,
  alertsReady,
  alertsFailed,
  liveCount,
  emotionPoints,
  emotionPartial,
  pathReady,
  pathFailed,
  sectorsReady,
  sectorsFailed,
  breadth,
  breadthReady,
  breadthFailed,
  limitsReady,
  limitsFailed,
  fastError,
  loading,
  gainers,
  losers,
  ladderMini,
  leaderRows,
  sentimentAsOfMs,
  start,
  stop,
  refreshAll,
} = useMarketBoard()

/** 时段钟走服务端钟秒针（StatusBar 同法）：computed 无响应依赖会冻在挂载时刻，
 *  时段徽注必须随钟走（30s 一跳，跨 9:15/9:30/11:30/13:00/15:00 边界的滞后 ≤30s）。 */
const nowMs = ref(serverNow())
let sessionTimer: ReturnType<typeof setInterval> | undefined

const sessionText = computed(() => SESSION_TEXT[sessionOf(nowMs.value)])

onMounted(() => {
  start()
  sessionTimer = setInterval(() => {
    nowMs.value = serverNow()
  }, 30000)
})
onBeforeUnmount(() => {
  stop()
  if (sessionTimer !== undefined) clearInterval(sessionTimer)
})

/** 个股深链（D8）：symbol store 切票 + 打开行情面板（与 OverviewView/SymbolName 同一 idiome）。 */
function openMarket(symbol: string) {
  if (!symbol) return
  const store = useSymbolStore()
  store.setSymbol(symbol, 'market-board')
  getWorkbench()?.openPanel('market', { symbol: store.code || symbol })
}

function onSectorSelect(sector: SectorRow) {
  // 板块无行情图：点击看的是该板块龙头的个股行情；无龙头行在 NtSectorRank 内置 disabled
  //（悬停/点击均不生效），此处早退为第二道兜底。
  if (sector.leader) openMarket(sector.leader)
}

function onAlertSelect(alert: IntradayAlert) {
  openMarket(alert.symbol)
}

function onLeaderSelect(symbol: string) {
  openMarket(symbol)
}
</script>

<template>
  <div class="board">
    <header class="nt-head">
      <div>
        <h1 class="nt-title">市场看板 <span class="board__session">{{ sessionText }}</span></h1>
        <p class="nt-note">指数 / 情绪 / 广度 / 梯队 / 板块 / 异动一屏总览；点击板块或异动行直达个股行情。</p>
      </div>
      <div class="board__actions">
        <NtFreshness label="情绪" :at="sentimentAsOfMs" :stale-after-ms="60000" />
        <NtButton size="sm" :loading="loading" @click="refreshAll">刷新</NtButton>
      </div>
    </header>

    <NtErrorGuide :error="fastError" context="看板读面" retry="重试" :retry-loading="loading" @retry="refreshAll" />

    <main class="board__grid">
      <!-- 指数卡墙：三卡子网格，自身即卡片层（不套 .nt-panel，避免双层背板） -->
      <section class="board__idx" role="region" aria-label="三大指数">
        <NtIndexCards :indices="indices" :loading="!indicesReady && !indicesFailed" />
      </section>

      <!-- KPI 六格：涨平跌 / 涨跌停 / 炸板率 / 最高连板 / 成交额 / 情绪阶段 -->
      <section class="board__kpi" role="region" aria-label="市场关键指标">
        <NtKpiRow :sentiment="sentiment" :loading="!sentimentReady && !sentimentFailed" />
      </section>

      <section class="nt-panel board__brd" role="region" aria-label="涨跌广度">
        <h2 class="nt-panel__title">涨跌广度</h2>
        <NtBreadthBar
          :advance="sentiment?.advance ?? null"
          :flat="sentiment?.flat ?? null"
          :decline="sentiment?.decline ?? null"
          :loading="!sentimentReady && !sentimentFailed"
        />
      </section>

      <section class="nt-panel board__emo" role="region" aria-label="盘中情绪路径">
        <h2 class="nt-panel__title">情绪路径</h2>
        <NtEmotionPath
          :points="emotionPoints"
          :partial="emotionPartial"
          :ready="pathReady"
          :loading="!pathReady && !pathFailed"
          height="180px"
        />
      </section>

      <section class="nt-panel board__trd" role="region" aria-label="趋势强度">
        <h2 class="nt-panel__title">趋势强度</h2>
        <NtTrendStrength :breadth="breadth" :ready="breadthReady" :loading="!breadthReady && !breadthFailed" />
      </section>

      <section class="nt-panel board__lad" role="region" aria-label="涨停梯队概览">
        <h2 class="nt-panel__title">涨停梯队</h2>
        <NtLadderMini :model="ladderMini" :loading="!limitsReady && !limitsFailed" />
      </section>

      <section class="nt-panel board__sec" role="region" aria-label="板块热度榜">
        <h2 class="nt-panel__title">板块热度 · 领涨 / 领跌</h2>
        <NtSectorRank
          :gainers="gainers"
          :losers="losers"
          :ready="sectorsReady"
          :loading="!sectorsReady && !sectorsFailed"
          @select="onSectorSelect"
        />
      </section>

      <section class="nt-panel board__alt" role="region" aria-label="盘中异动流">
        <h2 class="nt-panel__title">
          异动流
          <span v-if="liveCount" class="board__live">{{ liveCount }} 条实时</span>
        </h2>
        <div class="board__feed">
          <NtAlertFeed
            :alerts="alerts"
            :ready="alertsReady"
            :loading="!alertsReady && !alertsFailed"
            @select="onAlertSelect"
          />
        </div>
      </section>

      <section class="nt-panel board__ldr" role="region" aria-label="板块龙头榜">
        <h2 class="nt-panel__title">龙头榜</h2>
        <NtLeaderBoard
          :rows="leaderRows"
          :ready="sectorsReady"
          :loading="!sectorsReady && !sectorsFailed"
          @select="onLeaderSelect"
        />
      </section>
    </main>
  </div>
</template>

<style scoped>
.board {
  container-type: inline-size;
  height: 100%;
  overflow: auto;
  padding: var(--nt-pad-panel);
  display: flex;
  flex-direction: column;
  gap: 10px;
}

.board__session {
  margin-left: 8px;
  font-size: var(--nt-fs-caption);
  font-weight: 400;
  color: var(--nt-text-3);
}

.board__actions {
  display: flex;
  gap: 10px;
  align-items: center;
}

.board__live {
  margin-left: 6px;
  font-family: var(--nt-font-mono);
  font-variant-numeric: tabular-nums;
  font-size: var(--nt-fs-micro);
  font-weight: 400;
  color: var(--nt-warn);
}

/* 九卡网格：DOM 序 = 视觉阅读序（idx→kpi→brd→emo→trd→lad→sec→alt→ldr），
   空数据由各卡内 NtEmpty/NtMissing 保卡位，grid 不塌。 */
.board__grid {
  display: grid;
  gap: 8px;
  grid-template-columns: 1fr;
  grid-template-areas:
    'idx'
    'kpi'
    'brd'
    'emo'
    'trd'
    'lad'
    'sec'
    'alt'
    'ldr';
}

.board__idx {
  grid-area: idx;
}

.board__kpi {
  grid-area: kpi;
}

.board__brd {
  grid-area: brd;
}

.board__emo {
  grid-area: emo;
}

.board__trd {
  grid-area: trd;
}

.board__lad {
  grid-area: lad;
}

.board__sec {
  grid-area: sec;
}

.board__alt {
  grid-area: alt;
  display: flex;
  flex-direction: column;
}

.board__ldr {
  grid-area: ldr;
}

/* 异动流：窄档封 340px 自滚动；≥1080 跨 sec/ldr 双行时放开由 flex 撑满（仍自滚动）。 */
.board__feed {
  flex: 1;
  min-height: 0;
  max-height: 340px;
  overflow: auto;
}

@container (min-width: 540px) {
  .board__grid {
    grid-template-columns: repeat(6, 1fr);
    grid-template-areas:
      'idx idx idx idx idx idx'
      'kpi kpi kpi kpi kpi kpi'
      'brd brd brd emo emo emo'
      'trd trd trd lad lad lad'
      'sec sec sec sec sec sec'
      'alt alt alt alt alt alt'
      'ldr ldr ldr ldr ldr ldr';
  }
}

@container (min-width: 780px) {
  .board__grid {
    grid-template-columns: repeat(8, 1fr);
    grid-template-areas:
      'idx idx idx idx idx idx idx idx'
      'kpi kpi kpi kpi kpi kpi kpi kpi'
      'brd brd brd brd emo emo emo emo'
      'trd trd trd trd lad lad lad lad'
      'sec sec sec sec sec sec sec sec'
      'alt alt alt alt alt alt alt alt'
      'ldr ldr ldr ldr ldr ldr ldr ldr';
  }
}

@container (min-width: 1080px) {
  .board__grid {
    grid-template-columns: repeat(12, 1fr);
    grid-template-areas:
      'idx idx idx idx idx idx idx idx idx idx idx idx'
      'kpi kpi kpi kpi kpi kpi kpi kpi kpi kpi kpi kpi'
      'brd brd brd emo emo emo trd trd trd lad lad lad'
      'sec sec sec sec sec sec sec sec alt alt alt alt'
      'ldr ldr ldr ldr ldr ldr ldr ldr alt alt alt alt';
  }

  .board__feed {
    max-height: none;
  }
}
</style>
