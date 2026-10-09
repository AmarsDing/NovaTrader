<script setup lang="ts">
import { nextTick, watch } from 'vue'
import { useOverlay } from './useOverlay'
import NtButton from './NtButton.vue'
import NtIcon from './NtIcon.vue'

const props = withDefaults(
  defineProps<{
    open: boolean
    title?: string
    width?: string
    closable?: boolean
  }>(),
  { title: '', width: '440px', closable: true },
)

const emit = defineEmits<{ (e: 'update:open', value: boolean): void }>()

const overlay = useOverlay(() => {
  if (props.closable) emit('update:open', false)
})

watch(
  () => props.open,
  async (value) => {
    if (value) {
      overlay.open()
      await nextTick()
      document.querySelector<HTMLElement>('.nt-modal__sheet')?.focus()
    } else {
      overlay.close()
    }
  },
  { immediate: true },
)

function close() {
  emit('update:open', false)
}
</script>

<template>
  <Teleport to="body">
    <div
      v-if="open"
      class="nt-modal"
      :style="{ zIndex: overlay.zIndex.value }"
      @mousedown.self="closable && close()"
    >
      <section
        class="nt-modal__sheet"
        :style="{ width: `min(${width}, 100%)` }"
        role="dialog"
        aria-modal="true"
        tabindex="-1"
      >
        <header v-if="title || closable" class="nt-modal__head">
          <h2 class="nt-modal__title">{{ title }}</h2>
          <NtButton v-if="closable" variant="quiet" size="sm" @click="close">
            <NtIcon name="close" :size="13" />
          </NtButton>
        </header>
        <div class="nt-modal__body"><slot /></div>
        <footer v-if="$slots.footer" class="nt-modal__foot"><slot name="footer" /></footer>
      </section>
    </div>
  </Teleport>
</template>

<style scoped>
/* 玻璃只留给弹层（发光预算允许的三处 chrome 之一）。 */
.nt-modal {
  position: fixed;
  inset: 0;
  display: grid;
  place-items: center;
  padding: 24px;
  background: rgba(4, 6, 12, 0.68);
  backdrop-filter: blur(6px);
}

.nt-modal__sheet {
  display: flex;
  flex-direction: column;
  gap: 12px;
  padding: 18px 20px;
  background: var(--nt-bg-glass);
  border: 1px solid var(--nt-border-2);
  border-radius: var(--nt-radius-l);
  outline: none;
  box-shadow: 0 18px 50px rgba(0, 0, 0, 0.5);
}

.nt-modal__head {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: 12px;
}

.nt-modal__title {
  margin: 0;
  font-size: 15px;
  font-weight: 600;
  color: var(--nt-text-1);
}

.nt-modal__body {
  min-height: 0;
}

.nt-modal__foot {
  display: flex;
  justify-content: flex-end;
  gap: 8px;
}
</style>
