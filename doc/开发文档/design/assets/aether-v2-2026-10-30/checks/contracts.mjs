/* Static checks for the offline design artifact. Run from any working directory. */
import fs from 'node:fs';
import path from 'node:path';
import vm from 'node:vm';
import crypto from 'node:crypto';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
import { execFileSync } from 'node:child_process';
const base = fileURLToPath(new URL('..', import.meta.url));
const repo = path.resolve(base, '../../../../..');
const doc = path.join(repo, 'doc/开发文档');
const names = ['README.md','design/README.md','design/01-视觉风格.md','design/02-组件规范.md','design/03-设计令牌.md','design/04-落地检查清单.md','M12-桌面客户端/设计文档.md','M12-桌面客户端/开发进度.md'];
let links = 0;
const broken = [];
for (const name of names) {
  const filename = path.join(doc, name), source = fs.readFileSync(filename, 'utf8');
  assert.ok(!source.includes('\uFFFD'), `${name}: encoding replacement`);
  for (const match of source.matchAll(/!?\[[^\]]*\]\(([^)]+)\)/g)) {
    const target = match[1].split('#')[0].split('?')[0];
    if (!target || /^[a-z]+:/i.test(target)) continue;
    links++;
    const resolved=path.resolve(path.dirname(filename),decodeURIComponent(target));
    // This script creates its own linked result only after all assertions pass.
    if (!fs.existsSync(resolved)&&resolved!==path.join(base,'r6-static-verification.json')) broken.push({ name, target });
  }
}
assert.deepEqual(broken, []);
for (const name of ['aether.js','galaxy-lab.js','galaxy-catalog.js','galaxy-controls.js','galaxy-library.js','galaxy-preview.js','galaxy-library/index.js']) new vm.Script(fs.readFileSync(path.join(base, name), 'utf8'), { filename: name });
const sandbox = { window: {} };
vm.runInNewContext(fs.readFileSync(path.join(base, 'galaxy-catalog.js'), 'utf8'), sandbox);
const catalog = sandbox.window.NovaGalaxyCatalog;
assert.equal(catalog.components.length, 24);
assert.equal(new Set(catalog.components.map(item => item.id)).size, 24);
assert.equal(new Set(catalog.components.map(item => item.key)).size,24);
const document = fs.readFileSync(path.join(doc, 'design/archive/前端UI-README-2026-11-02.md'), 'utf8');
for (const item of catalog.components) for (const key of ['style','behavior','motion','keyboard','fallback','use','adaptation']) assert.ok(document.includes(item[key]), `${item.id}: stale ${key}`);
const sources = JSON.parse(fs.readFileSync(path.join(base, 'galaxy-source-manifest.json'), 'utf8'));
assert.equal(sources.revision, catalog.revision);
assert.equal(sources.selected.length, 24);
assert.equal(sources.uniqueSelectedFiles,21);
assert.equal(new Set(sources.selected.map(item=>item.path)).size,21);
let hashesChecked = 0;
for (const item of sources.selected) {
  assert.match(item.sha256, /^[0-9a-f]{64}$/);
  const cached = path.join(repo, '.tmp/references/galaxy', item.path);
  if (fs.existsSync(cached)) { const blob=execFileSync('git',['-C',path.join(repo,'.tmp/references/galaxy'),'show',`${catalog.revision}:${item.path}`],{maxBuffer:2e6});assert.equal(crypto.createHash('sha256').update(blob).digest('hex'), item.sha256); hashesChecked++; }
}
const license = fs.readFileSync(path.join(base, 'GALAXY-LICENSE.txt'), 'utf8');
assert.ok(license.includes('Copyright (c) 2023 Uiverse.io') && license.includes('MIT License'));
const colors = fs.readFileSync(path.join(repo, 'web/src/theme/tokens.css'), 'utf8') + fs.readFileSync(path.join(base, 'aether.css'), 'utf8');
const css = fs.readFileSync(path.join(base, 'galaxy-lab.css'), 'utf8') + fs.readFileSync(path.join(base, 'galaxy-controls.css'), 'utf8') + fs.readFileSync(path.join(base,'galaxy-library.css'),'utf8');
const undefinedTokens = [...new Set([...css.matchAll(/var\((--nt-[\w-]+)/g)].map(match => match[1]))].filter(token => !colors.includes(token + ':'));
assert.deepEqual(undefinedTokens, []);
assert.ok(!/#[0-9a-f]{3,8}\b/i.test(css), 'hardcoded external color');
assert.ok(!css.includes('var(--nt-data-inset)'), 'lab controls must not reuse the opaque data inset material');
const html = fs.readFileSync(path.join(base, 'index.html'), 'utf8');
const ids = [...html.matchAll(/\bid="([^"]+)"/g)].map(match => match[1]);
assert.equal(new Set(ids).size, ids.length);
for (const match of html.matchAll(/(?:src|href)="([^"#]+\.(?:js|css)(?:\?[^"]*)?)"/g)) assert.ok(fs.existsSync(path.resolve(base, match[1].split('?')[0])), match[1]);
const review = JSON.parse(fs.readFileSync(path.join(base, 'r6-verification.json'), 'utf8'));
assert.equal(review.checks.length, 27);
assert.equal(review.layouts.length, 300);
assert.equal(review.focusLayouts.length, 144);
assert.ok(review.checks.every(item => item.pass));
assert.ok(review.layouts.every(item => item.pass && item.chromeSeparated));
assert.ok(review.focusLayouts.every(item => item.pass));
assert.deepEqual(review.errors, []);
const menus=JSON.parse(fs.readFileSync(path.join(base,'r6-menu-verification.json'),'utf8'));
assert.equal(menus.positions.length,24);assert.ok(menus.positions.every(item=>item.pass&&item.hit&&item.topLayer));
assert.equal(menus.fallback.length,4);assert.ok(menus.fallback.every(item=>item.pass));
assert.ok(menus.pointerRange.pass&&menus.touchSelection.pass);assert.deepEqual(menus.errors,[]);
const libraryManifest=JSON.parse(fs.readFileSync(path.join(base,'galaxy-library/manifest.json'),'utf8'));
const libraryCheck=JSON.parse(fs.readFileSync(path.join(base,'r6-library-verification.json'),'utf8'));
const libraryVisual=JSON.parse(fs.readFileSync(path.join(base,'r6-library-visual-verification.json'),'utf8')),libraryState=JSON.parse(fs.readFileSync(path.join(base,'r6-library-state-verification.json'),'utf8'));
assert.equal(libraryManifest.total,3802);assert.equal(libraryManifest.categories.length,11);assert.equal(libraryManifest.entries.length,3802);assert.equal(libraryManifest.revision,catalog.revision);
assert.equal(libraryManifest.sourceBytes,10260098);assert.equal(libraryManifest.chunks,60);assert.ok(libraryManifest.entries.every(item=>/^[0-9a-f]{40}$/.test(item.gitBlob)));
assert.equal(libraryCheck.importCoverage.sourceHashes,3802);assert.equal(libraryCheck.importCoverage.sanitizedMarkupChecks,7604);assert.equal(libraryCheck.checks.length,9);assert.ok(libraryCheck.checks.every(item=>item.pass));assert.ok(libraryCheck.layouts.every(item=>item.pass));assert.deepEqual(libraryCheck.errors,[]);assert.deepEqual(libraryCheck.remoteRequests,[]);
assert.equal(libraryVisual.summary.count,3802);assert.equal(libraryVisual.summary.paletteLoads,7604);assert.equal(libraryVisual.summary.structuralDifferences,0);assert.equal(libraryVisual.summary.paletteGeometryChanges,0);assert.equal(libraryVisual.summary.paletteAnimationChanges,0);assert.equal(libraryVisual.summary.fitOutliers,0);assert.deepEqual(libraryVisual.failures,[]);assert.deepEqual(libraryVisual.errors,[]);assert.deepEqual(libraryVisual.remoteRequests,[]);
assert.equal(libraryState.checks.length,6);assert.ok(libraryState.checks.every(item=>item.pass));assert.deepEqual(libraryState.errors,[]);assert.deepEqual(libraryState.remoteRequests,[]);
for(const file of ['LICENSE.txt','TAILWIND-LICENSE.txt'])assert.ok(fs.readFileSync(path.join(base,'galaxy-library',file),'utf8').includes('MIT License'));
const result = { revision: 'R6', date: '2026-11-02', retainedInteractionEvidenceDate: review.date, scope:'Current documentation and static contracts; retained R6 browser results are checked for consistency, not rerun by this script.', localLinks: links, brokenLinks: broken, compiledScripts: 7, componentContracts: 24, uniqueSourceFiles:21, sourceHashesCheckedAgainstLocalCache: hashesChecked, undefinedColorTokens: undefinedTokens, interactionGroups: review.checks.length, layouts: review.layouts.length, focusLayouts: review.focusLayouts.length, menuPositions:menus.positions.length, fallbackMenus:menus.fallback.length, errors: [...review.errors,...menus.errors] };
Object.assign(result,{fullLibrary:3802,libraryCategories:11,libraryChunks:60,canonicalSourceBytes:libraryManifest.sourceBytes,librarySourceHashes:3802,libraryMarkupChecks:7604,libraryPaletteLoads:7604,libraryStructuralSubtrees:libraryVisual.summary.structuralSubtrees,libraryInteractionGroups:libraryCheck.checks.length,libraryStateGroups:libraryState.checks.length,libraryLayouts:libraryCheck.layouts.length});
fs.writeFileSync(path.join(base, 'r6-static-verification.json'), JSON.stringify(result, null, 2) + '\n');
console.log(JSON.stringify(result, null, 2));

