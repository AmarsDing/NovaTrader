/* R6: preserve component geometry, choose useful still frames, and fit painted content. */
(() => {
  'use strict';
  const common=window.NovaGalaxyLibraryAssets,repairs=new WeakMap();
  function variables(){const css=getComputedStyle(document.documentElement),out=[];for(let i=0;i<css.length;i++)if(css[i].startsWith('--nt-'))out.push(`${css[i]}:${css.getPropertyValue(css[i])};`);return out.join('');}
  function themeStyle(mode){return `:root{${variables()}color-scheme:${mode==='original'?'light':document.documentElement.dataset.theme==='dark'?'dark':'light'};}`;}
  function documentFor(item,record,{mode='stellar'}={}){
    const stellar=mode==='stellar',root=document.documentElement,sub=text=>text.replace(/__(gxa-asset-[\w]+)__/g,(_,key)=>common.assets[key]||common.placeholder);
    const utility=item.usesUtility?((item.utilityReset?(stellar?common.stellarBase:common.utilityBase)||'':'')+(stellar?common.stellarUtility:common.utility)):'';
    const native=stellar?'input,textarea,select{color:inherit;background-color:var(--nt-bg-1);accent-color:var(--nt-accent)}svg{fill:currentColor}':'';
    const css=native+'\n'+utility+'\n'+(stellar?record.stellarCss:record.css),html=stellar?record.stellarHtml:record.html;
    return `<!doctype html><html id="gxa-canvas" lang="en" class="${root.dataset.theme==='dark'?'dark':''}" data-palette="${mode}" data-play="false" data-poster="true" data-reduced="${root.dataset.motion!=='full'}" data-eco="${root.dataset.visualQuality==='eco'}"><head><meta charset="utf-8"><meta http-equiv="Content-Security-Policy" content="default-src 'none'; style-src 'unsafe-inline'; img-src data:; font-src 'none'; script-src 'none'; connect-src 'none'; form-action 'none'; base-uri 'none'; object-src 'none'"><style data-gxa-theme>${themeStyle(mode)}</style><style>html,body{margin:0;width:100%;height:100%;background:${stellar?'transparent':'white'};color:${stellar?'var(--nt-text-1)':'CanvasText'}}body{font-family:var(--nt-font-sans);display:flex;align-items:center;justify-content:center;box-sizing:border-box;padding:24px;position:relative}button,input,select,textarea{font:inherit}</style><style>${sub(css)}</style><style data-gxa-layout>html,body{margin:0!important;width:100%!important;height:100%!important;min-width:0!important;min-height:0!important;overflow:hidden!important}body{overflow:visible!important}*:focus-visible{outline:2px solid var(--nt-accent);outline-offset:3px}:root[data-play="false"] *,:root[data-play="false"] *::before,:root[data-play="false"] *::after,:root[data-reduced="true"] *,:root[data-reduced="true"] *::before,:root[data-reduced="true"] *::after{animation-play-state:paused!important}:root[data-reduced="true"] *,:root[data-reduced="true"] *::before,:root[data-reduced="true"] *::after{transition:none!important}:root[data-eco="true"] *{backdrop-filter:none!important}:root[data-gxa-measuring="true"] *{transition:none!important}html[data-natural="true"],html[data-natural="true"] body{overflow:auto!important}html[data-natural="true"] #gxa-mount{min-height:100%!important;height:auto!important;padding:32px!important;align-items:flex-start!important}</style></head><body id="gxa-mount">${sub(html)}</body></html>`;
  }
  function poster(frame){
    const doc=frame.contentDocument;if(!doc?.documentElement)return;
    const animations=doc.getAnimations(),loops=animations.filter(a=>a.effect?.getTiming().iterations===Infinity),durations=loops.map(a=>Number(a.effect.getTiming().duration)).filter(Number.isFinite).sort((a,b)=>a-b);
    const loopTime=Math.max(600,Math.min(1600,(durations[Math.floor(durations.length/2)]||1600)*.37));
    for(const animation of animations){const timing=animation.effect?.getTiming();if(!timing||!Number.isFinite(Number(timing.duration))||!Number(timing.duration))continue;
      const duration=Number(timing.duration),delay=Number(timing.delay)||0,frames=animation.effect.getKeyframes(),first=frames.find(k=>k.opacity!==undefined),last=[...frames].reverse().find(k=>k.opacity!==undefined);
      const fraction=(first&&last&&Number(last.opacity)<Number(first.opacity))?0.15:0.85;
      animation.currentTime=timing.iterations===Infinity?loopTime:Math.max(0,delay+duration*fraction);
    }
    doc.documentElement.dataset.poster='true';
  }
  function setPlaying(frame,want,{restart=false}={}){
    const doc=frame?.contentDocument;if(!doc?.documentElement)return;
    const active=Boolean(want&&document.documentElement.dataset.motion==='full'&&document.documentElement.dataset.visible!=='false'&&!document.hidden);
    if(active&&restart){
      for(const animation of doc.getAnimations())animation.currentTime=0;
      doc.documentElement.dataset.poster='false';
    }
    if(active)doc.documentElement.dataset.poster='false';doc.documentElement.dataset.play=String(active);
    if(restart&&!active)poster(frame);
  }
  function geometry(doc){
    const mount=doc.querySelector('#gxa-mount'),styles=new Map(),style=el=>{if(!styles.has(el))styles.set(el,doc.defaultView.getComputedStyle(el));return styles.get(el);},boxes=[];
    const roots=[...mount?.children||[]],sceneLayers=new Set(roots.filter(el=>{const css=style(el);return roots.length>1&&['absolute','fixed'].includes(css.position)&&el.offsetWidth>=mount.clientWidth*.98&&el.offsetHeight>=mount.clientHeight*.98&&!el.textContent.trim()&&!el.querySelector('button,input,textarea,select,a,svg,img');}));
    for(const el of mount?.querySelectorAll('*')||[]){
      if(el.closest('defs,mask,clipPath,filter'))continue;
      let root=el;while(root.parentElement&&root.parentElement!==mount)root=root.parentElement;if(sceneLayers.has(root))continue;
      const css=style(el),r=el.getBoundingClientRect();if(!r.width||!r.height||css.display==='none'||css.visibility==='hidden'||Number(css.opacity)<.02)continue;
      const clip=css.clip.match(/-?\d*\.?\d+/g)?.map(Number);if(clip?.length===4&&(clip[1]<=clip[3]||clip[2]<=clip[0]))continue;
      if(/inset\(50%/.test(css.clipPath))continue;
      if(['absolute','fixed'].includes(css.position)&&(r.right<-960||r.bottom<-720||r.left>doc.documentElement.clientWidth*4||r.top>doc.documentElement.clientHeight*4))continue;
      let box={left:r.left,right:r.right,top:r.top,bottom:r.bottom},parent=el.parentElement,hidden=false;
      while(parent&&parent!==mount){const s=style(parent);if(s.display==='none'||s.visibility==='hidden'||Number(s.opacity)<.02){hidden=true;break;}
        const p=parent.getBoundingClientRect();if(/^(hidden|clip|auto|scroll)$/.test(s.overflowX)){box.left=Math.max(box.left,p.left);box.right=Math.min(box.right,p.right);}if(/^(hidden|clip|auto|scroll)$/.test(s.overflowY)){box.top=Math.max(box.top,p.top);box.bottom=Math.min(box.bottom,p.bottom);}parent=parent.parentElement;
      }
      if(!hidden&&box.right>box.left&&box.bottom>box.top)boxes.push(box);
    }
    if(!boxes.length)return null;return {left:Math.min(...boxes.map(r=>r.left)),right:Math.max(...boxes.map(r=>r.right)),top:Math.min(...boxes.map(r=>r.top)),bottom:Math.max(...boxes.map(r=>r.bottom)),boxes:boxes.length};
  }
  function layout(frame,{natural=false}={}){
    const doc=frame.contentDocument,canvas=doc?.querySelector('#gxa-mount');if(!canvas)return;
    doc.documentElement.dataset.natural=String(natural);canvas.style.setProperty('scale','none','important');canvas.style.setProperty('translate','none','important');
    const rect=geometry(doc),width=doc.documentElement.clientWidth,height=doc.documentElement.clientHeight;if(!rect)return;
    const scale=natural?1:Math.min(frame.closest('.gxa-detail-preview')?1.5:1,(width-40)/Math.max(1,rect.right-rect.left),(height-40)/Math.max(1,rect.bottom-rect.top));
    if(!natural){canvas.style.setProperty('translate',`${(width/2-(rect.left+rect.right)/2)*scale}px ${(height/2-(rect.top+rect.bottom)/2)*scale}px`,'important');canvas.style.setProperty('scale',String(scale),'important');}
    doc.documentElement.dataset.fitScale=String(scale);
  }
  function rgba(doc,color){
    let canvas=doc.__gxaColorCanvas;if(!canvas){canvas=doc.createElement('canvas');canvas.width=canvas.height=1;doc.__gxaColorCanvas=canvas;}
    const ctx=canvas.getContext('2d',{willReadFrequently:true});ctx.clearRect(0,0,1,1);ctx.fillStyle=color;ctx.fillRect(0,0,1,1);return [...ctx.getImageData(0,0,1,1).data].map((n,i)=>i===3?n/255:n);
  }
  const over=(front,back)=>{const a=front[3];return [front[0]*a+back[0]*(1-a),front[1]*a+back[1]*(1-a),front[2]*a+back[2]*(1-a),1];};
  const luminance=color=>color.slice(0,3).map(x=>{x/=255;return x<=.04045?x/12.92:((x+.055)/1.055)**2.4;}).reduce((n,x,i)=>n+x*[.2126,.7152,.0722][i],0);
  const ratio=(a,b)=>{const x=luminance(a),y=luminance(b);return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);};
  function backgroundSamples(doc,element){
    const root=doc.defaultView.getComputedStyle(doc.documentElement),base=rgba(doc,root.getPropertyValue('--nt-bg-2')),chain=[];let p=element;
    while(p){chain.unshift(p);p=p.parentElement;}let backgrounds=[base];
    for(const node of chain){const css=doc.defaultView.getComputedStyle(node),color=rgba(doc,css.backgroundColor);backgrounds=backgrounds.map(bg=>over(color,bg));
      if(css.backgroundImage.includes('gradient(')){
        const colors=css.backgroundImage.match(/(?:rgba?|color|oklch|oklab|hsl|hsla)\([^()]*\)/g)||[];
        if(colors.length)backgrounds=colors.slice(0,8).map(value=>over(rgba(doc,value),backgrounds[0]));
      }
    }return backgrounds;
  }
  function readable(frame){
    const doc=frame.contentDocument;if(!doc?.documentElement||doc.documentElement.dataset.palette!=='stellar')return;
    doc.documentElement.dataset.gxaMeasuring='true';
    for(const previous of repairs.get(doc)||[])if(previous.el.isConnected){const property=previous.property||'color';if(previous.value)previous.el.style.setProperty(property,previous.value,previous.priority);else previous.el.style.removeProperty(property);}
    const changed=[],rootCss=doc.defaultView.getComputedStyle(doc.documentElement),candidateTokens=['--nt-text-0','--nt-text-1','--nt-bg-0','--nt-bg-1'];
    const candidates=candidateTokens.map(token=>({token,color:rgba(doc,rootCss.getPropertyValue(token))}));
    for(const el of doc.querySelectorAll('#gxa-mount *')){
      if(el.closest('svg,mask,defs')||!([...el.childNodes].some(n=>n.nodeType===3&&n.textContent.trim())||el.matches('input,textarea,select')))continue;
      const css=doc.defaultView.getComputedStyle(el),r=el.getBoundingClientRect();if(!r.width||!r.height||css.display==='none'||css.visibility==='hidden'||Number(css.opacity)<.8||el.matches(':disabled,[aria-disabled="true"]'))continue;
      const fg=rgba(doc,css.color);if(fg[3]<.8||css.backgroundClip==='text'||css.webkitTextFillColor==='rgba(0, 0, 0, 0)')continue;
      const backgrounds=backgroundSamples(doc,el),score=Math.min(...backgrounds.map(bg=>ratio(over(fg,bg),bg)));
      if(score>=4.5)continue;
      const best=candidates.map(c=>({...c,score:Math.min(...backgrounds.map(bg=>ratio(c.color,bg)))})).sort((a,b)=>b.score-a.score)[0];
      if(best.score<=score+.5)continue;
      changed.push({el,value:el.style.getPropertyValue('color'),priority:el.style.getPropertyPriority('color'),before:score,after:best.score});el.style.setProperty('color',`var(${best.token})`,'important');
    }
    // Small monochrome icons share their control's contrast; masks and illustrated SVG remain untouched.
    for(const svg of doc.querySelectorAll('#gxa-mount svg')){
      const rect=svg.getBoundingClientRect();if(!rect.width||!rect.height||rect.width>64||rect.height>64||svg.querySelector('defs,mask,clipPath,filter,linearGradient,radialGradient'))continue;
      const shapes=[svg,...svg.querySelectorAll('*')],paint=[];
      for(const el of shapes){const css=doc.defaultView.getComputedStyle(el);for(const property of ['fill','stroke']){const value=css[property];if(value&&value!=='none'&&!value.includes('url(')){const color=rgba(doc,value);if(color[3]>.8)paint.push({el,property,color});}}}
      if(new Set(paint.map(p=>p.color.slice(0,3).join(','))).size!==1)continue;
      const backgrounds=backgroundSamples(doc,svg),score=paint.length?Math.min(...backgrounds.map(bg=>ratio(paint[0].color,bg))):10;if(score>=3)continue;
      const best=candidates.map(c=>({...c,score:Math.min(...backgrounds.map(bg=>ratio(c.color,bg)))})).sort((a,b)=>b.score-a.score)[0];if(best.score<=score+.5)continue;
      for(const {el,property}of paint){changed.push({el,property,value:el.style.getPropertyValue(property),priority:el.style.getPropertyPriority(property),before:score,after:best.score});el.style.setProperty(property,`var(${best.token})`,'important');}
    }
    repairs.set(doc,changed);doc.documentElement.dataset.readabilityRepairs=String(changed.length);doc.documentElement.getBoundingClientRect();delete doc.documentElement.dataset.gxaMeasuring;
  }
  function prepare(frame,{natural=false}={}){poster(frame);layout(frame,{natural});readable(frame);}
  function sync(frame){const doc=frame.contentDocument;if(!doc?.documentElement)return;const root=document.documentElement,reduced=root.dataset.motion!=='full',wasReduced=doc.documentElement.dataset.reduced==='true';doc.documentElement.dataset.reduced=String(reduced);doc.documentElement.dataset.eco=String(root.dataset.visualQuality==='eco');doc.documentElement.classList.toggle('dark',root.dataset.theme==='dark');const style=doc.querySelector('[data-gxa-theme]');if(style)style.textContent=themeStyle(doc.documentElement.dataset.palette);if(reduced&&!wasReduced)poster(frame);readable(frame);}
  window.NovaGalaxyPreview=Object.freeze({documentFor,prepare,layout,poster,setPlaying,readable,sync,geometry});
})();


