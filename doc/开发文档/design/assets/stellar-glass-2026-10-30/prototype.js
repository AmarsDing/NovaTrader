/* Documentation fixture. No fetch, backend connection, storage, or real config writes. */
const root = document.documentElement;
const content = document.getElementById('view-root');
const previewDialog = document.getElementById('preview-dialog');
const receiptDialog = document.getElementById('receipts-dialog');
const announcement = document.getElementById('announcement');
let noticeTimer;
let view = 'summary';
let revision = 42;
let prepared = null;
const preferences = { theme: 'dark', density: 'normal', quality: 'full' };
const systemMotion = window.matchMedia('(prefers-reduced-motion: reduce)');
const metadata = {
  summary: { title: '配置概览', crumb: '概览', description: '清晰的参数分区，让每次调整都有据可查。' },
  look: { title: '外观与动效', crumb: '外观与动效', description: '调整光线、材质与空间密度，找到适合长时间使用的界面。' },
  models: { title: '模型服务', crumb: '模型服务', description: '按用途配置模型与服务地址。先预览影响，再保存新版本。' },
  datasource: { title: '数据接口', crumb: '数据接口', description: '来源、凭据与采集参数分组管理；启用参数不代表接口已连通。' },
};
const labels = {
  theme: { dark: '深色', light: '亮色' },
  density: { normal: '标准', compact: '紧凑' },
  quality: { full: '完整', eco: '节能', reduced: '减少动态' },
};
const groups = {
  models: [
    { id: 'large', title: '大模型', description: '综合研判、消息分析与晨报', domain: 'ai', icon: 'chip', fields: [
      { key: 'app.brain.large.model', label: '模型名称', value: 'Qwen3.5-122B-A10B-GPTQ-Int4', hint: '示例名称，可修改后预览差异。' },
      { key: 'app.brain.large.url', label: '服务地址', value: 'http://127.0.0.1:8000/v1', type: 'url', hint: 'OpenAI 兼容服务地址；演示页面不会请求此地址。' },
      { key: 'app.brain.large.key', label: '访问密钥', value: '', type: 'password', hint: '演示状态：已设置。留空保留，输入仅模拟替换。', secretSet: true },
    ] },
    { id: 'small', title: '小模型', description: '技术面、资金面与异动解读', domain: 'blue', icon: 'chip', fields: [
      { key: 'app.brain.small.model', label: '模型名称', value: 'Qwen2.5-7B-Instruct-GPTQ-Int8', hint: '模型用途与大模型分开，参数独立保存。' },
      { key: 'app.brain.small.url', label: '服务地址', value: 'http://127.0.0.1:8001/v1', type: 'url', hint: '演示地址；可与大模型使用同一服务。' },
      { key: 'app.brain.small.key', label: '访问密钥', value: '', type: 'password', hint: '演示状态：未设置。此输入不会持久保存。' },
    ] },
  ],
  datasource: [
    { id: 'tushare', title: 'Tushare', description: '日线、资金与龙虎榜', domain: 'blue', icon: 'data', toggle: 'app.ds.tushare.on', fields: [
      { key: 'app.ds.tushare.token', label: '访问令牌', value: '', type: 'password', hint: '演示状态：已设置。留空保留，输入仅模拟替换。', secretSet: true },
      { key: 'app.ds.tushare.qps', label: '每秒请求数', value: '2', type: 'number', hint: '次/秒', short: true },
      { key: 'app.ds.tushare.timeout_sec', label: '请求超时', value: '15', type: 'number', hint: '秒', short: true },
    ] },
    { id: 'tavily', title: 'Tavily', description: '新闻搜索与事件线索', domain: '', icon: 'data', toggle: 'app.ds.tavily.on', fields: [
      { key: 'app.ds.tavily.token', label: '访问密钥', value: '', type: 'password', hint: '演示状态：未设置。真实凭据请在项目配置页管理。' },
      { key: 'app.ds.tavily.feeds', label: '查询词', value: 'A股 快讯,证监会', hint: '多个查询词用逗号分隔。' },
      { key: 'app.ds.tavily.timeout_sec', label: '请求超时', value: '20', type: 'number', hint: '秒', short: true },
    ] },
  ],
};
const baseline = { models: {}, datasource: {} };
const drafts = { models: {}, datasource: {} };
for (const [category, entries] of Object.entries(groups)) {
  for (const group of entries) {
    if (group.toggle) baseline[category][group.toggle] = 'true';
    for (const field of group.fields) baseline[category][field.key] = field.value;
  }
  drafts[category] = { ...baseline[category] };
}
const secretReplacements = new Set();

function escapeHtml(value) {
  return String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
}
function icon(name) {
  return `<svg class="nt-icon" aria-hidden="true"><use href="#icon-${name}"/></svg>`;
}
function seat(name) { return `<span class="nt-icon-seat">${icon(name)}</span>`; }
function notice(message) {
  clearTimeout(noticeTimer);
  announcement.textContent = message;
  noticeTimer = setTimeout(() => { announcement.textContent = ''; }, 6500);
}
function changeEntries(category = view) {
  if (!drafts[category]) return [];
  return Object.entries(drafts[category]).filter(([key, value]) => value !== baseline[category][key]);
}
function describeField(key, category = view) {
  for (const group of groups[category] || []) {
    if (group.toggle === key) return { key, label: `${group.title} · 启用`, type: 'bool' };
    const field = group.fields.find(item => item.key === key);
    if (field) return field;
  }
  return { key, label: key };
}
function displayValue(field, value, before = false) {
  if (field.type === 'password') return before ? '现有密钥状态（不回显）' : '替换密钥（内容已隐藏）';
  if (field.type === 'bool') return value === 'true' ? '启用' : '关闭';
  return value || '留空';
}
function overview() {
  return `<div class="nt-overview-grid">
    <section class="nt-glass-card nt-overview-hero">
      <div class="nt-card-top">${seat('display')}<span class="nt-pill">仅此设备 · 即时预览</span></div>
      <div class="nt-hero-body"><h2>让界面，进入你的节奏</h2><p>半透明玻璃、轻柔边缘光，以及清晰有序的信息层级。</p>
        <div class="nt-hero-summary"><div><strong>${labels.theme[preferences.theme]}</strong><span>当前主题</span></div><div><strong>${labels.density[preferences.density]}</strong><span>空间密度</span></div><div><strong>${labels.quality[preferences.quality]}</strong><span>材质与动效</span></div></div>
        <button class="nt-control nt-control-primary" type="button" data-view="look">调整外观与动效${icon('arrow')}</button>
      </div>
    </section>
    <div class="nt-overview-services">
      <section class="nt-glass-card nt-glass-card-blue nt-entry-card">${seat('data')}<h2>数据接口</h2><p>按来源管理凭据、配额与采集节奏。</p><button class="nt-control nt-control-quiet" type="button" data-view="datasource">配置数据接口${icon('arrow')}</button></section>
      <section class="nt-glass-card nt-glass-card-ai nt-entry-card">${seat('chip')}<h2>模型服务</h2><p>为不同研判任务设置合适的模型。</p><button class="nt-control nt-control-quiet" type="button" data-view="models">配置模型服务${icon('arrow')}</button></section>
    </div>
    <div class="nt-overview-side">
      <section class="nt-glass-card nt-entry-card"><div class="nt-card-top">${seat('link')}<span class="nt-pill">作用范围</span></div><h2>本机与共享配置</h2><p>外观作用于此设备。模型与数据参数由服务加载。</p><button class="nt-control nt-control-quiet" type="button" data-action="scope">查看配置边界${icon('arrow')}</button></section>
      <section class="nt-glass-card"><div class="nt-card-top">${seat('history')}<span class="nt-pill">演示 #${revision}</span></div><ol class="nt-receipt-steps"><li><span class="nt-step-icon">1</span><div><strong>编辑草稿</strong><small>仅保存当前分类的改动</small></div></li><li><span class="nt-step-icon">2</span><div><strong>核对影响并保存</strong><small>创建可追溯的新版本</small></div></li><li><span class="nt-step-icon">3</span><div><strong>查看服务加载回执</strong><small>保存与运行状态分开呈现</small></div></li></ol><button class="nt-control nt-control-quiet" type="button" data-action="receipts">版本与回执${icon('arrow')}</button></section>
    </div>
  </div><p class="nt-section-note">主卡引导当前任务，辅卡承接能力入口；装饰流光不代表服务在线或配置已生效。</p>`;
}
function segment(key, values) {
  return `<div class="nt-segment" role="group" aria-label="${{theme:'主题',density:'空间密度',quality:'材质与动效'}[key]}">${values.map(value => `<button type="button" data-preference="${key}" data-value="${value}" aria-pressed="${preferences[key] === value}">${labels[key][value]}</button>`).join('')}</div>`;
}
function appearance() {
  return `<div class="nt-appearance-grid"><section class="nt-glass-card nt-preference-card" aria-label="界面偏好">
    <div class="nt-preference-row"><h2>主题与文字</h2><p>沿用系统字体，让中文与数字都清晰。</p>${segment('theme',['dark','light'])}</div>
    <div class="nt-preference-row"><h2>空间密度</h2><p>调整控件高度与间隔，保持内容层级。</p>${segment('density',['normal','compact'])}</div>
    <div class="nt-preference-row"><h2>材质与动效</h2><p>完整呈现玻璃与淡流光；节能或减少动态时停止装饰运动。</p>${segment('quality',['full','eco','reduced'])}</div>
    <div class="nt-preference-row"><span class="nt-pill">此页改动即时预览</span><p style="margin-top:12px;margin-bottom:0">样稿偏好仅保留在当前页面；不修改项目设置。</p></div>
    </section><div class="nt-preview-column"><section class="nt-material-stage" aria-label="材质预览"><p class="nt-material-label"><span>外观预览</span><span id="quality-status">完整材质</span></p><div class="nt-glass-card nt-material-card"><div class="nt-card-top">${seat('display')}<span class="nt-pill">示例控件</span></div><h2>清晰的信息，安静的光</h2><p>玻璃负责层次，稳定背板承载内容。</p><div class="nt-material-value">16<span class="nt-small"> px</span><small>卡片间距示例</small></div><label class="nt-small" for="preview-model">等宽文字示例</label><input class="nt-preview-input nt-mono" id="preview-model" value="Qwen · 模型服务" readonly><div class="nt-material-actions"><button class="nt-control nt-control-primary" type="button" data-action="preview-feedback">查看交互反馈${icon('arrow')}</button><button class="nt-control" type="button" data-view="models">打开模型服务</button></div></div></section><div class="nt-info-strip">${icon('shield')}<p id="motion-note">窗体仅保留一条低强度流光。减少动态模式保留静态层次，重要信息始终即时显示。</p></div></div></div>`;
}
function fieldHtml(field) {
  const id = field.key.replaceAll('.', '-');
  const isSecret = field.type === 'password';
  const dirty = drafts[view][field.key] !== baseline[view][field.key];
  const value = drafts[view][field.key];
  const hasSecret = field.secretSet || secretReplacements.has(field.key);
  return `<div class="nt-field ${field.short ? '' : 'nt-field-wide'}" data-search-text="${escapeHtml(`${field.label} ${field.key}`)}"><div class="nt-field-label"><label for="${id}">${field.label}</label><span class="nt-field-dirty" data-dirty-for="${field.key}" ${dirty ? '' : 'hidden'}>未保存</span></div><input id="${id}" class="nt-mono" data-field="${field.key}" type="${field.type || 'text'}" value="${escapeHtml(value)}" ${field.type === 'number' ? 'step="1"' : ''} ${isSecret ? 'autocomplete="new-password" placeholder="留空保留，输入模拟替换"' : ''} aria-describedby="${id}-hint ${id}-error"><small id="${id}-hint">${isSecret && secretReplacements.has(field.key) ? '演示状态：已模拟替换，内容不持久保存。' : escapeHtml(field.hint)}${hasSecret && isSecret ? '（密钥不回显）' : ''}</small><small class="nt-field-error" id="${id}-error" role="alert" hidden></small></div>`;
}
function receiptSummary() {
  return `<aside class="nt-glass-card nt-edit-receipt"><h2>保存与加载分开核验</h2><p>演示保存版本 <span class="nt-mono">#${revision}</span></p><span class="nt-pill nt-pill-warn">加载证据待核验</span><p style="margin-top:12px">当前没有真实服务回执。配置已填写、已保存，都不能证明外部接口可达。</p><button class="nt-control nt-control-quiet" type="button" data-action="receipts">查看回执边界${icon('arrow')}</button></aside>`;
}
function editor() {
  return `<div class="nt-edit-toolbar"><label class="nt-search">${icon('search')}<input id="field-search" type="search" placeholder="查找当前分类字段" aria-label="查找当前分类字段"></label><span class="nt-pill">共享运行参数 · 演示</span></div><div class="nt-edit-grid"><form id="config-form" class="nt-edit-cards" novalidate>${groups[view].map(group => `<section class="nt-glass-card nt-edit-card nt-glass-card-${group.domain}" data-config-group="${group.id}"><header class="nt-edit-card-head">${seat(group.icon)}<div><h2>${group.title}</h2><p>${group.description}</p></div></header>${group.toggle ? `<label class="nt-toggle-field"><input type="checkbox" data-field="${group.toggle}" ${drafts[view][group.toggle] === 'true' ? 'checked' : ''}>启用此来源<span class="nt-small">仅修改演示草稿</span></label><div style="height:16px"></div>` : ''}<div class="nt-fields">${group.fields.map(fieldHtml).join('')}</div></section>`).join('')}<p class="nt-section-note" id="search-empty" hidden>没有匹配字段。清除搜索后查看全部配置。</p></form>${receiptSummary()}</div><p class="nt-section-note">此处展示代表字段的排版与保存流程。完整分类、情报模型、嵌入服务及字段规则见设计文档。</p>`;
}
function updateDirty() {
  const count = changeEntries().length;
  const isEditor = Boolean(groups[view]);
  document.getElementById('savebar').hidden = !isEditor;
  document.getElementById('dirty-text').textContent = count ? `未保存 ${count} 项演示改动` : '没有未保存改动';
  document.getElementById('base-text').textContent = `基于演示保存版本 #${revision} · 保存后仍需加载回执`;
  document.getElementById('preview').disabled = count === 0;
  document.getElementById('discard').disabled = count === 0;
  for (const key of Object.keys(groups)) {
    const dot = document.querySelector(`.nt-nav-item[data-view="${key}"] .nt-draft-dot`);
    dot.hidden = changeEntries(key).length === 0;
    dot.parentElement.setAttribute('aria-description', dot.hidden ? '' : '有未保存演示草稿');
  }
  content.querySelectorAll('[data-dirty-for]').forEach(badge => {
    badge.hidden = drafts[view][badge.dataset.dirtyFor] === baseline[view][badge.dataset.dirtyFor];
  });
}
function applyPreferences() {
  root.dataset.theme = preferences.theme;
  root.dataset.density = preferences.density;
  root.dataset.visualQuality = preferences.quality;
  root.dataset.motion = systemMotion.matches || preferences.quality !== 'full' ? 'reduced' : 'full';
  document.getElementById('theme-shortcut').querySelector('span').textContent = preferences.theme === 'dark' ? '切换亮色' : '切换深色';
  document.getElementById('appearance-status').textContent = Object.entries(labels).map(([key,map]) => map[preferences[key]]).join(' · ');
  const qualityStatus = document.getElementById('quality-status');
  if (qualityStatus) qualityStatus.textContent = systemMotion.matches ? '系统要求减少动态' : `${labels.quality[preferences.quality]}材质`;
  const motionNote = document.getElementById('motion-note');
  if (motionNote && systemMotion.matches) motionNote.textContent = '已按系统偏好停止动画。玻璃材质与必要状态保持可读。';
  const exampleValue = content.querySelector('.nt-material-value');
  if (exampleValue) exampleValue.firstChild.textContent = preferences.density === 'normal' ? '16' : '12';
}
function render({ focusHeading = false } = {}) {
  prepared = null;
  const info = metadata[view];
  document.getElementById('page-title').textContent = info.title;
  document.getElementById('page-description').textContent = info.description;
  document.getElementById('breadcrumb-current').textContent = info.crumb;
  document.querySelectorAll('.nt-nav-item').forEach(item => {
    if (item.dataset.view === view) item.setAttribute('aria-current','page');
    else item.removeAttribute('aria-current');
  });
  content.innerHTML = view === 'summary' ? overview() : view === 'look' ? appearance() : editor();
  updateDirty();
  applyPreferences();
  if (focusHeading) notice(`已打开${info.title}，其它分类的演示草稿已保留。`);
}
function validateEditor() {
  let firstInvalid;
  for (const input of content.querySelectorAll('input[data-field]')) {
    if (input.type === 'checkbox') continue;
    const error = document.getElementById(`${input.id}-error`);
    let message = '';
    if (!input.validity.valid) message = input.type === 'url' ? '请填写完整的服务地址，例如 http://127.0.0.1:8000/v1。' : '请填写整数。';
    input.setAttribute('aria-invalid',String(Boolean(message)));
    error.textContent = message;
    error.hidden = !message;
    if (message && !firstInvalid) firstInvalid = input;
  }
  if (firstInvalid) {
    const field = firstInvalid.closest('.nt-field');
    field.hidden = false;
    field.closest('[data-config-group]').hidden = false;
    firstInvalid.focus();
    return false;
  }
  return true;
}
function openPreview() {
  if (!changeEntries().length || !validateEditor()) return;
  prepared = { category: view, revision, changes: changeEntries().map(([key,value]) => ({ key, before: baseline[view][key], after: value })) };
  document.getElementById('preview-content').innerHTML = `<p class="nt-section-note">基于演示版本 #${revision} · ${prepared.changes.length} 项变更 · ${metadata[view].title}</p><dl class="nt-diff">${prepared.changes.map(change => { const field = describeField(change.key); return `<div><dt>${escapeHtml(field.label)}</dt><dd><span>${escapeHtml(displayValue(field,change.before,true))}</span><span aria-hidden="true">→</span><span>${escapeHtml(displayValue(field,change.after))}</span></dd></div>`; }).join('')}</dl><div class="nt-info-strip" style="margin-top:20px">${icon('shield')}<p>影响与目标服务在产品中应来自真实预览接口。本页没有请求接口，因此不声明任何业务影响已通过验证。</p></div>`;
  previewDialog.showModal();
}
function openReceipts() {
  document.getElementById('receipts-content').innerHTML = `<span class="nt-pill">演示保存版本 #${revision}</span><h3>尚未读取真实服务加载回执</h3><p>此设计样稿未连接后端，不显示在线状态、运行版本或接口延迟。</p><ul><li>已保存：只代表版本已经持久化。</li><li>配置缓存已加载：只代表指定实例的缓存加载。</li><li>已安装到运行态：必须带服务、实例、范围、版本与观测时间。</li><li>外部接口可达：需要独立探测证据，不能从配置回执推断。</li></ul><p class="nt-small">产品实现需按目标实例逐项核验；当前版本没有证据的项目仍标待核验。</p>`;
  receiptDialog.showModal();
}
document.addEventListener('click', event => {
  const target = event.target.closest('button');
  if (!target) return;
  if (target.dataset.view && metadata[target.dataset.view]) { view = target.dataset.view; render({ focusHeading: true }); }
  if (target.dataset.preference) { preferences[target.dataset.preference] = target.dataset.value; render(); }
  if (target.dataset.action === 'receipts') openReceipts();
  if (target.dataset.action === 'scope') notice('仅此设备：主题、密度、材质。共享服务参数：模型、数据接口等，先预览再保存。');
  if (target.dataset.action === 'preview-feedback') notice('交互反馈示例：即时响应、清晰的文字与静态焦点；光效不代表业务操作成功。');
  if (target.hasAttribute('data-close-dialog')) target.closest('dialog').close();
});
document.getElementById('theme-shortcut').addEventListener('click', () => {
  preferences.theme = preferences.theme === 'dark' ? 'light' : 'dark';
  if (view === 'summary' || view === 'look') render(); else applyPreferences();
});
content.addEventListener('input', event => {
  const target = event.target;
  if (target.dataset.field && drafts[view]) {
    drafts[view][target.dataset.field] = target.type === 'checkbox' ? String(target.checked) : target.value;
    prepared = null;
    target.removeAttribute('aria-invalid');
    const error = document.getElementById(`${target.id}-error`);
    if (error) error.hidden = true;
    updateDirty();
  }
  if (target.id === 'field-search') {
    const query = target.value.toLocaleLowerCase().trim();
    let matches = 0;
    content.querySelectorAll('[data-config-group]').forEach(group => {
      let groupMatches = 0;
      group.querySelectorAll('[data-search-text]').forEach(field => {
        const match = `${field.dataset.searchText} ${group.querySelector('h2').textContent}`.toLocaleLowerCase().includes(query);
        field.hidden = !match;
        if (match) groupMatches++;
      });
      group.hidden = groupMatches === 0;
      matches += groupMatches;
    });
    document.getElementById('search-empty').hidden = matches > 0;
  }
});
content.addEventListener('submit', event => event.preventDefault());
document.getElementById('preview').addEventListener('click', openPreview);
document.getElementById('discard').addEventListener('click', () => {
  if (!drafts[view]) return;
  drafts[view] = { ...baseline[view] };
  render();
  notice('已放弃当前分类的演示改动，其它分类草稿保留。');
});
document.getElementById('confirm-save').addEventListener('click', () => {
  if (!prepared || prepared.category !== view || prepared.revision !== revision) return;
  for (const change of prepared.changes) {
    const field = describeField(change.key);
    if (field.type === 'password') {
      secretReplacements.add(change.key);
      drafts[view][change.key] = '';
      baseline[view][change.key] = '';
    } else baseline[view][change.key] = change.after;
  }
  revision++;
  previewDialog.close();
  render();
  notice(`演示：已生成保存版本 #${revision}。尚无真实服务加载回执。`);
});
function syncMotion() { applyPreferences(); }
systemMotion.addEventListener('change', syncMotion);
document.addEventListener('visibilitychange', () => { root.dataset.pageVisibility = document.hidden ? 'hidden' : 'visible'; });
window.addEventListener('blur', () => { root.dataset.pageVisibility = 'hidden'; });
window.addEventListener('focus', () => { root.dataset.pageVisibility = document.hidden ? 'hidden' : 'visible'; });
render();
