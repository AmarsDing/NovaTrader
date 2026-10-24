<template>
  <div class="blank">
    <div class="grid-bg" aria-hidden="true" />
    <div class="vignette" aria-hidden="true" />
    <main class="content">
      <slot />
    </main>
  </div>
</template>

<style scoped>
.blank {
  position: relative;
  min-height: 100vh;
  overflow: hidden;
  background: radial-gradient(ellipse 80% 60% at 50% -10%, var(--nt-accent-soft), transparent), var(--nt-bg-0);
}

.grid-bg {
  position: absolute;
  inset: 0;
  /* 网格线走 --nt-space-grid：暗主题 白0.03→冰青0.045（亮度近等、色相入族），
     亮主题从近不可见白线修为可见冷灰线——与下方渐晕收敛同批声明。 */
  background-image:
    linear-gradient(var(--nt-space-grid) 1px, transparent 1px),
    linear-gradient(90deg, var(--nt-space-grid) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(ellipse 70% 55% at 50% 40%, #000 20%, transparent 75%);
}

.vignette {
  position: absolute;
  inset: 0;
  /* 压边渐晕走 scrim 令牌：暗主题与纯黑 0.65 近等效，亮主题改为低透明冷灰（修亮边发乌）。 */
  box-shadow: inset 0 0 120px var(--nt-scrim);
  pointer-events: none;
}

.content {
  position: relative;
  z-index: 1;
  min-height: 100vh;
  display: flex;
  align-items: center;
  justify-content: center;
  padding: 2rem 1.25rem;
}
</style>
