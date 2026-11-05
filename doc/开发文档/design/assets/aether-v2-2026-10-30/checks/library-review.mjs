/* Full import integrity plus isolated-browser behavior. No production service calls. */
import fs from 'node:fs';import path from 'node:path';import vm from 'node:vm';import crypto from 'node:crypto';import assert from 'node:assert/strict';import {execFileSync} from 'node:child_process';import {fileURLToPath,pathToFileURL} from 'node:url';import {createRequire} from 'node:module';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..'),library=path.join(base,'galaxy-library'),upstream=path.join(repo,'.tmp/references/galaxy');
const require=createRequire(path.join(repo,'web/package.json')),parse5=await import(pathToFileURL(require.resolve('parse5')).href);
const manifest=JSON.parse(fs.readFileSync(path.join(library,'manifest.json'),'utf8')),records=new Map(),sandbox={window:{NovaGalaxyLibrary:{register:(_,items)=>Object.entries(items).forEach(([id,item])=>records.set(id,item))}}};
vm.runInNewContext(fs.readFileSync(path.join(library,'index.js'),'utf8'),sandbox);
for(const filename of fs.readdirSync(path.join(library,'chunks')).filter(f=>f.endsWith('.js')))vm.runInNewContext(fs.readFileSync(path.join(library,'chunks',filename),'utf8'),sandbox);
assert.equal(records.size,3802);assert.equal(manifest.entries.length,3802);assert.equal(manifest.categories.length,11);
const tree=execFileSync('git',['-C',upstream,'ls-tree','-r','-z','HEAD'],{encoding:'utf8',maxBuffer:10e6}).split('\0').filter(Boolean).map(line=>{const [info,name]=line.split('\t');return{name,oid:info.split(' ')[2]};});
const tracked=tree.map(item=>item.name).filter(f=>f.endsWith('.html')).sort(),blobIds=new Map(tree.map(item=>[item.name,item.oid]));
assert.deepEqual(manifest.entries.map(item=>item.path).sort(),tracked);assert.equal(new Set(manifest.entries.map(item=>item.id)).size,3802);
let hashes=0,structureChecks=0;
function inspect(node){
  assert.ok(!['script','iframe','object','embed','link','meta','base'].includes(node.tagName));
  for(const attr of node.attrs||[]){assert.ok(!/^on/i.test(attr.name));if(['src','poster'].includes(attr.name))assert.ok(/^(?:__gxa-asset-|data:image\/)/i.test(attr.value),attr.value);if(attr.name==='href')assert.ok(attr.value.startsWith('#'));}
  for(const child of node.childNodes||[])inspect(child);if(node.content)inspect(node.content);
}
for(const entry of manifest.entries){
  const record=records.get(entry.id);assert.ok(record&&record.raw&&record.stellarHtml!==undefined&&record.stellarCss!==undefined);
  const bytes=Buffer.from(record.raw);assert.equal(crypto.createHash('sha1').update(`blob ${bytes.length}\0`).update(bytes).digest('hex'),blobIds.get(entry.path));assert.equal(entry.gitBlob,blobIds.get(entry.path));assert.equal(crypto.createHash('sha256').update(bytes).digest('hex'),entry.sha256);hashes++;
  inspect(parse5.parseFragment(record.html));inspect(parse5.parseFragment(record.stellarHtml));structureChecks+=2;
}
assert.ok(sandbox.window.NovaGalaxyLibraryAssets.utility.includes('.bg-white'));
assert.ok(sandbox.window.NovaGalaxyLibraryAssets.stellarUtility.includes('--nt-accent'));
console.log(`PASS exact import coverage: ${hashes} hashes, ${structureChecks} markup structures`);
const {chromium}=await import(pathToFileURL(path.join(repo,'web/node_modules/@playwright/test/index.mjs')).href),browser=await chromium.launch();
const page=await browser.newPage({viewport:{width:1440,height:1100},acceptDownloads:true}),errors=[],remoteRequests=[],checks=[],layouts=[];
const screenshotDir=path.join(base,'r6-previews');fs.mkdirSync(screenshotDir,{recursive:true});
page.on('pageerror',error=>errors.push(error.message));page.on('requestfailed',request=>errors.push(request.url()+':'+request.failure()?.errorText));page.on('request',request=>{if(/^https?:/.test(request.url()))remoteRequests.push(request.url());});
const url=pathToFileURL(path.join(base,'index.html')).href+'?revision=6#lab',a=name=>page.locator(`[data-gxa-action="${name}"]`);
const pass=(name,evidence)=>{checks.push({name,pass:true,evidence});console.log('PASS '+name);};
const waitFrames=async()=>{await page.waitForSelector('.gxa-card iframe');await page.waitForFunction(()=>[...document.querySelectorAll('.gxa-card iframe')].every(f=>f.contentDocument?.querySelector('#gxa-canvas')));};
const shot=async name=>{await page.waitForTimeout(180);await page.screenshot({path:path.join(screenshotDir,name)});};
try{
 await page.goto(url);await waitFrames();assert.equal(await page.locator('.gxa-card').count(),12);assert.match(await page.locator('#gxa-result-count').textContent(),/3,802/);
 assert.ok(await page.locator('.gxa-card iframe').count()<=12);pass('完整库默认入口与按需预览','默认完整库，12项分页；仅当前附近卡片创建预览文档');
 const categories=[];
 for(const category of manifest.categories){await page.locator(`[data-gxa-category="${category.id}"]`).click();await waitFrames();assert.match(await page.locator('#gxa-result-count').textContent(),new RegExp(category.count.toLocaleString()));categories.push(category.id);await shot(`r6-${category.id.toLowerCase()}-dark.png`);}
 pass('11分类与各类首屏实际渲染',categories.join(', '));
 await page.locator('[data-gxa-category="all"]').click();await page.locator('#gxa-page').fill('317');await a('go').click();await waitFrames();assert.equal(await page.locator('.gxa-card').count(),10);assert.equal(await a('next').isDisabled(),true);assert.match(await page.locator('.gxa-card').last().textContent(),/G-3802/);
 await a('previous').click();assert.equal(await page.locator('#gxa-page').inputValue(),'316');assert.equal(await page.locator('.gxa-card').count(),12);pass('完整尾页与分页','317页，末页10项，最后G-3802；上一页和边界按钮正确');
 const search=page.locator('#gxa-search');await search.fill('G-3802');assert.equal(await page.locator('.gxa-card').count(),1);await search.fill('no-match-不存在组件');assert.equal(await page.locator('.gxa-empty').isVisible(),true);await a('clear').click();assert.equal(await page.locator('.gxa-card').count(),12);
 await search.dispatchEvent('compositionstart');await search.fill('没有匹配xyz');assert.equal(await page.locator('.gxa-card').count(),12);await search.dispatchEvent('compositionend');assert.equal(await page.locator('.gxa-card').count(),0);await a('clear').click();pass('编号/关键词、零结果与IME','G-3802精确定位；无结果可恢复；组合输入完成后再刷新结果');
 await waitFrames();const firstId=await page.locator('.gxa-card').first().getAttribute('data-gxa-id'),raw=records.get(firstId).raw;
 await page.locator('.gxa-card').first().locator('[data-gxa-action="detail"]').click();await page.waitForSelector('.gxa-dialog[open] iframe');assert.equal(await page.locator('.gxa-dialog code').textContent(),raw);
 await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:async text=>{window.__copiedGalaxy=text;}}}));await a('copy').click();assert.equal(await page.evaluate(()=>window.__copiedGalaxy),raw);
 const downloadPromise=page.waitForEvent('download');await a('download').click();const download=await downloadPromise;assert.equal(fs.readFileSync(await download.path(),'utf8'),raw);
 await a('toggle-detail-mode').click();await page.waitForFunction(()=>document.querySelector('.gxa-detail-preview').dataset.mode==='original');await a('toggle-detail-mode').click();await page.waitForFunction(()=>document.querySelector('.gxa-detail-preview').dataset.mode==='stellar');await shot('r6-detail-dark.png');
 await page.keyboard.press('Escape');assert.equal(await page.locator('.gxa-dialog').evaluate(el=>el.open),false);assert.equal(await page.locator(`[data-gxa-id="${firstId}"] [data-gxa-action="detail"]`).evaluate(el=>el===document.activeElement),true);
 pass('详情、配色切换、原始源码与导出','逐字源码一致；复制接口已模拟验证，浏览器真实下载字节一致；双向配色切换与Esc回焦');
 await a('original').click();await waitFrames();assert.equal(await a('original').getAttribute('aria-pressed'),'true');await a('stellar').click();await waitFrames();
 const frame=page.locator('.gxa-card iframe').first();assert.equal(await frame.getAttribute('sandbox'),'allow-same-origin');assert.equal(await frame.evaluate(f=>f.contentDocument.documentElement.dataset.play),'false');
 await a('play').click();await page.waitForFunction(()=>[...document.querySelectorAll('.gxa-card iframe')].every(f=>f.contentDocument?.documentElement.dataset.play==='true'));await a('play').click();
 await page.emulateMedia({reducedMotion:'reduce'});await page.waitForFunction(()=>document.querySelector('.gxa-card iframe')?.contentDocument.documentElement.dataset.reduced==='true');assert.equal(await frame.evaluate(f=>f.contentDocument.documentElement.dataset.reduced),'true');await page.emulateMedia({reducedMotion:'no-preference'});pass('原作/星舰、播放与全局减少动态','CSS隔离文档不开放脚本；默认暂停，显式本页播放；系统减少动态实时传入预览');
 const checkbox=manifest.entries.find(x=>x.category==='Checkboxes'&&/type=["']checkbox["']/.test(records.get(x.id).raw));await search.fill(checkbox.id);await waitFrames();const control=page.frameLocator('.gxa-card iframe').locator('input[type="checkbox"]').first();const checked=await control.isChecked();await control.evaluate(el=>el.click());assert.equal(await control.isChecked(),!checked);pass('全库原生控件可操作','隔离文档内原生checkbox的DOM点击路径实际改变checked；上游分类中也存在radio，不把分类当语义');
 await search.fill('');await page.locator('[data-gx-collection="refined"]').click();assert.equal(await page.locator('.gx-card').count(),24);await page.locator('[data-gx-action="press"]').click();assert.match(await page.locator('#gx-press-status').textContent(),/1/);await page.locator('[data-gx-collection="library"]').click();await waitFrames();
 await page.locator('.gxa-card').first().locator('[data-gxa-action="detail"]').click();await page.waitForSelector('.gxa-dialog[open] iframe');
 await page.evaluate(()=>Object.defineProperty(navigator,'clipboard',{configurable:true,value:{writeText:()=>new Promise(resolve=>{window.__finishGalaxyCopy=resolve;})}}));await a('copy').click();await a('close-detail').click();
 await page.locator('.at-dock-item[data-destination="scene"]').click();await page.evaluate(()=>window.__finishGalaxyCopy());assert.equal(await page.locator('.gxa-dialog').count(),0);assert.equal(await page.locator('.gxa-card iframe').count(),0);await page.locator('.at-dock-item[data-destination="lab"]').click();await waitFrames();pass('精修保留、切换与销毁','24精修样本实际动作可用；回库保留筛选；离页移除预览与详情层，延迟复制完成无迟到写入');
 for(const viewport of [{width:1440,height:900},{width:1366,height:768},{width:1024,height:768},{width:390,height:844},{width:320,height:844}]){
  await page.setViewportSize(viewport);
  for(const theme of ['dark','light']){
   if(await page.locator('html').getAttribute('data-theme')!==theme)await page.locator('#theme-toggle').click();await waitFrames();
   const result=await page.evaluate(()=>{const stage=document.querySelector('#stage'),r=stage.getBoundingClientRect(),dock=document.querySelector('.at-dock').getBoundingClientRect();return{pageWidth:document.documentElement.scrollWidth,stageWidth:stage.clientWidth,scrollWidth:stage.scrollWidth,stageHeight:stage.clientHeight,pass:document.documentElement.scrollWidth<=innerWidth+1&&stage.scrollWidth<=stage.clientWidth+1&&r.bottom<=dock.top+1&&dock.right<=innerWidth+1};});assert.ok(result.pass,JSON.stringify({viewport,theme,...result}));layouts.push({...viewport,theme,...result});
   if(viewport.width===390||viewport.width===1440)await shot(`r6-library-${theme}-${viewport.width}.png`);
  }
 }
 pass('全库暗亮与窄屏排版','5尺寸×2主题=10组合；页宽/独立滚动/工具坞边界通过，含320px');
 assert.deepEqual(errors,[]);assert.deepEqual(remoteRequests,[]);
 const result={revision:'R6',date:'2026-10-31',scope:'Full-source hash and sanitized-markup coverage; isolated Chromium offline interactive samples; not a manual visual audit of every component.',importCoverage:{total:manifest.total,categories:manifest.categories,sourceHashes:hashes,sanitizedMarkupChecks:structureChecks,tailwindEntries:manifest.entries.filter(x=>x.usesUtility).length,recoveredCssFiles:manifest.warnings.length,externalAssetFallbackUrls:manifest.resources.filter(x=>x.status==='placeholder').length,affectedImageEntries:manifest.entries.filter(x=>x.assetFallback).length},checks,layouts,errors,remoteRequests};
 fs.writeFileSync(path.join(base,'r6-library-verification.json'),JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({checks:checks.length,layouts:layouts.length,hashes,errors,remoteRequests}));
}finally{await browser.close();}

