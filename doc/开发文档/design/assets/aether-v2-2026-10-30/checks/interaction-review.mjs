/* Offline prototype review. Uses the repository's installed Playwright; no production calls. */
import fs from 'node:fs';
import path from 'node:path';
import assert from 'node:assert/strict';
import { reviewControls } from './controls-review.mjs';
import { fileURLToPath, pathToFileURL } from 'node:url';
const base = fileURLToPath(new URL('..', import.meta.url));
const repo = path.resolve(base, '../../../../..');
const { chromium } = await import(pathToFileURL(path.join(repo, 'web/node_modules/@playwright/test/index.mjs')).href);
const browser = await chromium.launch({ headless: true });
const page = await browser.newPage({ viewport: { width: 1440, height: 1100 }, deviceScaleFactor: 1 });
const checks = [], errors = [], layouts = [], focusLayouts = [];
const url = pathToFileURL(path.join(base, 'index.html')).href;
const screenshots = path.join(base, 'r6-previews');
fs.mkdirSync(screenshots, { recursive: true });
page.on('pageerror', error => errors.push(error.message));
page.on('requestfailed', request => errors.push(`${request.url()}: ${request.failure()?.errorText}`));
const action = name => page.locator(`[data-gx-action="${name}"]`);
const dock = view => page.locator(`.at-dock-item[data-destination="${view}"]`);
const show = async group => {
  if (await page.locator('#gx-family').isVisible()) await page.locator('#gx-family').selectOption(group);
  else await page.locator(`[data-gx-group="${group}"]`).click();
};
const add = (name, evidence) => { checks.push({ name, pass: true, evidence }); console.log(`PASS ${name}`); };
const css = (selector, property) => page.locator(selector).evaluate((el, key) => getComputedStyle(el)[key], property);
const fresh = async () => { await page.goto(url + '?revision=6#lab'); await page.reload(); await page.locator('[data-gx-collection="refined"]').click(); await page.waitForSelector('.gx-card'); };
const shot = async name => { await page.waitForTimeout(240); await page.screenshot({ path: path.join(screenshots, name) }); };
function layoutInPage() {
  const stage = document.getElementById('stage');
  const bounds = stage.getBoundingClientRect();
  const dock = document.querySelector('.at-dock').getBoundingClientRect();
  const root = document.documentElement;
  const rail = document.getElementById('commit-rail');
  const railBounds = rail.getBoundingClientRect();
  const railVisible = getComputedStyle(rail).display !== 'none';
  return {
    viewport: [innerWidth, innerHeight], stageHeight: stage.clientHeight,
    pageWidth: root.scrollWidth, pageHeight: root.scrollHeight, stageWidth: stage.clientWidth, stageScrollWidth: stage.scrollWidth,
    dockWithin: dock.left >= 0 && dock.right <= innerWidth + 1 && dock.bottom <= innerHeight + 1,
    chromeSeparated: bounds.bottom <= dock.top + 1 && (!railVisible || (bounds.bottom <= railBounds.top + 1 && railBounds.bottom <= dock.top + 1)),
    pass: root.scrollWidth <= innerWidth + 1 && root.scrollHeight <= innerHeight + 1 && stage.scrollWidth <= stage.clientWidth + 1 && stage.clientHeight > 100 && dock.left >= 0 && dock.right <= innerWidth + 1 && dock.bottom <= innerHeight + 1 && bounds.bottom <= dock.top + 1,
  };
}
try {
  await fresh();
  assert.equal(await page.locator('.gx-card').count(), 24);
  const keys = await page.locator('[data-gx-card]').evaluateAll(items => items.map(item => item.dataset.gxCard));
  for (const [group, count] of [['action', 3], ['input', 6], ['object', 3], ['feedback', 4], ['selection',4], ['special',4]]) { await show(group); assert.equal(await page.locator('.gx-card:visible').count(), count); }
  await show('all');
  await page.locator('#gx-filter').fill('没有这种组件-xyz');
  assert.equal(await page.locator('#gx-empty').isVisible(), true);
  await action('clear-filter').click();
  assert.equal(await page.locator('.gx-card:visible').count(), 24);
  add('分类、检索与零结果恢复', '六家族3/6/3/4/4/4，恢复24项');

  for (const key of keys) {
    const expand = page.locator(`[data-gx-focus="${key}"]`);
    await expand.click();
    const card = page.locator(`[data-gx-card="${key}"]`);
    assert.equal(await page.locator('.gx-card:visible').count(), 1);
    assert.equal(await card.locator('.gx-inline-spec').isVisible(), true);
    await card.locator('[data-gx-spec]').click();
    assert.equal(await page.locator('.gx-spec-list>div').count(), 7);
    assert.match(await page.locator('.gx-spec-source a').first().getAttribute('href'), /adbd2adde0a299a3956ea288fb444ec01891ca41/);
    await page.keyboard.press('Escape');
    assert.equal(await card.locator('[data-gx-spec]').evaluate(el => el === document.activeElement), true);
    await action('exit-focus').click();
  }
  add('24组件放大、规范、退出与回焦', '逐项实际进入，7维合同与固定版本来源可达');
  await page.locator('[data-gx-focus="shimmer"]').click();
  assert.match(await page.locator('[data-gx-focus="shimmer"]').getAttribute('aria-label'), /收起/);
  await page.keyboard.press('Escape'); assert.equal(await page.locator('.gx-card:visible').count(),24);

  await show('action');
  await action('press').focus(); await page.keyboard.press('Enter');
  assert.equal(await page.locator('.gx-preview-ticket').getAttribute('data-open'), 'true');
  assert.match(await page.locator('.gx-preview-ticket strong').textContent(), /已展开/);
  assert.equal(await page.locator('.gx-shimmer').evaluate(el => getComputedStyle(el, '::after').animationName), 'gx-activate');
  assert.equal(await page.getByRole('button', { name: '禁用态', exact: true }).isDisabled(), true);
  await action('launch').focus(); await page.keyboard.press('Enter');
  assert.equal(await action('launch').getAttribute('aria-disabled'), 'true');
  assert.equal(await action('launch').getAttribute('aria-busy'), 'true');
  assert.equal(await action('launch').evaluate(el => el === document.activeElement), true);
  await page.keyboard.press('Enter');
  await page.waitForFunction(() => document.querySelector('.gx-launch-target').dataset.state === 'done');
  assert.equal(await action('launch').getAttribute('aria-busy'), 'false');
  add('掠光与跃迁的可见结果、禁用/忙碌和焦点', '预览实际展开；CSS掠光触发；忙碌不重入、不丢焦点；模型图形落位');

  for (const [operation, property] of [['pin', 'pinned'], ['focus', 'focused']]) {
    await action('toggle-menu').click(); await action(`satellite-${operation}`).click();
    assert.equal(await page.locator('.gx-action-target').getAttribute(`data-${property}`), 'true');
    assert.equal(await action(`satellite-${operation}`).getAttribute('aria-pressed'), 'true');
  }
  assert.equal(await css('.gx-action-target>i', 'visibility'), 'visible');
  assert.notEqual(await css('.gx-action-target', 'boxShadow'), 'none');
  await action('toggle-menu').click(); await page.keyboard.press('ArrowRight');
  assert.equal(await action('satellite-focus').evaluate(el => el === document.activeElement), true);
  await page.keyboard.press('Escape');
  assert.equal(await action('toggle-menu').evaluate(el => el === document.activeElement), true);
  await action('toggle-menu').click(); await action('satellite-restore').click();
  assert.equal(await page.locator('.gx-action-target').getAttribute('data-pinned'), 'false');
  assert.equal(await page.locator('.gx-action-target').getAttribute('data-focused'), 'false');
  add('卫星动作确实改变对象并可还原', '标记可见、聚焦轮廓改变、方向键/Esc/回焦和复位通过');

  await show('input');
  const haloBefore = await css('.gx-ambient-halo', 'opacity');
  await page.locator('#gx-switch-input').uncheck(); await page.waitForTimeout(260);
  assert.equal(await css('.gx-ambient-halo', 'opacity'), '0');
  assert.notEqual(haloBefore, '0');
  await page.locator('[data-gx-check="1"]').check();
  assert.equal(await css('.gx-check-preview>span', 'visibility'), 'visible');
  await page.locator('[data-gx-check="0"]').uncheck(); await page.waitForTimeout(200);
  const edgeOff = await css('.gx-check-preview', 'borderTopColor');
  await page.locator('[data-gx-check="0"]').check(); await page.waitForTimeout(200);
  assert.notEqual(await css('.gx-check-preview', 'borderTopColor'), edgeOff);
  await page.locator('[name="gx-perspective"]').first().focus(); await page.keyboard.press('ArrowRight');
  assert.match(await page.locator('#gx-perspective-panel').textContent(), /局部参数/);
  await page.locator('[name="gx-perspective"][value="2"]').check();
  assert.match(await page.locator('#gx-perspective-panel').textContent(), /设计证据/);
  add('开关、多选与分段器控制真实图形/内容', '环境光透明度、标签可见性、边框颜色和内容面均有实际变化');

  await action('validate-name').click(); assert.equal(await page.locator('#gx-name').getAttribute('aria-invalid'), 'true');
  await page.locator('#gx-name').fill('😀'.repeat(24)); await page.locator('#gx-name').press('Enter');
  assert.equal(await page.locator('#gx-name').getAttribute('aria-invalid'), 'false');
  await page.locator('#gx-name').fill('名'.repeat(25)); await action('validate-name').click();
  await page.locator('#theme-toggle').click();
  assert.equal(await page.locator('#gx-name').getAttribute('aria-invalid'), 'true');
  assert.match(await page.locator('#gx-name-error').textContent(), /最多24/);
  await page.locator('#gx-name').fill('星舰参考');
  assert.equal(await page.locator('#gx-name').getAttribute('aria-invalid'), null);
  await action('validate-name').click();
  add('输入校验与重绘一致性', '空值、Unicode码点上限、错误修改清除、主题切换保留校验结果');

  await page.locator('#gx-power').focus(); await page.keyboard.press('Home');
  assert.equal(await page.locator('#gx-power-value').textContent(), '20%');
  const dim = await css('.gx-energy>i', 'opacity'); const small = await css('.gx-energy>i', 'transform');
  await page.keyboard.press('End');
  assert.equal(await page.locator('#gx-power-value').textContent(), '100%');
  assert.notEqual(await css('.gx-energy>i', 'opacity'), dim); assert.notEqual(await css('.gx-energy>i', 'transform'), small);
  await page.keyboard.press('ArrowLeft'); assert.equal(await page.locator('#gx-power-value').textContent(), '95%');
  await page.locator('#gx-search').dispatchEvent('compositionstart');
  await page.locator('#gx-search').evaluate(el => { el.value = '模型'; el.dispatchEvent(new InputEvent('input', { bubbles: true, isComposing: true })); });
  assert.equal(await page.locator('#gx-search-results li').count(), 3);
  await page.locator('#gx-search').dispatchEvent('compositionend');
  assert.equal(await page.locator('#gx-search-results li').count(), 1);
  await page.locator('#gx-search-results button').click();
  assert.match(await page.evaluate(() => location.hash), /^#models\//);
  await dock('lab').click(); await show('input'); await action('clear-search').click();
  assert.equal(await page.locator('#gx-search').evaluate(el => el === document.activeElement), true);
  add('光强可见变化与可执行检索', '光环透明度/半径改变，步长正确；IME结束再过滤；结果实际导航并可返回');

  await show('object'); await action('pin-object').click();
  assert.match(await page.locator('.gx-holo').evaluate(el => getComputedStyle(el, '::after').content), /已固定/);
  await action('flip').click();
  assert.match(await page.locator('.gx-flip-copy>span:visible').textContent(), /背面/);
  await page.locator('#motion-toggle').click();
  assert.equal(await css('.gx-flip-front', 'visibility'), 'hidden');
  assert.equal(await css('.gx-flip-back', 'transform'), 'none');
  assert.equal(await page.locator('.gx-flip-copy>span:visible').count(), 1);
  await action('pattern-dots').click();
  assert.equal(await css('.gx-pattern-texture', 'backgroundSize'), '16px 16px');
  await action('pattern-grid').click(); assert.equal(await css('.gx-pattern-texture', 'backgroundSize'), '32px 32px, 32px 32px');
  await page.locator('#motion-toggle').click();
  add('对象固定、双面图形降级与纹理', '固定标记实际出现；减少动态显示真正背面；网格/点阵尺寸随选择变化');

  await show('feedback');
  await action('start-orbit').focus(); await page.keyboard.press('Enter');
  assert.equal(await action('start-orbit').evaluate(el => el === document.activeElement), true);
  await dock('lab').click(); assert.equal(await page.locator('.gx-orbit').getAttribute('data-running'),'true');
  await action('fail-orbit').click();
  assert.match(await page.locator('#gx-orbit-status').textContent(), /模拟失败/);
  await page.waitForTimeout(2500); assert.equal(await page.locator('.gx-orbit').getAttribute('data-outcome'), 'error');
  await action('start-orbit').click(); await action('cancel-orbit').click();
  assert.match(await page.locator('#gx-orbit-status').textContent(), /已取消/);
  await action('start-orbit').click(); await page.waitForFunction(() => document.querySelector('.gx-orbit').dataset.outcome === 'done');
  await action('start-skeleton').click(); await action('fail-skeleton').click();
  assert.equal(await page.locator('#gx-skeleton-slot').getAttribute('aria-busy'), 'false');
  assert.equal(await page.locator('.gx-loaded-content').isVisible(), true);
  await page.waitForTimeout(1900); assert.match(await page.locator('#gx-skeleton-status').textContent(), /模拟失败/);
  await action('start-skeleton').click(); await action('cancel-skeleton').click();
  await action('start-skeleton').click(); await page.waitForFunction(() => document.querySelector('#gx-skeleton-slot').getAttribute('aria-busy') === 'false');
  add('等待与骨架的全部终态', '完成/取消/失败/重试，失败保留内容且旧计时不覆盖终态');

  await page.locator('#gx-tip-trigger').focus();
  assert.equal(await page.locator('#gx-tooltip').getAttribute('data-show'), 'true');
  await page.locator('#gx-tip-trigger').click();
  assert.equal(await page.locator('#gx-tip-trigger').getAttribute('aria-pressed'), 'true');
  await page.keyboard.press('Escape');
  assert.equal(await page.locator('#gx-tooltip').getAttribute('data-show'), 'false');
  await action('favorite').click(); assert.equal(await page.locator('.gx-favorite-target i').textContent(), '★');
  assert.equal(await action('favorite').evaluate(el => el === document.activeElement), true);
  await page.locator('#theme-toggle').click();
  assert.equal(await page.locator('.gx-favorite-target i').textContent(), '★');
  await action('undo-favorite').click(); assert.equal(await page.locator('.gx-favorite-target i').textContent(), '☆');
  await action('favorite').click(); await action('dismiss-toast').click(); assert.equal(await page.locator('.gx-toast').isVisible(), false);
  await action('favorite').click(); assert.equal(await page.locator('.gx-favorite-target i').textContent(), '☆');
  add('注释与收藏回声的一致性', '固定/Esc关闭；收藏标记真实切换、主题切换一致、撤销/关闭和焦点正确');

  await action('start-orbit').click(); await action('start-skeleton').click();
  await dock('models').click();
  await page.locator('[data-model="small"]').first().click();
  await page.locator('[data-field="app.brain.small.model"]').fill('R2-fixture-small');
  await dock('look').click(); await page.goBack();
  await page.waitForFunction(() => location.hash === '#models/small');
  assert.equal(await page.locator('[data-field="app.brain.small.model"]').inputValue(), 'R2-fixture-small');
  await page.goForward(); await page.waitForFunction(() => location.hash === '#look');
  await dock('lab').click(); await show('feedback');
  assert.match(await page.locator('#gx-orbit-status').textContent(), /已取消/);
  await action('reset').click();
  assert.match(await page.locator('#commit-status').textContent(), /1 项/);
  assert.equal(await page.locator('#gx-spec-dialog').count(), 1);
  add('真实历史导航、跨页草稿与销毁', 'hash、Back/Forward、对象选择和草稿一致；离页取消，重置不清业务草稿');

  await dock('models').click(); await page.locator('[data-model="large"]').first().click();
  const service = page.locator('[data-field="app.brain.large.url"]');
  for (const invalid of ['javascript:alert(1)', 'file:///C:/fixture', 'ftp://example.invalid', 'not-an-url']) {
    await service.fill(invalid); await page.locator('#review-trigger').click();
    assert.equal(await page.locator('#review-dialog').evaluate(el => el.open), false);
    assert.equal(await service.getAttribute('aria-invalid'), 'true');
  }
  await service.fill('http://127.0.0.1:8000/v1');
  await page.locator('[data-field="app.brain.large.key"]').fill('fixture-secret-review-r2');
  await page.locator('#review-trigger').click();
  assert.equal((await page.locator('#review-dialog').textContent()).includes('fixture-secret-review-r2'), false);
  await page.locator('[data-action="confirm-save"]').click();
  assert.equal(await page.locator('[data-field="app.brain.large.key"]').inputValue(), '');
  assert.match(await page.locator('#commit-status').textContent(), /#43/);
  await dock('datasource').click();
  const qps = page.locator('[data-field="app.ds.tushare.qps"]');
  for (const invalid of ['0', '-1', '1.5', '9007199254740992']) {
    await qps.fill(invalid); await page.locator('#review-trigger').click(); assert.equal(await qps.getAttribute('aria-invalid'), 'true');
  }
  await qps.fill(''); await page.locator('#review-trigger').click();
  assert.equal(await page.locator('#review-dialog').evaluate(el => el.open),true);
  assert.match(await page.locator('#review-content').textContent(),/清除覆盖，跟随配置文件/);
  await page.keyboard.press('Escape');
  await qps.fill('3');
  await page.locator('#command-trigger').click();
  await page.locator('#command-input').fill('模型');
  await page.locator('#command-input').evaluate(el => el.dispatchEvent(new KeyboardEvent('keydown', { key: 'Enter', isComposing: true, bubbles: true })));
  assert.equal(await page.locator('#command-dialog').evaluate(el => el.open), true);
  await page.locator('#command-input').press('Enter');
  await page.waitForFunction(() => document.documentElement.dataset.workspace === 'models');
  await dock('scene').click();
  await page.getByRole('button', { name: '进入组件实验室', exact: true }).click();
  add('配置审阅、输入边界、密钥遮蔽和命令IME', '拒绝4类坏地址与无效数值；clearable留空预览为清除覆盖；保存版本#43、密钥不回显；组合输入Enter不执行命令；工作台入口可达');

  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.waitForFunction(() => document.documentElement.dataset.motion === 'reduced');
  assert.equal(await page.locator('.gx-shimmer').evaluate(el => getComputedStyle(el, '::after').animationName), 'none');
  await show('object'); await action('flip').click();
  assert.equal(await css('.gx-flip-front', 'visibility'), 'hidden');
  await page.emulateMedia({ reducedMotion: 'no-preference' });
  add('系统减少动态', 'CSS运动停止，翻面继续显示正确的静态背面');

  await fresh();
  await reviewControls({page,add,show,css,shot});
  await fresh();
  const sizes = [{ width: 1440, height: 900 }, { width: 1366, height: 768 }, { width: 1024, height: 768 }, { width: 390, height: 844 }, { width: 320, height: 844 }];
  for (const size of sizes) {
    await page.setViewportSize(size);
    for (const view of ['scene', 'models', 'datasource', 'look', 'lab']) {
      await dock(view).click();
      for (const theme of ['dark', 'light']) for (const density of ['normal', 'compact']) for (const quality of ['full', 'eco', 'reduced']) {
        await page.evaluate(({ theme, density, quality }) => { const root = document.documentElement; Object.assign(root.dataset, { theme, density, visualQuality: quality, motion: quality === 'full' ? 'full' : 'reduced' }); }, { theme, density, quality });
        const result = await page.evaluate(layoutInPage);
        layouts.push({ ...size, view, theme, density, quality, ...result });
      }
    }
  }
  assert.deepEqual(layouts.filter(item => !item.pass || !item.chromeSeparated), []);
  add('5工作面的完整尺寸/样式矩阵', '5尺寸×5工作面×2主题×2密度×3质量=300组合；页面无横纵溢出、正文独立滚动、工具坞/托盘不重叠');

  for (const size of [{ width: 1440, height: 1100 }, { width: 390, height: 844 }, { width: 320, height: 844 }]) {
    await page.setViewportSize(size); await show('all');
    for (const theme of ['dark', 'light']) {
      await page.evaluate(theme => { Object.assign(document.documentElement.dataset, { theme, density: 'normal', visualQuality: 'full', motion: 'full' }); }, theme);
      for (const key of keys) {
        await page.locator(`[data-gx-focus="${key}"]`).click();
        const result = await page.evaluate(layoutInPage);
        focusLayouts.push({ ...size, theme, key, ...result });
        assert.equal(await page.locator(`[data-gx-card="${key}"] .gx-inline-spec`).isVisible(), true);
        await action('exit-focus').click();
      }
    }
  }
  assert.deepEqual(focusLayouts.filter(item => !item.pass), []);
  add('全部24组件的专注视图矩阵', '3尺寸×2主题×24组件=144组合，含320px，控件和说明均可滚动到达');

  await fresh(); await page.setViewportSize({ width: 1366, height: 768 }); await shot('r6-short-window.png');
  await page.locator('[data-gx-focus="segment"]').click(); await page.locator('[name="gx-perspective"][value="2"]').check(); await shot('r6-focus.png');
  await action('exit-focus').click();
  await page.setViewportSize({ width: 1440, height: 1480 });
  for (const theme of ['dark', 'light']) {
    if (await page.locator('html').getAttribute('data-theme') !== theme) await page.locator('#theme-toggle').click();
    for (const group of ['action', 'input', 'object', 'feedback','selection','special']) {
      await show(group);
      if (group === 'action') { await action('press').click(); await action('launch').click(); await page.waitForTimeout(720); await action('toggle-menu').click(); }
      if (group === 'input') { await page.locator('[data-gx-check="1"]').check(); await page.locator('[name="gx-perspective"][value="2"]').check(); await action('validate-name').click(); }
      if (group === 'object') { if (await action('flip').getAttribute('aria-expanded') === 'false') await action('flip').click(); await action('pattern-dots').click(); }
      if (group === 'feedback') { await action('start-orbit').click(); await action('fail-orbit').click(); await action('start-skeleton').click(); await action('fail-skeleton').click(); if (await action('favorite').getAttribute('aria-pressed') === 'false') await action('favorite').click(); await page.locator('#gx-tip-trigger').click(); }
      await page.locator('.gx-grid').evaluate(el => el.scrollIntoView({ block: 'start' }));
      await shot(`r6-${group}-${theme}.png`);
    }
  }
  await show('all'); await page.setViewportSize({ width: 390, height: 844 });
  await page.locator('#gx-family').selectOption('object');
  await page.locator('[data-gx-card="holo"]').evaluate(el => el.scrollIntoView({ block: 'start' })); await shot('r6-mobile.png');
  const touchContext = await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true});
  const touchPage = await touchContext.newPage();
  touchPage.on('pageerror',error => errors.push(error.message));
  await touchPage.goto(url+'?touch=1#lab');
  await touchPage.locator('[data-gx-collection="refined"]').tap();
  await touchPage.locator('#gx-family').selectOption('input');
  await touchPage.locator('#gx-switch-input').tap(); assert.equal(await touchPage.locator('#gx-switch-input').isChecked(),false);
  await touchPage.locator('#gx-family').selectOption('action');
  await touchPage.locator('[data-gx-action="toggle-menu"]').tap(); await touchPage.locator('[data-gx-action="satellite-pin"]').tap();
  assert.equal(await touchPage.locator('.gx-action-target').getAttribute('data-pinned'),'true');
  const smallTargets = await touchPage.locator('.gx-lab button:visible,.gx-lab select:visible,.gx-lab input:visible').evaluateAll(items => items.filter(item => item.getBoundingClientRect().height < 43.5).map(item => ({tag:item.tagName,id:item.id,action:item.dataset.gxAction,height:item.getBoundingClientRect().height})));
  assert.deepEqual(smallTargets,[]);
  await touchContext.close();
  add('触屏事件与有效目标', '移动模拟中实际tap开关/快捷动作，当前可见实验室控件有效高度均≥44px；不等于真实触屏硬件验收');
  assert.deepEqual(errors, []);
  fs.writeFileSync(path.join(base, 'r6-verification.json'), JSON.stringify({ revision: 'R6', date: '2026-10-31', environment: 'Isolated headless Chromium; offline file:// prototype; no production services or live user browser.', checks, layouts, focusLayouts, errors, screenshots: fs.readdirSync(screenshots).filter(name => name.endsWith('.png')) }, null, 2) + '\n');
  console.log(JSON.stringify({ checks: checks.length, layouts: layouts.length, focusLayouts: focusLayouts.length, errors }));
} finally {
  await browser.close();
}

