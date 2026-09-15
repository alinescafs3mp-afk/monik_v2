import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {execFileSync} from 'node:child_process';
import {reactive,computed,watch,nextTick} from 'vue';
import ts from 'typescript';
const baseline=process.env.MONIK_TEST_BASELINE;
async function source(path){return baseline?execFileSync('git',['show',`${baseline}:${path}`],{cwd:new URL('../..',import.meta.url),encoding:'utf8'}):readFile(new URL('../..',import.meta.url).pathname+'/'+path,'utf8');}
const js=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
async function load(name){return import('data:text/javascript;base64,'+Buffer.from(js(await source(`web/src/${name}.ts`))).toString('base64'));}
const display=await load('display'),refresh=await load('refreshQueue'),api=await load('api'),presentation=await load('presentation'),format=await load('format');

// Execute the EXACT watcher used by the component with real Vue reactivity.
// Only DOM actions are spies; a source-string assertion could miss this defect.
async function editorWatch(){
 const text=await source('web/src/components/CheckEditor.vue');
 const begin=text.indexOf('watch(',text.indexOf('defineExpose('));
 const end=text.indexOf('watch(()=>props.detail?',begin);
 assert.ok(begin>0&&end>begin);
 const detail=reactive({services:[{id:'svc'}]}),route=reactive({query:{service:'svc'}}),services=computed(()=>detail.services);
 let focus=0,open=0;
 const install=new Function('watch','route','services','openService','nextTick','focusEditor',`return ${js(text.slice(begin,end))}`);
 const stop=install(watch,route,services,()=>{open++;return true;},nextTick,()=>focus++);
 const flush=async()=>{await nextTick();await nextTick();};await flush();
 return {detail,route,flush,stop,get focus(){return focus;},get open(){return open;}};
}
test('V16 passive inventory replacement never re-scrolls/re-focuses the editor',async()=>{
 const w=await editorWatch();try{assert.equal(w.focus,1);for(let i=0;i<8;i++){w.detail.services=[{id:'svc',cpu:i}];await w.flush();}assert.equal(w.focus,1);}finally{w.stop();}
});
test('V16 new unrelated services do not refocus an already opened editor',async()=>{
 const w=await editorWatch();try{w.detail.services=[{id:'svc'},{id:'other'}];await w.flush();assert.equal(w.focus,1);w.route.query.service='other';await w.flush();assert.equal(w.focus,2);}finally{w.stop();}
});
test('V16 background I/O keeps refresh button idle, manual refresh still visible',async()=>{
 const seen=[];let release;const q=refresh.createPollingController(()=>new Promise(r=>release=r),s=>seen.push({...s}));
 const first=q.background();await Promise.resolve();assert.equal(seen.at(-1).loading,true);assert.equal(seen.at(-1).refreshing,false);release();await first;
 const bg=q.background();await Promise.resolve();assert.equal(seen.at(-1).loading,false);assert.equal(seen.at(-1).refreshing,false);release();await bg;
 const manual=q.refresh();await Promise.resolve();assert.equal(seen.at(-1).refreshing,true);release();await manual;assert.equal(seen.at(-1).refreshing,false);q.dispose();
});
test('V16 timers cannot create a background refresh storm under a slow network',async()=>{
 let calls=0,release;const q=refresh.createPollingController(async()=>{calls++;await new Promise(r=>release=r)},()=>{});
 const a=q.background();await Promise.resolve();for(let i=0;i<20;i++)await q.background();assert.equal(calls,1);release();await a;q.dispose();
});
test('V16 explicit refresh during background work gets one final fresh read',async()=>{
 const waiting=[];let calls=0;const q=refresh.createPollingController(async()=>{calls++;if(calls===1)await new Promise(r=>waiting.push(r))},()=>{});
 const a=q.background();await Promise.resolve();const b=q.refresh(),c=q.refresh();waiting.shift()();await Promise.all([a,b,c]);assert.equal(calls,2);q.dispose();
});
test('V16 errors remain visible and recover without resetting the content to loading',async()=>{
 const seen=[];let bad=true;const q=refresh.createPollingController(async()=>{if(bad)throw Error('offline')},s=>seen.push({...s}));
 await q.background();assert.equal(seen.at(-1).error,'offline');assert.equal(seen.at(-1).loading,false);bad=false;await q.background();assert.equal(seen.at(-1).error,'');q.dispose();
});
test('V16 disposed background work does not publish completion into another page',async()=>{
 const seen=[];let release;const q=refresh.createPollingController(()=>new Promise(r=>release=r),s=>seen.push(s));const p=q.background();await Promise.resolve();q.dispose();const n=seen.length;release();await p;assert.equal(seen.length,n);
});
test('V16 5-second checks tolerate 10 and 19 seconds but do expire after 20 seconds',()=>{
 const at=Date.parse('2026-09-15T12:00:00Z');const s={fresh:true,observation:{observed_at:new Date(at).toISOString(),interval_seconds:5}};
 for(const age of [0,10,15,19,20])assert.equal(display.serviceFresh(s,at+age*1000),true);
 assert.equal(display.serviceFresh(s,at+20001),false);s.observation.interval_seconds=30;assert.equal(display.serviceFresh(s,at+85000),true);assert.equal(display.serviceFresh(s,at+91000),false);
});
test('V16 malformed intervals never permit unbounded freshness',()=>{
 const now=Date.now(),s={fresh:true,observation:{observed_at:new Date(now-60000).toISOString(),interval_seconds:Infinity}};assert.equal(display.serviceFresh(s,now),false);
});
test('V16 successful baseline response is green; failures/stale/pending are not green',()=>{
 const at=Date.now();for(const state of ['ok','responds'])assert.equal(display.serviceTone({state,fresh:true,observation:{observed_at:new Date(at).toISOString()}},at),'ok');
 for(const state of ['http_error','app_fail','transport_fail','unknown','pending','stale'])assert.equal(display.serviceTone({state,fresh:false},at),'crit');
 for(const state of ['unmonitored','paused','inactive'])assert.equal(display.serviceTone({state,fresh:false},at),'idle');
});
test('V16 presentation clock ignores browser wall-clock skew and late older requests',()=>{
 const at=Date.parse('2026-09-15T12:00:00Z');api.observeServerTime(new Date(at).toISOString(),1000,1020);assert.equal(api.serverNow(2020),at+1000);
 api.observeServerTime(new Date(at-60000).toISOString(),900,2500);assert.equal(api.serverNow(3020),at+2000);
 api.observeServerTime('bad',4000,5000);assert.equal(api.serverNow(3020),at+2000);
});
test('V16 columns fit three services down each column with bounded visible count',()=>{
 assert.equal(presentation.serviceColumnCapacity(200),3);assert.equal(presentation.serviceColumnCapacity(412),6);assert.equal(presentation.serviceColumnCapacity(624),9);assert.equal(presentation.serviceColumnCapacity(0),3);assert.ok(presentation.serviceColumnCapacity(20000)<=36);
});
test('V16 ping diagnostic distinguishes permission, no reply, send failure, initial sample',()=>{
 assert.match(format.pingDetail({permission:'permission_denied'}),/запрещён/);
 assert.match(format.pingDetail({sent:12,received:0}),/ответов нет/);
 assert.match(format.pingDetail({status:'send_failed'}),/отправить/);
 assert.match(format.pingDetail({sent:0}),/первое/);
 assert.equal(format.pingDetail({mean_ms:0,sent:1,received:1,status:'ok'}),'');
});
test('V16 disabled ping is explicit',()=>assert.match(format.pingDetail({status:'disabled'}),/выключен/));
