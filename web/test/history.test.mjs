import { test } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';
const text=await readFile(new URL('../src/history.ts',import.meta.url),'utf8');
const js=ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const h=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
test('history uses elapsed hours and explicit UTC across offset changes',()=>{
 for(const hours of [1,2,3,6,12,24]){const r=h.historyRange(hours,'2026-10-25T02:30:00+02:00');assert.equal(Date.parse(r.to)-Date.parse(r.from),hours*3600000);assert.ok(r.from.endsWith('Z'));}
});
test('invalid time and ranges are rejected before HTTP',()=>{assert.throws(()=>h.historyRange(7,new Date()));assert.throws(()=>h.historyRange(1,'invalid'));});
test('download only exists for the completed expected local export',()=>{
 const op={action:'history.export',operation_id:'123',targets:[{agent_id:'server',status:'succeeded',evidence:{download_url:'/api/v1/exports/123'}}]};assert.equal(h.exportDownload(op),'/api/v1/exports/123');op.targets[0].status='accepted';assert.equal(h.exportDownload(op),null);op.targets[0].status='succeeded';op.targets[0].evidence.download_url='https://example.invalid/';assert.equal(h.exportDownload(op),null);
});
test('incident lifecycle statuses have readable labels',()=>{for(const v of ['pending','confirmed','resolved','interrupted'])assert.notEqual(h.incidentStateLabel(v),v);});
