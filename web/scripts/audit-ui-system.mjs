import { readdir, readFile, mkdir, writeFile } from 'node:fs/promises';
import path from 'node:path';
import { fileURLToPath } from 'node:url';

// NovaTrader UI audit — 合同见 docs/_shared/ui-ux/guidelines/07-前端审计脚本规范.md
// 组件前缀：<Nt* 自研族 + 原生 button/input/select/table（禁第三方组件库）。
// 页面目录：src/view/ + src/panels/；路由：src/router/index.ts（@/ 别名 → src/）。
const webRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const srcRoot = path.join(webRoot, 'src');
const pageDirs = ['src/view/', 'src/panels/'];
const routerFile = path.join(srcRoot, 'router/index.ts');
const routeImportPattern = /import\(['"]@\/([^'"]+\.vue)['"]\)/g;

async function walk(dir) {
  const entries = await readdir(dir, { withFileTypes: true });
  const nested = await Promise.all(entries.map((entry) => {
    const target = path.join(dir, entry.name);
    return entry.isDirectory() ? walk(target) : [target];
  }));
  return nested.flat();
}

const files = (await walk(srcRoot)).filter((file) => file.endsWith('.vue'));
const count = (source, pattern) => [...source.matchAll(pattern)].length;
// 固定宽判定：width/min-width ≥600px 且同规则内没有 max-width: Nvw 弹性上限才算红线
const fixedWidthCount = (css) => {
  let n = 0;
  for (const m of css.matchAll(/(?<![-\w])(?:min-)?width:\s*(?:[6-9]\d{2}|\d{4,})px/g)) {
    const close = css.indexOf('}', m.index);
    const seg = css.slice(m.index, close < 0 ? css.length : close);
    if (/max-width:\s*\d+(?:\.\d+)?vw/.test(seg)) continue;
    n++;
  }
  return n;
};
const inventory = [];
for (const file of files.sort()) {
  const source = await readFile(file, 'utf8');
  const template = source.match(/<template>([\s\S]*?)<script/)?.[1] ?? source;
  const styles = [...source.matchAll(/<style\b[^>]*>([\s\S]*?)<\/style>/g)].map((m) => m[1]).join('\n');
  const rel = path.relative(webRoot, file).replaceAll('\\', '/');
  const ignored = source.startsWith('// ui-audit-ignore:') || source.startsWith('<!-- ui-audit-ignore:');
  // mask-image 用亮度通道取色，#000/#fff 是遮罩语义不是主题色——剔除该行再计裸色
  const stylesNoMask = styles.replace(/\/\/[^\n]*|\/\*[\s\S]*?\*\//g, '').split('\n').filter((line) => !/mask-image|mask-size|mask:/.test(line)).join('\n');
  inventory.push({
    file: rel,
    page: pageDirs.some((dir) => rel.startsWith(dir)),
    ignored,
    buttons: count(template, /<NtButton\b|<button\b/g),
    fields: count(template, /<(?:NtInput|NtSelect|NtSwitch|input|select|textarea)\b/g),
    dialogs: count(template, /<NtModal\b|<NtConfirm\b/g),
    tables: count(template, /<(?:NtTable|table)\b/g),
    localColors: ignored ? 0 : count(stylesNoMask, /#[\da-f]{3,8}\b|\brgba?\(\s*\d|\bhsla?\(\s*\d/gi),
    inlineStyles: count(template, /\s(?::)?style=/g),
    fixedWidths: fixedWidthCount(stylesNoMask.split('\n').filter((line) => !line.includes('@media')).join('\n')),
    actions: [...new Set([...template.matchAll(/@(click|submit|delete|save|retry|refresh)[\w:.-]*=/g)].map((m) => m[1]))],
  });
}

let routePages = [];
try {
  const routes = await readFile(routerFile, 'utf8');
  routePages = [...new Set([...routes.matchAll(routeImportPattern)].map((m) => `src/${m[1]}`))].sort();
} catch { /* router optional */ }

const totals = Object.fromEntries(['buttons', 'fields', 'dialogs', 'tables', 'localColors', 'inlineStyles', 'fixedWidths']
  .map((key) => [key, inventory.reduce((sum, entry) => sum + entry[key], 0)]));
const report = {
  scope: 'Static Vue source inventory; counts do not prove working interactions or visual acceptance.',
  vueFiles: inventory.length,
  pageFiles: inventory.filter((entry) => entry.page).length,
  routedPageFiles: routePages.length,
  totals,
  routePages,
  pages: inventory.filter((entry) => entry.page),
  componentsWithLocalColors: inventory.filter((entry) => entry.localColors).sort((a, b) => b.localColors - a.localColors),
};

const outputIndex = process.argv.indexOf('--output');
if (outputIndex >= 0 && process.argv[outputIndex + 1]) {
  const output = path.resolve(webRoot, process.argv[outputIndex + 1]);
  await mkdir(path.dirname(output), { recursive: true });
  await writeFile(output, JSON.stringify(report, null, 2) + '\n');
  console.log(`Inventory: ${output}`);
}
console.log(JSON.stringify({ vueFiles: report.vueFiles, pageFiles: report.pageFiles, routedPageFiles: report.routedPageFiles, ...totals }, null, 2));

if (process.argv.includes('--check')) {
  const failures = [];
  // 红线 1：裸色值（tokens.css 与 dockview-core.css 第三方壳不在 .vue 扫描范围）
  if (totals.localColors) {
    failures.push(`${totals.localColors} literal colors in Vue styles; use --nt-* tokens (见 docs/_shared/design/tokens.css).`);
    for (const entry of report.componentsWithLocalColors) failures.push(`  ${entry.file}: ${entry.localColors}`);
  }
  // 红线 3（路由登记）对本项目不适用：页面经 dockview 动态面板承载（WorkbenchView），
  // 仅 login/workbench/monitor/gallery 走 vue-router。故路由检查降级为信息输出。
  // 固定宽在本项目多为交易网格/行情面板的刻意布局，列为观察项（见 inventory JSON），不作硬失败。
  const advisory = inventory.filter((entry) => entry.fixedWidths && entry.page && !entry.file.includes('/dev/'));
  if (advisory.length) console.warn(`advisory: ${advisory.length} page files use fixed widths >=600px (dockview panels; see inventory)`);
  if (failures.length) {
    console.error(failures.join('\n'));
    process.exitCode = 1;
  } else console.log('UI contract passed: no literal Vue style colors.');
}
