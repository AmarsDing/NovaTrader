/**
 * 预警声：WebAudio 双音警报循环（不引音频资源文件）。
 * 只在 AlertCenter 未确认期间响；用户手势前置（浏览器自动播放策略）由按钮保证。
 */

let ctx: AudioContext | null = null
let timer: ReturnType<typeof setInterval> | null = null

const TONE_MS = 180
const GAP_MS = 120

function beep(when: number, freq: number) {
  if (!ctx) return
  const osc = ctx.createOscillator()
  const gain = ctx.createGain()
  osc.type = 'square'
  osc.frequency.value = freq
  gain.gain.setValueAtTime(0.0001, when)
  gain.gain.exponentialRampToValueAtTime(0.18, when + 0.02)
  gain.gain.exponentialRampToValueAtTime(0.0001, when + TONE_MS / 1000)
  osc.connect(gain)
  gain.connect(ctx.destination)
  osc.start(when)
  osc.stop(when + TONE_MS / 1000 + 0.05)
}

function burst() {
  if (!ctx) return
  const t0 = ctx.currentTime + 0.01
  beep(t0, 880)
  beep(t0 + (TONE_MS + GAP_MS) / 1000, 660)
}

/** 启动警报循环；幂等。返回 false 表示当前环境无声（SSR/无 AudioContext）。 */
export function startAlarm(): boolean {
  if (timer) return true
  try {
    if (!ctx) ctx = new AudioContext()
    if (ctx.state === 'suspended') void ctx.resume()
  } catch {
    return false
  }
  burst()
  timer = setInterval(burst, (TONE_MS + GAP_MS) * 2 + 200)
  return true
}

export function stopAlarm() {
  if (timer) clearInterval(timer)
  timer = null
}

/**
 * L3 预警单响（方案 §8.4 声音预算：单响不循环）：复用既有 beep/burst 原子发一次短音，
 * 不起 timer、不循环。本函数不读 settings，声音开关由调用方判门（同 startAlarm 的门用法）。
 * 返回 false 表示当前环境无声（SSR/无 AudioContext）。
 */
export function playOnce(): boolean {
  try {
    if (!ctx) ctx = new AudioContext()
    if (ctx.state === 'suspended') void ctx.resume()
  } catch {
    return false
  }
  burst()
  return true
}
