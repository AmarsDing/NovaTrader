/**
 * NovaTrader 44 UI 冒烟（Playwright）
 * 依赖：在 web/ 下有 playwright，或 npx playwright
 * 用法：node deploy/scripts/test-44-ui.mjs
 */
import { createRequire } from 'module'
import { fileURLToPath } from 'url'
import path from 'path'
const webPkg = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '../../web/package.json')
const { chromium } = createRequire(webPkg)('playwright')

const WEB = process.env.NT_WEB || 'http://192.168.4.44:9302'
const HOST = process.env.NT_HOST || '192.168.4.44'
const HTTP_PORT = process.env.NT_HTTP_PORT || '22001'
const WS_PORT = process.env.NT_WS_PORT || '22003'
const USER = process.env.NT_USER || 'admin'
const PASS = process.env.NT_PASS || '123456'

const results = []
let pass = 0, fail = 0, warn = 0

function rec(level, name, detail) {
  const line = `[${level}] ${name} — ${detail}`
  results.push(line)
  console.log(line)
  if (level === 'PASS') pass++
  else if (level === 'FAIL') fail++
  else warn++
}

async function main() {
  console.log(`=== UI Test WEB=${WEB} ===`)
  const browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 1440, height: 900 } })
  page.setDefaultTimeout(20000)

  try {
    await page.goto(WEB, { waitUntil: 'domcontentloaded' })
    await page.waitForTimeout(1500)
    const title = await page.title()
    const bodyText = await page.locator('body').innerText().catch(() => '')
    if (/NovaTrader|智脑|登录|驾驶舱|admin/i.test(bodyText + title)) {
      rec('PASS', 'SPA 加载', `title=${title}`)
    } else {
      rec('FAIL', 'SPA 加载', `title=${title} text=${bodyText.slice(0, 120)}`)
    }

    // 填主机与端口（若登录表单可见）
    const hostInput = page.getByLabel(/admin 主机|主机/).first()
    if (await hostInput.count()) {
      await hostInput.fill(HOST)
      const httpInput = page.getByLabel(/HTTP/).first()
      const wsInput = page.getByLabel(/WebSocket|WS/).first()
      if (await httpInput.count()) await httpInput.fill(HTTP_PORT)
      if (await wsInput.count()) await wsInput.fill(WS_PORT)
      rec('PASS', '填写 endpoint', `${HOST}:${HTTP_PORT}/${WS_PORT}`)
    } else {
      // 注入 localStorage 后刷新
      await page.evaluate(({ HOST, HTTP_PORT, WS_PORT }) => {
        localStorage.setItem('nt_admin_host', HOST)
        localStorage.setItem('nt_admin_http_port', HTTP_PORT)
        localStorage.setItem('nt_admin_ws_port', WS_PORT)
      }, { HOST, HTTP_PORT, WS_PORT })
      await page.reload({ waitUntil: 'domcontentloaded' })
      await page.waitForTimeout(1000)
      rec('WARN', 'endpoint 表单', '未见 label，已写 localStorage')
    }

    const userInput = page.getByLabel(/账号|用户/).first()
    const passInput = page.getByLabel(/密码/).first()
    if (await userInput.count() && await passInput.count()) {
      await userInput.fill(USER)
      await passInput.fill(PASS)
      const submit = page.getByRole('button', { name: /进入驾驶舱|登录|Login/i }).first()
      await submit.click()
      await page.waitForTimeout(3000)
      const after = await page.locator('body').innerText()
      if (/决策台|信号|行情|风控|驾驶舱|Kill|模拟|SIM|LIVE|概览/i.test(after)) {
        rec('PASS', '登录进入驾驶舱', '看到工作台关键词')
      } else if (/无响应|无法连接|失败|错误/i.test(after)) {
        rec('FAIL', '登录进入驾驶舱', after.slice(0, 200))
      } else {
        rec('WARN', '登录进入驾驶舱', `未匹配关键词: ${after.slice(0, 180)}`)
      }
    } else {
      rec('FAIL', '登录表单', '找不到账号/密码输入')
    }

    // 尝试打开命令面板 / 面板（Ctrl+K 或常见入口）
    await page.keyboard.press('Control+k').catch(() => {})
    await page.waitForTimeout(500)
    const cmd = await page.locator('body').innerText()
    if (/命令|面板|决策台|JT|信号/i.test(cmd)) {
      rec('PASS', '命令面板/快捷', '可见命令相关文案')
      await page.keyboard.press('Escape').catch(() => {})
    } else {
      rec('WARN', '命令面板', 'Ctrl+K 未见明显命令 UI')
    }

    // 截图
    const shot = 'deploy/scripts/test-44-ui-shot.png'
    await page.screenshot({ path: shot, fullPage: true })
    rec('PASS', '截图', shot)

    // 数据源相关文案（若已进工作台）
    const text = await page.locator('body').innerText()
    if (/数据源|源健康|akshare|baostock|eastmoney|通达信|stale|断路/i.test(text)) {
      rec('PASS', '数据源 UI 痕迹', '页面含数据源相关文案')
    } else {
      rec('WARN', '数据源 UI 痕迹', '首屏未见数据源文案（可能需打开面板）')
    }
  } catch (e) {
    rec('FAIL', 'UI 异常', String(e && e.message ? e.message : e))
  } finally {
    await browser.close()
  }

  console.log(`\n=== UI SUMMARY PASS=${pass} WARN=${warn} FAIL=${fail} ===`)
  if (fail > 0) process.exit(1)
}

main()
