/**
 * P2-12 viz 图形回归：/dev/gallery 用例定格态 × 主题(dark/light) × 密度(normal/compact)。
 * 用例即登记表（web/src/dev/graphics-cases.ts）：新增走查 section 登记一行，比对矩阵自动扩展。
 *
 * 【golden 权威源 = CI 镜像】重刷走 .github/workflows/graphics-update.yml（workflow_dispatch →
 * artifact 下发 golden/ → 与本地 diff 核对后提交）；本机 npm run test:graphics:update 只当
 * smoke 自查，产物不入库——本机与 CI 镜像的系统字体版本/DirectWrite 不同源（评审钉死的立项前提）。
 * 比对失败时看 graphics/test-results/ 下的 actual/diff 图。
 */
import { expect, test } from '@playwright/test'
import { GRAPHICS_CASES } from '../src/dev/graphics-cases'

const THEMES = ['dark', 'light'] as const
const DENSITIES = ['normal', 'compact'] as const

/** 走查页偏好=用例的一部分：主题/密度/色觉/画质档位钉死（settings.load 读五键，多余键不写）。 */
function presetPrefs(theme: string, density: string): string {
  return `try { localStorage.setItem('nt.settings.v1', JSON.stringify({ theme: '${theme}', density: '${density}', colorVision: 'default', sound: false, visualQuality: 'full' })) } catch {}`
}

for (const theme of THEMES) {
  for (const density of DENSITIES) {
    test.describe(`矩阵 ${theme}/${density}`, () => {
      test.beforeEach(async ({ page }) => {
        await page.addInitScript(presetPrefs(theme, density))
      })
      for (const item of GRAPHICS_CASES) {
        test(`${item.id}（${item.title}）`, async ({ page }) => {
          await page.goto(`/dev/gallery?case=${item.id}`, { waitUntil: 'networkidle' })
          /* 画布组件经 rAF 首帧上画（定格态同样是 rAF 驱动一帧），抢帧截屏会得到空白 canvas。
           * 统一等两帧；DOM/ECharts 组件此等待成本为毫秒级，换画布用例不是空白 baseline。 */
          await page.evaluate(() => new Promise((resolve) => requestAnimationFrame(() => requestAnimationFrame(() => resolve(null)))))
          const name = `${item.id}.${theme}.${density}.png`
          if (item.id === 'modal') {
            /* Modal teleport 到 body：截整页（浮层 + 背景徽标区）。 */
            await expect(page).toHaveScreenshot(name)
            return
          }
          const frame = page.locator('.g-sec').first()
          await expect(frame).toBeVisible()
          await expect(frame).toHaveScreenshot(name)
        })
      }
    })
  }
}
