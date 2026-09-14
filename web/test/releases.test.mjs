import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const text=await readFile(new URL('../src/releases.ts',import.meta.url),'utf8');
const js=ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {releaseReason,agentUpdateReason,selectedUpdateTargets,importNotice}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
const now='2026-09-14T12:00:00Z';
const rel={immutable:true,trust_ok:true,metadata_expires_at:'2026-09-15T00:00:00Z'};
const ag={id:'a',managed_ready:true,capabilities:{immutable_release_v1:{status:'supported'}}};
test('legacy release is not silently considered immutable',()=>assert.ok(releaseReason({...rel,immutable:false},now)));
test('expiry boundary is rejected, not rounded to a day',()=>assert.ok(releaseReason({...rel,metadata_expires_at:now},now)));
test('unknown server clock is not guessed ready',()=>assert.ok(releaseReason(rel,'invalid')));
test('valid immutable release passes explanatory preflight',()=>assert.equal(releaseReason(rel,now),''));
test('old agent needs an explicit compatible upgrade',()=>assert.ok(agentUpdateReason({...ag,capabilities:{}})));
test('revoked or unmanaged agents cannot be selected',()=>{assert.ok(agentUpdateReason({...ag,revoked:true}));assert.ok(agentUpdateReason({...ag,managed_ready:false}));});
test('stale selection removes newly ineligible machines and duplicates',()=>assert.deepEqual(selectedUpdateTargets(['a','a','gone','bad'],[ag,{...ag,id:'bad',revoked:true}]),['a']));

test('accepted or reconciled import does not claim publication',()=>{
 assert.match(importNotice({status:'accepted'}),/Дождитесь/);
 assert.match(importNotice({status:'completed',targets:[]}),/Дождитесь/);
 assert.match(importNotice({status:'completed',targets:[{agent_id:'server',status:'succeeded',stage:'verified_catalog_commit'}]}),/проверен и опубликован/);
});
