/* Bounded offline load smoke for every imported Stellar preview. Not visual acceptance. */
import fs from 'node:fs';import path from 'node:path';import assert from 'node:assert/strict';import {fileURLToPath,pathToFileURL} from 'node:url';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..'),{chromium}=await import(pathToFileURL(path.join(repo,'web/node_modules/@playwright/test/index.mjs')).href);
const browser=await chromium.launch(),page=await browser.newPage({viewport:{width:1440,height:900}}),errors=[],remoteRequests=[];
page.on('pageerror',error=>errors.push(error.message));page.on('request',request=>{if(/^https?:/.test(request.url()))remoteRequests.push(request.url());});
try{
 await page.goto(pathToFileURL(path.join(base,'index.html')).href+'?revision=6#lab');await page.waitForSelector('.gxa-card iframe');
 const total=await page.evaluate(()=>NovaGalaxyLibraryIndex.total),results=[];
 for(let start=0;start<total;start+=48){
  const batch=await page.evaluate(async start=>{
   const api=NovaGalaxyLibrary,entries=NovaGalaxyLibraryIndex.entries.slice(start,start+48),result=[];
   const holder=document.createElement('div');holder.style.cssText='position:fixed;left:0;top:-2000px;width:480px;height:360px;overflow:hidden';document.body.append(holder);
   for(let i=0;i<entries.length;i+=6){
    await Promise.all(entries.slice(i,i+6).map(async item=>{
     const record=await api.load(item),frame=document.createElement('iframe');frame.width='480';frame.height='360';frame.setAttribute('sandbox','allow-same-origin');
     const status=await new Promise(resolve=>{const timer=setTimeout(()=>resolve({id:item.id,path:item.path,pass:false,reason:'timeout'}),6000);frame.onload=()=>{clearTimeout(timer);try{api.prepareFrame(frame);const d=frame.contentDocument;resolve({id:item.id,path:item.path,pass:Boolean(d?.querySelector('#gxa-canvas')&&d?.styleSheets.length>=3),elements:d?.querySelector('#gxa-mount')?.querySelectorAll('*').length||0,styles:d?.styleSheets.length||0});}catch(error){resolve({id:item.id,path:item.path,pass:false,reason:error.message});}};frame.srcdoc=api.documentFor(item,record,{mode:'stellar'});holder.append(frame);});result.push(status);frame.remove();
    }));
   }holder.remove();return result;
  },start);
  results.push(...batch);if(start%480===0)console.log(`Loaded ${results.length}/${total}`);
 }
 assert.equal(results.length,3802);assert.deepEqual(results.filter(item=>!item.pass),[]);assert.deepEqual(errors,[]);assert.deepEqual(remoteRequests,[]);
 const result={revision:'R6',date:'2026-10-31',scope:'All 3802 Stellar srcdoc previews loaded in isolated Chromium, at most six probe documents at once. Checks DOM/style-sheet construction only; no pixel comparison, per-state manual audit or production acceptance.',count:results.length,failures:results.filter(item=>!item.pass),errors,remoteRequests,results};
 fs.writeFileSync(path.join(base,'r6-library-render-verification.json'),JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({count:results.length,failures:result.failures,errors,remoteRequests}));
}finally{await browser.close();}

