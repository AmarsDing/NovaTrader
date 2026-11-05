/* Local interaction atlas. Galaxy sources/license: Galaxy来源与改编.md / GALAXY-LICENSE.txt. */
(() => {
  'use strict';
  const catalog = window.NovaGalaxyCatalog;
  let collection = 'library';
  const groups = { all: '全部组件', action: '动作与入口', input: '输入与选择', object: '对象与空间', feedback: '等待与反馈', selection: '选择与菜单', special: '特殊交互' };
  const defaults = () => ({ group: 'all', query: '', focused: '', presses: 0, launch: 'idle', glow: true, checks: [true, false], segment: 0, name: '', nameValidity: null, nameMessage: '', search: '', power: 65, pinned: false, flipped: false, pattern: 'grid', orbit: 'idle', skeleton: 'ready', favorite: false, toast: false, menu: false, menuPinned: false, menuFocused: false, menuMessage: '围绕模型对象展开动作。' });
  let sample = defaults();
  let host;
  let controller;
  let observer;
  let toolbarObserver;
  let specDialog;
  let specTrigger;
  let generation = 0;
  let pinnedTip = false;
  let composingFilter = false;
  let composingSearch = false;
  const timers = new Map();
  const esc = value => String(value).replace(/[&<>"']/g, char => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[char]);
  const icon = name => `<svg class="at-icon" aria-hidden="true"><use href="#at-icon-${name}"/></svg>`;
  const svgCheck = '<svg viewBox="0 0 36 36" aria-hidden="true"><circle cx="18" cy="18" r="15"/><path d="m11 18 5 5 10-11"/></svg>';
  const sourceUrl = item => `${catalog.repository}/blob/${catalog.revision}/${item.source.split('/').map(encodeURIComponent).join('/')}`;
  const output = (id, text) => `<p class="gx-status" id="gx-${id}-status" role="status" aria-live="polite">${text}</p>`;
  const button = (action, text, classes = '', attributes = '') => `<button class="gx-control ${classes}" type="button" data-gx-action="${action}" ${attributes}>${text}</button>`;
  const hints = { shimmer: '悬停掠光 · 点击打开预览', launch: '点击跃迁 · 对象落入舱位', switch: '切换开关 · 观察环境光', check: '勾选选项 · 观察对象变化', segment: '切换视角 · 内容同步更换', focus: '试试空值、长名称和正常名称', search: '检索后可直接进入对应工作区', range: '拖动滑轨 · 观察光环强弱', holo: '移动指针 · 固定这个对象', flip: '显式翻面 · 查看两面档案', pattern: '切换网格与点阵坐标', orbit: '体验完成、取消与失败三种终态', skeleton: '重播加载 · 取消或模拟失败', tooltip: '悬停/聚焦查看 · 点击固定', toast: '收藏后可撤销，也可再次取消', satellite: '固定、聚焦和还原都会改变对象' };
  const perspectives = [
    { icon: 'model', title: '对象概览', rows: ['研判模型', '仅此设备 · 演示对象'] },
    { icon: 'appearance', title: '局部参数', rows: ['材质：星舰玻璃', '范围：当前样本'] },
    { icon: 'shield', title: '设计证据', rows: ['版本：V2 / Galaxy', '来源：固定版本归档'] },
  ];
  const perspectiveHtml = () => { const item = perspectives[sample.segment]; return `${icon(item.icon)}<span><strong>${item.title}</strong>${item.rows.map(row => `<small>${row}</small>`).join('')}</span>`; };
  Object.assign(hints,{native:'布局预设 · 缩略图与说明 · 改变样本排列',combobox:'可搜索单选 · 支持键盘与禁用项',multiselect:'多选标签 · 分组、上限与移除',splitmenu:'主动作与菜单分开 · 选择后可重播',stepper:'按钮步进或精确输入 · 保留边界',interval:'双端区间 · 键盘与数字均可调整',hold:'按住蓄能 · 松开取消 · 普通按钮同等可达',wizard:'步骤草稿保留 · 前进、返回与校验'});
  const demos = {
    shimmer: () => `<div class="gx-demo-column"><div class="gx-preview-ticket" data-open="${sample.presses > 0}">${icon('workspace')}<span><strong>${sample.presses ? '工作区预览已展开' : '你的下一个工作区'}</strong><small>冰青边缘 · 稳定的阅读面</small></span></div><div class="gx-inline-actions"><button class="gx-shimmer" type="button" data-gx-action="press">${icon('arrow')}<span>打开样本预览</span></button><button type="button" class="gx-control" disabled>禁用态</button></div>${output('press', `已试按 ${sample.presses} 次 · 仅此样本`)}</div>`,
    launch: () => `<div class="gx-demo-column"><div class="gx-launch-target" data-state="${sample.launch}" aria-hidden="true"><i></i><span>${icon('model')}</span><i></i></div><button class="gx-launch" type="button" data-gx-action="launch" aria-disabled="${sample.launch === 'running'}" aria-busy="${sample.launch === 'running'}"><span>${icon('arrow')}</span><b>${sample.launch === 'running' ? '跃迁演示中' : '展开模型对象'}</b></button>${output('launch', sample.launch === 'done' ? '演示完成 · 对象已落入舱位' : '在当前样本内展开，不运行分析任务。')}</div>`,
    switch: () => `<div class="gx-switch-scene" data-on="${sample.glow}"><div class="gx-ambient-scene" aria-hidden="true"><i class="gx-ambient-halo"></i><span>${icon('lab')}</span><i class="gx-ambient-floor"></i></div><label class="gx-switch"><input id="gx-switch-input" type="checkbox" ${sample.glow ? 'checked' : ''}><span class="gx-switch-track" aria-hidden="true"><i></i></span><span>样本环境光</span></label>${output('switch', sample.glow ? '已开启 · 仅此样本' : '已关闭 · 仅此样本')}</div>`,
    check: () => `<div class="gx-check-group"><div class="gx-check-preview" data-edge="${sample.checks[0]}" data-label="${sample.checks[1]}">${icon('model')}<span>研判模型 <small>LOCAL OBJECT</small></span></div>${['保留边缘反光', '显示对象标签'].map((label, i) => `<label class="gx-check"><input type="checkbox" data-gx-check="${i}" ${sample.checks[i] ? 'checked' : ''}><span class="gx-check-mark">${svgCheck}</span><span>${label}</span></label>`).join('')}${output('check', `已选择 ${sample.checks.filter(Boolean).length} 项`)}</div>`,
    segment: () => `<div class="gx-demo-column"><fieldset class="gx-segments"><legend class="gx-sr-only">样本视角</legend>${['概览', '参数', '证据'].map((label, i) => `<label><input type="radio" name="gx-perspective" value="${i}" ${sample.segment === i ? 'checked' : ''}><span>${label}</span></label>`).join('')}<i class="gx-segment-sled" aria-hidden="true" style="--gx-index:${sample.segment}"></i></fieldset><div class="gx-perspective" id="gx-perspective-panel" aria-label="样本视角内容">${perspectiveHtml()}</div>${output('segment', ['概览：先看到对象的用途。', '参数：再调整需要的属性。', '证据：最后核对变更依据。'][sample.segment])}</div>`,
    focus: () => `<form class="gx-name-form" data-gx-form novalidate><label for="gx-name">演示名称</label><div class="gx-focus-input"><input id="gx-name" type="text" value="${esc(sample.name)}" placeholder="例如：我的研究空间" ${sample.nameValidity == null ? '' : `aria-invalid="${!sample.nameValidity}"`} aria-describedby="gx-name-help gx-name-error"><i aria-hidden="true"></i></div><span class="gx-help" id="gx-name-help">非空且最多24字符，仅用于体验校验。</span><span class="gx-error" id="gx-name-error" role="status" aria-live="polite" data-valid="${sample.nameValidity === true}">${esc(sample.nameMessage)}</span>${button('validate-name', '校验名称')}</form>`,
    search: () => `<div class="gx-search-demo"><label class="gx-search-lens">${icon('search')}<span class="gx-sr-only">检索样本入口</span><input id="gx-search" type="search" value="${esc(sample.search)}" placeholder="试试：模型" autocomplete="off">${button('clear-search', icon('close'), 'gx-clear', 'aria-label="清空样本检索"')}</label><ul class="gx-search-results" id="gx-search-results" aria-label="本地检索结果"></ul><p class="gx-help">三个本地入口 · 不请求服务</p></div>`,
    range: () => `<div class="gx-range-demo"><div class="gx-energy" style="--gx-power:${sample.power / 100}" aria-hidden="true"><i></i><span>${icon('appearance')}</span></div><div class="gx-range-caption"><label for="gx-power">局部光强</label><output id="gx-power-value" for="gx-power">${sample.power}%</output></div><input class="gx-range" id="gx-power" type="range" min="20" max="100" step="5" value="${sample.power}" style="--gx-fill:${(sample.power - 20) / 80 * 100}%"><p class="gx-help">只调样本图形，不改变全局玻璃。</p></div>`,
    holo: () => `<div class="gx-holo" data-light-object data-pinned="${sample.pinned}"><div class="gx-holo-art" aria-hidden="true"><i></i><i></i><span>${icon('model')}</span></div><div class="gx-holo-copy"><span class="gx-overline">MODEL / OBJECT</span><strong>研判模型</strong><small>一层光，一层秩序。</small></div>${button('pin-object', `${icon('check')}<span>${sample.pinned ? '已固定' : '固定对象'}</span>`, '', `aria-pressed="${sample.pinned}"`)}</div>`,
    flip: () => `<div class="gx-flip" data-flipped="${sample.flipped}"><div class="gx-flip-art" aria-hidden="true"><div class="gx-flip-sheet"><span class="gx-flip-front">${icon('lab')}<b>NOVA / OBJECT</b><i></i></span><span class="gx-flip-back">${icon('shield')}<b>SCOPE / DEVICE</b><i></i><i></i></span></div></div><div class="gx-flip-copy" id="gx-flip-copy"><span ${sample.flipped ? 'hidden' : ''}><strong>正面 · 对象概览</strong><small>研判模型 / 本机设计样本</small></span><span ${sample.flipped ? '' : 'hidden'}><strong>背面 · 设计档案</strong><small>局部作用域 / 可取消 / 无配置写入</small></span></div>${button('flip', sample.flipped ? '返回正面' : '查看背面', '', `aria-expanded="${sample.flipped}" aria-controls="gx-flip-copy"`)}</div>`,
    pattern: () => `<div class="gx-pattern-scene" data-pattern="${sample.pattern}"><div class="gx-pattern-texture" aria-hidden="true"></div><div class="gx-pattern-label">${icon('workspace')}<span>空间有坐标，信息有落点。</span></div></div><div class="gx-small-options" role="group" aria-label="样本背景纹理">${button('pattern-grid', '网格', '', `aria-pressed="${sample.pattern === 'grid'}"`)}${button('pattern-dots', '点阵', '', `aria-pressed="${sample.pattern === 'dots'}"`)}</div>`,
    orbit: () => `<div class="gx-demo-column"><div class="gx-orbit" data-running="${sample.orbit === 'running'}" data-outcome="${sample.orbit}" aria-hidden="true"><i></i><span>${icon(sample.orbit === 'done' ? 'check' : sample.orbit === 'error' ? 'close' : 'lab')}</span></div>${output('orbit', ({ idle: '尚未开始 · 本地计时演示', running: '演示中 · 未知进度，不显示百分比', done: '演示结束 · 未连接任何真实任务', cancelled: '已取消本次演示', error: '模拟失败 · 可以重新开始' })[sample.orbit])}<div class="gx-inline-actions">${button('start-orbit', sample.orbit === 'error' ? '重试演示' : '开始演示', '', `aria-disabled="${sample.orbit === 'running'}"`)}${button('cancel-orbit', '取消', '', sample.orbit === 'running' ? '' : 'disabled')}${button('fail-orbit', '模拟失败', 'gx-link-control', sample.orbit === 'running' ? '' : 'disabled')}</div></div>`,
    skeleton: () => `<div class="gx-demo-column"><div class="gx-skeleton-slot" id="gx-skeleton-slot" aria-busy="${sample.skeleton === 'loading'}"><div class="gx-skeleton-lines" ${sample.skeleton === 'loading' ? '' : 'hidden'} aria-hidden="true"><i></i><span></span><span></span><span></span></div><div class="gx-loaded-content" ${sample.skeleton === 'loading' ? 'hidden' : ''}>${icon('data')}<span><strong>内容落在原来的位置。</strong><small>这是演示内容，无真实行情数值。</small></span></div></div>${output('skeleton', sample.skeleton === 'loading' ? '本地加载演示中' : sample.skeleton === 'cancelled' ? '演示已取消 · 保留原内容' : sample.skeleton === 'error' ? '模拟失败 · 保留原内容，可重试' : '已呈现演示内容')}<div class="gx-inline-actions">${button('start-skeleton', sample.skeleton === 'error' ? '重试加载' : '重播加载', '', `aria-disabled="${sample.skeleton === 'loading'}"`)}${button('cancel-skeleton', '取消', '', sample.skeleton === 'loading' ? '' : 'disabled')}${button('fail-skeleton', '模拟失败', 'gx-link-control', sample.skeleton === 'loading' ? '' : 'disabled')}</div></div>`,
    tooltip: () => `<div class="gx-tooltip-demo"><div class="gx-tip-wrap"><span class="gx-tooltip" id="gx-tooltip" role="tooltip" data-show="false">玻璃负责层次，<br>实底负责清楚。</span><button class="gx-control" id="gx-tip-trigger" type="button" aria-describedby="gx-tooltip" aria-pressed="false">${icon('appearance')}材质是什么？</button></div><p class="gx-help">悬停或聚焦查看，点击固定，Esc关闭。</p></div>`,
    toast: () => `<div class="gx-toast-demo"><div class="gx-favorite-target" data-favorite="${sample.favorite}">${icon('lab')}<span>星舰交互档案</span><i aria-hidden="true">${sample.favorite ? '★' : '☆'}</i></div>${button('favorite', `${icon('check')}<span>${sample.favorite ? '已收藏这个样本' : '收藏这个样本'}</span>`, '', `aria-pressed="${sample.favorite}"`)}<div class="gx-toast-slot"><div class="gx-toast" ${sample.toast ? '' : 'hidden'}><span class="gx-toast-message" role="status" aria-live="polite">${icon('check')}已收藏 · 仅此样本</span><div>${button('undo-favorite', '撤销')}${button('dismiss-toast', icon('close'), 'gx-clear', 'aria-label="关闭样本通知"')}</div></div><p class="gx-help" id="gx-toast-result" ${sample.toast ? 'hidden' : ''}>通知可撤销，不抢焦点。</p></div></div>`,
    satellite: () => `<div class="gx-menu-demo"><div class="gx-action-target" data-pinned="${sample.menuPinned}" data-focused="${sample.menuFocused}">${icon('model')}<span>研判模型</span><i>已固定</i></div><div class="gx-satellite-menu" data-open="${sample.menu}"><div class="gx-radial-shell" aria-hidden="true"><svg viewBox="0 0 260 204"><path d="M130 166Q60 146 36 92M130 166V36M130 166Q200 146 224 92"/></svg></div><div class="gx-satellites" id="gx-satellites" role="group" aria-label="对象快捷动作" ${sample.menu ? '' : 'hidden'}>${[['pin', 'check', sample.menuPinned ? '已固定' : '固定'], ['focus', 'search', sample.menuFocused ? '已聚焦' : '聚焦'], ['restore', 'back', '还原']].map(([key, symbol, label], i) => `<button class="gx-satellite" type="button" data-gx-action="satellite-${key}" ${key === 'restore' ? '' : `aria-pressed="${key === 'pin' ? sample.menuPinned : sample.menuFocused}"`} style="--gx-order:${i}">${icon(symbol)}<span>${label}</span></button>`).join('')}</div><button class="gx-menu-trigger" type="button" data-gx-action="toggle-menu" aria-expanded="${sample.menu}" aria-controls="gx-satellites" aria-label="${sample.menu ? '收起' : '展开'}对象快捷动作"><span aria-hidden="true">+</span></button></div>${output('menu', sample.menuMessage)}</div>`,
  };
  function card(item) {
    return `<article class="gx-card" data-gx-card="${item.key}" data-group="${item.group}" data-focused="false" aria-labelledby="gx-title-${item.key}">
      <header class="gx-card-head"><span class="gx-card-number">${item.id}</span><span>${groups[item.group]}</span><button type="button" class="gx-spec-trigger gx-focus-trigger" data-gx-focus="${item.key}" aria-expanded="false" aria-label="放大查看${item.title}">放大</button><button type="button" class="gx-spec-trigger" data-gx-spec="${item.key}" aria-label="查看${item.title}规范">规范${icon('arrow')}</button></header>
      <h2 id="gx-title-${item.key}" tabindex="-1">${item.title}</h2><p class="gx-card-subtitle">${item.subtitle}</p>
      <div class="gx-playground gx-demo-${item.key}">${demos[item.key] ? demos[item.key]() : window.NovaGalaxyControls.render(item.key)}</div>
      <aside class="gx-inline-spec" hidden aria-label="${item.title}交互要点"><span class="gx-overline">INTERACTION NOTES</span><h3>这个组件，应该怎样工作。</h3><dl><dt>操作与结果</dt><dd>${esc(item.behavior)}</dd><dt>动作节奏</dt><dd>${esc(item.motion)}</dd><dt>静态替代</dt><dd>${esc(item.fallback)}</dd></dl><p>完整的样式、键盘与项目用途可打开右上角“规范”。</p></aside>
      <footer class="gx-card-footer"><span>${hints[item.key]}</span><span>仅此样本</span></footer></article>`;
  }
  function renderRefined() {
    return `<div class="gx-lab">
      <div class="at-scene-heading gx-heading"><div><span class="at-eyebrow">GALAXY × NOVA / 本地交互样稿 · ${catalog.designRevision}</span><h1>星舰组件实验室</h1><p>${catalog.components.length} 种交互，直接体验。点击“放大”，专注查看动作与规范。</p></div><a class="gx-document-link" href="../../README.md#components">组件开发规范${icon('arrow')}</a></div>
      <div class="gx-toolbar"><div class="gx-filter-groups" role="group" aria-label="组件家族筛选">${Object.entries(groups).map(([key, label]) => `<button class="gx-filter" type="button" data-gx-group="${key}" aria-pressed="${sample.group === key}">${label}</button>`).join('')}</div><label class="gx-mobile-family"><span class="gx-sr-only">选择组件家族</span><select id="gx-family">${Object.entries(groups).map(([key,label]) => `<option value="${key}" ${sample.group === key ? 'selected' : ''}>${label}</option>`).join('')}</select></label><label class="gx-filter-search">${icon('search')}<span class="gx-sr-only">搜索组件</span><input id="gx-filter" type="search" value="${esc(sample.query)}" placeholder="搜组件或交互…" autocomplete="off"></label><span id="gx-result-count" class="gx-count" role="status" aria-live="polite">${catalog.components.length} 个可体验组件</span>${button('exit-focus','退出专注','gx-exit-focus','hidden')}${button('reset', '还原样本', 'gx-toolbar-reset')}</div>
      <div class="gx-grid">${catalog.components.map(card).join('')}</div>
      <div class="gx-empty" id="gx-empty" hidden><h2>还没有这个组件。</h2><p>试试“玻璃”“按钮”“开关”，或清除筛选。</p>${button('clear-filter', '显示全部组件')}</div>
      <footer class="gx-attribution"><span>效果灵感来自 <a href="https://github.com/uiverse-io/galaxy" target="_blank" rel="noopener noreferrer">Uiverse Galaxy</a> 与社区作者，已按星舰设计改编。</span><a href="../../验证与来源.md#sources">源码来源与 MIT 许可${icon('arrow')}</a></footer>
    </div>`;
  }
  function render() {
    const total=window.NovaGalaxyLibraryIndex.total;
    return `<nav class="gx-collection-switch" aria-label="组件集合"><button type="button" data-gx-collection="library" aria-pressed="${collection==='library'}">Galaxy完整组件库<small>${total.toLocaleString()}</small></button><button type="button" data-gx-collection="refined" aria-pressed="${collection==='refined'}">星舰精修交互<small>${catalog.components.length}</small></button></nav>${collection==='library'?window.NovaGalaxyLibrary.render():renderRefined()}`;
  }
  function text(selector, value) { const element = host?.querySelector(selector); if (element) element.textContent = value; }
  function cancelTimer(key) { clearTimeout(timers.get(key)); timers.delete(key); }
  function later(key, fn, duration) {
    cancelTimer(key);
    const current = generation;
    timers.set(key, setTimeout(() => { timers.delete(key); if (current === generation && host?.isConnected) fn(); }, duration));
  }
  function applyFilter() {
    window.NovaGalaxyControls?.suspend();
    const query = sample.query.trim().toLocaleLowerCase();
    let count = 0;
    for (const item of catalog.components) {
      const matches = sample.focused ? sample.focused === item.key : (sample.group === 'all' || sample.group === item.group) && `${item.id} ${item.title} ${item.key} ${item.subtitle} ${item.behavior} ${item.style}`.toLocaleLowerCase().includes(query);
      const card = host.querySelector(`[data-gx-card="${item.key}"]`);
      card.hidden = !matches;
      const focused = sample.focused === item.key;
      card.dataset.focused = String(focused);
      card.querySelector('.gx-inline-spec').hidden = !focused;
      card.querySelector('[data-gx-focus]').setAttribute('aria-expanded',String(focused));
      card.querySelector('[data-gx-focus]').setAttribute('aria-label',`${focused ? '收起' : '放大查看'}${item.title}`);
      card.querySelector('[data-gx-focus]').textContent = focused ? '收起' : '放大';
      if (matches) count++;
    }
    host.querySelectorAll('[data-gx-group]').forEach(el => el.setAttribute('aria-pressed', String(el.dataset.gxGroup === sample.group)));
    text('#gx-result-count', `${count} 个可体验组件`);
    host.querySelector('#gx-family').value = sample.group;
    host.querySelector('[data-gx-action="exit-focus"]').hidden = !sample.focused;
    host.querySelector('#gx-empty').hidden = count !== 0;
    closeMenu(false); pinnedTip = false; host.querySelector('#gx-tip-trigger').setAttribute('aria-pressed', 'false'); setTip(false);
  }
  function validateName() {
    const input = host.querySelector('#gx-name');
    const value = input.value.trim();
    const error = !value ? '请填写演示名称。' : [...value].length > 24 ? '名称最多24字符，请缩短后重试。' : '';
    sample.nameValidity = !error;
    sample.nameMessage = error || '名称通过本地演示校验。';
    input.setAttribute('aria-invalid', String(Boolean(error)));
    text('#gx-name-error', error || '名称通过本地演示校验。');
    host.querySelector('#gx-name-error').dataset.valid = String(!error);
    return !error;
  }
  function searchSamples() {
    const entries = [{label:'模型服务',view:'models'},{label:'数据接口',view:'datasource'},{label:'外观与动效',view:'look'}].filter(item => item.label.includes(sample.search.trim()));
    host.querySelector('#gx-search-results').innerHTML = entries.length ? entries.map(item => `<li><button type="button" data-destination="${item.view}"><span>${item.label}</span>${icon('arrow')}</button></li>`).join('') : '<li>没有匹配的本地入口。</li>';
  }
  function setTip(show) {
    cancelTimer('tooltip');
    const tooltip = host?.querySelector('#gx-tooltip');
    if (tooltip) tooltip.dataset.show = String(show);
  }
  function focusCard(key) {
    const previous = sample.focused;
    sample.focused = sample.focused === key ? '' : key;
    applyFilter();
    const card = host.querySelector(`[data-gx-card="${sample.focused || previous || key}"]`);
    card?.scrollIntoView({block:'start'});
    card?.querySelector('[data-gx-focus]')?.focus({preventScroll:true});
  }
  function closeMenu(returnFocus = false) {
    if (!host) return;
    const wasOpen = sample.menu;
    sample.menu = false;
    const satellites = host.querySelector('.gx-satellites');
    satellites.inert = true; satellites.setAttribute('aria-hidden','true');
    cancelTimer('satellite-close');
    if (wasOpen && document.documentElement.dataset.motion === 'full') later('satellite-close',() => { satellites.hidden = true; },260);
    else satellites.hidden = true;
    host.querySelector('.gx-satellite-menu').dataset.open = 'false';
    const trigger = host.querySelector('.gx-menu-trigger');
    trigger.setAttribute('aria-expanded', 'false'); trigger.setAttribute('aria-label', '展开对象快捷动作');
    if (returnFocus && wasOpen) trigger.focus({ preventScroll: true });
  }
  function updateOrbit() {
    host.querySelector('.gx-orbit').dataset.running = String(sample.orbit === 'running');
    host.querySelector('.gx-orbit').dataset.outcome = sample.orbit;
    host.querySelector('[data-gx-action="start-orbit"]').setAttribute('aria-disabled',String(sample.orbit === 'running'));
    host.querySelector('[data-gx-action="start-orbit"]').textContent = sample.orbit === 'error' ? '重试演示' : '开始演示';
    host.querySelector('[data-gx-action="cancel-orbit"]').disabled = sample.orbit !== 'running';
    host.querySelector('[data-gx-action="fail-orbit"]').disabled = sample.orbit !== 'running';
    host.querySelector('.gx-orbit span').innerHTML = icon(sample.orbit === 'done' ? 'check' : sample.orbit === 'error' ? 'close' : 'lab');
    text('#gx-orbit-status', ({ idle: '尚未开始 · 本地计时演示', running: '演示中 · 未知进度，不显示百分比', done: '演示结束 · 未连接任何真实任务', cancelled: '已取消本次演示', error: '模拟失败 · 可以重新开始' })[sample.orbit]);
  }
  function updateSkeleton() {
    const loading = sample.skeleton === 'loading';
    host.querySelector('#gx-skeleton-slot').setAttribute('aria-busy', String(loading));
    host.querySelector('.gx-skeleton-lines').hidden = !loading;
    host.querySelector('.gx-loaded-content').hidden = loading;
    host.querySelector('[data-gx-action="start-skeleton"]').setAttribute('aria-disabled',String(loading));
    host.querySelector('[data-gx-action="start-skeleton"]').textContent = sample.skeleton === 'error' ? '重试加载' : '重播加载';
    host.querySelector('[data-gx-action="cancel-skeleton"]').disabled = !loading;
    host.querySelector('[data-gx-action="fail-skeleton"]').disabled = !loading;
    text('#gx-skeleton-status', loading ? '本地加载演示中' : sample.skeleton === 'cancelled' ? '演示已取消 · 保留原内容' : sample.skeleton === 'error' ? '模拟失败 · 保留原内容，可重试' : '已呈现演示内容');
  }
  function updateFavorite(message = '通知可撤销，不抢焦点。') {
    const trigger = host.querySelector('[data-gx-action="favorite"]');
    trigger.disabled = false;
    trigger.setAttribute('aria-pressed', String(sample.favorite));
    trigger.querySelector('span').textContent = sample.favorite ? '已收藏这个样本' : '收藏这个样本';
    host.querySelector('.gx-favorite-target').dataset.favorite = String(sample.favorite);
    host.querySelector('.gx-favorite-target i').textContent = sample.favorite ? '★' : '☆';
    host.querySelector('.gx-toast').hidden = !sample.toast;
    host.querySelector('#gx-toast-result').hidden = sample.toast;
    text('#gx-toast-result', message);
  }
  function showSpec(key, trigger) {
    const item = catalog.components.find(entry => entry.key === key);
    if (!item || document.querySelector('dialog[open]')) return;
    window.NovaGalaxyControls?.closeAll();
    specTrigger = trigger;
    const fields = [['样式', item.style], ['行为与状态', item.behavior], ['动作与时长', item.motion], ['键盘与触屏', item.keyboard], ['降级策略', item.fallback], ['适用位置', item.use], ['相对原作的改编', item.adaptation]];
    specDialog.innerHTML = `<header class="at-dialog-head"><div><span class="at-eyebrow">${item.id} / ${groups[item.group]}</span><h2 id="gx-spec-title">${item.title}</h2></div>${button('close-spec', icon('close'), 'gx-clear', 'aria-label="关闭组件规范"')}</header><dl class="gx-spec-list">${fields.map(([label, content]) => `<div><dt>${label}</dt><dd>${esc(content)}</dd></div>`).join('')}</dl><div class="gx-spec-source"><span>社区作者：${esc(item.author)} · MIT</span><a href="${sourceUrl(item)}" target="_blank" rel="noopener noreferrer">查看固定版本源码${icon('arrow')}</a><a href="../../README.md#components">完整开发文档${icon('arrow')}</a></div>`;
    specDialog.showModal(); specDialog.querySelector('button').focus();
  }
  function onClick(event) {
    const trigger = event.target.closest('button');
    if (!trigger || trigger.disabled || trigger.getAttribute('aria-disabled') === 'true') return;
    if (trigger.dataset.gxSpec) { showSpec(trigger.dataset.gxSpec, trigger); return; }
    if (trigger.dataset.gxFocus) { focusCard(trigger.dataset.gxFocus); return; }
    if (trigger.dataset.gxGroup) { sample.group = trigger.dataset.gxGroup; sample.focused = ''; applyFilter(); return; }
    const action = trigger.dataset.gxAction;
    if (!action) return;
    if (action === 'close-spec') { specDialog.close(); return; }
    if (action === 'press') {
      sample.presses++; text('#gx-press-status', `已试按 ${sample.presses} 次 · 仅此样本`);
      host.querySelector('.gx-preview-ticket').dataset.open = 'true';
      text('.gx-preview-ticket strong','工作区预览已展开');
      delete trigger.dataset.flash; void trigger.offsetWidth; trigger.dataset.flash = 'true';
      later('shimmer',() => { delete trigger.dataset.flash; },720);
    }
    if (action === 'launch' && sample.launch !== 'running') {
      sample.launch = 'running'; trigger.setAttribute('aria-disabled','true'); trigger.setAttribute('aria-busy','true'); trigger.dataset.running = 'true';
      host.querySelector('.gx-launch-target').dataset.state = 'running';
      trigger.querySelector('b').textContent = '跃迁演示中'; text('#gx-launch-status', '正在播放一次本地图标动作。');
      later('launch', () => { sample.launch = 'done'; trigger.setAttribute('aria-disabled','false'); trigger.setAttribute('aria-busy','false'); trigger.dataset.running = 'false'; host.querySelector('.gx-launch-target').dataset.state = 'done'; trigger.querySelector('b').textContent = '再次展开对象'; text('#gx-launch-status', '演示完成 · 对象已落入舱位'); }, 700);
    }
    if (action === 'validate-name') validateName();
    if (action === 'clear-search') { sample.search = ''; host.querySelector('#gx-search').value = ''; searchSamples(); host.querySelector('#gx-search').focus(); }
    if (action === 'pin-object') { sample.pinned = !sample.pinned; trigger.setAttribute('aria-pressed', String(sample.pinned)); trigger.querySelector('span').textContent = sample.pinned ? '已固定' : '固定对象'; trigger.closest('.gx-holo').dataset.pinned = String(sample.pinned); }
    if (action === 'flip') {
      sample.flipped = !sample.flipped; trigger.closest('.gx-flip').dataset.flipped = String(sample.flipped);
      trigger.setAttribute('aria-expanded', String(sample.flipped)); trigger.textContent = sample.flipped ? '返回正面' : '查看背面';
      host.querySelectorAll('.gx-flip-copy>span').forEach((el, i) => { el.hidden = i !== Number(sample.flipped); });
    }
    if (action.startsWith('pattern-')) { sample.pattern = action.slice(8); host.querySelector('.gx-pattern-scene').dataset.pattern = sample.pattern; host.querySelectorAll('[data-gx-action^="pattern-"]').forEach(el => el.setAttribute('aria-pressed', String(el === trigger))); }
    if (action === 'start-orbit' && sample.orbit !== 'running') { sample.orbit = 'running'; updateOrbit(); later('orbit', () => { sample.orbit = 'done'; updateOrbit(); }, 2400); }
    if (action === 'cancel-orbit') { cancelTimer('orbit'); sample.orbit = 'cancelled'; updateOrbit(); host.querySelector('[data-gx-action="start-orbit"]').focus({ preventScroll: true }); }
    if (action === 'fail-orbit') { cancelTimer('orbit'); sample.orbit = 'error'; updateOrbit(); host.querySelector('[data-gx-action="start-orbit"]').focus({preventScroll:true}); }
    if (action === 'start-skeleton' && sample.skeleton !== 'loading') { sample.skeleton = 'loading'; updateSkeleton(); later('skeleton', () => { sample.skeleton = 'ready'; updateSkeleton(); }, 1800); }
    if (action === 'cancel-skeleton') { cancelTimer('skeleton'); sample.skeleton = 'cancelled'; updateSkeleton(); host.querySelector('[data-gx-action="start-skeleton"]').focus({ preventScroll: true }); }
    if (action === 'fail-skeleton') { cancelTimer('skeleton'); sample.skeleton = 'error'; updateSkeleton(); host.querySelector('[data-gx-action="start-skeleton"]').focus({preventScroll:true}); }
    if (action === 'favorite') { sample.favorite = !sample.favorite; sample.toast = sample.favorite; updateFavorite(sample.favorite ? '已收藏 · 仅此样本' : '已取消本次样本收藏。'); }
    if (action === 'undo-favorite') { sample.favorite = false; sample.toast = false; updateFavorite('已撤销本次样本收藏。'); host.querySelector('[data-gx-action="favorite"]').focus({ preventScroll: true }); }
    if (action === 'dismiss-toast') { sample.toast = false; updateFavorite('样本保持收藏，通知已关闭。'); host.querySelector('[data-gx-action="favorite"]').focus({ preventScroll: true }); }
    if (action === 'toggle-menu') {
      if (sample.menu) closeMenu(true);
      else { cancelTimer('satellite-close'); sample.menu = true; const satellites = host.querySelector('.gx-satellites'); satellites.hidden = false; satellites.inert = false; satellites.removeAttribute('aria-hidden'); host.querySelector('.gx-satellite-menu').dataset.open = 'true'; trigger.setAttribute('aria-expanded', 'true'); trigger.setAttribute('aria-label', '收起对象快捷动作'); host.querySelector('.gx-satellite').focus({ preventScroll: true }); }
    }
    if (action.startsWith('satellite-')) {
      if (action === 'satellite-pin') sample.menuPinned = !sample.menuPinned;
      if (action === 'satellite-focus') sample.menuFocused = !sample.menuFocused;
      if (action === 'satellite-restore') { sample.menuPinned = false; sample.menuFocused = false; }
      const target = host.querySelector('.gx-action-target'); target.dataset.pinned = String(sample.menuPinned); target.dataset.focused = String(sample.menuFocused);
      const pin = host.querySelector('[data-gx-action="satellite-pin"]'); pin.setAttribute('aria-pressed',String(sample.menuPinned)); pin.querySelector('span').textContent = sample.menuPinned ? '已固定' : '固定';
      const focus = host.querySelector('[data-gx-action="satellite-focus"]'); focus.setAttribute('aria-pressed',String(sample.menuFocused)); focus.querySelector('span').textContent = sample.menuFocused ? '已聚焦' : '聚焦';
      sample.menuMessage = action === 'satellite-pin' ? sample.menuPinned ? '已固定样本对象。' : '已取消固定样本对象。' : action === 'satellite-focus' ? sample.menuFocused ? '已聚焦样本对象。' : '已退出对象聚焦。' : '已还原样本对象。';
      text('#gx-menu-status', sample.menuMessage); closeMenu(true);
    }
    if (action === 'exit-focus') focusCard(sample.focused);
    if (action === 'clear-filter') { sample.group = 'all'; sample.query = ''; sample.focused = ''; host.querySelector('#gx-filter').value = ''; applyFilter(); host.querySelector('#gx-filter').focus(); }
    if (action === 'reset') { const element = host; unmount(); sample = defaults(); window.NovaGalaxyControls.reset(); element.innerHTML = render(); mount(element); element.querySelector('#gx-filter').focus({ preventScroll: true }); }
  }
  function mount(element) {
    host = element; controller = new AbortController();
    const { signal } = controller;
    host.addEventListener('click', event => {
      const button=event.target.closest('[data-gx-collection]');if(!button||button.dataset.gxCollection===collection)return;
      const next=button.dataset.gxCollection,target=host;unmount();collection=next;target.innerHTML=render();mount(target);
      target.querySelector(`[data-gx-collection="${next}"]`).focus({preventScroll:true});
    },{signal});
    if(collection==='library'){window.NovaGalaxyLibrary.mount(element);return;}
    specDialog = document.createElement('dialog');
    specDialog.id = 'gx-spec-dialog'; specDialog.className = 'at-dialog gx-spec-dialog'; specDialog.setAttribute('aria-labelledby', 'gx-spec-title'); document.body.append(specDialog);
    host.addEventListener('click', onClick, { signal }); specDialog.addEventListener('click', onClick, { signal });
    specDialog.addEventListener('close', () => { if (specTrigger?.isConnected) specTrigger.focus({ preventScroll: true }); }, { signal });
    host.addEventListener('input', event => {
      const input = event.target;
      if (input.id === 'gx-filter') { sample.query = input.value; sample.focused = ''; if (!event.isComposing && !composingFilter) applyFilter(); }
      if (input.id === 'gx-search') { sample.search = input.value; if (!event.isComposing && !composingSearch) searchSamples(); }
      if (input.id === 'gx-name') { sample.name = input.value; sample.nameValidity = null; sample.nameMessage = ''; input.removeAttribute('aria-invalid'); text('#gx-name-error', ''); }
      if (input.id === 'gx-power') { sample.power = Number(input.value); text('#gx-power-value', `${sample.power}%`); input.style.setProperty('--gx-fill', `${(sample.power - 20) / 80 * 100}%`); host.querySelector('.gx-energy').style.setProperty('--gx-power', sample.power / 100); }
    }, { signal });
    host.addEventListener('compositionstart', event => { if (event.target.id === 'gx-filter') composingFilter = true; if (event.target.id === 'gx-search') composingSearch = true; }, { signal });
    host.addEventListener('compositionend', event => { if (event.target.id === 'gx-filter') { composingFilter = false; sample.query = event.target.value; applyFilter(); } if (event.target.id === 'gx-search') { composingSearch = false; sample.search = event.target.value; searchSamples(); } }, { signal });
    host.addEventListener('change', event => {
      const input = event.target;
      if (input.id === 'gx-switch-input') { sample.glow = input.checked; host.querySelector('.gx-switch-scene').dataset.on = String(input.checked); text('#gx-switch-status', input.checked ? '已开启 · 仅此样本' : '已关闭 · 仅此样本'); }
      if (input.id === 'gx-family') { sample.group = input.value; sample.focused = ''; applyFilter(); }
      if (input.dataset.gxCheck != null) { sample.checks[Number(input.dataset.gxCheck)] = input.checked; text('#gx-check-status', `已选择 ${sample.checks.filter(Boolean).length} 项`); host.querySelector('.gx-check-preview').dataset.edge = String(sample.checks[0]); host.querySelector('.gx-check-preview').dataset.label = String(sample.checks[1]); }
      if (input.name === 'gx-perspective') { sample.segment = Number(input.value); host.querySelector('.gx-segment-sled').style.setProperty('--gx-index', sample.segment); host.querySelector('#gx-perspective-panel').innerHTML = perspectiveHtml(); text('#gx-segment-status', ['概览：先看到对象的用途。', '参数：再调整需要的属性。', '证据：最后核对变更依据。'][sample.segment]); }
    }, { signal });
    host.addEventListener('focusout', event => { if (event.target.id === 'gx-name' && !event.target.closest('[data-gx-card]').hidden) validateName(); }, { signal });
    host.addEventListener('submit', event => { if (event.target.matches('[data-gx-form]')) { event.preventDefault(); validateName(); } }, { signal });
    const tipWrap = host.querySelector('.gx-tip-wrap');
    const tipTrigger = host.querySelector('#gx-tip-trigger');
    tipWrap.addEventListener('pointerenter', event => { if (event.pointerType === 'mouse') later('tooltip', () => setTip(true), 250); }, { signal });
    tipWrap.addEventListener('pointerleave', () => { if (!pinnedTip && !tipWrap.contains(document.activeElement)) setTip(false); }, { signal });
    tipTrigger.addEventListener('focus', () => setTip(true), { signal });
    tipTrigger.addEventListener('blur', () => { if (!pinnedTip) setTip(false); }, { signal });
    tipTrigger.addEventListener('click', () => { pinnedTip = !pinnedTip; tipTrigger.setAttribute('aria-pressed', String(pinnedTip)); setTip(pinnedTip); }, { signal });
    host.addEventListener('pointerdown', event => { if (!event.target.closest('.gx-satellite-menu')) closeMenu(false); if (!event.target.closest('.gx-tip-wrap')) { pinnedTip = false; tipTrigger.setAttribute('aria-pressed', 'false'); setTip(false); } }, { signal });
    host.addEventListener('keydown', event => {
      if (event.defaultPrevented) return;
      if (window.NovaGalaxyControls?.hasOpenPopover()) return;
      if (event.key === 'Escape' && (sample.menu || host.querySelector('#gx-tooltip').dataset.show === 'true')) { event.preventDefault(); event.stopPropagation(); closeMenu(true); pinnedTip = false; tipTrigger.setAttribute('aria-pressed', 'false'); setTip(false); }
      else if (event.key === 'Escape' && sample.focused) { event.preventDefault(); event.stopPropagation(); focusCard(sample.focused); }
      if (event.target.closest('.gx-satellites') && ['ArrowLeft', 'ArrowRight', 'ArrowUp', 'ArrowDown'].includes(event.key)) { event.preventDefault(); const items = [...host.querySelectorAll('.gx-satellite')]; const delta = ['ArrowRight', 'ArrowDown'].includes(event.key) ? 1 : -1; items[(items.indexOf(document.activeElement) + delta + items.length) % items.length].focus(); }
    }, { signal });
    observer = new IntersectionObserver(entries => { for (const entry of entries) entry.target.dataset.gxVisible = String(entry.isIntersecting); }, { root: host, threshold: 0 });
    host.querySelectorAll('[data-gx-card]').forEach(card => observer.observe(card));
    toolbarObserver = new ResizeObserver(() => {
      if (!host) return;
      const toolbar = host.querySelector('.gx-toolbar');
      host.style.setProperty('--gx-sticky-offset',`${getComputedStyle(toolbar).position === 'sticky' ? toolbar.offsetHeight + 16 : 16}px`);
    });
    toolbarObserver.observe(host.querySelector('.gx-toolbar'));
    window.NovaGalaxyControls.mount(host);
    searchSamples(); applyFilter();
  }
  function unmount() {
    window.NovaGalaxyLibrary?.unmount();
    window.NovaGalaxyControls?.unmount();
    generation++;
    controller?.abort(); observer?.disconnect(); toolbarObserver?.disconnect();
    for (const timer of timers.values()) clearTimeout(timer);
    timers.clear(); specDialog?.remove(); specDialog = null; specTrigger = null;
    if (sample.launch === 'running') sample.launch = 'idle';
    if (sample.orbit === 'running') sample.orbit = 'cancelled';
    if (sample.skeleton === 'loading') sample.skeleton = 'cancelled';
    sample.menu = false; pinnedTip = false; composingFilter = false; composingSearch = false;
    host?.style.removeProperty('--gx-sticky-offset'); host = null;
  }
  window.NovaGalaxyLab = Object.freeze({ render, mount, unmount });
})();
