/* R3 selection and special controls. Local fixtures only; no network or storage. */
(() => {
  'use strict';
  const initial = () => ({ layout:'balanced', combo:'large', tags:['price'], menu:'overview', step:100, stepDraft:'100', stepError:'', lower:25, upper:70, charged:false, wizard:0, visited:0, wizardName:'研究空间', wizardGlass:true });
  let value = initial(), host, controller, current, frame = 0, holding = false, holdTimer = 0, holdFrame = 0, epoch = 0, activeOption = 0, composing = false, layoutActive = 0, layoutQuery = '', layoutTypedAt = 0;
  const layouts = [
    {id:'compact',title:'紧凑排列',hint:'收紧留白，集中查看',alias:'compact'},
    {id:'balanced',title:'均衡排列',hint:'内容与留白保持平衡',alias:'balanced'},
    {id:'spacious',title:'宽松排列',hint:'拉开间距，舒展阅读',alias:'spacious'},
  ];
  const options = [
    {id:'large',title:'综合研判',hint:'复杂任务 · 本地样本',icon:'model'},
    {id:'small',title:'轻量分析',hint:'高频阅读 · 本地样本',icon:'model'},
    {id:'embedding',title:'文档嵌入',hint:'检索用途 · 本地样本',icon:'data'},
    {id:'standby',title:'备用节点',hint:'禁用态示例，不能选择',icon:'shield',disabled:true},
  ];
  const tags = [{id:'price',title:'价格图层',group:'数据'},{id:'volume',title:'成交量',group:'数据'},{id:'news',title:'消息标签',group:'信息'},{id:'evidence',title:'证据摘要',group:'信息'},{id:'notes',title:'个人笔记',group:'信息'}];
  const menu = [{id:'overview',title:'对象概览',hint:'展示用途与范围',group:'查看',icon:'workspace'},{id:'parameters',title:'局部参数',hint:'展示样本材质',group:'查看',icon:'appearance'},{id:'evidence',title:'设计证据',hint:'展示固定来源',group:'检查',icon:'shield'}];
  const e = text => String(text).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon = name => `<svg class="at-icon" aria-hidden="true"><use href="#at-icon-${name}"/></svg>`;
  const arrow = `<span class="gw-chevron">${icon('arrow')}</span>`;
  const layoutGlyph = id => `<span class="gw-layout-glyph" data-layout="${id}" aria-hidden="true"><i></i><i></i><i></i></span>`;
  const layoutLabel = () => { const item=layouts.find(item=>item.id===value.layout);return `${layoutGlyph(item.id)}<span><strong id="gw-layout-value">${item.title}</strong><small>${item.hint}</small></span>${arrow}`; };
  const button = (action,label,attrs='') => `<button type="button" class="gx-control" data-gw-action="${action}" ${attrs}>${label}</button>`;
  const chosen = () => options.find(item=>item.id===value.combo);
  const chips = () => value.tags.map(id=>`<span class="gw-chip">${tags.find(item=>item.id===id).title}<button type="button" data-gw-remove="${id}" aria-label="移除${tags.find(item=>item.id===id).title}">×</button></span>`).join('');
  const tagOptions = () => ['数据','信息'].map(group=>`<fieldset><legend>${group}</legend>${tags.filter(item=>item.group===group).map(item=>`<label class="gw-check-option"><input type="checkbox" data-gw-tag="${item.id}" ${value.tags.includes(item.id)?'checked':''}><span>${item.title}</span></label>`).join('')}</fieldset>`).join('');
  const result = (id,text) => `<p class="gx-status" id="${id}" role="status" aria-live="polite">${text}</p>`;
  const pop = (id,role,content,label) => `<div class="gw-pop" id="${id}" popover="auto" role="${role}" aria-label="${label}">${content}</div>`;
  const renderers = {
    native: () => `<div class="gw-widget"><div class="gw-layout-preview" data-layout="${value.layout}" aria-hidden="true"><i></i><i></i><i></i></div><span class="gw-field-title" id="gw-layout-label">样本排列方式</span><button id="gw-layout" class="gw-layout-trigger" type="button" role="combobox" data-gw-action="open-layout" value="${value.layout}" aria-haspopup="listbox" aria-expanded="false" aria-controls="gw-layout-pop" aria-labelledby="gw-layout-label gw-layout-value">${layoutLabel()}</button>${pop('gw-layout-pop','listbox','', '样本排列预设')}${result('gw-layout-status','选择预设，观察上方留白与间距。')}</div>`,
    combobox: () => `<div class="gw-widget"><label for="gw-combo">选择样本用途</label><div class="gw-input-shell gw-combo-shell">${icon('search')}<input id="gw-combo" type="text" role="combobox" aria-autocomplete="list" aria-controls="gw-combo-pop" aria-expanded="false" autocomplete="off" value="${e(chosen().title)}">${button('open-combo',arrow,'aria-label="展开用途选项"')}</div>${pop('gw-combo-pop','listbox','', '样本用途选项')}<div class="gw-selection-target" id="gw-combo-target">${icon(chosen().icon)}<span><strong>${chosen().title}</strong><small>${chosen().hint}</small></span></div></div>`,
    multiselect: () => `<div class="gw-widget"><label id="gw-tags-label">组合图层，最多3项</label><button class="gw-select-trigger" type="button" data-gw-action="open-tags" aria-haspopup="dialog" aria-expanded="false" aria-controls="gw-tags-pop" aria-labelledby="gw-tags-label gw-tags-value"><span id="gw-tags-value">已选 ${value.tags.length} 项图层</span>${arrow}</button><div class="gw-chips" id="gw-chips">${chips()}</div>${pop('gw-tags-pop','dialog',`<p class="gw-pop-caption" id="gw-tags-help">最多3项，取消一项后可继续选择。</p>${tagOptions()}`,'组合图层')}<div class="gw-layer-target" id="gw-layer-target"><span>样本图层</span>${tags.map(item=>`<i data-gw-layer="${item.id}" ${value.tags.includes(item.id)?'':'hidden'}>${item.title}</i>`).join('')}</div>${result('gw-tags-status',`已选择 ${value.tags.length}/3 项`)}</div>`,
    splitmenu: () => `<div class="gw-widget"><div class="gw-selection-target" id="gw-menu-target">${icon(menu.find(item=>item.id===value.menu).icon)}<span><strong>${menu.find(item=>item.id===value.menu).title}</strong><small>${menu.find(item=>item.id===value.menu).hint}</small></span></div><div class="gw-split"><button type="button" data-gw-action="run-menu">预览当前视角${icon('arrow')}</button><button type="button" data-gw-action="open-menu" aria-label="选择预览视角" aria-haspopup="menu" aria-expanded="false" aria-controls="gw-menu-pop">${arrow}</button></div>${pop('gw-menu-pop','menu',menu.map((item,index)=>`${index===0||menu[index-1].group!==item.group?`<p class="gw-pop-group">${item.group}</p>`:''}<button type="button" role="menuitemradio" data-gw-menu="${item.id}" aria-checked="${item.id===value.menu}">${icon(item.icon)}<span>${item.title}<small>${item.hint}</small></span>${icon('check')}</button>`).join(''),'预览视角')}${result('gw-menu-status','选择视角，主按钮会预览当前选项。')}</div>`,
    stepper: () => `<div class="gw-widget"><div class="gw-scale-preview" style="--gw-scale:${value.step/100}" aria-hidden="true"><span>${icon('lab')}</span></div><label for="gw-step">样本图形缩放</label><div class="gw-input-shell gw-stepper">${button('step-down','−','aria-label="缩小样本"')}<input id="gw-step" type="number" min="80" max="140" step="5" value="${e(value.stepDraft)}" aria-describedby="gw-step-help gw-step-status"><span>%</span>${button('step-up','+','aria-label="放大样本"')}</div><p class="gx-help" id="gw-step-help">80–140%，每步5；输入后Enter确认。</p>${result('gw-step-status',`已应用 ${value.step}%`)}</div>`,
    interval: () => `<div class="gw-widget"><div class="gw-window-target" aria-hidden="true"><i></i><i></i><i></i><span id="gw-window-selection" style="left:${value.lower}%;right:${100-value.upper}%"></span></div><span class="gw-field-title">样本可见区间</span><div class="gw-range-shell"><div class="gw-range-track"><i id="gw-range-fill" style="left:${value.lower}%;right:${100-value.upper}%"></i></div><input class="gw-range" id="gw-lower" type="range" min="0" max="100" step="1" value="${value.lower}" aria-label="区间下界"><input class="gw-range" id="gw-upper" type="range" min="0" max="100" step="1" value="${value.upper}" aria-label="区间上界"></div><div class="gw-bounds"><label>下界<input id="gw-lower-number" type="number" min="0" max="95" step="1" value="${value.lower}"></label><label>上界<input id="gw-upper-number" type="number" min="5" max="100" step="1" value="${value.upper}"></label></div>${result('gw-interval-status',`当前 ${value.lower}–${value.upper}，最小间隔5`)}</div>`,
    hold: () => `<div class="gw-widget gw-hold-widget"><div class="gw-charge" data-charged="${value.charged}" style="--gw-charge:${value.charged?1:0}" aria-hidden="true"><span>${icon(value.charged?'check':'lab')}</span></div><button class="gw-hold" id="gw-hold" type="button">按住，点亮样本</button><div class="gx-inline-actions">${button('light-now','直接点亮')}${button('reset-charge','复位')}</div>${result('gw-hold-status',value.charged?'样本已点亮':'按住约0.8秒；松开或移出可取消。')}</div>`,
    wizard: () => `<div class="gw-widget"><nav class="gw-steps" aria-label="样本步骤">${['命名','外观','核对','完成'].map((title,index)=>`<button type="button" data-gw-step="${index}" ${index>value.visited?'disabled':''} ${index===value.wizard?'aria-current="step"':''}><b>${index<value.wizard?icon('check'):index+1}</b><span>${title}</span></button>`).join('')}</nav><div class="gw-wizard-body" id="gw-wizard-body"></div><div class="gw-wizard-actions">${button('wizard-back','上一步')}${button('wizard-next','下一步')}</div>${result('gw-wizard-status','只组织本地样本，不保存项目配置。')}</div>`,
  };
  const el = selector => host?.querySelector(selector);
  const say = (id,text) => { const element=el(id); if(element) element.textContent=text; };
  function isOpen(element) { return element?.dataset.fallback==='true' ? !element.hidden : Boolean(element?.matches(':popover-open')); }
  function position() {
    if (!current) return;
    const {panel,anchor}=current, bounds=(panel.id==='gw-combo-pop'?anchor.closest('.gw-combo-shell'):anchor).getBoundingClientRect(), stage=host.getBoundingClientRect();
    if (!anchor.isConnected || !bounds.width || bounds.bottom<stage.top || bounds.top>stage.bottom) { closeAll(); return; }
    const width=Math.min(Math.max(bounds.width,280),innerWidth-24);
    panel.style.width=`${width}px`;
    const safeTop=Math.max(12,stage.top+8), safeBottom=Math.min(innerHeight-12,stage.bottom-8);
    const below=safeBottom-bounds.bottom-8, above=bounds.top-safeTop-8;
    panel.style.maxHeight=`${Math.max(100,Math.min(320,Math.max(below,above)))}px`;
    const height=panel.offsetHeight;
    panel.style.left=`${Math.max(12,Math.min(innerWidth-width-12,bounds.left))}px`;
    panel.style.top=`${Math.max(safeTop,Math.min(safeBottom-height,below<height&&above>below?bounds.top-height-8:bounds.bottom+8))}px`;
  }
  function closeAll({returnFocus=false}={}) {
    const previous=current; if (!previous) return;
    current=null;
    if (previous.panel.dataset.fallback==='true') previous.panel.hidden=true;
    else if (isOpen(previous.panel)) previous.panel.hidePopover();
    previous.anchor.setAttribute('aria-expanded','false');
    previous.anchor.removeAttribute('aria-activedescendant');
    if(previous.panel.id==='gw-combo-pop') { el('#gw-combo')?.removeAttribute('aria-activedescendant'); if(el('#gw-combo')) el('#gw-combo').value=chosen().title; }
    if(returnFocus&&previous.anchor.isConnected) previous.anchor.focus({preventScroll:true});
  }
  function open(id,anchor,{focusFirst=false}={}) {
    if(current?.panel.id===id) { closeAll({returnFocus:true}); return; }
    closeAll(); const panel=el('#'+id); current={panel,anchor};
    anchor.setAttribute('aria-expanded','true');
    if(panel.dataset.fallback==='true') panel.hidden=false;
    else { try { panel.showPopover({source:anchor}); } catch(error) { if(error instanceof TypeError)panel.showPopover();else throw error; } }
    position();
    if(focusFirst) panel.querySelector('button:not(:disabled),input:not(:disabled)')?.focus({preventScroll:true});
  }
  function comboResults(query='') {
    const filtered=options.filter(item=>`${item.title} ${item.hint}`.includes(query.trim()));
    const enabled=filtered.filter(item=>!item.disabled);
    activeOption=Math.max(0,Math.min(activeOption,enabled.length-1));
    el('#gw-combo-pop').innerHTML=filtered.length?filtered.map(item=>`<div role="option" id="gw-option-${item.id}" data-gw-option="${item.id}" aria-selected="${item.id===value.combo}" aria-disabled="${Boolean(item.disabled)}" data-active="${enabled[activeOption]?.id===item.id}">${icon(item.icon)}<span>${item.title}<small>${item.hint}</small></span>${item.id===value.combo?icon('check'):''}</div>`).join(''):'<p class="gw-pop-caption">没有匹配的用途。按Esc保留原选择。</p>';
    if(enabled[activeOption]) el('#gw-combo').setAttribute('aria-activedescendant',`gw-option-${enabled[activeOption].id}`); else el('#gw-combo').removeAttribute('aria-activedescendant');
    return enabled;
  }
  function layoutResults() {
    el('#gw-layout-pop').innerHTML=layouts.map((item,index)=>`<div role="option" id="gw-layout-option-${item.id}" data-gw-layout="${item.id}" aria-selected="${item.id===value.layout}" data-active="${index===layoutActive}">${layoutGlyph(item.id)}<span>${item.title}<small>${item.hint}</small></span><span class="gw-layout-check" aria-hidden="true">${icon('check')}</span></div>`).join('');
  }
  function activeLayout(index) {
    layoutActive=index;
    host.querySelectorAll('[data-gw-layout]').forEach((item,i)=>item.dataset.active=String(i===index));
    el('#gw-layout').setAttribute('aria-activedescendant',`gw-layout-option-${layouts[index].id}`);
    el('#gw-layout-option-'+layouts[index].id)?.scrollIntoView({block:'nearest'});
  }
  function openLayout() {
    if(current?.panel.id==='gw-layout-pop'){closeAll({returnFocus:true});return;}
    layoutActive=layouts.findIndex(item=>item.id===value.layout);layoutQuery='';layoutResults();
    open('gw-layout-pop',el('#gw-layout'));activeLayout(layoutActive);
  }
  function selectLayout(id) {
    const item=layouts.find(item=>item.id===id);if(!item)return;
    value.layout=id;el('.gw-layout-preview').dataset.layout=id;
    el('#gw-layout').value=id;el('#gw-layout').innerHTML=layoutLabel();
    layoutResults();say('#gw-layout-status',`已应用：${item.title}`);closeAll({returnFocus:true});
  }
  function selectCombo(id) {
    const item=options.find(option=>option.id===id&&!option.disabled); if(!item) return;
    value.combo=id; el('#gw-combo').value=item.title;
    el('#gw-combo-target').innerHTML=`${icon(item.icon)}<span><strong>${item.title}</strong><small>${item.hint}</small></span>`;
    closeAll({returnFocus:true});
  }
  function syncTags() {
    say('#gw-tags-value',`已选 ${value.tags.length} 项图层`); el('#gw-chips').innerHTML=chips();
    host.querySelectorAll('[data-gw-tag]').forEach(input=>{ input.checked=value.tags.includes(input.dataset.gwTag); input.disabled=value.tags.length>=3&&!input.checked; });
    host.querySelectorAll('[data-gw-layer]').forEach(layer=>{layer.hidden=!value.tags.includes(layer.dataset.gwLayer);});
    say('#gw-tags-status',`已选择 ${value.tags.length}/3 项`); position();
  }
  function syncStep(next) {
    value.step=next; value.stepDraft=String(next); value.stepError=''; el('#gw-step').value=value.stepDraft; el('#gw-step').removeAttribute('aria-invalid');
    el('.gw-scale-preview').style.setProperty('--gw-scale',next/100); say('#gw-step-status',`已应用 ${next}%`);
    el('[data-gw-action="step-down"]').disabled=next===80; el('[data-gw-action="step-up"]').disabled=next===140;
  }
  function commitStep() {
    const next=Number(value.stepDraft);
    if(!value.stepDraft.trim()||!Number.isInteger(next)||next<80||next>140||next%5!==0) { value.stepError='请填写80–140之间、步长为5的整数。';el('#gw-step').setAttribute('aria-invalid','true'); say('#gw-step-status',value.stepError); return; }
    syncStep(next);
  }
  function syncInterval() {
    for(const [key,id] of [['lower','gw-lower'],['upper','gw-upper']]) { el('#'+id).value=value[key]; el('#'+id+'-number').value=value[key]; el('#'+id+'-number').removeAttribute('aria-invalid'); }
    el('#gw-lower').setAttribute('aria-valuetext',`${value.lower}，上界${value.upper}，最小间隔5`);
    el('#gw-upper').setAttribute('aria-valuetext',`${value.upper}，下界${value.lower}，最小间隔5`);
    for(const target of ['#gw-window-selection','#gw-range-fill']) { el(target).style.left=`${value.lower}%`; el(target).style.right=`${100-value.upper}%`; }
    say('#gw-interval-status',`当前 ${value.lower}–${value.upper}，最小间隔5`);
  }
  function commitBound(input,key) {
    const next=Number(input.value), valid=input.value!==''&&Number.isInteger(next)&&next>=0&&next<=100&&(key==='lower'?next<=value.upper-5:next>=value.lower+5);
    if(!valid) { input.setAttribute('aria-invalid','true'); say('#gw-interval-status','上下界须为0–100的整数，且至少相隔5。'); return; }
    value[key]=next; syncInterval();
  }
  function paintCharge(progress) { el('.gw-charge')?.style.setProperty('--gw-charge',progress); }
  function cancelHold() { holding=false; clearTimeout(holdTimer); cancelAnimationFrame(holdFrame); holdTimer=0;holdFrame=0;paintCharge(value.charged?1:0); if(!value.charged) say('#gw-hold-status','尚未点亮；可继续按住或直接点亮。'); }
  function charge() { holding=false; clearTimeout(holdTimer); cancelAnimationFrame(holdFrame); value.charged=true; paintCharge(1); el('.gw-charge').dataset.charged='true'; el('.gw-charge>span').innerHTML=icon('check'); say('#gw-hold-status','样本已点亮 · 仅此局部效果'); }
  function startHold() {
    if(holding||value.charged) return;
    holding=true; const ticket=epoch,start=performance.now(); say('#gw-hold-status','继续按住，松开可取消。');
    holdTimer=setTimeout(()=>{if(ticket===epoch&&holding) charge();},800);
    const tick=now=>{ if(!holding||ticket!==epoch) return; paintCharge(Math.min((now-start)/800,1)); holdFrame=requestAnimationFrame(tick); }; if(document.documentElement.dataset.motion==='full')holdFrame=requestAnimationFrame(tick);
  }
  function wizardBody() {
    const contents=[`<label for="gw-wizard-name">样本名称</label><div class="gw-input-shell"><input id="gw-wizard-name" value="${e(value.wizardName)}" type="text"></div><p class="gx-help">1–24字符，后续步骤会保留这个名称。</p>`,`<label class="gw-check-option"><input type="checkbox" id="gw-wizard-glass" ${value.wizardGlass?'checked':''}><span>保留玻璃边缘</span></label><div class="gw-wizard-object" data-glass="${value.wizardGlass}">${icon('lab')}<span>${e(value.wizardName)}</span></div>`,`<dl class="gw-review"><dt>名称</dt><dd>${e(value.wizardName)}</dd><dt>材质</dt><dd>${value.wizardGlass?'玻璃边缘':'稳定实底'}</dd><dt>作用范围</dt><dd>仅本地样本</dd></dl>`,`<div class="gw-wizard-complete">${icon('check')}<strong>${e(value.wizardName)}</strong><span>样本步骤已完成，没有保存项目配置。</span></div>`];
    el('#gw-wizard-body').innerHTML=`<h3 tabindex="-1">${['先给对象一个名字','调整局部外观','确认这次样本','完成这个演示'][value.wizard]}</h3>${contents[value.wizard]}`;
    host.querySelectorAll('[data-gw-step]').forEach(item=>{const index=Number(item.dataset.gwStep);item.disabled=index>value.visited;item.querySelector('b').innerHTML=index<value.wizard?icon('check'):String(index+1);if(index===value.wizard)item.setAttribute('aria-current','step');else item.removeAttribute('aria-current');});
    el('[data-gw-action="wizard-back"]').disabled=value.wizard===0;
    el('[data-gw-action="wizard-next"]').disabled=value.wizard===3;
    el('[data-gw-action="wizard-next"]').textContent=value.wizard===2?'完成样本':'下一步';
  }
  function click(event) {
    const layout=event.target.closest('[data-gw-layout]');if(layout){selectLayout(layout.dataset.gwLayout);return;}
    const option=event.target.closest('[data-gw-option]'); if(option) {selectCombo(option.dataset.gwOption);return;}
    const remove=event.target.closest('[data-gw-remove]'); if(remove) {value.tags=value.tags.filter(id=>id!==remove.dataset.gwRemove);syncTags();el('[data-gw-action="open-tags"]').focus({preventScroll:true});return;}
    const menuItem=event.target.closest('[data-gw-menu]');if(menuItem){value.menu=menuItem.dataset.gwMenu;host.querySelectorAll('[data-gw-menu]').forEach(item=>item.setAttribute('aria-checked',String(item===menuItem)));runMenu();closeAll({returnFocus:true});return;}
    const step=event.target.closest('[data-gw-step]');if(step&&!step.disabled){value.wizard=Number(step.dataset.gwStep);wizardBody();el('#gw-wizard-body h3').focus({preventScroll:true});return;}
    const trigger=event.target.closest('[data-gw-action]');if(!trigger||trigger.disabled)return;
    const action=trigger.dataset.gwAction;
    if(action==='open-layout')openLayout();
    if(action==='open-combo'){activeOption=0;comboResults();open('gw-combo-pop',el('#gw-combo'));el('#gw-combo').focus({preventScroll:true});}
    if(action==='open-tags'){syncTags();open('gw-tags-pop',trigger,{focusFirst:true});}
    if(action==='open-menu')open('gw-menu-pop',trigger,{focusFirst:true});
    if(action==='run-menu')runMenu();
    if(action==='step-down')syncStep(Math.max(80,value.step-5));
    if(action==='step-up')syncStep(Math.min(140,value.step+5));
    if(action==='light-now')charge();
    if(action==='reset-charge'){cancelHold();value.charged=false;paintCharge(0);el('.gw-charge').dataset.charged='false';el('.gw-charge>span').innerHTML=icon('lab');say('#gw-hold-status','已复位，可再次点亮。');}
    if(action==='wizard-back'){value.wizard--;wizardBody();el('#gw-wizard-body h3').focus({preventScroll:true});}
    if(action==='wizard-next'){
      const name=value.wizardName.trim();if(!name||[...name].length>24){say('#gw-wizard-status','请填写1–24字符的样本名称。');el('#gw-wizard-name')?.setAttribute('aria-invalid','true');return;}
      value.wizard=Math.min(3,value.wizard+1);value.visited=Math.max(value.visited,value.wizard);wizardBody();say('#gw-wizard-status',value.wizard===3?'本地步骤已完成，无配置写入。':'草稿保留在本样本中。');el('#gw-wizard-body h3').focus({preventScroll:true});
    }
  }
  function runMenu(){const item=menu.find(item=>item.id===value.menu);el('#gw-menu-target').innerHTML=`${icon(item.icon)}<span><strong>${item.title}</strong><small>${item.hint}</small></span>`;say('#gw-menu-status',`已预览：${item.title} · 仅此样本`);}
  function keydown(event) {
    if(event.isComposing)return;
    if(event.key==='Escape'&&current){event.preventDefault();event.stopPropagation();closeAll({returnFocus:true});return;}
    if(event.target.id==='gw-layout'){
      const opened=current?.panel.id==='gw-layout-pop';
      if(event.key==='Tab'&&opened){closeAll();return;}
      if(event.altKey&&event.key==='ArrowUp'&&opened){event.preventDefault();closeAll({returnFocus:true});return;}
      if(['ArrowDown','ArrowUp','Home','End','Enter',' '].includes(event.key)){
        event.preventDefault();
        if(opened&&['Enter',' '].includes(event.key)){selectLayout(layouts[layoutActive].id);return;}
        if(!opened)openLayout();
        if(event.key==='Home')activeLayout(0);
        else if(event.key==='End')activeLayout(layouts.length-1);
        else if(opened&&['ArrowDown','ArrowUp'].includes(event.key))activeLayout((layoutActive+(event.key==='ArrowDown'?1:-1)+layouts.length)%layouts.length);
        return;
      }
      if(event.key.length===1&&!event.ctrlKey&&!event.metaKey&&!event.altKey){
        const now=performance.now();layoutQuery=now-layoutTypedAt<650?layoutQuery+event.key:event.key;layoutTypedAt=now;
        let index=layouts.findIndex(item=>item.title.startsWith(layoutQuery)||item.alias.startsWith(layoutQuery.toLowerCase()));
        if(index<0){layoutQuery=event.key;index=layouts.findIndex(item=>item.title.startsWith(layoutQuery)||item.alias.startsWith(layoutQuery.toLowerCase()));}
        if(index>=0){event.preventDefault();if(!opened){const query=layoutQuery;openLayout();layoutQuery=query;}activeLayout(index);}
      }
    }
    if(event.target.id==='gw-combo'){
      if(['ArrowDown','ArrowUp'].includes(event.key)||(['Home','End'].includes(event.key)&&current?.panel.id==='gw-combo-pop')){
        event.preventDefault();const opened=!current||current.panel.id!=='gw-combo-pop';if(opened){activeOption=0;comboResults();open('gw-combo-pop',event.target);}
        const filtered=options.filter(item=>!item.disabled&&`${item.title} ${item.hint}`.includes(event.target.value===chosen().title?'':event.target.value.trim()));
        if(!filtered.length)return;
        activeOption=opened?(event.key==='ArrowUp'?filtered.length-1:0):event.key==='Home'?0:event.key==='End'?filtered.length-1:(activeOption+(event.key==='ArrowUp'?-1:1)+filtered.length)%filtered.length;
        comboResults(event.target.value===chosen().title?'':event.target.value);el('#gw-option-'+filtered[activeOption].id)?.scrollIntoView({block:'nearest'});
      }
      if(event.key==='Enter'&&current?.panel.id==='gw-combo-pop'){event.preventDefault();const option=el('#gw-combo-pop [data-active="true"]');if(option)selectCombo(option.dataset.gwOption);}
    }
    if(event.target.closest('#gw-menu-pop')&&['ArrowDown','ArrowUp','Home','End'].includes(event.key)){
      event.preventDefault();const items=[...host.querySelectorAll('[data-gw-menu]')],index=items.indexOf(document.activeElement);items[event.key==='Home'?0:event.key==='End'?items.length-1:(index+(event.key==='ArrowUp'?-1:1)+items.length)%items.length].focus();
    }
    if(event.target.id==='gw-step'){
      if(event.key==='Enter'){event.preventDefault();commitStep();}
      if(event.key==='Escape'){event.preventDefault();syncStep(value.step);}
      if(['ArrowUp','ArrowDown','Home','End'].includes(event.key)){event.preventDefault();syncStep(event.key==='Home'?80:event.key==='End'?140:Math.max(80,Math.min(140,value.step+(event.key==='ArrowUp'?5:-5))));}
    }
    if(['gw-lower-number','gw-upper-number'].includes(event.target.id)&&event.key==='Enter'){event.preventDefault();commitBound(event.target,event.target.id.includes('lower')?'lower':'upper');}
    if(event.target.id==='gw-hold'&&[' ','Enter'].includes(event.key)){event.preventDefault();if(!event.repeat)startHold();}
  }
  function mount(element) {
    host=element;controller=new AbortController();const {signal}=controller;
    host.addEventListener('click',click,{signal});host.addEventListener('keydown',keydown,{signal,capture:true});
    host.addEventListener('keyup',event=>{if(event.target.id==='gw-hold'&&[' ','Enter'].includes(event.key)){event.preventDefault();if(holding)cancelHold();}},{signal});
    host.addEventListener('pointerdown',event=>{
      if(event.target.closest('[data-gw-option],[data-gw-layout]'))event.preventDefault();
      const hold=event.target.closest('#gw-hold');if(hold&&event.button===0){event.preventDefault();hold.focus({preventScroll:true});hold.setPointerCapture(event.pointerId);startHold();}
    },{signal});
    host.addEventListener('pointermove',event=>{
      const option=event.target.closest('[data-gw-layout]');
      if(option&&current?.panel.id==='gw-layout-pop'){const index=layouts.findIndex(item=>item.id===option.dataset.gwLayout);if(index!==layoutActive)activeLayout(index);}
      if(holding){const r=el('#gw-hold').getBoundingClientRect();if(event.clientX<r.left||event.clientX>r.right||event.clientY<r.top||event.clientY>r.bottom)cancelHold();}
    },{signal});
    for(const name of ['pointerup','pointercancel','lostpointercapture'])host.addEventListener(name,()=>{if(holding)cancelHold();},{signal});
    host.addEventListener('input',event=>{
      const input=event.target;
      if(input.id==='gw-combo'&&!event.isComposing&&!composing){activeOption=0;comboResults(input.value);if(!current||current.panel.id!=='gw-combo-pop')open('gw-combo-pop',input);position();}
      if(input.id==='gw-step'){value.stepDraft=input.value;value.stepError='';input.removeAttribute('aria-invalid');}
      if(['gw-lower','gw-upper'].includes(input.id)){if(input.id==='gw-lower')value.lower=Math.min(Number(input.value),value.upper-5);else value.upper=Math.max(Number(input.value),value.lower+5);syncInterval();}
      if(input.id==='gw-wizard-name'){value.wizardName=input.value;value.visited=0;input.removeAttribute('aria-invalid');host.querySelectorAll('[data-gw-step]').forEach(item=>item.disabled=Number(item.dataset.gwStep)>0);}
    },{signal});
    host.addEventListener('compositionstart',event=>{if(event.target.id==='gw-combo')composing=true;},{signal});
    host.addEventListener('compositionend',event=>{if(event.target.id==='gw-combo'){composing=false;activeOption=0;comboResults(event.target.value);if(!current)open('gw-combo-pop',event.target);position();}},{signal});
    host.addEventListener('change',event=>{
      const input=event.target;
      if(input.dataset.gwTag){if(input.checked&&value.tags.length<3)value.tags.push(input.dataset.gwTag);else value.tags=value.tags.filter(id=>id!==input.dataset.gwTag);syncTags();}
      if(input.id==='gw-step')commitStep();
      if(['gw-lower-number','gw-upper-number'].includes(input.id))commitBound(input,input.id.includes('lower')?'lower':'upper');
      if(input.id==='gw-wizard-glass'){value.wizardGlass=input.checked;value.visited=Math.min(value.visited,1);el('.gw-wizard-object').dataset.glass=String(input.checked);host.querySelectorAll('[data-gw-step]').forEach(item=>item.disabled=Number(item.dataset.gwStep)>value.visited);}
    },{signal});
    host.addEventListener('focusout',event=>{
      const next=event.relatedTarget;
      if(current&&next&&!current.panel.contains(next)&&!current.anchor.contains(next)&&!(current.panel.id==='gw-combo-pop'&&current.anchor.closest('.gw-combo-shell').contains(next)))closeAll();
    },{signal});
    host.querySelectorAll('.gw-pop').forEach(panel=>{
      if(typeof panel.showPopover!=='function'){panel.removeAttribute('popover');panel.dataset.fallback='true';panel.hidden=true;}
      panel.addEventListener('beforetoggle',event=>{if(event.newState==='closed'&&current?.panel===panel){const anchor=current.anchor;current=null;anchor.setAttribute('aria-expanded','false');anchor.removeAttribute('aria-activedescendant');if(panel.id==='gw-combo-pop'&&el('#gw-combo'))el('#gw-combo').value=chosen().title;}},{signal});
    });
    document.addEventListener('pointerdown',event=>{if(current&&!current.panel.contains(event.target)&&!current.anchor.contains(event.target)&&!(current.panel.id==='gw-combo-pop'&&event.target.closest('.gw-combo-shell')))closeAll();},{signal});
    const schedule=()=>{if(current&&!frame)frame=requestAnimationFrame(()=>{frame=0;position();});};
    window.addEventListener('resize',schedule,{signal});host.addEventListener('scroll',schedule,{signal,capture:true});
    window.addEventListener('blur',cancelHold,{signal});document.addEventListener('visibilitychange',()=>{if(document.hidden)cancelHold();},{signal});
    syncTags();syncInterval();wizardBody();
    el('[data-gw-action="step-down"]').disabled=value.step===80;el('[data-gw-action="step-up"]').disabled=value.step===140;
    if(value.stepError){el('#gw-step').setAttribute('aria-invalid','true');say('#gw-step-status',value.stepError);}
  }
  function unmount(){epoch++;closeAll();cancelHold();controller?.abort();cancelAnimationFrame(frame);frame=0;host=null;composing=false;}
  window.NovaGalaxyControls=Object.freeze({render:key=>renderers[key]?.()||'',mount,unmount,closeAll,suspend:()=>{closeAll();if(holding)cancelHold();},hasOpenPopover:()=>Boolean(current),reset:()=>{value=initial();}});
})();
