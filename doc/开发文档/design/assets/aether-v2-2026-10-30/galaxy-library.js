/* Full local Galaxy browser. CSS/HTML fragments stay in isolated script-free documents. */
(() => {
  'use strict';
  const index=window.NovaGalaxyLibraryIndex,common=window.NovaGalaxyLibraryAssets,byId=new Map(index.entries.map(item=>[item.id,item]));
  const state={category:'all',query:'',page:1,mode:'stellar',playing:false},size=12,records=new Map(),chunks=new Map(),pending=new Map();
  let host,controller,observer,resizeObserver,themeObserver,dialog,detailTrigger,epoch=0,composing=false;
  const e=text=>String(text).replace(/[&<>"']/g,c=>({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c]));
  const icon=name=>`<svg class="at-icon" aria-hidden="true"><use href="#at-icon-${name}"/></svg>`;
  const action=(name,label,attrs='')=>`<button type="button" class="gxa-control" data-gxa-action="${name}" ${attrs}>${label}</button>`;
  const categoryLabel=id=>index.categories.find(item=>item.id===id)?.label||'全部组件';
  function filtered(){const q=state.query.trim().toLocaleLowerCase(),ordinal=/^g-\d+$/.test(q)?Number(q.slice(2)):null;return index.entries.filter(item=>(state.category==='all'||item.category===state.category)&&(!q||(ordinal!==null?item.ordinal===ordinal:`${item.id} ${item.ordinal} ${item.author} ${item.name} ${item.title} ${item.tags} ${item.features.join(' ')} ${item.path} ${categoryLabel(item.category)}`.toLocaleLowerCase().includes(q))));}
  function render(){return `<section class="gxa-library gx-lab" aria-label="Galaxy完整组件库"><header class="gxa-heading"><div><span class="at-eyebrow">GALAXY × NOVA / 完整组件库 · R6</span><h1>一整个组件宇宙。</h1><p>${index.total.toLocaleString()} 个组件 · ${index.categories.length} 个分类。悬停或聚焦体验，打开详情查看动作与源码。</p></div><a href="../../README.md#galaxy-preview">全库开发参考 ↗</a></header><div class="gxa-shell"><nav class="gxa-categories" aria-label="Galaxy组件分类"><span>组件分类</span><button type="button" data-gxa-category="all" aria-pressed="${state.category==='all'}">全部组件<small>${index.total}</small></button>${index.categories.map(item=>`<button type="button" data-gxa-category="${item.id}" aria-pressed="${state.category===item.id}">${item.label}<small>${item.count}</small></button>`).join('')}</nav><div class="gxa-main"><div class="gxa-toolbar"><label class="gxa-search">${icon('search')}<span class="gx-sr-only">搜索全部Galaxy组件</span><input id="gxa-search" type="search" placeholder="搜索名称、作者、效果或编号…" value="${e(state.query)}" autocomplete="off"></label><select class="gxa-mobile-category" aria-label="Galaxy组件分类">${[{id:'all',label:'全部组件',count:index.total},...index.categories].map(item=>`<option value="${item.id}" ${state.category===item.id?'selected':''}>${item.label} · ${item.count}</option>`).join('')}</select><div class="gxa-mode" role="group" aria-label="组件配色">${action('stellar','星舰适配',`aria-pressed="${state.mode==='stellar'}"`)}${action('original','原作配色',`aria-pressed="${state.mode==='original'}"`)}</div>${action('play',state.playing?'暂停本页':'播放本页',`aria-pressed="${state.playing}"`)}</div><p id="gxa-message" role="status" aria-live="polite" hidden></p><div class="gxa-meta"><span id="gxa-result-count" role="status" aria-live="polite"></span><span>每页12项 · 表单仅本地体验</span></div><div class="gxa-grid" id="gxa-grid"></div><nav class="gxa-pagination" aria-label="Galaxy组件分页">${action('previous','上一页')}<label>第 <input id="gxa-page" aria-describedby="gxa-page-error" type="number" min="1" step="1" value="${state.page}" aria-label="前往页码"> 页</label><span id="gxa-page-total"></span><span id="gxa-page-error" role="alert" hidden></span>${action('go','前往')}${action('next','下一页')}</nav></div></div><footer class="gxa-footer"><span>完整本地快照 · 原作者、MIT与原始源码逐项保留</span><a href="galaxy-library/manifest.json">来源清单与文件哈希 ↗</a></footer></section>`;}
  function register(key,payload){chunks.set(key,payload);for(const [id,record]of Object.entries(payload))records.set(id,record);pending.get(key)?.resolve(payload);pending.delete(key);}
  async function load(item){
    if(records.has(item.id))return records.get(item.id);
    if(!pending.has(item.chunk)){
      let resolve,reject;const promise=new Promise((yes,no)=>{resolve=yes;reject=no;});pending.set(item.chunk,{promise,resolve,reject});
      const script=document.createElement('script');script.src=`galaxy-library/chunks/${item.chunk}.js`;script.dataset.gxaChunk=item.chunk;
      script.onerror=()=>{pending.get(item.chunk)?.reject(Error('无法读取本地组件文件'));pending.delete(item.chunk);script.remove();};document.head.append(script);
    }
    await pending.get(item.chunk)?.promise;return records.get(item.id);
  }
  const preview=window.NovaGalaxyPreview,liveFrames=new Map(),fieldStates=new Map();
  let renderTicket=0,detailRequest=0;
  function documentFor(item,record,{mode=state.mode}={}){return preview.documentFor(item,record,{mode});}
  function play(frame,want,options){if(frame)preview.setPlaying(frame,want,options);}
  function fit(frame){
    if(frame.closest('.gxa-detail-preview')){frame.style.width='100%';frame.style.height='100%';frame.style.transform='none';}
    else{frame.style.width='480px';frame.style.height='360px';const width=frame.parentElement?.clientWidth;if(width)frame.style.transform='scale('+width/480+')';}
  }
  function remember(frame){
    const id=frame.dataset.gxaFrame,doc=frame.contentDocument;if(!id||!doc?.querySelector('#gxa-mount'))return;
    const values=[...doc.querySelectorAll('input,textarea,select')].map(el=>({value:el.type==='file'?'':el.value,checked:el.checked,selected:el.options&&el.multiple?[...el.options].map(o=>o.selected):null}));
    fieldStates.delete(id);fieldStates.set(id,values);while(fieldStates.size>128)fieldStates.delete(fieldStates.keys().next().value);
  }
  function restore(frame){const values=fieldStates.get(frame.dataset.gxaFrame);if(!values)return;[...frame.contentDocument.querySelectorAll('input,textarea,select')].forEach((el,i)=>{const saved=values[i];if(!saved)return;if(el.type!=='file')el.value=saved.value;if('checked' in el)el.checked=saved.checked;if(saved.selected)[...el.options].forEach((option,j)=>option.selected=Boolean(saved.selected[j]));});}
  function dispose(frame){remember(frame);const binding=liveFrames.get(frame);binding?.controller.abort();binding?.documentController?.abort();if(binding?.raf)cancelAnimationFrame(binding.raf);if(frame.parentElement)resizeObserver?.unobserve(frame.parentElement);liveFrames.delete(frame);frame.remove();}
  function disposeWithin(element){if(!element)return;for(const frame of [...liveFrames.keys()])if(element.contains(frame))dispose(frame);}
  function message(text){const node=host?.querySelector('#gxa-message');if(node){node.textContent=text;node.hidden=!text;}}
  function syncPreferences(){
    const reduced=document.documentElement.dataset.motion!=='full';if(reduced)state.playing=false;
    for(const frame of liveFrames.keys()){preview.sync(frame);play(frame,state.playing||Boolean(frame.closest('.gxa-detail-preview'))||Boolean(frame.closest('.gxa-card')?.matches(':hover'))||frame.contentDocument?.hasFocus());}
    const button=host?.querySelector('[data-gxa-action="play"]');if(button){button.disabled=reduced;button.textContent=reduced?'静态预览':state.playing?'暂停本页':'播放本页';button.setAttribute('aria-pressed',String(state.playing&&!reduced));}
    const replay=dialog?.querySelector('[data-gxa-action="replay"]');if(replay){replay.disabled=reduced;replay.textContent=reduced?'静态帧':'重播动效';}
  }
  async function hydrate(card,detail=false){
    const item=byId.get(card.dataset.gxaId);if(!item||card.querySelector('iframe'))return;
    const ticket=epoch,request=String(++renderTicket);card.dataset.gxaTicket=request;card.dataset.loading='true';
    try{const record=await load(item);if(ticket!==epoch||!card.isConnected||card.dataset.gxaTicket!==request)return;
      const frame=document.createElement('iframe');frame.title=`${categoryLabel(item.category)}：${item.title}，作者${item.author}`;frame.setAttribute('sandbox','allow-same-origin');frame.setAttribute('referrerpolicy','no-referrer');frame.srcdoc=documentFor(item,record);frame.dataset.gxaFrame=item.id;
      const binding={controller:new AbortController(),raf:0};liveFrames.set(frame,binding);
      frame.addEventListener('load',()=>{if(!frame.isConnected)return;binding.documentController?.abort();binding.documentController=new AbortController();card.dataset.loading='false';fit(frame);restore(frame);preview.prepare(frame,{natural:card.dataset.sizing==='natural'});play(frame,detail||state.playing||card.matches(':hover'));const doc=frame.contentDocument,{signal}=binding.documentController;
        const repaint=()=>{if(binding.raf)return;binding.raf=requestAnimationFrame(()=>{binding.raf=0;if(frame.isConnected)preview.readable(frame);});};
        doc?.addEventListener('submit',event=>event.preventDefault(),{signal});
        doc?.addEventListener('click',event=>{if(event.target.closest('a'))event.preventDefault();repaint();},{signal});
        for(const name of ['input','change'])doc?.addEventListener(name,()=>{remember(frame);repaint();},{signal});
        doc?.addEventListener('pointerover',repaint,{signal});doc?.addEventListener('transitionend',repaint,{signal});
        doc?.addEventListener('focusin',()=>{play(frame,true);repaint();},{signal});
        doc?.addEventListener('focusout',event=>{if(!event.relatedTarget)play(frame,detail||state.playing||card.matches(':hover'));},{signal});
        doc?.addEventListener('keydown',event=>{if(event.key==='Escape'&&dialog?.open){event.preventDefault();dialog.close();}},{signal});
      },{signal:binding.controller.signal});
      card.querySelector('.gxa-preview').replaceChildren(frame);resizeObserver.observe(frame.parentElement);
    }catch(error){if(card.isConnected){card.dataset.loading='false';card.querySelector('.gxa-preview').innerHTML=`<p class="gxa-detail-note">${e(error.message)}。${action('retry','重新读取',`data-id="${item.id}"`)}</p>`;}}
  }
  function bindCards(){observer?.disconnect();for(const card of host.querySelectorAll('.gxa-card'))observer.observe(card);}
  function draw({scroll=false}={}){
    const items=filtered(),pages=Math.max(1,Math.ceil(items.length/size));state.page=Math.min(Math.max(1,state.page),pages);
    const selected=items.slice((state.page-1)*size,state.page*size),grid=host.querySelector('#gxa-grid');
    host.querySelector('#gxa-result-count').textContent=`${categoryLabel(state.category)} · ${items.length.toLocaleString()} 项${state.query?'匹配结果':''}`;
    disposeWithin(grid);grid.innerHTML=selected.length?selected.map(item=>`<article class="gxa-card" data-gxa-id="${item.id}" data-loading="true"><header><span>${categoryLabel(item.category)}</span><b>G-${String(item.ordinal).padStart(4,'0')}</b></header><div class="gxa-preview"></div><h2 title="${e(item.title)}">${e(item.title)}</h2><div class="gxa-tags">${(item.features.length?item.features:['静态']).slice(0,4).map(text=>`<span>${text}</span>`).join('')}${item.assetFallback?'<span>图片占位</span>':''}</div><footer><span>${e(item.author)}</span>${action('detail','放大与源码',`data-id="${item.id}" aria-label="查看${e(item.title)}详情"`)}</footer></article>`).join(''):`<div class="gxa-empty"><p>没有匹配的组件。</p>${action('clear','清除搜索与分类')}</div>`;
    host.querySelector('#gxa-page').value=state.page;host.querySelector('#gxa-page').max=pages;host.querySelector('#gxa-page-total').textContent=`/ ${pages} 页`;
    host.querySelector('#gxa-page').removeAttribute('aria-invalid');host.querySelector('#gxa-page-error').hidden=true;
    host.querySelector('[data-gxa-action="previous"]').disabled=state.page===1;host.querySelector('[data-gxa-action="next"]').disabled=state.page===pages;
    host.querySelectorAll('[data-gxa-category]').forEach(el=>el.setAttribute('aria-pressed',String(el.dataset.gxaCategory===state.category)));
    host.querySelector('.gxa-mobile-category').value=state.category;bindCards();
    if(scroll)host.querySelector('.gxa-main').scrollIntoView({block:'start'});
  }
  function setCategory(id){state.category=id;state.page=1;message('');draw({scroll:true});}
  async function detail(id,trigger){
    const item=byId.get(id);if(!item)return;detailTrigger=trigger;const ticket=epoch,request=++detailRequest,record=await load(item);if(ticket!==epoch||request!==detailRequest||!host?.isConnected)return;disposeWithin(dialog);
    const behavior=[item.features.includes('悬停')?'悬停触发原作样式反馈':'可查看静态形态',item.features.includes('焦点')?'原作包含焦点样式':'预览补充键盘焦点',item.features.includes('选中')?'原生选中状态触发CSS反馈':'可操作原作HTML控件',item.features.includes('动画')?'动画按悬停、聚焦或显式播放运行':'无需持续播放'].join('；');
    dialog.innerHTML=`<header class="at-dialog-head"><div><span class="at-eyebrow">G-${String(item.ordinal).padStart(4,'0')} · ${categoryLabel(item.category)}</span><h2>${e(item.title)}</h2></div>${action('close-detail','关闭',`aria-label="关闭组件详情"`)}</header><div class="gxa-detail"><div class="gxa-detail-preview" data-gxa-id="${item.id}" data-sizing="fit"><div class="gxa-preview-tools" role="group" aria-label="预览操作">${action('fit','适应窗口','aria-pressed="true"')}${action('natural','原尺寸','aria-pressed="false"')}${action('replay','重播动效')}${action('reset-sample','重置样本',`data-id="${item.id}"`)}</div><div class="gxa-preview"></div><p class="gxa-detail-note">${state.mode==='stellar'?'星舰自动适配':'原作配色'} · 可直接悬停、点击或Tab体验</p></div><div class="gxa-detail-notes"><dl><dt>原作者与来源</dt><dd>${e(item.author)} · <a href="${index.repository}/blob/${index.revision}/${encodeURI(item.path)}" target="_blank" rel="noopener noreferrer">固定版本原文件 ↗</a></dd><dt>样式与行为观察</dt><dd>${behavior}。</dd><dt>源码动作参数</dt><dd>${e(item.motion.join('；')||'未声明CSS动画时长；保留静态形态。')}</dd><dt>减少动态与节能</dt><dd>继承全局偏好；减少动态停止动画/过渡，节能停止背景模糊。</dd><dt>资源与适配</dt><dd>${item.usesUtility?'Tailwind已本地编译。':'HTML/CSS本地预览。'}${item.assetFallback?'原作图片地址失效，已使用本地占位。':''}${index.warnings.some(w=>w.path===item.path)?'原作CSS含语法问题，预览已做容错恢复。':''}配色为自动适配，迁入生产前按具体组件复核。</dd></dl><div class="gxa-detail-actions">${action('copy','复制原始HTML',`data-id="${item.id}"`)}${action('download','下载原始HTML',`data-id="${item.id}"`)}${action('toggle-detail-mode','切换原作/星舰',`data-id="${item.id}"`)}</div><p id="gxa-copy-status" role="status" aria-live="polite"></p></div></div><details class="gxa-source"><summary>查看完整原始HTML / CSS</summary><pre><code></code></pre></details>`;
    dialog.querySelector('code').textContent=record.raw;if(!dialog.open)dialog.showModal();await hydrate(dialog.querySelector('[data-gxa-id]'),true);syncPreferences();if(dialog?.open)dialog.querySelector('[data-gxa-action="close-detail"]').focus({preventScroll:true});
  }
  async function click(event){
    const category=event.target.closest('[data-gxa-category]');if(category){setCategory(category.dataset.gxaCategory);return;}
    const button=event.target.closest('[data-gxa-action]');if(!button||button.disabled)return;const action=button.dataset.gxaAction,id=button.dataset.id;
    if(action==='detail'){try{await detail(id,button);}catch(error){message(error.message+'，请重新读取。');}return;}
    if(action==='close-detail'){dialog.close();return;}
    if(action==='retry')return hydrate(button.closest('[data-gxa-id]'));
    if(action==='previous'||action==='next'){state.page+=action==='next'?1:-1;draw({scroll:true});}
    if(action==='go'){const input=host.querySelector('#gxa-page'),value=Number(input.value),pages=Math.max(1,Math.ceil(filtered().length/size));if(Number.isSafeInteger(value)&&value>=1&&value<=pages){state.page=value;draw({scroll:true});}else{input.setAttribute('aria-invalid','true');const error=host.querySelector('#gxa-page-error');error.textContent=`请输入1–${pages}之间的整数页码。`;error.hidden=false;input.focus({preventScroll:true});}}
    if(action==='clear'){state.category='all';state.query='';state.page=1;host.querySelector('#gxa-search').value='';draw();host.querySelector('#gxa-search').focus();}
    if(action==='stellar'||action==='original'){state.mode=action;host.querySelectorAll('.gxa-mode button').forEach(el=>el.setAttribute('aria-pressed',String(el.dataset.gxaAction===action)));draw();}
    if(action==='play'){state.playing=!state.playing;button.textContent=state.playing?'暂停本页':'播放本页';button.setAttribute('aria-pressed',String(state.playing));host.querySelectorAll('iframe').forEach(frame=>play(frame,state.playing));}
    if(action==='copy'||action==='download'){
      const ticket=epoch,request=detailRequest,activeDialog=dialog,record=await load(byId.get(id));
      const live=()=>ticket===epoch&&request===detailRequest&&activeDialog?.isConnected&&activeDialog.open&&activeDialog.querySelector('[data-gxa-id]')?.dataset.gxaId===id;
      if(!live())return;
      if(action==='copy'){try{await navigator.clipboard.writeText(record.raw);if(live())activeDialog.querySelector('#gxa-copy-status').textContent='原始HTML已复制。';}catch{if(!live())return;activeDialog.querySelector('.gxa-source').open=true;const range=document.createRange();range.selectNodeContents(activeDialog.querySelector('code'));const selection=getSelection();selection.removeAllRanges();selection.addRange(range);activeDialog.querySelector('#gxa-copy-status').textContent='已选中源码，可按Ctrl+C复制。';}}
      else{const url=URL.createObjectURL(new Blob([record.raw],{type:'text/html;charset=utf-8'})),link=document.createElement('a');link.href=url;link.download=byId.get(id).path.split('/').pop();link.click();setTimeout(()=>URL.revokeObjectURL(url),1000);}
    }
    if(action==='toggle-detail-mode'){
      const card=dialog.querySelector('[data-gxa-id]'),frame=card.querySelector('iframe'),record=await load(byId.get(id));if(!card.isConnected||!frame?.isConnected||!dialog?.open||card.dataset.gxaId!==id)return;remember(frame);card.dataset.mode=card.dataset.mode==='original'?'stellar':card.dataset.mode==='stellar'?'original':state.mode==='stellar'?'original':'stellar';
      card.dataset.loading='true';frame.srcdoc=documentFor(byId.get(id),record,{mode:card.dataset.mode});card.querySelector('.gxa-detail-note').textContent=card.dataset.mode==='stellar'?'星舰自动适配':'原作配色';
    }
    if(action==='fit'||action==='natural'){const card=dialog.querySelector('[data-gxa-id]'),frame=card.querySelector('iframe');if(frame){card.dataset.sizing=action;preview.layout(frame,{natural:action==='natural'});dialog.querySelectorAll('[data-gxa-action="fit"],[data-gxa-action="natural"]').forEach(el=>el.setAttribute('aria-pressed',String(el===button)));}}
    if(action==='replay'){const frame=dialog.querySelector('iframe');if(frame)play(frame,true,{restart:true});}
    if(action==='reset-sample'){const card=dialog.querySelector('[data-gxa-id]'),frame=card.querySelector('iframe'),record=await load(byId.get(id));if(frame?.isConnected){fieldStates.delete(id);card.dataset.loading='true';frame.srcdoc=documentFor(byId.get(id),record,{mode:card.dataset.mode||state.mode});}}
  }
  function mount(element){
    host=element;controller=new AbortController();const {signal}=controller;
    dialog=document.createElement('dialog');dialog.className='at-dialog gxa-dialog';dialog.setAttribute('aria-label','Galaxy组件详情');document.body.append(dialog);
    dialog.addEventListener('click',click,{signal});dialog.addEventListener('close',()=>{if(dialog.open)return;detailRequest++;disposeWithin(dialog);for(const frame of host?.querySelectorAll('iframe')||[])restore(frame);if(detailTrigger?.isConnected)detailTrigger.focus({preventScroll:true});},{signal});
    host.addEventListener('click',click,{signal});host.addEventListener('input',event=>{if(event.target.id==='gxa-search'){state.query=event.target.value;state.page=1;if(!event.isComposing&&!composing)draw();}},{signal});
    host.addEventListener('compositionstart',event=>{if(event.target.id==='gxa-search')composing=true;},{signal});host.addEventListener('compositionend',event=>{if(event.target.id==='gxa-search'){composing=false;state.query=event.target.value;state.page=1;draw();}},{signal});
    host.addEventListener('change',event=>{if(event.target.matches('.gxa-mobile-category'))setCategory(event.target.value);},{signal});host.addEventListener('keydown',event=>{if(event.target.id==='gxa-page'&&event.key==='Enter'){event.preventDefault();host.querySelector('[data-gxa-action="go"]').click();}},{signal});
    host.addEventListener('pointerover',event=>{const card=event.target.closest('.gxa-card');if(card&&!card.contains(event.relatedTarget))play(card.querySelector('iframe'),true);},{signal});
    host.addEventListener('pointerout',event=>{const card=event.target.closest('.gxa-card');if(card&&!card.contains(event.relatedTarget)){const frame=card.querySelector('iframe');play(frame,state.playing||frame?.contentDocument?.hasFocus());}},{signal});
    resizeObserver=new ResizeObserver(entries=>{for(const entry of entries){const frame=entry.target.querySelector('iframe');if(frame){fit(frame);if(frame.closest('.gxa-detail-preview'))preview.layout(frame,{natural:frame.closest('[data-sizing]')?.dataset.sizing==='natural'});}}});
    observer=new IntersectionObserver(entries=>{for(const entry of entries){if(entry.isIntersecting)hydrate(entry.target);else if(!entry.target.contains(document.activeElement)){const frame=entry.target.querySelector('iframe');if(frame)dispose(frame);}}},{root:host,rootMargin:'120px 0px'});
    themeObserver=new MutationObserver(syncPreferences);themeObserver.observe(document.documentElement,{attributes:true,attributeFilter:['data-motion','data-theme','data-visual-quality','data-visible','style']});
    document.addEventListener('visibilitychange',()=>{host?.querySelectorAll('iframe').forEach(frame=>play(frame,state.playing));},{signal});window.addEventListener('blur',()=>{host?.querySelectorAll('iframe').forEach(frame=>play(frame,false));},{signal});
    draw();syncPreferences();
  }
  function unmount(){epoch++;detailRequest++;controller?.abort();observer?.disconnect();for(const frame of [...liveFrames.keys()])dispose(frame);resizeObserver?.disconnect();themeObserver?.disconnect();dialog?.remove();dialog=null;detailTrigger=null;host=null;composing=false;}
  window.NovaGalaxyLibrary=Object.freeze({render,mount,unmount,register,load,documentFor,index,prepareFrame:preview.prepare});
})();
