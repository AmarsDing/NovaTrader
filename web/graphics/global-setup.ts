/**
 * P2-12 冷编译暖身（评审收口）：webServer 探活只拿到 SPA 回吐的 index.html（/dev/gallery 是懒路由，
 * 不触发 vite transform），GalleryView 静态 import 的整套走查组件图（ui/viz/display/monitor +
 * echarts/depth 深链，约 40 个组件）的按需编译会全压在首条用例的 goto 上；
 * CI（npm ci 清缓存 + 2 workers + Defender 实时扫描）首波可吃满 60s 预算，retries:0 下一次超时稳定记红、
 * 重跑（缓存已暖）又绿——典型 CI flake 签名。这里先开一枪把 transform/deps 缓存暖透再进矩阵
 *（一页即覆盖整棵模块图：所有 case 共用同一个 GalleryView chunk）。
 *
 * Playwright 保证 webServer 先于 globalSetup 就绪；baseURL 经 env 覆盖，不 import config 自身（循环引用）。
 */
import { chromium } from '@playwright/test'

export default async function globalSetup(): Promise<void> {
  const baseURL = process.env.GRAPHICS_BASE_URL ?? 'http://127.0.0.1:5199'
  const browser = await chromium.launch()
  try {
    const page = await browser.newPage()
    try {
      const resp = await page.goto(`${baseURL}/dev/gallery?case=tokens`, {
        waitUntil: 'networkidle',
        timeout: 120_000,
      })
      if (!resp || !resp.ok()) throw new Error(`graphics warmup failed: HTTP ${resp?.status() ?? 'no-response'}`)
    } finally {
      await page.close()
    }
  } finally {
    await browser.close()
  }
}
