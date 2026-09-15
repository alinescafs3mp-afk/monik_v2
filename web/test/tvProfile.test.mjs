import test from 'node:test';import assert from 'node:assert/strict';import {readFile} from 'node:fs/promises';import ts from 'typescript';
const source=await readFile(new URL('../src/tvProfile.ts',import.meta.url),'utf8');const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const {createTVProfileSync,initialTVValue,validTVValue,parseTVResponse}=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
const clone=x=>structuredClone(x);let keys=0;const key=()=>`00000000-1111-2222-3333-${String(++keys).padStart(12,'0')}`;
const deferred=()=>{let resolve,reject;const promise=new Promise((a,b)=>{resolve=a;reject=b});return{promise,resolve,reject}};
function fixture(){let data={controller_id:'controller',profile:{schema_version:1,revision:0,value:initialTVValue(),updated_by:'',updated_at:null,last_request_id:''}},writes=0,reads=0,drop=false,fail=false;
 const read=async()=>{reads++;return clone(data)};
 const write=async body=>{writes++;if(fail)throw{status:0};if(body.expected_revision!==data.profile.revision)throw{status:409};data.profile={schema_version:1,revision:data.profile.revision+1,value:clone(body.value),updated_by:'owner',updated_at:'2026-09-15T17:00:00Z',last_request_id:body.request_id};if(drop)throw{status:0};return{...clone(data),saved:true}};
 function device(overrides={},local=initialTVValue()){let state;const io={read,write,key,...overrides};const sync=createTVProfileSync(io,s=>state=s);sync.start('controller/owner','controller',true,local);return{sync,io,get state(){return state}}}
 return {device,get data(){return clone(data)},get writes(){return writes},get reads(){return reads},drop:v=>drop=v,fail:v=>fail=v,read,write};
}
test('V19 two screens sync widths density autoplay without publishing on mount',async()=>{
 const f=fixture(),pc=f.device(),tv=f.device({}, {...initialTVValue(),density:'14'});await Promise.all([pc.sync.read(),tv.sync.read()]);assert.equal(f.writes,0);assert.equal(tv.state.value.density,'14');
 assert.equal(await pc.sync.update({widths:[14,4,7,6,8],density:'12',autoplay:true}),true);await tv.sync.read();assert.deepEqual(tv.state.value,pc.state.value);assert.equal(f.writes,1);
 for(let i=0;i<5;i++)await tv.sync.read();assert.equal(f.writes,1,'spectator rewrote shared settings');
});
test('V19 remote change during local gesture retains draft and forbids stale write',async()=>{
 const f=fixture(),a=f.device(),b=f.device();await Promise.all([a.sync.read(),b.sync.read()]);await a.sync.update({});await b.sync.read();assert.equal(b.sync.begin(),true);
 await a.sync.update({density:'12'});await b.sync.read();assert.equal(b.state.value.density,'10');const before=f.writes;
 assert.equal(await b.sync.update({widths:[15,4,7,6,8]}),false);assert.equal(b.state.phase,'conflict');assert.equal(f.writes,before);
 await b.sync.adopt();assert.equal(b.state.value.density,'12');assert.equal(b.state.phase,'synced');
});
test('V19 CAS rejects a stale writer even without a preceding SSE hint',async()=>{
 const f=fixture(),a=f.device(),b=f.device();await Promise.all([a.sync.read(),b.sync.read()]);await a.sync.update({density:'12'});assert.equal(await b.sync.update({density:'14'}),false);assert.equal(b.state.phase,'conflict');assert.equal(f.data.profile.value.density,'12');
});
test('V19 lost save response reconciles request identity with no second mutation',async()=>{
 const f=fixture(),a=f.device();await a.sync.read();f.drop(true);assert.equal(await a.sync.update({density:'14'}),true);assert.equal(f.writes,1);assert.equal(a.state.phase,'synced');assert.equal(a.state.value.density,'14');
});
test('V19 unknown outcome blocks another click and never replays on reconnect',async()=>{
 const f=fixture(),a=f.device();await a.sync.read();await a.sync.update({});f.fail(true);assert.equal(await a.sync.update({density:'14'}),false);assert.equal(a.state.phase,'unknown');assert.equal(a.state.value.density,'14');
 const before=f.writes;assert.equal(await a.sync.update({density:'12'}),false);f.fail(false);await a.sync.read();assert.equal(f.writes,before);await a.sync.adopt();assert.equal(a.state.value.density,'10');
});
test('V19 already-started older GET cannot rewind an acknowledged write',async()=>{
 const f=fixture(),a=f.device();await a.sync.read();await a.sync.update({});const old=f.data,d=deferred();a.io.read=()=>d.promise; // IO closure retains the same object.
 const reading=a.sync.read();await a.sync.update({density:'12'});d.resolve(old);await reading;assert.equal(a.state.value.density,'12');assert.equal(a.state.profile.revision,2);
});
test('V19 one in-flight write rejects duplicate gestures',async()=>{
 const f=fixture(),d=deferred(),a=f.device({write:async b=>{await d.promise;return f.write(b)}});await a.sync.read();const save=a.sync.update({density:'12'});assert.equal(a.state.phase,'saving');assert.equal(a.sync.begin(),false);assert.equal(await a.sync.update({density:'14'}),false);d.resolve();await save;assert.equal(f.writes,1);
});
test('V19 logout invalidates all old reads and writes, not just the rendered component',async()=>{
 const f=fixture(),d=deferred(),a=f.device({read:()=>d.promise});a.sync.stop();d.resolve(f.data);await a.sync.read();await Promise.resolve();assert.equal(a.state.controller,'');assert.equal(a.state.profile,null);assert.equal(a.sync.begin(),false);
});
test('V19 read-only device receives profile but cannot publish',async()=>{
 const f=fixture(),a=f.device();await a.sync.read();a.sync.start('controller/owner','controller',false);assert.equal(await a.sync.update({density:'14'}),false);assert.equal(f.writes,0);
});
test('V19 same revision different contents, wrong controller or CSS are rejected',()=>{
 const f=fixture();for(const mutate of [x=>x.controller_id='other',x=>x.profile.value.density='url(evil)',x=>x.profile.value.widths[1]='4px',x=>x.profile.value.widths[0]=NaN,x=>x.profile.value.autoplay='true',x=>x.profile.value.style='evil',x=>x.profile.revision=1.5]){const data=f.data;mutate(data);assert.throws(()=>parseTVResponse(data,'controller'))}
 assert.equal(validTVValue(initialTVValue()),true);
});
test('V19 malformed successful response is not claimed saved',async()=>{
 const f=fixture(),a=f.device({write:async()=>({saved:true})});await a.sync.read();assert.equal(await a.sync.update({density:'12'}),false);assert.equal(a.state.phase,'unknown');
});
test('V19 explicit cancellation adopts a remote change without publishing it again',async()=>{
 const f=fixture(),a=f.device(),b=f.device();await Promise.all([a.sync.read(),b.sync.read()]);a.sync.begin();await b.sync.update({density:'14'});await a.sync.read();a.sync.cancel();assert.equal(a.state.value.density,'14');assert.equal(f.writes,1);
});
test('V19 definite authorization rejection is not mislabelled unknown execution',async()=>{
 const f=fixture(),a=f.device({write:async()=>{throw{status:403,message:'Forbidden'}}});await a.sync.read();assert.equal(await a.sync.update({density:'12'}),false);assert.equal(a.state.phase,'error');assert.equal(a.state.message,'Forbidden');
});
test('V19 profile updates have a dedicated hint and never trigger general refresh storm',async()=>{
 const shell=await readFile(new URL('../src/components/AppShell.vue',import.meta.url),'utf8');assert.match(shell,/addEventListener\("display",\(\)=>window.dispatchEvent\(new Event\("monik:display-refresh"\)\)\)/);
 const bridge=await readFile(new URL('../src/composables/useTVProfile.ts',import.meta.url),'utf8');assert.match(bridge,/visibilitychange/);assert.match(bridge,/online/);assert.doesNotMatch(bridge,/localStorage\.setItem|submitOp/);
});
test('V19 SSE may confirm a save before POST returns, but a second write must still wait',async()=>{
 const f=fixture(),d=deferred();const a=f.device({write:async body=>{const result=await f.write(body);await d.promise;return result;}});await a.sync.read();const task=a.sync.update({density:'12'});await Promise.resolve();await a.sync.read();
 assert.equal(a.state.profile.revision,1);assert.equal(a.sync.begin(),false,'opened a second editor while first request was still in flight');d.resolve();await task;assert.equal(a.sync.begin(),true);a.sync.cancel();
});
