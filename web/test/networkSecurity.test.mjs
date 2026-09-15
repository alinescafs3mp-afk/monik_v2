import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';

test('V20 narrow TV CSS overrides measured tracks while observer updates, without clipping the page',async()=>{
 const css=await readFile(new URL('../src/styles.css',import.meta.url),'utf8');
 const rules=css.slice(css.indexOf('/* V20: viewport CSS'));
 assert.match(rules,/@media\(max-width:600px\)/);
 assert.match(rules,/:root \.tv-board\.columns-adjustable \.tv-machine\s*\{\s*grid-template-columns:repeat\(4,minmax\(0,1fr\)\)/);
 assert.match(rules,/:root \.tv-board \.column-dividers \{display:none;\}/);
 assert.doesNotMatch(rules,/overflow(?:-x)?:\s*(hidden|clip)/);
 assert.match(rules,/box-sizing:border-box/);
});

const apiSource=await readFile(new URL('../src/api.ts',import.meta.url),'utf8');
const apiJS=ts.transpileModule(apiSource,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const api=await import('data:text/javascript;base64,'+Buffer.from(apiJS).toString('base64'));

test('V20 login/setup client sends non-simple JSON with same-origin credentials',async()=>{
 const saved=globalThis.fetch;
 try {
  let calls=0;
  globalThis.fetch=async(path,init)=>{
   calls++;assert.equal(init.headers.get('Content-Type'),'application/json');
   assert.equal(init.credentials,'same-origin');
   assert.deepEqual(JSON.parse(init.body),{username:'fixture',password:'test-only'});
   return new Response(JSON.stringify({ok:true}),{headers:{'Content-Type':'application/json'}});
  };
  await api.post('/api/v1/login',{username:'fixture',password:'test-only'});
  await api.post('/api/v1/setup',{username:'fixture',password:'test-only'});
  assert.equal(calls,2);
 }finally{globalThis.fetch=saved;}
});

test('V20 authentication capacity failure stays visible and is not automatically retried',async()=>{
 const saved=globalThis.fetch;
 try {
  let calls=0;globalThis.fetch=async()=>{calls++;return new Response(JSON.stringify({error:'authentication_busy',message:'Retry later'}),{status:503,headers:{'Content-Type':'application/json','Retry-After':'2'}});};
  await assert.rejects(api.post('/api/v1/login',{username:'fixture',password:'test-only'}),e=>e.status===503&&e.error==='authentication_busy');
  assert.equal(calls,1);
 }finally{globalThis.fetch=saved;}
});
