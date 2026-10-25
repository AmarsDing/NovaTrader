<script setup lang="ts">
// NtAdminScaffold：管理类面板三段式骨架（W4a；UI 宪法 R-UI-44/45）。
// 「筛选条 → 主体 → 抽屉」固定节奏：筛选与主体是结构槽，抽屉为可选详情层；
// 抽屉阶梯宽度走 narrow(320)/wide(480)，面板只声明档位不裸写像素（栅格纪律）。
// 纯布局件：不取数、不引 store/router，槽内容由接线面板供给。
withDefaults(
  defineProps<{
    title?: string
    /** 抽屉阶梯宽度档位；0/缺省 = 不渲染抽屉槽位 */
    drawerWidth?: number
    drawerOpen?: boolean
  }>(),
  { title: '', drawerWidth: 0, drawerOpen: false },
)

const emit = defineEmits<{ (e: 'update:drawerOpen', open: boolean): void }>()

function toggle(open: boolean) {
  emit('update:drawerOpen', open)
}
</script>

<template>
  <section class="nt-admin" :class="{ 'nt-admin--drawer': drawerWidth > 0 && drawerOpen }">
    <header class="nt-admin__head">
      <h3 v-if="title" class="nt-admin__title">{{ title }}</h3>
      <slot name="actions" />
      <button
        v-if="drawerWidth > 0"
        type="button"
        class="nt-admin__toggle"
        :aria-expanded="drawerOpen"
        :data-open="drawerOpen"
        @click="toggle(!drawerOpen)"
      >
        {{ drawerOpen ? '收起' : '详情' }}
      </button>
    </header>

    <div v-if="$slots.filter" class="nt-admin__filter" data-region="filter">
      <slot name="filter" />
    </div>

    <div class="nt-admin__main">
      <div class="nt-admin__body" data-region="body">
        <slot />
      </div>
      <aside
        v-if="drawerWidth > 0 && drawerOpen"
        class="nt-admin__drawer"
        data-region="drawer"
        :style="{ width: drawerWidth + 'px' }"
      >
        <slot name="drawer" />
      </aside>
    </div>
  </section>
</template>

<style scoped>
.nt-admin {
  display: flex;
  flex-direction: column;
  height: 100%;
  background: var(--nt-bg-0, var(--nt-bg-1));
  border: 1px solid var(--nt-border-1);
  border-radius: var(--nt-radius-m);
  overflow: hidden;
}

.nt-admin__head {
  display: flex;
  align-items: center;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--nt-border-1);
}

.nt-admin__title {
  margin: 0;
  font-size: 13px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-admin__toggle {
  margin-left: auto;
  padding: 2px 10px;
  font-size: 11px;
  color: var(--nt-text-2);
  background: transparent;
  border: 1px solid var(--nt-border-2, var(--nt-border-1));
  border-radius: 3px;
  cursor: pointer;
}

.nt-admin__toggle[data-open='true'] {
  color: var(--nt-accent);
  border-color: var(--nt-accent);
}

.nt-admin__filter {
  display: flex;
  flex-wrap: wrap;
  gap: 8px;
  padding: 8px 12px;
  border-bottom: 1px solid var(--nt-border-1);
}

.nt-admin__main {
  display: flex;
  flex: 1;
  min-height: 0;
}

.nt-admin__body {
  flex: 1;
  min-width: 0;
  overflow: auto;
}

.nt-admin__drawer {
  flex: none;
  border-left: 1px solid var(--nt-border-1);
  background: var(--nt-bg-1);
  overflow: auto;
}
</style>
