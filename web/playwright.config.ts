/**
 * P2-12 viz 图形回归 —— Playwright 驱动（golden = pixelmatch 比对，内置 toHaveScreenshot）。
 * 用例登记表 web/src/dev/graphics-cases.ts 即合同；矩阵 = 用例 × 主题(2) × 密度(2)。
 *
 * 定论与约束（与 §7 P2-12 记录同源；对抗评审 2026-10-20 收口后口径）：
 * - 只跑 chromium + Windows；CI 双工作流同钉 windows-2025 镜像（勿换 ubuntu，也不要回退 latest 浮动签）。
 * - 【golden 唯一权威源不是本机】本机 Win10 与 CI 镜像 WS2025 的系统字体版本/DirectWrite 不同源，
 *   同平台家族 ≠ 同渲染栈：本机恒绿而 CI 恒红的立项陷阱。重刷走 .github/workflows/graphics-update.yml
 *   （workflow_dispatch 同镜像跑 test:graphics:update → artifact 下发 golden/ → 提交），本机比对只当 smoke。
 * - 定格态等宽字形在 :root 被钉成 Cascadia Code（GalleryView case 模式），装不装 JetBrains/Plex 都不污染基线。
 * - canvas 光栅：Playwright 托管 headless Chromium 默认即 SwiftShader 软件光栅（有 GPU 的机器也一样，
 *   探针实测 --disable-gpu 下截图 hash 不变），本机与 CI 同后端，无需另钉 launchOptions。
 * - dev server 由 webServer 托管（gallery 仅 DEV 构建存在，CI 产物校验又要求 dist 无 gallery，
 *   因此走 `npm run dev` 常驻而不是 preview dist）。
 * - 容差 maxDiffPixelRatio 0.004：只吞抗锯齿/次像素抖动，字形与配色漂移必然越线。
 */
import { defineConfig } from '@playwright/test'

export default defineConfig({
  testDir: './graphics',
  /* 首条用例会触发 vite 全量按需编译（echarts/monitor 深图）——globalSetup 已暖透，
   * 但 CI 清缓存环境仍比本机慢，给 CI 单独放宽到 90s；retries 保持 0 不掩盖失败。 */
  timeout: process.env.CI ? 90_000 : 60_000,
  globalSetup: './graphics/global-setup.ts',
  fullyParallel: true,
  forbidOnly: !!process.env.CI,
  retries: 0,
  workers: process.env.CI ? 2 : undefined,
  reporter: process.env.CI ? [['github'], ['list']] : 'list',
  outputDir: 'graphics/test-results',
  /* 平台中立 golden 路径（只跑 Windows，不写 -chromium-win32 后缀）：<case>.<theme>.<density>.png */
  snapshotPathTemplate: 'graphics/golden/{arg}{ext}',
  expect: {
    toHaveScreenshot: { maxDiffPixelRatio: 0.004 },
  },
  use: {
    baseURL: 'http://127.0.0.1:5199',
    viewport: { width: 1100, height: 860 },
    deviceScaleFactor: 1,
    locale: 'zh-CN',
    timezoneId: 'Asia/Shanghai',
    /* 产品既有约定：reducedMotion → ECharts motionOption 关动画、画布视觉环路只画定格帧。 */
    reducedMotion: 'reduce',
    screenshot: 'only-on-failure',
    trace: 'retain-on-failure',
  },
  webServer: {
    /* vite 8 默认只绑 ::1；钉 127.0.0.1 保证 webServer 探活与 CI Windows 上一致走 IPv4。 */
    command: 'npm run dev -- --port 5199 --strictPort --host 127.0.0.1',
    url: 'http://127.0.0.1:5199/dev/gallery',
    /* 钉 false（评审收口）：日常 dev 走 5173，5199 专属 Playwright——本地复用会把测试/Regenerate
     * 静默打到残留的并行 worktree 旧 dev server 上，CI(reuse=false) 必然红而本地全绿。 */
    reuseExistingServer: false,
    timeout: 120_000,
  },
})
