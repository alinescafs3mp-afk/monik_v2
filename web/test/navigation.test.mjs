import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const source=await readFile(new URL('../src/navigationFailure.ts',import.meta.url),'utf8');
const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {navigationFailureMessage}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
test('missing code chunk explains deployment/connectivity without exposing URL',()=>{
 const m=navigationFailureMessage(new Error('Failed to fetch dynamically imported module: /assets/X.js?secret=private'));
 assert.match(m,/сервер обновлён/);assert.match(m,/черновик/);assert.doesNotMatch(m,/secret|private/);
});
test('other route failures do not claim logout or repeat mutations',()=>{
 const m=navigationFailureMessage(new Error('network failed'));
 assert.match(m,/не повторялись/);assert.doesNotMatch(m,/войдите|выйти/);
});
test('router failure UI reload is explicit and warns about drafts',async()=>{
 const app=await readFile(new URL('../src/App.vue',import.meta.url),'utf8');
 assert.match(app,/router\.onError/);assert.match(app,/window\.confirm/);assert.match(app,/Обновить страницу/);
 assert.match(app,/removeRouteError\(\)/);
});
