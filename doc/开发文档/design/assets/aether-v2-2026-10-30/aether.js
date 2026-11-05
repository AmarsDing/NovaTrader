/* Aether V2: local design fixture. No fetch, network APIs, persistent storage, or real trades. */
const root = document.documentElement;
const stage = document.getElementById('stage');
const commandDialog = document.getElementById('command-dialog');
const reviewDialog = document.getElementById('review-dialog');
const receiptDialog = document.getElementById('receipt-dialog');
const motionMedia = window.matchMedia('(prefers-reduced-motion: reduce)');
const finePointer = window.matchMedia('(pointer: fine)');
function routeFromLocation() {
  const [name, id] = location.hash.slice(1).split('/');
  const view = ['models','datasource','look','lab'].includes(name) ? name : 'scene';
  return { view, model: view === 'models' && ['large','small','intel','embedding'].includes(id) ? id : 'large', source: view === 'datasource' && ['tushare','tavily'].includes(id) ? id : 'tushare' };
}
const state = { ...routeFromLocation(), revision: 42, theme: 'dark', density: 'normal', quality: 'full', glass: 72 };
function writeRoute(mode = 'push') {
  const hash = `#${state.view === 'scene' ? 'workspace' : state.view}${state.view === 'models' ? `/${state.model}` : state.view === 'datasource' ? `/${state.source}` : ''}`;
  if (location.hash !== hash) history[mode === 'replace' ? 'replaceState' : 'pushState'](null,'',hash);
}
let prepared = null;
let noticeTimer;
let activeTransition;
let lightFrame = 0;
let lightTarget;
let lightPoint;
const scrollPositions = new Map();
const viewKey = () => `${state.view}:${state.view === 'models' ? state.model : state.view === 'datasource' ? state.source : ''}`;
const modelObjects = {
  large: { title: '大模型', description: '负责综合研判、消息分析与晨报。把高复杂度任务交给这一组模型。', usage: '综合 · 消息 · 外围 · 晨报', fields: [
    { key: 'app.brain.large.model', label: '模型名称', value: 'Qwen3.5-122B-A10B-GPTQ-Int4', help: '示例名称，按实际服务中的模型标识填写。' },
    { key: 'app.brain.large.url', label: '服务地址', type: 'url', value: 'http://127.0.0.1:8000/v1', help: 'OpenAI 兼容地址。样稿不会请求此地址。' },
    { key: 'app.brain.large.key', label: '访问密钥', type: 'password', value: '', help: '演示：已设置。留空保留，输入仅模拟替换。', secretSet: true },
  ] },
  small: { title: '小模型', description: '处理技术面、资金面与异动解读。保留独立的模型与连接参数。', usage: '技术面 · 资金面 · 异动解读', fields: [
    { key: 'app.brain.small.model', label: '模型名称', value: 'Qwen2.5-7B-Instruct-GPTQ-Int8', help: '可与大模型使用同一服务，模型名称独立配置。' },
    { key: 'app.brain.small.url', label: '服务地址', type: 'url', value: 'http://127.0.0.1:8001/v1', help: '演示地址，不代表服务已启动或可以连接。' },
    { key: 'app.brain.small.key', label: '访问密钥', type: 'password', value: '', help: '演示：未设置。输入仅保留在当前页内存中。' },
  ] },
  intel: { title: '情报模型', description: '对重要消息进行模型重评。让规则评分与模型复核各有明确的位置。', usage: '情报重评 · 重要消息复核', fields: [
    { key: 'app.intel.enabled', label: '启用情报重评', type: 'bool', value: 'false', help: '仅修改演示草稿，不会启动任务。' },
    { key: 'app.intel.url', label: '服务地址', type: 'url', value: 'http://127.0.0.1:8000/v1', help: '情报小模型与大模型共用此服务地址。' },
    { key: 'app.intel.small', label: '情报小模型', value: 'Qwen2.5-7B-Instruct-GPTQ-Int8', help: '示例名称，用于初次评分。' },
    { key: 'app.intel.key', label: '访问密钥', type: 'password', value: '', help: '只模拟替换，不读取已保存的密钥明文。' },
  ] },
  embedding: { title: '知识库嵌入', description: '为知识检索提供向量表示。维度与后端合同仍需在真实服务中校验。', usage: '文档向量 · 语义检索', fields: [
    { key: 'app.kb.model', label: '嵌入模型', value: 'BAAI/bge-large-zh-v1.5', help: '示例目录要求维度 1024，样稿不执行维度探测。' },
    { key: 'app.kb.endpoint', label: '服务地址', type: 'url', value: 'http://127.0.0.1:8002/v1', help: '示例地址。嵌入方式和完整字段由真实 schema 决定。' },
    { key: 'app.kb.key', label: '访问密钥', type: 'password', value: '', help: '留空保留，填写仅模拟替换。' },
  ] },
};
const sourceObjects = {
  tushare: { title: 'Tushare', description: '按来源调整凭据与请求节奏。启用参数和接口可达是两类独立证据。', usage: '日线 · 资金 · 龙虎榜', fields: [
    { key: 'app.ds.tushare.on', label: '启用此数据源', type: 'bool', value: 'true', help: '开启只改变配置草稿，不声明接口在线。' },
    { key: 'app.ds.tushare.token', label: '访问令牌', type: 'password', value: '', secretSet: true, help: '演示：已设置。留空保留，输入仅模拟替换。' },
    { key: 'app.ds.tushare.qps', label: '每秒请求数', type: 'number', clearable: true, value: '2', help: '单位：次/秒。留空清除覆盖并跟随配置文件；真实约束以服务端为准。' },
    { key: 'app.ds.tushare.timeout_sec', label: '请求超时', type: 'number', clearable: true, value: '15', help: '单位：秒。留空清除覆盖并跟随配置文件；样稿不执行网络探测。' },
  ] },
  tavily: { title: 'Tavily', description: '设置新闻搜索凭据与查询词。消息来源和查询行为保持可追溯。', usage: '新闻搜索 · 事件线索', fields: [
    { key: 'app.ds.tavily.on', label: '启用此数据源', type: 'bool', value: 'true', help: '仅改变演示草稿，保存前仍需核对。' },
    { key: 'app.ds.tavily.token', label: '访问密钥', type: 'password', value: '', help: '演示：未设置。不要将样稿用于真实凭据管理。' },
    { key: 'app.ds.tavily.feeds', label: '查询词', value: 'A股 快讯,证监会', help: '多个查询词以逗号分隔。' },
    { key: 'app.ds.tavily.timeout_sec', label: '请求超时', type: 'number', clearable: true, value: '20', help: '单位：秒。留空清除覆盖并跟随配置文件。' },
  ] },
};
const catalog = { models: modelObjects, datasource: sourceObjects };
const baseline = { models: {}, datasource: {} };
const drafts = { models: {}, datasource: {} };
for (const [category, objects] of Object.entries(catalog)) {
  for (const object of Object.values(objects)) for (const field of object.fields) baseline[category][field.key] = field.value;
  drafts[category] = { ...baseline[category] };
}
const replacedSecrets = new Set();
const categoryName = { models: '模型服务', datasource: '数据接口' };
const esc = value => String(value).replace(/[&<>"']/g,char => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'})[char]);
const icon = name => `<svg class="at-icon" aria-hidden="true"><use href="#at-icon-${name}"/></svg>`;
const crystal = (name = 'model', domain = 'at-model') => `<div class="at-crystal" style="--nt-at-domain:var(--nt-${domain === 'ai' ? 'at-model' : domain})" aria-hidden="true"><span class="at-crystal-face">${icon(name)}</span></div>`;
const roundArrow = () => `<span class="at-round-arrow">${icon('arrow')}</span>`;
function notify(message) {
  clearTimeout(noticeTimer);
  document.getElementById('announcement').textContent = message;
  noticeTimer = setTimeout(() => { document.getElementById('announcement').textContent = ''; },6500);
}
function changes(category) { return Object.entries(drafts[category] || {}).filter(([key,value]) => value !== baseline[category][key]); }
function totalChanges() { return Object.keys(catalog).reduce((sum,category) => sum + changes(category).length,0); }
function fieldFor(category,key) {
  for (const object of Object.values(catalog[category])) { const found = object.fields.find(field => field.key === key); if (found) return found; }
  return { key, label: key };
}
function renderScene() {
  return `<div class="at-scene-heading"><div><span class="at-eyebrow">你的配置工作台</span><h1>让系统，<em>按你的方式运行。</em></h1><p>模型、数据与工作空间。在需要的地方，展开刚刚好的控制。</p></div><div class="at-scene-caption">轻触对象，进入它的工作区<b>外观即时预览 · 服务参数先审阅</b></div></div>
    <div class="at-scene-grid"><section class="at-object at-object-hero" data-light-object><div class="at-object-kicker"><span>${icon('model')}智能与研判</span><span class="at-tag">共享服务参数</span></div><div class="at-hero-content"><div class="at-hero-copy"><h2>智脑与模型</h2><p>让每一种研判，都有适合它的模型。<br>连接与分工，在同一处从容调整。</p><button class="at-text-action" type="button" data-destination="models">进入模型工作区${roundArrow()}</button></div>${crystal()}</div><div class="at-hero-models"><button class="at-model-shortcut" type="button" data-model="large"><span>${icon('model')}大模型</span><b>${esc(drafts.models['app.brain.large.model'])}</b><small>综合研判 · 晨报</small></button><button class="at-model-shortcut" type="button" data-model="small"><span>${icon('model')}小模型</span><b>${esc(drafts.models['app.brain.small.model'])}</b><small>技术面 · 资金面</small></button></div></section>
    <div class="at-scene-side"><section class="at-object at-object-small" style="--nt-at-domain:var(--nt-accent-2)" data-light-object><div class="at-mini-top"><div class="at-mini-symbol">${icon('appearance')}</div><span>仅此设备 · 即时预览</span></div><h2>你的空间，你的光</h2><p>玻璃的浓度，界面的节奏，都由你决定。</p><div class="at-material-swatches"><button class="at-theme-swatch" type="button" data-theme-choice="dark" aria-pressed="${state.theme === 'dark'}">深海</button><button class="at-theme-swatch" type="button" data-theme-choice="light" aria-pressed="${state.theme === 'light'}">月白</button><button class="at-mini-open" type="button" data-destination="look" aria-label="调整外观与动效">调整${icon('arrow')}</button></div></section>
    <section class="at-object at-object-small" data-light-object><div class="at-mini-top"><div class="at-mini-symbol">${icon('data')}</div><span>来源与采集参数</span></div><h2>让数据，各就其位</h2><p>凭据、请求频率与超时，按来源独立管理。</p><div class="at-data-preview"><span>Tushare</span><span>Tavily</span><button class="at-mini-open" type="button" data-destination="datasource">配置${icon('arrow')}</button></div></section></div></div>
    <section class="at-object gx-entry" data-light-object><span class="gx-entry-icon">${icon('lab')}</span><div><span class="at-eyebrow">GALAXY × NOVATRADER</span><h2>一些有趣的交互，正在这里生长。</h2><p>${window.NovaGalaxyLibraryIndex.total.toLocaleString()} 个Galaxy组件，另有 ${window.NovaGalaxyCatalog.components.length} 个星舰精修交互。</p></div><button class="at-button" type="button" data-destination="lab">进入组件实验室${icon('arrow')}</button></section>
    <p class="at-scene-guidance">${icon('shield')}装饰光效不表达连接状态；配置保存与服务加载分别核验。</p>`;
}
function fieldHtml(category,field) {
  const id = `field-${field.key.replaceAll('.','-')}`;
  const value = drafts[category][field.key];
  const dirty = value !== baseline[category][field.key];
  const help = field.type === 'password' && replacedSecrets.has(field.key) ? '演示：已模拟替换。密钥内容不会持久保存或回显。' : field.help;
  if (field.type === 'bool') return `<div class="at-property-row"><label class="at-toggle" for="${id}"><span>${field.label}</span><input id="${id}" type="checkbox" data-field="${field.key}" ${value === 'true' ? 'checked' : ''} aria-describedby="${id}-help"></label><span class="at-field-help" id="${id}-help">${esc(help)}</span></div>`;
  return `<div class="at-property-row"><div class="at-property-label"><label for="${id}">${field.label}</label><small data-dirty-field="${field.key}" ${dirty ? '' : 'hidden'}>未保存</small></div><input id="${id}" type="${field.type || 'text'}" data-field="${field.key}" value="${esc(value)}" ${field.type === 'number' ? 'step="1"' : ''} ${field.type === 'password' ? 'autocomplete="new-password" placeholder="留空保留，输入模拟替换"' : ''} aria-describedby="${id}-help ${id}-error"><span class="at-field-help" id="${id}-help">${esc(help)}</span><span class="at-field-error" id="${id}-error" role="alert" hidden></span></div>`;
}
function renderFocus(category) {
  const isModel = category === 'models';
  const selected = isModel ? state.model : state.source;
  const object = catalog[category][selected];
  return `<div class="at-focus-heading"><button class="at-back-action" type="button" data-destination="scene">${icon('back')}回到工作台</button><div class="at-focus-title"><span class="at-tag">共享参数 · 演示</span><h1>${categoryName[category]}</h1></div></div><section class="at-object at-focus-shell" data-family="${category}"><div class="at-focus-tabs" role="group" aria-label="${isModel ? '选择模型对象' : '选择数据源'}">${Object.entries(catalog[category]).map(([key,item]) => `<button class="at-focus-tab" type="button" data-${isModel ? 'model' : 'source'}="${key}" aria-pressed="${selected === key}">${icon(isModel ? 'model' : 'data')}${item.title}</button>`).join('')}</div><div class="at-focus-content"><div class="at-object-identity">${crystal(isModel ? 'model' : 'data',isModel ? 'ai' : 'accent')}<h2>${object.title}</h2><p>${object.description}</p><span class="at-tag">${object.usage}</span><div class="at-identity-caption"><strong>先看用途，再调整参数</strong><span>当前值为演示；真实字段以服务端目录为准。</span></div></div><form class="at-property-form" id="property-form" novalidate>${object.fields.map(field => fieldHtml(category,field)).join('')}</form></div><div class="at-focus-actions"><button class="at-button at-button-light" id="discard-category" type="button" data-action="discard">放弃本类改动</button><button class="at-button" type="button" data-action="receipts">查看加载回执</button></div><p class="at-focus-footnote">${icon('shield')}每次编辑只进入草稿。确认新版本后，仍需要目标服务与实例的加载证据。</p></section>`;
}
function options(key,entries) {
  return `<div class="at-option-group" role="group" aria-label="${{quality:'材质与动效',density:'空间密度',theme:'主题'}[key]}">${entries.map(([value,label]) => `<button type="button" data-preference="${key}" data-value="${value}" aria-pressed="${state[key] === value}">${label}</button>`).join('')}</div>`;
}
function renderAppearance() {
  return `<div class="at-focus-heading"><button class="at-back-action" type="button" data-destination="scene">${icon('back')}回到工作台</button><div class="at-focus-title"><span class="at-tag">仅此设备</span><h1>外观与动效</h1></div></div><section class="at-object at-focus-shell"><div class="at-appearance-layout"><div class="at-appearance-controls"><div class="at-preference"><h2>光线与主题</h2><p>深海的静谧，或月白的清晰。</p>${options('theme',[['dark','深海'],['light','月白']])}</div><div class="at-preference"><h2>玻璃浓度</h2><p>只改变悬浮控件的材质，正文背板保持清晰。这是 V2 的新增偏好提案。</p><label class="at-intensity"><input id="glass-intensity" type="range" min="54" max="90" value="${state.glass}" aria-label="玻璃浓度"><output for="glass-intensity">${state.glass}%</output></label></div><div class="at-preference"><h2>运动的节奏</h2><p>动效解释对象的去向；你也可以让一切安静下来。</p>${options('quality',[['full','完整'],['eco','节能'],['reduced','减少动态']])}</div><div class="at-preference"><h2>空间密度</h2><p>调整控件和留白，不压缩必要的说明。</p>${options('density',[['normal','标准'],['compact','紧凑']])}</div></div><div class="at-appearance-preview"><div class="at-glass at-preview-lens">${crystal('appearance','accent-2')}<h2>光有层次，信息有秩序</h2><p>薄边反光、分层图标、稳定的阅读表面。</p></div><p class="at-preview-caption" id="motion-description">${motionMedia.matches ? '已按系统偏好停止动画' : '此处只预览材质，不表示任何业务状态'}</p></div></div></section>`;
}
function syncDock() {
  const selected = document.querySelector(`.at-dock-item[data-destination="${state.view}"]`);
  const dock = document.querySelector('.at-dock');
  document.querySelectorAll('.at-dock-item[data-destination]').forEach(item => {
    if (item === selected) item.setAttribute('aria-current','page'); else item.removeAttribute('aria-current');
  });
  if (selected) { dock.style.setProperty('--at-dock-x',`${selected.offsetLeft}px`); dock.style.setProperty('--at-dock-w',`${selected.offsetWidth}px`); }
}
function updateChanges() {
  const count = totalChanges();
  const rail = document.getElementById('commit-rail');
  rail.dataset.dirty = String(count > 0);
  document.getElementById('commit-status').textContent = count ? `${count} 项演示改动，等待你确认 · 基于 #${state.revision}` : `演示保存版本 #${state.revision} · 没有待确认改动`;
  document.getElementById('review-trigger').disabled = count === 0;
  document.getElementById('review-trigger').setAttribute('aria-label',count ? `审阅 ${count} 项演示变更` : '审阅变更，当前无改动');
  for (const category of Object.keys(catalog)) {
    const nav = document.querySelector(`.at-dock-item[data-destination="${category}"]`);
    nav.querySelector('.at-unsaved').hidden = changes(category).length === 0;
    nav.setAttribute('aria-description',changes(category).length ? '有未保存演示草稿' : '');
  }
  if (catalog[state.view]) {
    stage.querySelectorAll('[data-dirty-field]').forEach(element => { element.hidden = drafts[state.view][element.dataset.dirtyField] === baseline[state.view][element.dataset.dirtyField]; });
    const discard = document.getElementById('discard-category');
    if (discard) discard.disabled = changes(state.view).length === 0;
  }
}
function applyAppearance() {
  root.dataset.theme = state.theme;
  root.dataset.density = state.density;
  root.dataset.visualQuality = state.quality;
  root.dataset.motion = motionMedia.matches || state.quality !== 'full' ? 'reduced' : 'full';
  root.style.setProperty('--nt-at-glass-alpha',`${state.glass}%`);
  document.getElementById('theme-toggle').setAttribute('aria-label',state.theme === 'dark' ? '切换亮色主题' : '切换深色主题');
  document.getElementById('motion-toggle').setAttribute('aria-pressed',String(state.quality !== 'full' || motionMedia.matches));
  document.getElementById('motion-toggle').setAttribute('aria-label',state.quality === 'full' ? '减少动态' : '恢复完整动效');
  document.getElementById('preference-description').textContent = `${state.theme === 'dark' ? '深海' : '月白'} · ${state.density === 'normal' ? '标准' : '紧凑'} · ${{full:'完整材质',eco:'节能',reduced:'减少动态'}[state.quality]}${motionMedia.matches ? ' · 系统减少动态' : ''}`;
  const description = document.getElementById('motion-description');
  if (description) description.textContent = motionMedia.matches ? '已按系统偏好停止动画' : state.quality === 'full' ? '此处只预览材质，不表示任何业务状态' : '装饰运动已停止，界面保留清晰的层级';
}
function render() {
  prepared = null;
  root.dataset.workspace = state.view;
  const previous = document.activeElement;
  const preference = previous?.dataset?.preference;
  const preferenceValue = previous?.dataset?.value;
  const themeChoice = previous?.dataset?.themeChoice;
  window.NovaGalaxyLab?.unmount();
  stage.innerHTML = state.view === 'scene' ? renderScene() : state.view === 'look' ? renderAppearance() : state.view === 'lab' ? window.NovaGalaxyLab.render() : renderFocus(state.view);
  if (state.view === 'lab') window.NovaGalaxyLab.mount(stage);
  applyAppearance(); updateChanges(); syncDock();
  if (preference) stage.querySelector(`[data-preference="${preference}"][data-value="${preferenceValue}"]`)?.focus({preventScroll:true});
  else if (themeChoice) stage.querySelector(`[data-theme-choice="${themeChoice}"]`)?.focus({preventScroll:true});
}
function navigate(view,{ model,source, focus = true, historyMode = 'push' } = {}) {
  if (!['scene','models','datasource','look','lab'].includes(view)) return;
  if (view === state.view && (!model || model === state.model) && (!source || source === state.source)) return;
  prepared = null;
  document.querySelectorAll('dialog[open]').forEach(dialog => dialog.close());
  scrollPositions.set(viewKey(),stage.scrollTop);
  const family = view === 'scene' ? state.view : view;
  const sourceElement = family === 'models' ? stage.querySelector('.at-object-hero,.at-focus-shell[data-family="models"]') : family === 'datasource' ? stage.querySelector('.at-object-small[data-light-object]:last-child,.at-focus-shell[data-family="datasource"]') : null;
  const previousBounds = sourceElement?.getBoundingClientRect();
  if (activeTransition) { activeTransition.cancel(); activeTransition = null; }
  document.querySelector('.at-transition-surface')?.remove();
  const update = () => {
    state.view = view; if (model) state.model = model; if (source) state.source = source;
    if (historyMode) writeRoute(historyMode);
    render(); stage.scrollTop=scrollPositions.get(viewKey()) || 0;
    if (focus && view !== 'scene') {
      const target = model ? stage.querySelector(`[data-model="${model}"]`) : source ? stage.querySelector(`[data-source="${source}"]`) : stage.querySelector('.at-back-action');
      target?.focus({preventScroll:true});
    }
  };
  update();
  const target = family === 'models' ? stage.querySelector('.at-object-hero,.at-focus-shell[data-family="models"]') : family === 'datasource' ? stage.querySelector('.at-object-small[data-light-object]:last-child,.at-focus-shell[data-family="datasource"]') : null;
  if (previousBounds && target && root.dataset.motion === 'full' && !document.hidden && typeof target.animate === 'function') {
    const bounds=target.getBoundingClientRect();
    const appBounds=stage.getBoundingClientRect();
    const outline=document.createElement('div');
    outline.className='at-transition-surface'; outline.setAttribute('aria-hidden','true');
    Object.assign(outline.style,{left:`${bounds.left-appBounds.left+stage.scrollLeft}px`,top:`${bounds.top-appBounds.top+stage.scrollTop}px`,width:`${bounds.width}px`,height:`${bounds.height}px`});
    stage.append(outline);
    const animation=outline.animate([{transform:`translate(${previousBounds.left-bounds.left}px,${previousBounds.top-bounds.top}px) scale(${previousBounds.width/bounds.width},${previousBounds.height/bounds.height})`,opacity:.65},{transform:'none',opacity:0}],{duration:340,easing:'cubic-bezier(.22,.8,.22,1)'});
    activeTransition=animation;
    animation.finished.catch(()=>{}).finally(()=>{outline.remove();if(activeTransition===animation)activeTransition=null;});
  }
  if (focus) document.getElementById('announcement').textContent = `已打开${{scene:'配置工作台',models:'模型服务',datasource:'数据接口',look:'外观与动效',lab:'组件实验室'}[view]}，未保存草稿保留。`;
}
function validateCategory(category) {
  for (const [objectId,object] of Object.entries(catalog[category])) {
    for (const field of object.fields) {
      if (field.type === 'password' || field.type === 'bool') continue;
      const value = drafts[category][field.key].trim();
      if (field.clearable && !value) continue;
      let message = '';
      if (field.type === 'url') { try { const url = new URL(value); if (!['http:','https:'].includes(url.protocol) || !url.hostname) throw new Error('unsupported protocol'); } catch { message = '请填写完整的 HTTP 或 HTTPS 服务地址，例如 http://127.0.0.1:8000/v1。'; } }
      else if (field.type === 'number' && (!/^\d+$/.test(value) || !Number.isSafeInteger(Number(value)) || Number(value) <= 0)) message = '请填写大于0的安全整数。';
      else if (!value) message = '请填写内容。';
      if (!message) continue;
      if (reviewDialog.open) reviewDialog.close();
      state.view=category;
      if(category==='models') state.model=objectId; else state.source=objectId;
      writeRoute('replace');
      render();
      const input=stage.querySelector(`[data-field="${field.key}"]`);
      const error=document.getElementById(`${input.id}-error`);
      input.setAttribute('aria-invalid','true'); error.textContent=message; error.hidden=false; input.focus();
      return false;
    }
  }
  return true;
}
function displayDiff(field,value,before) {
  if (field.type === 'password') return before ? '已保存状态，不回显' : '替换密钥，内容隐藏';
  if (field.type === 'bool') return value === 'true' ? '启用' : '关闭';
  if (field.clearable && !value.trim()) return '清除覆盖，跟随配置文件';
  return value || '留空';
}
function populateReview(category) {
  const entries = changes(category);
  if (!entries.length) return;
  if (!validateCategory(category)) return;
  prepared = { category, revision: state.revision, changes: entries.map(([key,after]) => ({ key,before: baseline[category][key],after })) };
  document.getElementById('review-title').textContent = `核对${categoryName[category]}变更`;
  document.getElementById('review-content').innerHTML = `<p class="at-review-note">基于演示版本 #${state.revision} · 当前分类 ${entries.length} 项。其它分类草稿保留，分别确认。</p><dl class="at-diff">${prepared.changes.map(change => { const field = fieldFor(category,change.key); return `<div><dt>${esc(field.label)}</dt><dd><span>${esc(displayDiff(field,change.before,true))}</span><span aria-hidden="true">→</span><span>${esc(displayDiff(field,change.after,false))}</span></dd></div>`; }).join('')}</dl><p class="at-review-note">本页没有请求真实影响预览接口；不声明任何服务、计划或外部接口已经验证。保存仅演示生成新版本。</p>`;
  document.getElementById('review-actions').innerHTML = `<button class="at-button" type="button" data-close-dialog>继续编辑</button><button class="at-button at-button-primary" type="button" data-action="confirm-save">演示保存新版本${icon('arrow')}</button>`;
  return true;
}
function openReview() {
  if (!totalChanges()) return;
  if (catalog[state.view] && !validateCategory(state.view)) return;
  const dirtyCategories = Object.keys(catalog).filter(category => changes(category).length > 0);
  if (dirtyCategories.length === 1 || (catalog[state.view] && changes(state.view).length)) {
    if (!populateReview(catalog[state.view] && changes(state.view).length ? state.view : dirtyCategories[0])) return;
  }
  else {
    prepared = null;
    document.getElementById('review-title').textContent = '选择要确认的配置分类';
    document.getElementById('review-content').innerHTML = `<p class="at-review-note">共享配置按分类分别预览、确认。不会将多类改动伪装成一次原子保存。</p>${dirtyCategories.map(category => `<button class="at-category-choice" type="button" data-review-category="${category}"><span>${categoryName[category]}</span><span>${changes(category).length} 项 ${icon('arrow')}</span></button>`).join('')}`;
    document.getElementById('review-actions').innerHTML = `<button class="at-button" type="button" data-close-dialog>继续编辑</button>`;
  }
  reviewDialog.showModal();
}
function openReceipts() {
  document.getElementById('receipt-content').innerHTML = `<span class="at-tag">演示保存版本 #${state.revision}</span><h3>加载证据待核验</h3><p class="at-receipt-note">当前样稿不连接后端，没有真实服务回执，因此不显示在线状态、运行版本或接口延迟。</p><p class="at-receipt-note">真实产品按服务、实例、范围、版本和观测时间逐项展示。“配置缓存已加载”不等于外部接口连通；“已保存”也不能代替运行证据。</p><span class="at-tag at-tag-warn">尚未读取真实加载回执</span>`;
  receiptDialog.showModal();
}
const commands = [
  { title:'模型服务', hint:'调整大模型、小模型、情报与嵌入', words:'模型 model llm 大模型 小模型 智脑', icon:'model', run: () => navigate('models') },
  { title:'数据接口', hint:'按来源调整凭据和采集参数', words:'数据 source tushare tavily 来源 接口', icon:'data', run: () => navigate('datasource') },
  { title:'外观与动效', hint:'主题、玻璃浓度与空间密度', words:'外观 主题 毛玻璃 透明 appearance', icon:'appearance', run: () => navigate('look') },
  { title:'组件实验室', hint:`${window.NovaGalaxyLibraryIndex.total.toLocaleString()} 个全库组件＋${window.NovaGalaxyCatalog.components.length} 个精修交互`, words:'组件 实验室 galaxy lab 按钮 开关 图标 下拉 菜单', icon:'lab', run: () => navigate('lab') },
  { title:'减少动态', hint:'停止装饰运动，保留信息层级', words:'减少动态 少一点动画 降低动效 安静 停止动画', icon:'motion', run: () => { state.quality='reduced'; render(); notify('已停止装饰运动，必要状态即时显示。'); } },
  { title:'恢复完整动效', hint:'恢复局部材质响应与连续转场', words:'完整 运动 动画 恢复 full', icon:'motion', run: () => { state.quality='full'; render(); notify(motionMedia.matches ? '系统仍要求减少动态，运动保持关闭。' : '已恢复完整材质与局部动效。'); } },
  { title:'切换主题', hint:'深海 / 月白', words:'亮色 深色 白色 黑色 theme 主题', icon:'sun', run: () => { state.theme=state.theme==='dark'?'light':'dark'; render(); } },
  { title:'审阅变更', hint:'按配置分类核对本页演示草稿', words:'变更 保存 草稿 preview', icon:'check', run: () => totalChanges() ? openReview() : notify('当前没有待确认的演示改动。') },
  { title:'查看加载回执', hint:'区分保存版本与服务运行证据', words:'版本 回执 加载 历史', icon:'history', run: openReceipts },
];
let matchedCommands = commands;
function updateCommandResults(query = '') {
  const words = query.toLocaleLowerCase().trim().split(/\s+/).filter(Boolean);
  matchedCommands = commands.filter(command => words.every(word => `${command.title} ${command.words}`.toLocaleLowerCase().includes(word)));
  document.getElementById('command-results').innerHTML = matchedCommands.length ? matchedCommands.map((command,index) => `<button class="at-command-result" type="button" data-command="${index}">${icon(command.icon)}<span>${command.title}<small>${command.hint}</small></span></button>`).join('') : '<p class="at-review-note">没有匹配的设置或操作。可以试试“模型”或“减少动态”。</p>';
}
function openCommands() {
  if (document.querySelector('dialog[open]')) return;
  window.NovaGalaxyControls?.closeAll();
  document.getElementById('command-input').value = '';
  updateCommandResults(); commandDialog.showModal(); document.getElementById('command-input').focus();
}
document.addEventListener('click',event => {
  const button = event.target.closest('button,a[data-destination]');
  if (!button || button.disabled) return;
  if (button.dataset.destination) { event.preventDefault(); navigate(button.dataset.destination); }
  if (button.dataset.model) navigate('models',{model:button.dataset.model});
  if (button.dataset.source) navigate('datasource',{source:button.dataset.source});
  if (button.dataset.themeChoice) { state.theme=button.dataset.themeChoice; render(); }
  if (button.dataset.preference) { state[button.dataset.preference]=button.dataset.value; render(); }
  if (button.dataset.action === 'receipts') openReceipts();
  if (button.dataset.action === 'discard' && catalog[state.view]) { drafts[state.view] = {...baseline[state.view]}; render(); notify('已放弃当前分类改动，其它分类草稿保留。'); }
  if (button.dataset.reviewCategory) populateReview(button.dataset.reviewCategory);
  if (button.dataset.action === 'confirm-save' && prepared && prepared.revision === state.revision) {
    const category = prepared.category;
    for (const change of prepared.changes) {
      if (fieldFor(category,change.key).type === 'password') { replacedSecrets.add(change.key); baseline[category][change.key]=''; drafts[category][change.key]=''; }
      else baseline[category][change.key]=change.after;
    }
    state.revision++; reviewDialog.close(); render(); notify(`演示：已生成保存版本 #${state.revision}，服务加载仍待核验。`);
  }
  if (button.dataset.command != null) { const command=matchedCommands[Number(button.dataset.command)]; commandDialog.close(); command?.run(); }
  if (button.hasAttribute('data-close-dialog')) button.closest('dialog').close();
});
stage.addEventListener('input',event => {
  const input=event.target;
  if (input.dataset.field && catalog[state.view]) {
    drafts[state.view][input.dataset.field]=input.type==='checkbox'?String(input.checked):input.value;
    prepared=null; input.removeAttribute('aria-invalid');
    const error=document.getElementById(`${input.id}-error`); if(error) error.hidden=true;
    updateChanges();
  }
  if (input.id==='glass-intensity') { state.glass=Number(input.value); input.nextElementSibling.textContent=`${state.glass}%`; applyAppearance(); }
});
stage.addEventListener('submit',event => event.preventDefault());
document.getElementById('review-trigger').addEventListener('click',openReview);
document.getElementById('command-trigger').addEventListener('click',openCommands);
document.getElementById('theme-toggle').addEventListener('click',() => { state.theme=state.theme==='dark'?'light':'dark'; render(); });
document.getElementById('motion-toggle').addEventListener('click',() => { state.quality=state.quality==='full'?'reduced':'full'; render(); notify(root.dataset.motion==='reduced'?'装饰运动已停止。':'完整材质与局部动效已恢复。'); });
document.getElementById('command-input').addEventListener('input',event => { if (!event.isComposing) updateCommandResults(event.target.value); });
document.getElementById('command-input').addEventListener('compositionend',event => updateCommandResults(event.target.value));
document.getElementById('command-input').addEventListener('keydown',event => {
  if (event.isComposing || event.keyCode === 229) return;
  if(event.key==='Enter' && matchedCommands[0]) { event.preventDefault(); commandDialog.close(); matchedCommands[0].run(); }
  if(event.key==='ArrowDown') { event.preventDefault(); document.querySelector('.at-command-result')?.focus(); }
});
document.addEventListener('keydown',event => { if((event.ctrlKey||event.metaKey)&&event.key.toLowerCase()==='k') { event.preventDefault(); if(commandDialog.open) commandDialog.close(); else openCommands(); } });
stage.addEventListener('pointermove',event => {
  if(root.dataset.motion!=='full'||root.dataset.visible==='false'||!finePointer.matches||document.hidden) return;
  const object=event.target.closest('[data-light-object]'); if(!object) return;
  lightTarget=object; lightPoint={x:event.clientX,y:event.clientY};
  if(lightFrame) return;
  lightFrame=requestAnimationFrame(() => { lightFrame=0; if(root.dataset.motion!=='full'||document.hidden||!lightTarget?.isConnected) return; const bounds=lightTarget.getBoundingClientRect(); lightTarget.style.setProperty('--at-x',`${lightPoint.x-bounds.left}px`); lightTarget.style.setProperty('--at-y',`${lightPoint.y-bounds.top}px`); });
});
motionMedia.addEventListener('change',applyAppearance);
document.addEventListener('visibilitychange',() => { root.dataset.visible=String(!document.hidden); if(document.hidden&&lightFrame) { cancelAnimationFrame(lightFrame); lightFrame=0; } });
window.addEventListener('blur',() => { root.dataset.visible='false'; if(lightFrame) { cancelAnimationFrame(lightFrame); lightFrame=0; } });
window.addEventListener('focus',() => { root.dataset.visible=String(!document.hidden); });
new ResizeObserver(syncDock).observe(document.querySelector('.at-dock'));
const restoreRoute = () => {
  const route = routeFromLocation();
  if (route.view === state.view && (route.view !== 'models' || route.model === state.model) && (route.view !== 'datasource' || route.source === state.source)) return;
  navigate(route.view,{model:route.view === 'models' ? route.model : undefined,source:route.view === 'datasource' ? route.source : undefined,historyMode:false});
};
window.addEventListener('popstate',restoreRoute);
window.addEventListener('hashchange',restoreRoute);
render();
