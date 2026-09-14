import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
async function load(name) {
 const text=await readFile(new URL('../src/'+name+'.ts',import.meta.url),'utf8');
 const js=ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
 return import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
}
const chart=await load('chart'), view=await load('presentation');
const point=(v,extra={})=>({t:'2026-09-13T10:00:00Z',v,...extra});
test('percent axes are labelled from zero to one hundred',()=>{assert.deepEqual(chart.numericAxis([point(47),point(49)],'%').ticks,[0,25,50,75,100]);});
test('constant and zero measurements have a finite nonzero axis',()=>{for(const v of [0,12,-12,.004]){const a=chart.numericAxis([point(v)]); assert.ok(Number.isFinite(a.low)); assert.ok(a.high>a.low); assert.ok(a.low<=v&&a.high>=v); assert.ok(a.ticks.length>=2);}});
test('extrema are included in numeric axis without pretending to be samples',()=>{const a=chart.numericAxis([point(10,{min:2,max:150})]);assert.equal(a.maximum,150); assert.ok(a.high>=150);assert.equal(a.minimum,2);});
test('empty and missing data never produce infinite labels',()=>{const a=chart.numericAxis([point(null),point(NaN)]);assert.equal(a.minimum,null);assert.ok(a.ticks.every(Number.isFinite));});
test('time axis preserves the requested empty edges',()=>{const a=chart.timeAxis([point(10)],'2026-09-13T08:00:00Z','2026-09-13T12:00:00Z');assert.equal(a.low,Date.parse('2026-09-13T08:00:00Z'));assert.equal(a.high,Date.parse('2026-09-13T12:00:00Z'));});
test('single timestamp has a usable time span',()=>{const a=chart.timeAxis([point(10)]);assert.equal(a.high-a.low,5000);});
test('cursor chooses the closest point after plot-margin conversion',()=>{assert.equal(chart.nearestPoint([point(1),point(2,{t:'2026-09-13T11:00:00Z'})],Date.parse('2026-09-13T10:58:00Z')),1);});
test('worst disk does not mutate the inventory or turn missing data into zero',()=>{const disks=[{mount:'/',used_percent:95},{mount:'/data',used_percent:25}];assert.equal(view.worstDisk(disks).mount,'/');assert.equal(disks[0].mount,'/');assert.equal(view.worstDisk([{used_percent:null}]),undefined);});
test('service failures precede healthy services without mutating original order',()=>{const services=[{id:'good',state:'ok'},{id:'bad',state:'app_fail'}];assert.equal(view.orderedServices(services)[0].id,'bad');assert.equal(services[0].id,'good');});
test('restricted browser storage degrades safely',()=>{globalThis.localStorage={getItem(){throw Error('blocked');},setItem(){throw Error('blocked');}};assert.equal(view.readPreference('key','rows'),'rows');assert.equal(view.savePreference('key','cards'),false);});
test('overview selection is not confused with pausing measurement',async()=>{const source=await readFile(new URL('../src/pages/Machines.vue',import.meta.url),'utf8');assert.ok(source.includes("submitOp('agent.pin'"));assert.ok(!source.includes("submitOp('service.pause'"));assert.ok(source.includes('type="checkbox"'));});
