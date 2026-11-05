/* Reproducible import of every tracked Galaxy HTML fragment. No production dependencies. */
import fs from 'node:fs';import path from 'node:path';import crypto from 'node:crypto';import assert from 'node:assert/strict';
import {execFileSync} from 'node:child_process';import {createRequire} from 'node:module';import {fileURLToPath,pathToFileURL} from 'node:url';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..'),upstream=path.join(repo,'.tmp/references/galaxy'),out=path.join(base,'galaxy-library');
const req=createRequire(path.join(repo,'web/package.json')),buildReq=createRequire(path.join(repo,'.tmp/galaxy-import-tools/package.json'));
const postcss=req('postcss'),parse5=await import(pathToFileURL(req.resolve('parse5')).href),valueParser=buildReq('postcss-value-parser'),safeParser=buildReq('postcss-safe-parser'),tailwind=buildReq('tailwindcss'),{parseColor}=buildReq('tailwindcss/lib/util/color.js');
const sha=bytes=>crypto.createHash('sha256').update(bytes).digest('hex');
const revision=execFileSync('git',['-C',upstream,'rev-parse','HEAD'],{encoding:'utf8'}).trim();
const files=execFileSync('git',['-C',upstream,'ls-tree','-r','--name-only','-z','HEAD'],{encoding:'utf8',maxBuffer:10e6}).split('\0').filter(file=>file.endsWith('.html')).sort();
const categoryNames={'Buttons':'按钮','Cards':'卡片','Checkboxes':'复选框','Forms':'表单','Inputs':'输入框','Notifications':'通知','Patterns':'背景纹理','Radio-buttons':'单选组','Toggle-switches':'开关','Tooltips':'提示气泡','loaders':'加载动画'};
fs.mkdirSync(out,{recursive:true});fs.mkdirSync(path.join(out,'chunks'),{recursive:true});
const blobStream=execFileSync('git',['-C',upstream,'cat-file','--batch'],{input:files.map(file=>`HEAD:${file}`).join('\n')+'\n',maxBuffer:40e6});
let blobOffset=0;
const records=files.map(file=>{const end=blobStream.indexOf(10,blobOffset),[oid,type,length]=blobStream.subarray(blobOffset,end).toString('utf8').split(' ');assert.equal(type,'blob');const start=end+1,bytes=blobStream.subarray(start,start+Number(length));blobOffset=start+Number(length)+1;return{path:file,raw:bytes.toString('utf8'),gitBlob:oid};});
const assetUrls=new Set(),assets={},resourceReport=[],warnings=[];
const cacheFile=path.join(repo,'.tmp/galaxy-import-tools/resource-cache.json');
const resourceCache=fs.existsSync(cacheFile)?JSON.parse(fs.readFileSync(cacheFile,'utf8')):{};
const normalizeUrl=url=>{try{return new URL(url,'https://uiverse.io/').href;}catch{return null;}};
const resourceToken=url=>`gxa-asset-${sha(url).slice(0,16)}`;
function visit(node,fn){fn(node);for(const child of node.childNodes||[])visit(child,fn);if(node.content)visit(node.content,fn);}
function cssUrls(css,fn){return valueParser(css).walk(node=>{if(node.type==='function'&&node.value.toLowerCase()==='url'){const url=valueParser.stringify(node.nodes).replace(/^["']|["']$/g,'').trim();fn(url,node);return false;}}).toString();}
for(const record of records){
  const doc=parse5.parseFragment(record.raw);visit(doc,node=>{
    for(const attr of node.attrs||[])if(['src','poster'].includes(attr.name)&&attr.value&&!/^(?:data:|#)/i.test(attr.value)){const url=normalizeUrl(attr.value);if(url)assetUrls.add(url);}
    const values=(node.attrs||[]).filter(a=>a.name==='style').map(a=>a.value);if(node.tagName==='style')values.push((node.childNodes||[]).map(n=>n.value||'').join(''));
    for(const css of values)cssUrls(css,url=>{if(url&&!/^(?:data:|#|var\()/i.test(url)){const normalized=normalizeUrl(url);if(normalized)assetUrls.add(normalized);}});
  });
}
const placeholder=`data:image/svg+xml,${encodeURIComponent('<svg xmlns="http://www.w3.org/2000/svg" width="320" height="220" viewBox="0 0 320 220"><rect width="320" height="220" rx="24" fill="#10263a"/><circle cx="160" cy="93" r="43" fill="none" stroke="#45e0ff" stroke-opacity=".55"/><path d="m110 156 50-88 50 88z" fill="none" stroke="#6c91ff"/><text x="160" y="196" fill="#b9d3e5" text-anchor="middle" font-family="sans-serif" font-size="12">LOCAL IMAGE PLACEHOLDER</text></svg>')}`;
let assetCursor=0;
await Promise.all(Array.from({length:6},async()=>{const urls=[...assetUrls];while(assetCursor<urls.length){const url=urls[assetCursor++],key=resourceToken(url);try{
  if(resourceCache[url]?.record?.status==='bundled'){assets[key]=resourceCache[url].data;resourceReport.push(resourceCache[url].record);continue;}
  const response=await fetch(url,{signal:AbortSignal.timeout(8000),headers:{'User-Agent':'NovaTrader-Galaxy-Offline-Importer/1.0'}});
  const type=(response.headers.get('content-type')||'').split(';')[0];if(!response.ok||!/^image\/(png|jpeg|gif|webp|svg\+xml|avif|x-icon)$/.test(type))throw Error(`HTTP ${response.status}, ${type||'unknown type'}`);
  const data=Buffer.from(await response.arrayBuffer());if(data.length>3e6)throw Error('image exceeds 3 MB');assets[key]=`data:${type};base64,${data.toString('base64')}`;const record={url,key,status:'bundled',type,bytes:data.length,sha256:sha(data)};resourceReport.push(record);resourceCache[url]={record,data:assets[key]};
}catch(error){assets[key]=placeholder;resourceReport.push({url,key,status:'placeholder',reason:error.message});}}}));
fs.writeFileSync(cacheFile,JSON.stringify(resourceCache));
function cleanUrls(value){return cssUrls(value,(url,node)=>{if(!url||url.startsWith('#')||/^data:image\//i.test(url))return;const normalized=normalizeUrl(url);node.nodes=[{type:'string',quote:'"',value:normalized?`__${resourceToken(normalized)}__`:placeholder}];});}
function colorToken(color,prop,selector=''){
  const parsed=parseColor(color.replace(/var\((--[\w-]+),\s*[^()]*\)/g,'var($1)'));if(!parsed)return null;
  let channels=parsed.color.map(parseFloat);if(channels.some(x=>!Number.isFinite(x)))return null;
  if(parsed.mode==='hsl'){
    const [h,s,l]=parsed.color;const hue=parseFloat(h)/360,sat=parseFloat(s)/100,lum=parseFloat(l)/100;
    const f=n=>{const k=(n+hue*12)%12,a=sat*Math.min(lum,1-lum);return 255*(lum-a*Math.max(-1,Math.min(k-3,9-k,1)));};channels=[f(0),f(8),f(4)];
  }
  const [r,g,b]=channels,max=Math.max(...channels),min=Math.min(...channels),neutral=max-min<35,luma=(r*.2126+g*.7152+b*.0722)/255;
  const bg=/background|shadow|^--tw-(?:gradient|bg|shadow)/i.test(prop),border=/border|outline|ring|stroke/i.test(prop);
  let token;
  if(/invalid|error|danger/i.test(selector+' '+prop))token='--nt-danger';
  else if(neutral)token=bg?(luma>.7?'--nt-bg-3':'--nt-bg-2'):border?'--nt-accent-border':luma>.8?'--nt-text-0':luma>.35?'--nt-text-2':'--nt-text-1';
  else token=b>r*1.1&&b>g*1.1?'--nt-accent-2':'--nt-accent';
  const baseColor=!neutral&&bg?`color-mix(in srgb,var(${token}) ${Math.round(35+luma*60)}%,var(--nt-bg-2))`:`var(${token})`;
  const alpha=parsed.alpha?.startsWith('var(')?color.match(/var\([^)]*\)/)?.[0]||parsed.alpha:parsed.alpha;
  if(alpha&&alpha!=='1')return `color-mix(in srgb,${baseColor} ${alpha.endsWith('%')?alpha:Number.isFinite(Number(alpha))?Math.round(Number(alpha)*10000)/100+'%':`calc((${alpha}) * 100%)`},transparent)`;
  return baseColor;
}
function themedValue(value,prop,selector,variables=new Set()){
  const parsed=valueParser(value);parsed.walk(node=>{
    if(node.type==='function'&&['rgb','rgba','hsl','hsla'].includes(node.value.toLowerCase())){const replacement=colorToken(valueParser.stringify(node),prop,selector);if(replacement){node.type='word';node.value=replacement;delete node.nodes;}return false;}
    if(node.type==='function'&&node.value.toLowerCase()==='url')return false;
    if(node.type==='function'&&node.value.toLowerCase()==='var'){
      const name=node.nodes.find(n=>n.type==='word');
      if(name&&variables.has(name.value))name.value=`--gxa-${paletteRole(prop)}-${name.value.slice(2)}`;
    }
    if(node.type==='word'){const replacement=colorToken(node.value,prop,selector);if(replacement)node.value=replacement;}
  });return parsed.toString();
}
function paletteRole(prop){return /border|outline|ring/.test(prop)?'edge':/background|shadow|gradient|filter/.test(prop)?'surface':'ink';}
function colorProperty(prop){return /^(?:background(?:-.+)?|color|border(?:-.+)?|outline(?:-.+)?|(?:box|text)-shadow|text-decoration(?:-color)?|caret-color|accent-color|column-rule(?:-.+)?|scrollbar-color|filter|backdrop-filter)$/i.test(prop);}
function paletteVariables(css){
  const root=safeParser(css),definitions=[];root.walkDecls(decl=>{if(decl.prop.startsWith('--'))definitions.push(decl);});
  const found=new Set();for(const decl of definitions){let color=false;valueParser(decl.value).walk(node=>{if(node.type==='function'&&['url','var'].includes(node.value.toLowerCase()))return false;if((node.type==='word'&&parseColor(node.value))||(node.type==='function'&&['rgb','rgba','hsl','hsla'].includes(node.value.toLowerCase())))color=true;});if(color)found.add(decl.prop);}
  for(let pass=0;pass<12;pass++){let changed=false;for(const decl of definitions)if(!found.has(decl.prop)&&[...decl.value.matchAll(/var\((--[\w-]+)/g)].some(m=>found.has(m[1]))){found.add(decl.prop);changed=true;}if(!changed)break;}
  return found;
}
function processCss(css,theme,file,variables){
  let root;try{root=postcss.parse(css);}catch(error){if(!warnings.some(item=>item.path===file))warnings.push({path:file,kind:'css-recovered',reason:error.reason});root=safeParser(css);}
  root.walkAtRules(rule=>{if(['import','font-face','charset'].includes(rule.name.toLowerCase()))rule.remove();});
  const palette=variables||paletteVariables(css),declarations=[];root.walkDecls(decl=>declarations.push(decl));
  for(const decl of declarations){
    decl.value=cleanUrls(decl.value);if(!theme)continue;
    if(decl.prop.startsWith('--')){
      if(palette.has(decl.prop))for(const [role,prop]of [['surface','background'],['ink','color'],['edge','border-color']])decl.cloneAfter({prop:`--gxa-${role}-${decl.prop.slice(2)}`,value:themedValue(decl.value,prop,decl.parent.selector||'',palette)});
    }else if(colorProperty(decl.prop))decl.value=themedValue(decl.value,decl.prop,decl.parent.selector||'',palette);
  }
  return root.toString().replace(/<\/style/gi,'<\\/style');
}
function sanitize(record){
  const doc=parse5.parseFragment(record.raw),styles=[],flags={removedTags:0,removedAttributes:0,assets:[]};
  function clean(node){
    if(node.childNodes)node.childNodes=node.childNodes.filter(child=>{
      if(child.tagName==='style'){styles.push((child.childNodes||[]).map(n=>n.value||'').join(''));return false;}
      if(['script','iframe','object','embed','link','meta','base','audio','video','source','track'].includes(child.tagName)){flags.removedTags++;return false;}
      return child.nodeName!=='#comment';
    });
    if(node.attrs)node.attrs=node.attrs.filter(attr=>{
      if(/^on/i.test(attr.name)||['srcdoc','action','formaction','target','ping','download','autofocus','srcset','integrity','crossorigin'].includes(attr.name)){flags.removedAttributes++;return false;}
      if(['src','poster'].includes(attr.name)&&!/^data:image\//i.test(attr.value)){const url=normalizeUrl(attr.value);if(url){const key=resourceToken(url);attr.value=`__${key}__`;flags.assets.push(key);}else attr.value=placeholder;}
      if(attr.name==='href'&&!attr.value.startsWith('#'))attr.value='#';
      if(attr.name==='style')attr.value=cleanUrls(attr.value);
      return true;
    });
    for(const child of node.childNodes||[])clean(child);if(node.content)clean(node.content);
  }clean(doc);
  const html=parse5.serialize(doc),themedDoc=parse5.parseFragment(html),css=styles.join('\n'),inline=[];visit(doc,node=>{for(const attr of node.attrs||[])if(attr.name==='style')inline.push(`x{${attr.value}}`);});
  const variables=paletteVariables(css+'\n'+inline.join('\n'));
  visit(themedDoc,node=>{let ancestor=node,structural=false;while(ancestor){if(['mask','clipPath','filter','defs'].includes(ancestor.tagName)){structural=true;break;}ancestor=ancestor.parentNode;}if(structural)return;
    for(const attr of node.attrs||[]){if(attr.name==='style'){const processed=processCss(`x{${attr.value}}`,true,record.path,variables);attr.value=processed.slice(processed.indexOf('{')+1,processed.lastIndexOf('}'));}else if(['color','bgcolor'].includes(attr.name))attr.value=themedValue(attr.value,attr.name==='bgcolor'?'background':'color',node.tagName,variables);}
  });
  return {html,stellarHtml:parse5.serialize(themedDoc),css:processCss(css,false,record.path),stellarCss:processCss(css,true,record.path,variables),flags};
}
console.log(`Resources: ${resourceReport.filter(x=>x.status==='bundled').length} bundled, ${resourceReport.filter(x=>x.status==='placeholder').length} local placeholders. Compiling Tailwind…`);
const utilityConfig={content:records.map(r=>({raw:r.raw,extension:'html'})),darkMode:'class',theme:{extend:{}},plugins:[]};
const utility=(await postcss([tailwind(utilityConfig)]).process('@tailwind utilities;',{from:undefined})).css;
const utilityBase=(await postcss([tailwind(utilityConfig)]).process('@tailwind base;',{from:undefined})).css;
const metadata=[],chunks=[];let sourceBytes=0;
for(let i=0;i<records.length;i++){
  const record=records[i],category=record.path.split('/')[0],filename=path.basename(record.path,'.html'),author=filename.slice(0,filename.lastIndexOf('_'))||filename,id=`galaxy-${sha(record.path).slice(0,12)}`,chunk=String(Math.floor(i/64)).padStart(3,'0');
  const clean=sanitize(record),classText=[...record.raw.matchAll(/class=["']([^"']*)/g)].map(m=>m[1]).join(' '),tagsMatch=record.raw.match(/Tags:\s*([^\n]*?)(?:\*\/|-->)/i),tags=tagsMatch?tagsMatch[1].trim():'';
  const textParts=[];function collectText(node){if(node.tagName==='svg')return;if(node.nodeName==='#text')textParts.push(node.value);for(const child of node.childNodes||[])collectText(child);}collectText(parse5.parseFragment(clean.html));const text=textParts.join(' ').replace(/\s+/g,' ').trim();
  const features=[];if(/:hover|\bhover:/.test(record.raw))features.push('悬停');if(/:focus|\bfocus:/.test(record.raw))features.push('焦点');if(/:checked|peer-checked/.test(record.raw))features.push('选中');if(/animation(?:-name)?\s*:|animate-/.test(record.raw))features.push('动画');if(/rotate[XYZ]|preserve-3d|perspective/.test(record.raw))features.push('立体');if(/backdrop-filter|backdrop-blur/.test(record.raw))features.push('玻璃');if(/gradient/.test(record.raw))features.push('渐变');
  const sourceClasses=new Set([...clean.css.matchAll(/\.([a-zA-Z_][\w-]*)/g)].map(m=>m[1]));
  const utilityClasses=classText.split(/\s+/).filter(name=>!sourceClasses.has(name)&&/^(?:(?:hover|focus|dark|group-hover|peer-checked):)*(?:bg-|text-|rounded-|items-|justify-|p[xytrbl]?-|m[xytrbl]?-|w-|h-|flex$|grid$|relative$|absolute$)/.test(name));
  const utilityReset=!/<style\b/i.test(record.raw)||/tailwind/i.test(tags),usesUtility=utilityReset||utilityClasses.length>1;
  const dependencies=[...new Set([...clean.flags.assets,...[...((clean.css+' '+clean.html).matchAll(/__(gxa-asset-[\w]+)__/g))].map(m=>m[1])])];
  const meta={id,ordinal:i+1,category,author,name:filename.slice(filename.lastIndexOf('_')+1).replaceAll('-',' '),title:text.slice(0,46)||`${categoryNames[category]} · ${filename.slice(filename.lastIndexOf('_')+1)}`,path:record.path,chunk,gitBlob:record.gitBlob,sha256:sha(Buffer.from(record.raw)),bytes:Buffer.byteLength(record.raw),tags,features,usesUtility,dependencies,assetFallback:dependencies.some(key=>resourceReport.find(a=>a.key===key)?.status==='placeholder'),motion:[...record.raw.matchAll(/(?:animation|transition)(?:-duration)?\s*:\s*([^;}]+)/gi)].map(m=>m[1].trim()).slice(0,8)};
  meta.utilityReset=utilityReset;metadata.push(meta);(chunks[Number(chunk)]??={})[id]={raw:record.raw,...clean};sourceBytes+=meta.bytes;
}
const categories=Object.entries(categoryNames).map(([id,label])=>({id,label,count:metadata.filter(item=>item.category===id).length}));
const manifest={revision,repository:'https://github.com/uiverse-io/galaxy',verifiedRemoteHeadAt:'2026-10-31',license:'MIT',build:{tailwind:'3.4.17',valueParser:'4.2.0',safeParser:'7.0.1'},total:metadata.length,sourceBytes,categories,chunks:chunks.length,resources:resourceReport,warnings,entries:metadata};
for(let i=0;i<chunks.length;i++)fs.writeFileSync(path.join(out,'chunks',`${String(i).padStart(3,'0')}.js`),`/* Galaxy MIT sources; see ../LICENSE.txt. */\nwindow.NovaGalaxyLibrary.register(${JSON.stringify(String(i).padStart(3,'0'))},${JSON.stringify(chunks[i]).replace(/</g,'\\u003c')});\n`);
fs.copyFileSync(path.join(upstream,'LICENSE'),path.join(out,'LICENSE.txt'));fs.copyFileSync(buildReq.resolve('tailwindcss/LICENSE'),path.join(out,'TAILWIND-LICENSE.txt'));
fs.writeFileSync(path.join(out,'manifest.json'),JSON.stringify(manifest,null,2)+'\n');
fs.writeFileSync(path.join(out,'index.js'),`/* Full Galaxy archive index, ${revision}. MIT: LICENSE.txt. */\nwindow.NovaGalaxyLibraryIndex=${JSON.stringify({revision,repository:manifest.repository,total:metadata.length,categories,entries:metadata,resources:resourceReport,warnings})};\nwindow.NovaGalaxyLibraryAssets=${JSON.stringify({utility,utilityBase,stellarBase:processCss(utilityBase,true,'Tailwind base'),stellarUtility:processCss(utility,true,'Tailwind compiled utilities'),assets,placeholder}).replace(/</g,'\\u003c')};\n`);
assert.equal(metadata.length,files.length);assert.equal(new Set(metadata.map(m=>m.path)).size,files.length);
console.log(JSON.stringify({total:metadata.length,categories,chunks:chunks.length,sourceBytes,resources:resourceReport.length,assetFallbacks:resourceReport.filter(r=>r.status==='placeholder').length,warnings:warnings.length}));
