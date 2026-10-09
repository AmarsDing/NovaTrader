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
  background: radial-gradient(ellipse 80% 60% at 50% -10%, rgba(0, 229, 255, 0.12), transparent),
    var(--nt-bg-0);
}

.grid-bg {
  position: absolute;
  inset: 0;
  background-image: linear-gradient(rgba(255, 255, 255, 0.03) 1px, transparent 1px),
    linear-gradient(90deg, rgba(255, 255, 255, 0.03) 1px, transparent 1px);
  background-size: 48px 48px;
  mask-image: radial-gradient(ellipse 70% 55% at 50% 40%, #000 20%, transparent 75%);
}

.vignette {
  position: absolute;
  inset: 0;
  box-shadow: inset 0 0 120px rgba(0, 0, 0, 0.65);
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
