/* ⚠️ 警告：本脚本会覆写 archive/ 下两份已冻结的历史文档（前端UI-README-2026-11-02.md、验证与来源-2026-11-02.md）
   以及本目录的 galaxy-source-manifest.json。仅供重建 2026-11-02 合并快照的来源证据时使用。
   默认 dry-run 不写盘；必须显式传 --apply 才会执行任何写入。 */
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import {fileURLToPath} from 'node:url';
import {execFileSync} from 'node:child_process';
const base=fileURLToPath(new URL('..',import.meta.url)),repo=path.resolve(base,'../../../../..');
const APPLY=process.argv.includes('--apply');
const upstream=path.join(repo,'.tmp/references/galaxy'),sandbox={window:{}};
vm.runInNewContext(fs.readFileSync(path.join(base,'galaxy-catalog.js'),'utf8'),sandbox);
const catalog=sandbox.window.NovaGalaxyCatalog;
const categories=fs.readdirSync(upstream,{withFileTypes:true}).filter(item=>item.isDirectory()&&!item.name.startsWith('.')).map(item=>({name:item.name,files:fs.readdirSync(path.join(upstream,item.name)).filter(name=>name.endsWith('.html')).length}));
const selected=catalog.components.map(item=>{
  const raw=execFileSync('git',['-C',upstream,'show',`${catalog.revision}:${item.source}`],{maxBuffer:2e6});
  assert.ok(raw.toString('utf8').includes(`by ${item.author}`),`${item.id}: author mismatch`);
  return {id:item.id,key:item.key,title:item.title,author:item.author,path:item.source,sha256:crypto.createHash('sha256').update(raw).digest('hex'),sourceUrl:`${catalog.repository}/blob/${catalog.revision}/${item.source}`,adaptation:item.adaptation};
});
const uniqueFiles=new Set(selected.map(item=>item.path)).size;
const manifest={repository:catalog.repository,revision:catalog.revision,designRevision:catalog.designRevision,researchedAt:catalog.researchedAt,license:catalog.license,scope:'Indexed repository HTML categories; read selected snippets and compared alternatives. Did not review every snippet. Multiple adapted components may share one source.',categories,totalHtml:categories.reduce((sum,item)=>sum+item.files,0),uniqueSelectedFiles:uniqueFiles,selected};
if(!APPLY){console.log(JSON.stringify({mode:'dry-run',hint:'传 --apply 才会写盘',wouldWrite:['galaxy-source-manifest.json','archive/验证与来源-2026-11-02.md(galaxy-sources 段)','archive/前端UI-README-2026-11-02.md(第5节)'],components:selected.length}));process.exit(0);}
fs.writeFileSync(path.join(base,'galaxy-source-manifest.json'),JSON.stringify(manifest,null,2)+'\n');
const lines=['| 编号 | 改编组件 | 社区作者 | 固定版本源码 | 改编范围 |','| --- | --- | --- | --- | --- |',...selected.map(item=>`| ${item.id} | ${item.title} | ${item.author} | [${item.path}](${item.sourceUrl}) | ${item.adaptation} |`)];
const evidenceFile=path.resolve(base,'../../archive/验证与来源-2026-11-02.md'),evidence=fs.readFileSync(evidenceFile,'utf8');
assert.ok(evidence.includes('<!-- galaxy-sources:start -->')&&evidence.includes('<!-- galaxy-sources:end -->'));
fs.writeFileSync(evidenceFile,evidence.replace(/<!-- galaxy-sources:start -->[\s\S]*?<!-- galaxy-sources:end -->/,'<!-- galaxy-sources:start -->\n'+lines.join('\n')+'\n<!-- galaxy-sources:end -->'));
const specFile=path.resolve(base,'../../archive/前端UI-README-2026-11-02.md');
let spec=fs.readFileSync(specFile,'utf8');
const start=spec.indexOf('## 5. 逐组件开发规范'),end=spec.indexOf('## 6. 生产实施与验收');
assert.ok(start>=0&&end>start);
const section=['## 5. 逐组件开发规范','','下列24项与演示“规范”弹层来自同一份galaxy-catalog.js。尺寸指标准密度候选；紧凑与触屏遵守统一下限。来源是视觉参考，新增业务与状态行为见“相对原作”。','',
...catalog.components.map(item=>`### ${item.id} ${item.title}\n\n来源：[${item.author} / ${item.source}](${catalog.repository}/blob/${catalog.revision}/${item.source})。\n\n| 维度 | 合同 |\n| --- | --- |\n| 样式 | ${item.style} |\n| 行为/状态 | ${item.behavior} |\n| 动作/时长 | ${item.motion} |\n| 键盘/触屏 | ${item.keyboard} |\n| 节能/减少动态 | ${item.fallback} |\n| 项目用途 | ${item.use} |\n| 相对原作 | ${item.adaptation} |`),'',''].join('\n');
spec=spec.slice(0,start)+section+spec.slice(end);
fs.writeFileSync(specFile,spec);
console.log(JSON.stringify({components:selected.length,uniqueSourceFiles:uniqueFiles,revision:catalog.revision}));
