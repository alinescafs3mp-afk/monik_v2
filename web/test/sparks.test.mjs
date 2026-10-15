import {test,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {parse,compileScript} from '@vue/compiler-sfc';
import {renderToString} from '@vue/server-renderer';
import * as Vue from 'vue';
const source=path=>readFileSync(new URL('../src/'+path,import.meta.url),'utf8');
function module(code,deps={}){const exports={};const js=ts.transpileModule(code,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;new Function('require','exports',js)(name=>{assert.ok(name in deps,'unmocked dependency '+name);return deps[name];},exports);return exports;}
const spark=module(source('sparks.ts')),api=module(source('api.ts'));
const at=Date.parse('2026-10-14T09:00:00Z'),stamp=new Date(at).toISOString();
const revision='sha256:'+'a'.repeat(64);
function fixture(){const profile={profile_id:'local-profile',profile_revision:revision,control_allowed:true,allowed_actions:['start','stop','restart'],settings:[]};const state={profile_id:profile.profile_id,generation:7,observed_at:stamp,supervisor_state:'stopped',readiness:'not_applicable',quality:'ok'};const detail={agent_id:'host-a',agent_session:'session-a',received_at:stamp,online:true,spark:{control_state:{enabled:true,epoch:'epoch-a',observed_at:stamp},metrics:[],model_states:[state]}};return {profile,state,detail};}
test('Spark metrics distinguish zero, missing, unsupported and ageing; memory activity is not capacity',()=>{
 const o={value:0,unit:'percent',source:'nvml',observed_at:stamp,quality:'ok'};
 assert.match(spark.formatObservation(o,at),/^0 %$/);
 assert.equal(spark.formatObservation({...o,value:null,quality:'unsupported'},at),'Не поддерживается');
 assert.match(spark.formatObservation(o,at+31000),/устарело/);
 assert.match(spark.formatObservation(o,at,false),/устарело/);
 assert.match(spark.metricLabel('gpu.0.memory_interface_percent'),/Активность интерфейса/);
 assert.equal(spark.freshAt('invalid',at),false);assert.equal(spark.freshAt(new Date(at+6000).toISOString(),at),false);
});
test('model PID/running is never ready without observed readiness; stale ready is visibly last known',()=>{
 const {state}=fixture();state.supervisor_state='running';state.readiness='unknown';
 assert.match(spark.modelLabel(state,true,at),/готовность не подтверждена/);
 state.readiness='ready';assert.equal(spark.modelLabel(state,true,at),'Готова к запросам');
 assert.match(spark.modelLabel(state,false,at),/последнее наблюдение/);
});
test('cached model and control observations cover idle polling while contact and metrics keep their own freshness',()=>{
 const {detail,profile,state}=fixture();state.supervisor_state='running';state.readiness='ready';
 for(const seconds of [20,31,45]){
  const now=at+seconds*1000;detail.received_at=new Date(now-5000).toISOString();
  assert.equal(spark.modelLabel(state,true,now),'Готова к запросам');
  assert.equal(spark.actionReason(detail,profile,state,'stop',true,now),'');
 }
 const now=at+46000;detail.received_at=new Date(now-5000).toISOString();
 assert.match(spark.modelLabel(state,true,now),/последнее наблюдение/);
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,now),spark.explainCode('state_stale'));
 state.observed_at=new Date(now-5000).toISOString();
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,now),spark.explainCode('state_stale'));
 detail.spark.control_state.observed_at=state.observed_at;state.observed_at=stamp;
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,now),spark.explainCode('state_stale'));
 state.observed_at=detail.spark.control_state.observed_at;detail.received_at=stamp;
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,now),spark.explainCode('state_stale'));
 assert.equal(spark.freshAt(stamp,at+31000),false);
 assert.match(spark.formatObservation({value:0,unit:'percent',source:'fixture',observed_at:stamp,quality:'ok'},at+31000),/устарело/);
 assert.equal(state.observed_at,new Date(now-5000).toISOString());
});
test('action matrix keeps stop during startup and rejects old sessions, offline, unknown and viewer effects',()=>{
 const {detail,profile,state}=fixture();
 assert.equal(spark.actionReason(detail,profile,state,'start',true,at),'');
 assert.match(spark.actionReason(detail,profile,state,'restart',true,at),/используйте «Старт»/);
 state.supervisor_state='starting';assert.equal(spark.actionReason(detail,profile,state,'stop',true,at),'');
 // No launch-only weights, memory or port checks are invented in Stop.
 profile.settings=[{name:'memory_limit_bytes',value:999999999999,provenance:'configured'}];
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,at),'');
 assert.ok(spark.actionReason(detail,profile,state,'stop',false,at));
 assert.ok(spark.actionReason({...detail,online:false},profile,state,'stop',true,at));
 assert.ok(spark.actionReason(detail,profile,state,'stop',true,at+31000));
 assert.ok(spark.actionReason({...detail,agent_session:''},profile,state,'stop',true,at));
 state.generation=NaN;assert.ok(spark.actionReason(detail,profile,state,'stop',true,at));
});
test('operation params contain only one frozen exact-profile binding and disruption intent',()=>{
 const {detail,profile,state}=fixture();
 assert.deepEqual(spark.modelParams(detail,profile,state,true),{profile_id:'local-profile',profile_revision:revision,expected_generation:7,control_epoch:'epoch-a',expected_agent_session:'session-a',confirm_disruption:true});
});
test('fresh exact standalone preflight permits safety stop while actual supervisor remains stopped',()=>{
 const {detail,profile,state}=fixture();state.reason='start_preflight_pending';
 assert.equal(spark.actionReason(detail,profile,state,'stop',true,at),'');
 assert.ok(spark.actionReason(detail,profile,state,'start',true,at));
 assert.match(spark.modelLabel(state,true,at),/проверка перед запуском/);
 assert.ok(spark.actionReason(detail,profile,state,'stop',false,at));
 assert.ok(spark.actionReason({...detail,online:false},profile,state,'stop',true,at));
 assert.ok(spark.actionReason(detail,profile,state,'stop',true,at+31000));
 assert.ok(spark.actionReason(detail,profile,state,'stop',true,at,true));
 state.reason='restart_start_pending';assert.ok(spark.actionReason(detail,profile,state,'stop',true,at));
 delete state.reason;assert.ok(spark.actionReason(detail,profile,state,'stop',true,at));
 state.reason='start_preflight_pending';state.supervisor_state='stopping';assert.ok(spark.actionReason(detail,profile,state,'stop',true,at));
});
test('log flooding, giant records, duplicate replay and terminal control bytes remain bounded without HTML execution',()=>{
 const records=Array.from({length:3000},(_,sequence)=>({sequence,at:stamp,text:'\x1b[31m<script>literal</script>\x00'+('Я'.repeat(20000))}));
 const bounded=spark.boundedLogs([],records);
 assert.ok(bounded.rows.length<=600);assert.ok(bounded.rows.reduce((n,r)=>n+r.text.length,0)<=180000);assert.ok(bounded.truncated);
 assert.ok(bounded.rows.every(r=>!r.text.includes('\x1b')&&!r.text.includes('\x00')));
 assert.match(bounded.rows.at(-1).text,/<script>literal<\/script>/);
 const replay=spark.boundedLogs(bounded.rows,[records.at(-1)]);assert.equal(replay.rows.length,bounded.rows.length);
});
beforeEach(()=>{api.setCsrf('csrf');api.setReauthHandler(null);});
function binary(flavor){return new Response(new Uint8Array(128),{headers:{'Content-Type':'application/octet-stream','Content-Length':'128','X-Monik-Profile-Expires':'2030-01-01T00:00:00Z',...(flavor?{'X-Monik-Agent-Flavor':flavor}:{})}});}
test('Spark installer selects actual ARM64 flavor without console or altered controller trust',async()=>{
 globalThis.fetch=async(path,init)=>{assert.equal(path,'/api/v1/installers/linux-arm64');assert.deepEqual(JSON.parse(init.body),{controller_url:'https://owner.example:9443',agent_flavor:'spark-light'});return binary('spark-light');};
 assert.equal((await api.downloadInstaller('linux-arm64','https://owner.example:9443',false,'spark-light')).blob.size,128);
 let calls=0;globalThis.fetch=async()=>{calls++;return binary('spark-light');};
 await assert.rejects(api.downloadInstaller('linux-amd64','https://c',false,'spark-light'));
 await assert.rejects(api.downloadInstaller('linux-arm64','https://c',true,'spark-light'));assert.equal(calls,0);
});
test('wrong or unconfirmed installer flavor cannot look like a successful Spark download',async()=>{
 for(const flavor of [undefined,'general']){globalThis.fetch=async()=>binary(flavor);await assert.rejects(api.downloadInstaller('linux-arm64','https://c',false,'spark-light'),e=>e.error==='invalid_flavor');}
});
test('log creation retries only a definite authentication refusal, never response loss',async()=>{
 let calls=0;api.setReauthHandler(async()=>true);globalThis.fetch=async()=>{calls++;return calls===1?new Response(JSON.stringify({error:'recent_auth_required'}),{status:401}):new Response(JSON.stringify({stream_id:'s'}));};
 assert.equal((await api.postRecentAuth('/logs',{profile_id:'p'},'Логи')).stream_id,'s');assert.equal(calls,2);
 calls=0;globalThis.fetch=async()=>{calls++;throw new Error('lost');};await assert.rejects(api.postRecentAuth('/logs',{profile_id:'p'},'Логи'));assert.equal(calls,1);
});

// Exercise the actual compiled stream component; timers and HTTP/SSE are bounded local fixtures.
test('actual ModelLogs releases exact old stream on pause, target change and unmount; preserves selection under updates',async()=>{
 const previous={window:globalThis.window,EventSource:globalThis.EventSource};
 const streams=[],deletes=[],timers=new Map(),cleanup=[];let timer=0,selected='';
 globalThis.window={setTimeout(fn){timers.set(++timer,fn);return timer;},clearTimeout(id){timers.delete(id);},getSelection:()=>({toString:()=>selected})};
 globalThis.EventSource=class{constructor(path){this.path=path;this.handlers={};streams.push(this);}addEventListener(name,fn){this.handlers[name]=fn;}close(){this.closed=true;}};
 const component=module(compileScript(parse(source('components/ModelLogs.vue')).descriptor,{id:'spark-log-test'}).content,{
  vue:{...Vue,onBeforeUnmount:fn=>cleanup.push(fn)},'../api':{api:async(path)=>deletes.push(path),postRecentAuth:async()=>({stream_id:'stream-a',expires_at:'2030-01-01T00:00:00Z'})},'../sparks':spark,
 }).default;
 const props=Vue.reactive({agentId:'host-a',binding:{profile_id:'p'},available:true}),scope=Vue.effectScope();let model;
 try{
  scope.run(()=>model=component.setup(props,{expose(){}}));await model.open();
  assert.match(streams[0].path,/host-a\/logs\/stream-a\/events$/);
  selected='selected text';streams[0].handlers.log({data:JSON.stringify({sequence:1,at:stamp,text:'<img onerror=evil>'})});
  [...timers.values()][0]();assert.equal(model.rows.value.length,0,'selection was overwritten');
  selected='';[...timers.values()].at(-1)();assert.equal(model.rows.value[0].text,'<img onerror=evil>');
  await model.pause();assert.ok(streams[0].closed);assert.ok(deletes[0].endsWith('/host-a/logs/stream-a'));
  await model.open();props.agentId='host-b';await Vue.nextTick();assert.ok(deletes.at(-1).endsWith('/host-a/logs/stream-a'),'cleanup used changed host instead of stream owner');
  await model.open();cleanup.forEach(fn=>fn());await Promise.resolve();assert.ok(deletes.at(-1).endsWith('/host-b/logs/stream-a'));
 }finally{scope.stop();Object.assign(globalThis,previous);}
});
test('actual viewer service toggle markup is disabled and cannot mutate',async()=>{
 const component=module(compileScript(parse(source('components/ServiceOverviewToggle.vue')).descriptor,{id:'viewer-toggle',inlineTemplate:true}).content,{vue:Vue,'../session':{useSession:()=>({owner:Vue.ref(false)})},'../api':{submitOp:async()=>assert.fail('viewer mutation')}}).default;
 const html=await renderToString(Vue.createSSRApp(component,{service:{id:'service-a',display_name:'Пример',pinned:true}}));
 assert.match(html,/<input[^>]*disabled/);assert.match(html,/В обзоре/);
});
test('actual setup cannot submit a hardcoded endpoint when setup status failed',async()=>{
 const mounted=[];let writes=0;
 const component=module(compileScript(parse(source('pages/Setup.vue')).descriptor,{id:'setup-status'}).content,{vue:{...Vue,onMounted:fn=>mounted.push(fn)},'vue-router':{useRouter:()=>({replace:async()=>{}})},'../api':{get:async()=>{throw Error('offline');},post:async()=>writes++}}).default;
 const model=component.setup({}, {expose(){}});await mounted[0]();assert.equal(model.advertised.value,'');assert.equal(model.statusLoaded.value,false);await model.submit();assert.equal(writes,0);
});
test('actual Spark detail supports monitoring-only profiles without fabricating an epoch or sending an action',async()=>{
 const previous=globalThis.window;globalThis.window={setInterval:()=>1,clearInterval(){}};
 const profile={...fixture().profile,control_allowed:false,control_reason:'control_disabled',allowed_actions:[],display_name:'Read-only profile',engine:'llama.cpp',supervisor:'systemd-user'};
 const d={...fixture().detail,spark:{schema_version:1,metrics:[],inventory:{profiles:[profile],complete:true},model_states:[fixture().state]}};
 const publicRow={agent_id:'host-a',display_name:'Spark',online:true,received_at:stamp,profiles:[],metrics:[]};
 let load;
 const route=Vue.reactive({params:{id:'host-a'},hash:'#models'}),owner=Vue.ref(true),scope=Vue.effectScope();
 const component=module(compileScript(parse(source('pages/SparkMachine.vue')).descriptor,{id:'spark-monitoring-only'}).content,{
  vue:{...Vue,onBeforeUnmount(){}},'vue-router':{useRoute:()=>route},'../api':{serverNow:()=>at,get:async path=>path==='/api/v1/sparks'?{sparks:[publicRow]}:path.startsWith('/api/v1/agents/')?{agent:{id:'host-a'}}:d,submitOp:async()=>assert.fail('read-only profile mutation')},
  '../composables/usePolling':{usePolling:fn=>{load=fn;return{loading:Vue.ref(false),error:Vue.ref(''),updated:Vue.ref(null),refresh:fn};}},'../session':{useSession:()=>({owner})},'../format':module(source('format.ts')),'../sparks':spark,
  '../components/SparkObservation.vue':{default:{}},'../components/ModelLogs.vue':{default:{}},'../components/Spark.vue':{default:{}},
 }).default;
 let model;
 try{scope.run(()=>model=component.setup({}, {expose(){},emit(){}}));await load();await Vue.nextTick();assert.deepEqual(model.logBinding.value,{});assert.ok(model.logReason.value);await model.ask(profile,'start');assert.ok(model.actionError.value);assert.equal(model.selected.value,profile.profile_id);}
 finally{scope.stop();globalThis.window=previous;}
});

test('existing controller metadata stays distinct from static units',()=>{assert.equal(spark.supervisorLabel('existing-coordinator'),'Существующий контроллер Spark');assert.equal(spark.supervisorLabel('systemd-system'),'systemd-system');});
test('start after a failed service requires explicit verified shutdown proof',()=>{const {profile,state,detail}=fixture();state.supervisor_state='failed';assert.notEqual(spark.actionReason(detail,profile,state,'start',true,at),'');state.reason='failed_service_shutdown_verified';assert.equal(spark.actionReason(detail,profile,state,'start',true,at),'');});
test('native Spark metric units and observation limits have clear labels without value scaling',()=>{for(const [unit,label] of [['percent_one_core','%'],['count','шт.'],['bytes/second','Б/с']]){const text=spark.formatObservation({value:125.25,unit,source:'native',observed_at:stamp,quality:'ok'},at);assert.match(text,/125,25/);assert.ok(text.endsWith(label));}for(const reason of ['observation_only','gb10_unified_memory_no_dedicated_framebuffer','active_helper_cpu_not_yet_reaped'])assert.notEqual(spark.explainCode(reason),reason);});
