import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
const text=await readFile(new URL('../src/refreshQueue.ts',import.meta.url),'utf8');
const js=ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {createRefreshQueue,isAuthenticationFailure}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
test('filter changes coalesce into one fresh load and callers await it',async()=>{
 let release,count=0,value='old';const seen=[];
 const q=createRefreshQueue(async()=>{count++;seen.push(value);if(count===1)await new Promise(r=>release=r);});
 const a=q.refresh();await Promise.resolve();value='new';const b=q.refresh(),c=q.refresh();assert.equal(a,b);assert.equal(b,c);release();await Promise.all([a,b,c]);assert.equal(count,2);assert.deepEqual(seen,['old','new']);
});
test('unmount prevents queued request, errors do not wedge future refreshes',async()=>{
 let release,count=0;const q=createRefreshQueue(async()=>{count++;await new Promise(r=>release=r)});const a=q.refresh();await Promise.resolve();q.refresh();q.dispose();release();await a;await q.refresh();assert.equal(count,1);
 let fail=true;const r=createRefreshQueue(async()=>{if(fail)throw Error('failed')});await assert.rejects(r.refresh());fail=false;await r.refresh();
});
test('network, timeouts, server errors and permissions are not expired login',()=>{
 for(const status of [0,400,403,408,429,500,503])assert.equal(isAuthenticationFailure({status}),false);assert.equal(isAuthenticationFailure({status:401}),true);assert.equal(isAuthenticationFailure(null),false);
});
