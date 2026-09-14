import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const text=await readFile(new URL('../src/rollouts.ts',import.meta.url),'utf8');
const js=ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {canaryPreview,rolloutCounts,canResume,controlResult,rolloutLabel,rolloutSubmitNotice}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
test('one separate canary per platform, sorted by stable ID',()=>{const a=[{id:'z',os:'linux',arch:'amd64'},{id:'a',os:'linux',arch:'amd64'},{id:'w',os:'windows',arch:'amd64'}];const before=JSON.stringify(a);assert.deepEqual(canaryPreview(['w','z','a'],a).map(x=>x.id),['a','w']);assert.equal(JSON.stringify(a),before)});
test('canary preview does not include future/nonselected machines',()=>assert.equal(canaryPreview(['a'],[{id:'b',os:'linux',arch:'amd64'}]).length,0));
test('rolled back is a problem, not confirmation',()=>{const c=rolloutCounts({members:[{status:'rolled_back',released_at:'now'},{status:'succeeded',released_at:'now'},{status:'queued'},{status:'cancelled_before_execution'}]});assert.deepEqual(c,{total:4,confirmed:1,held:1,failed:1,cancelled:1})});
test('blocked plan cannot resume by acknowledging error',()=>{assert.equal(canResume({state:'blocked',members:[]}),false);assert.equal(canResume({state:'paused',members:[{status:'unknown_result'}]}),false);assert.equal(canResume({state:'paused',members:[{status:'queued'}]}),true)});
test('acceptance and failed control are never announced as applied',()=>{assert.equal(controlResult({status:'running',targets:[]}),false);assert.equal(controlResult({status:'completed',targets:[]}),false);assert.equal(controlResult({status:'completed',targets:[{agent_id:'server',status:'succeeded',stage:'rollout.control_committed'}]}),true)});
test('rollout status has distinct paused, failed and completed labels',()=>{assert.notEqual(rolloutLabel('paused'),rolloutLabel('completed'));assert.notEqual(rolloutLabel('blocked'),rolloutLabel('completed'))});

test('rejected rollout is never announced as saved/running',()=>{
 assert.match(rolloutSubmitNotice({status:'completed_with_errors',targets:[{status:'unsupported',message:'offline capabilities'}]}),/не начата/);
 assert.match(rolloutSubmitNotice({status:'queued',targets:[]}),/не подтверждена/);
 assert.match(rolloutSubmitNotice({status:'running',targets:[{status:'waiting_offline'}]}),/План сохранён/);
});
