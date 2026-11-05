/* R6: every preview, both palettes, actual poster + fit; measurements are not manual pixel acceptance. */
import fs from 'node:fs';import path from 'node:path';import vm from 'node:vm';import assert from 'node:assert/strict';import {fileURLToPath,pathToFileURL} from 'node:url';import {createRequire} from 'node:module';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..'),req=createRequire(path.join(repo,'web/package.json'));
const parse5=await import(pathToFileURL(req.resolve('parse5')).href),manifest=JSON.parse(fs.readFileSync(path.join(base,'galaxy-library/manifest.json'),'utf8'));
const records=new Map(),sandbox={window:{NovaGalaxyLibrary:{register:(_,items)=>Object.entries(items).forEach(([id,item])=>records.set(id,item))}}};
for(const file of fs.readdirSync(path.join(base,'galaxy-library/chunks')))vm.runInNewContext(fs.readFileSync(path.join(base,'galaxy-library/chunks',file),'utf8'),sandbox);
function structural(html){const found=[];function walk(node){if(['defs','mask','clipPath','filter'].includes(node.tagName)){found.push(parse5.serializeOuter(node));return;}for(const child of node.childNodes||[])walk(child);}walk(parse5.parseFragment(html));return found;}
let structuralEntries=0,structuralSubtrees=0;
for(const item of manifest.entries){const r=records.get(item.id),a=structural(r.html),b=structural(r.stellarHtml);assert.deepEqual(b,a,item.path+' SVG structure');if(a.length)structuralEntries++;structuralSubtrees+=a.length;}
const {chromium}=await import(pathToFileURL(path.join(repo,'web/node_modules/@playwright/test/index.mjs')).href),browser=await chromium.launch(),page=await browser.newPage({viewport:{width:1440,height:900}}),errors=[],remoteRequests=[],rows=[];
page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(/^https?:/.test(request.url()))remoteRequests.push(request.url());});
try{
 await page.goto(pathToFileURL(path.join(base,'index.html')).href+'?revision=6#lab');await page.waitForSelector('.gxa-card iframe');
 // Keep the library mounted but stop its real cards competing with the bounded probes.
 await page.evaluate(()=>NovaGalaxyLibrary.unmount());
 for(let start=0;start<manifest.total;start+=48){
  rows.push(...await page.evaluate(async start=>{
   const result=[],holder=document.createElement('div');holder.style.cssText='position:fixed;left:0;top:-3000px;width:480px;height:360px';document.body.append(holder);
   function textSamples(doc){const out=[];for(const el of doc.body.querySelectorAll('*')){
    if(el.closest('svg')||![...el.childNodes].some(n=>n.nodeType===3&&n.textContent.trim()))continue;
    const s=doc.defaultView.getComputedStyle(el),r=el.getBoundingClientRect();if(!r.width||!r.height||s.display==='none'||s.visibility==='hidden'||Number(s.opacity)<.8||s.backgroundImage!=='none'||s.backgroundClip==='text')continue;
    let p=el,bg='rgba(0, 0, 0, 0)';while(p&&bg==='rgba(0, 0, 0, 0)'){bg=doc.defaultView.getComputedStyle(p).backgroundColor;p=p.parentElement;}
    out.push({text:el.textContent.trim().slice(0,32),fg:s.color,bg,font:s.fontSize});
   }return out.slice(0,32);}
   for(let i=0;i<48;i+=6)await Promise.all(NovaGalaxyLibraryIndex.entries.slice(start+i,start+i+6).map(async item=>{
    const record=await NovaGalaxyLibrary.load(item),row={id:item.id,ordinal:item.ordinal,path:item.path,category:item.category};
    for(const mode of ['original','stellar']){
     const frame=document.createElement('iframe');frame.width='480';frame.height='360';frame.setAttribute('sandbox','allow-same-origin');
     try{
      await new Promise((resolve,reject)=>{const timer=setTimeout(()=>reject(Error('load timeout')),8000);frame.onload=()=>{clearTimeout(timer);resolve();};frame.srcdoc=NovaGalaxyPreview.documentFor(item,record,{mode});holder.append(frame);});
      const doc=frame.contentDocument,initial=NovaGalaxyPreview.geometry(doc);NovaGalaxyPreview.poster(frame);const poster=NovaGalaxyPreview.geometry(doc);
      NovaGalaxyPreview.layout(frame);NovaGalaxyPreview.readable(frame);const fitted=NovaGalaxyPreview.geometry(doc),scale=Number(doc.documentElement.dataset.fitScale||1);
      row[mode]={elements:doc.body.querySelectorAll('*').length,styles:doc.styleSheets.length,animations:doc.getAnimations().filter(a=>'animationName' in a).length,initial,poster,fitted,scale,repairs:Number(doc.documentElement.dataset.readabilityRepairs||0),texts:textSamples(doc)};
     }catch(error){row[mode]={error:error.message};}finally{frame.remove();}
    }result.push(row);
   }));holder.remove();return result;
  },start));if(start%480===0)console.log(`Measured ${rows.length}/${manifest.total} × both palettes`);
 }
 rows.sort((a,b)=>a.ordinal-b.ordinal);
 const failures=rows.filter(r=>r.original.error||r.stellar.error),tiny=rows.filter(r=>r.stellar.scale<.35),outliers=rows.filter(r=>{const b=r.stellar.fitted;return b&&(b.left<-2||b.top<-2||b.right>482||b.bottom>362);});
 const geometryChanges=rows.filter(r=>{const a=r.original.poster,b=r.stellar.poster;return Boolean(a)!==Boolean(b)||(a&&b&&['left','right','top','bottom'].some(k=>Math.abs(a[k]-b[k])>1));}),animationChanges=rows.filter(r=>r.original.animations!==r.stellar.animations);
 const summary={count:rows.length,paletteLoads:rows.length*2,structuralEntries,structuralSubtrees,structuralDifferences:0,failures:failures.length,tinyPreviews:tiny.length,fitOutliers:outliers.length,paletteGeometryChanges:geometryChanges.length,paletteAnimationChanges:animationChanges.length,posterReveals:rows.filter(r=>!r.stellar.initial&&r.stellar.poster).length,readabilityRepairs:rows.reduce((n,r)=>n+(r.stellar.repairs||0),0)};
 const output={revision:'R6',date:'2026-10-31',scope:'All 3802 entries at 480×360 in both palettes. SVG subtree integrity, useful animation posters, clipped visible bounds, fit and computed text samples. Outliers require inspection; these measurements do not establish pixel fidelity or every interaction state.',summary,failures,tiny:tiny.map(r=>r.ordinal),outliers:outliers.map(r=>r.ordinal),geometryChanges:geometryChanges.map(r=>r.ordinal),errors,remoteRequests,rows};
 fs.writeFileSync(path.join(base,'r6-library-visual-verification.json'),JSON.stringify(output,null,2)+'\n');console.log(JSON.stringify(summary));console.log(JSON.stringify({tiny:output.tiny,outliers:output.outliers,geometryChanges:output.geometryChanges}));
 assert.equal(rows.length,3802);assert.deepEqual(failures,[]);assert.deepEqual(animationChanges.map(r=>r.ordinal),[]);assert.deepEqual(geometryChanges.map(r=>r.ordinal),[]);assert.deepEqual(outliers.map(r=>r.ordinal),[]);assert.deepEqual(errors,[]);assert.deepEqual(remoteRequests,[]);
}finally{await browser.close();}
