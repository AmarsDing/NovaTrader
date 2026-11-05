/* Top-layer positioning, fallback and pointer/touch paths for R6. Isolated browser. */
import fs from 'node:fs';import path from 'node:path';import assert from 'node:assert/strict';
import {fileURLToPath,pathToFileURL} from 'node:url';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..');
const {chromium}=await import(pathToFileURL(path.join(repo,'web/node_modules/@playwright/test/index.mjs')).href);
const browser=await chromium.launch(),url=pathToFileURL(path.join(base,'index.html')).href+'?revision=6#lab';
const errors=[],positions=[],fallback=[],screenshots=[];
const presets=[['native','open-layout','gw-layout-pop'],['combobox','open-combo','gw-combo-pop'],['multiselect','open-tags','gw-tags-pop'],['splitmenu','open-menu','gw-menu-pop']];
const page=await browser.newPage({viewport:{width:1440,height:1000}});page.on('pageerror',e=>errors.push(e.message));
const shot=async name=>{await page.waitForTimeout(240);await page.screenshot({path:path.join(base,'r6-previews',name)});screenshots.push(name);};
try {
  await page.goto(url);
  await page.locator('[data-gx-collection="refined"]').click();
  for(const size of [{width:1440,height:1000},{width:390,height:844},{width:320,height:844}]){
    await page.setViewportSize(size);
    for(const theme of ['dark','light']){
      if(await page.locator('html').getAttribute('data-theme')!==theme)await page.locator('#theme-toggle').click();
      for(const [key,action,id] of presets){
        await page.locator(`[data-gx-focus="${key}"]`).click();
        await page.locator(`[data-gw-action="${action}"]`).click();await page.waitForTimeout(220);
        const result=await page.locator('#'+id).evaluate(el=>{const r=el.getBoundingClientRect(),stage=document.querySelector('#stage').getBoundingClientRect(),center=document.elementFromPoint(r.left+r.width/2,r.top+Math.min(r.height/2,60));return {left:r.left,right:r.right,top:r.top,bottom:r.bottom,topLayer:el.matches(':popover-open'),hit:el.contains(center),pass:r.left>=11&&r.right<=innerWidth-11&&r.top>=stage.top+7&&r.bottom<=stage.bottom-7};});
        assert.ok(result.pass&&result.topLayer&&result.hit,JSON.stringify({size,theme,key,...result}));
        positions.push({...size,theme,key,...result});
        if(key==='combobox'){
          const before=await page.locator('#gw-combo').inputValue();
          await page.locator('[data-gw-option="standby"]').dispatchEvent('click');assert.equal(await page.locator('#gw-combo').inputValue(),before);
        }
        if(size.width===390||size.width===1440)await shot(`r6-${key}-${theme}-${size.width}.png`);
        await page.keyboard.press('Escape');await page.locator('[data-gx-action="exit-focus"]').click();
      }
    }
  }
  await page.setViewportSize({width:1440,height:1000});
  await page.locator('[data-gx-focus="interval"]').click();
  const slider=page.locator('#gw-lower');await slider.scrollIntoViewIfNeeded();
  const b=await slider.boundingBox();
  const centerX=value=>b.x+22+(b.width-44)*value/100;
  await page.mouse.move(centerX(25),b.y+22);await page.mouse.down();await page.mouse.move(centerX(45),b.y+22,{steps:8});await page.mouse.up();
  const dragged=Number(await slider.inputValue());assert.ok(dragged>=43&&dragged<=47,`actual drag=${dragged}`);
  await shot('r6-interval-pointer.png');
  await page.locator('[data-gx-action="exit-focus"]').click();
  const fallbackPage=await browser.newPage({viewport:{width:390,height:844}});fallbackPage.on('pageerror',e=>errors.push(e.message));
  await fallbackPage.addInitScript(()=>Object.defineProperty(HTMLElement.prototype,'showPopover',{configurable:true,value:undefined}));
  await fallbackPage.goto(url);
  await fallbackPage.locator('[data-gx-collection="refined"]').click();
  for(const [key,action,id] of presets){
    await fallbackPage.locator(`[data-gx-focus="${key}"]`).click();await fallbackPage.locator(`[data-gw-action="${action}"]`).click();
    assert.equal(await fallbackPage.locator('#'+id).isVisible(),true);
    assert.equal(await fallbackPage.locator('#'+id).getAttribute('popover'),null);
    if(key==='native'){await fallbackPage.locator('[data-gw-layout="compact"]').click();assert.equal(await fallbackPage.locator('#gw-layout').getAttribute('value'),'compact');}
    else if(key==='combobox'){await fallbackPage.locator('[data-gw-option="small"]').click();assert.equal(await fallbackPage.locator('#gw-combo').inputValue(),'轻量分析');}
    else await fallbackPage.keyboard.press('Escape');
    await fallbackPage.locator('[data-gx-action="exit-focus"]').click();fallback.push({key,pass:true});
  }
  await fallbackPage.close();
  const touch=await browser.newContext({viewport:{width:390,height:844},isMobile:true,hasTouch:true});const mobile=await touch.newPage();mobile.on('pageerror',e=>errors.push(e.message));await mobile.goto(url);
  await mobile.locator('[data-gx-collection="refined"]').tap();
  await mobile.locator('[data-gx-focus="native"]').tap();await mobile.locator('#gw-layout').tap();await mobile.locator('[data-gw-layout="spacious"]').tap();assert.equal(await mobile.locator('#gw-layout').getAttribute('value'),'spacious');await mobile.locator('[data-gx-action="exit-focus"]').tap();
  await mobile.locator('[data-gx-focus="combobox"]').tap();await mobile.locator('[data-gw-action="open-combo"]').tap();await mobile.locator('[data-gw-option="embedding"]').tap();assert.equal(await mobile.locator('#gw-combo').inputValue(),'文档嵌入');
  await mobile.locator('[data-gx-action="exit-focus"]').tap();await mobile.locator('[data-gx-focus="multiselect"]').tap();await mobile.locator('[data-gw-action="open-tags"]').tap();await mobile.locator('[data-gw-tag="volume"]').tap();assert.equal(await mobile.locator('[data-gw-tag="volume"]').isChecked(),true);
  await mobile.locator('[data-gw-action="open-tags"]').tap();
  const targets=await mobile.locator('.gw-widget button:visible,.gw-widget input:visible,.gw-widget select:visible').evaluateAll(items=>items.map(el=>({id:el.id,target:(el.type==='checkbox'?el.closest('label'):el).getBoundingClientRect().height})).filter(item=>item.target<43.5));assert.deepEqual(targets,[]);
  await mobile.screenshot({path:path.join(base,'r6-previews/r6-touch-tags.png')});screenshots.push('r6-touch-tags.png');await touch.close();
  assert.deepEqual(errors,[]);
  const result={revision:'R6',environment:'Isolated headless Chromium, offline prototype; simulated missing Popover method and mobile touch.',positions,fallback,pointerRange:{value:dragged,pass:true},touchSelection:{combobox:true,multiselect:true,effectiveTargets:targets,pass:true},errors,screenshots};
  fs.writeFileSync(path.join(base,'r6-menu-verification.json'),JSON.stringify(result,null,2)+'\n');console.log(JSON.stringify({positions:positions.length,fallback:fallback.length,dragged,touch:true,errors}));
}finally{await browser.close();}

