import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {execFileSync} from 'node:child_process';
import ts from 'typescript';
const baseline=process.env.MONIK_TEST_BASELINE;
async function source(path){return baseline?execFileSync('git',['show',`${baseline}:${path}`],{cwd:new URL('../..',import.meta.url),encoding:'utf8'}):readFile(new URL('../..',import.meta.url).pathname+'/'+path,'utf8');}
const js=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
async function module(name){return import('data:text/javascript;base64,'+Buffer.from(js(await source(`web/src/${name}.ts`))).toString('base64'));}

// Run the component's exact select handler. HTTP promises deliberately finish
// out of order. These are not string-shape checks or a second implementation.
async function selection(){
 const text=await source('web/src/pages/Machine.vue'),start=text.indexOf('async function select('),end=text.indexOf('async function exportRange(',start);
 assert.ok(start>=0&&end>start);
 const mode={value:'history'},cursor={value:''},point={value:null},error={value:''},endValue={value:''},pointLoading={value:false};
 const route={params:{id:'host-a'}},requests=[];
 const get=path=>new Promise((resolve,reject)=>requests.push({path,resolve,reject}));
 const factory=new Function('mode','cursor','point','error','end','route','get','localDateValue','pointLoading',
  `let pointGeneration=0;let generation=0;${js(text.slice(start,end))};return select;`);
 return {select:factory(mode,cursor,point,error,endValue,route,get,()=>'',pointLoading),mode,cursor,point,error,route,requests,pointLoading};
}
test('V17 latest graph selection wins even when previous request arrives last',async()=>{
 const s=await selection();const a=s.select('2026-09-15T10:00:00Z'),b=s.select('2026-09-15T10:01:00Z');
 s.requests[1].resolve({host:{cpu:20}});await b;s.requests[0].resolve({host:{cpu:90}});await a;
 assert.equal(s.point.value.host.cpu,20,'older point replaced the point selected by the user');
});
test('V17 point response for a previous machine cannot replace current host',async()=>{
 const s=await selection(),p=s.select('2026-09-15T10:00:00Z');s.route.params.id='host-b';s.point.value={host:{cpu:7}};
 s.requests[0].resolve({host:{cpu:90}});await p;assert.equal(s.point.value.host.cpu,7);
});
test('V17 older point error cannot overwrite a later successful selection',async()=>{
 const s=await selection(),a=s.select('2026-09-15T10:00:00Z'),b=s.select('2026-09-15T10:01:00Z');
 s.requests[1].resolve({host:{cpu:20}});await b;s.requests[0].reject(Error('old offline error'));await a;
 assert.equal(s.error.value,'');
});
test('V17 malformed service column measurement has a finite capacity',async()=>{
 const {serviceColumnCapacity}=await module('presentation');
 for(const n of [NaN,Infinity,-Infinity])assert.equal(serviceColumnCapacity(412,n),6);
});
test('V17 a reconciled queued export does not claim a file is ready',async()=>{
 const text=await source('web/src/pages/Machine.vue'),start=text.indexOf('async function exportRange('),end=text.indexOf('async function action(',start);
 assert.ok(start>=0&&end>start);
 const {exportDownload}=await module('history');const messages=[],exportURL={value:''};
 const handler=new Function('history','pending','exportURL','submitOp','exportDownload','emit','route',
  `${js(text.slice(start,end))};return exportRange;`)({value:{from:'2026-09-15T10:00:00Z',to:'2026-09-15T11:00:00Z'}},{value:''},exportURL,
   async()=>({op:{operation_id:'exp1',action:'history.export',status:'running',targets:[]}}),exportDownload,(event,message)=>messages.push(message),{params:{id:'host-a'}});
 await handler();assert.equal(exportURL.value,'');assert.match(messages[0],/пока не подтверждён/);
});
test('V17 row priority follows the same current service state as the lamp',async()=>{
 const {overviewPriority}=await module('presentation');
 const machine={id:'a',state:'ok',services:[{state:'http_error',pinned:true}],breaches:[]};
 assert.equal(overviewPriority(machine,true,()=> 'stale'),1,'expired failure claimed as a current red fault');
 assert.equal(overviewPriority(machine,true,()=> 'http_error'),2);
});

test('V17 TV page capacity reserves actual wrapped footer height',async()=>{
 const {boardCapacity}=await module('display');
 assert.equal(boardCapacity(540,100,60,180),4);
 assert.equal(boardCapacity(540,100,60,NaN),1);
 assert.equal(boardCapacity(540,100,60,-1),1);
});
