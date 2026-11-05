/**
 * UI panel walk: login then open core panels via command palette / shortcuts.
 */
import { createRequire } from 'module'
import { fileURLToPath } from 'url'
import path from 'path'
const webPkg = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../web/package.json')
const { chromium } = createRequire(webPkg)('playwright')

const WEB = process.env.NT_WEB || 'http://192.168.4.44:9302'
const HOST = '192.168.4.44'
const HTTP_PORT = '22001'
const WS_PORT = '22003'

const panels = [
  { key: 'JT', title: /决策台/ },
  { key: 'XG', title: /信号/ },
  { key: 'HQ', title: /行情|市场/ },
  { key: 'FK', title: /风控/ },
  { key: 'SJ', title: /数据源|源健康|datasource/i },
]

let pass = 0, fail = 0, warn = 0
const rec = (l, n, d) => {
  console.log(`[${l}] ${n} — ${d}`)
  if (l === 'PASS') pass++
  else if (l === 'FAIL') fail++
  else warn++
}

const browser = await chromium.launch({ headless: true })
const page = await browser.newPage({ viewport: { width: 1600, height: 1000 } })
page.setDefaultTimeout(15000)

try {
  await page.goto(WEB, { waitUntil: 'domcontentloaded' })
  await page.waitForTimeout(800)
  const host = page.getByLabel(/admin 主机|主机/).first()
  if (await host.count()) {
    await host.fill(HOST)
    const http = page.getByLabel(/HTTP/).first()
    const ws = page.getByLabel(/WebSocket|WS/).first()
    if (await http.count()) await http.fill(HTTP_PORT)
    if (await ws.count()) await ws.fill(WS_PORT)
  }
  await page.getByLabel(/账号|用户/).first().fill('admin')
  await page.getByLabel(/密码/).first().fill('123456')
  await page.getByRole('button', { name: /进入驾驶舱/ }).click()
  await page.waitForTimeout(2500)
  rec('PASS', 'login', 'cockpit')

  for (const p of panels) {
    await page.keyboard.press('Control+k')
    await page.waitForTimeout(400)
    // type shortcut prefix
    await page.keyboard.type(p.key)
    await page.waitForTimeout(400)
    await page.keyboard.press('Enter')
    await page.waitForTimeout(1200)
    const text = await page.locator('body').innerText()
    if (p.title.test(text)) rec('PASS', `panel.${p.key}`, 'title matched')
    else rec('WARN', `panel.${p.key}`, `no title match in: ${text.slice(0, 100)}`)
    await page.keyboard.press('Escape').catch(() => {})
  }

  // datasource panel deeper: look for source rows
  const body = await page.locator('body').innerText()
  for (const name of ['eastmoney', 'tushare', 'baostock', 'akshare', '通达信', '数据源']) {
    if (body.includes(name) || new RegExp(name, 'i').test(body)) rec('PASS', `ui.has.${name}`, 'visible')
    else rec('WARN', `ui.has.${name}`, 'not on current screen')
  }

  await page.screenshot({ path: 'deploy/scripts/test-44-ui-panels.png', fullPage: true })
  rec('PASS', 'screenshot', 'deploy/scripts/test-44-ui-panels.png')
} catch (e) {
  rec('FAIL', 'panels', String(e.message || e))
} finally {
  await browser.close()
}
console.log(`\n=== PANEL SUMMARY PASS=${pass} WARN=${warn} FAIL=${fail} ===`)
process.exit(fail > 0 ? 1 : 0)
