import test from 'node:test';
import assert from 'node:assert/strict';
import {readFileSync} from 'node:fs';
import ts from 'typescript';
import {parse,compileScript} from '@vue/compiler-sfc';
import {renderToString} from '@vue/server-renderer';
import * as Vue from 'vue';

const source=path=>readFileSync(new URL('../src/'+path,import.meta.url),'utf8');
const js=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
function module(source,modules={}){const exports={};new Function('require','exports',js(source))(name=>{assert.ok(name in modules,'unmocked dependency '+name);return modules[name]},exports);return exports;}
const screen=module(source('tvScreen.ts')),display=module(source('display.ts'));

test('V21 local TV choice accepts only literal flags, saved choice and explicit reset',()=>{
 for(const saved of [null,'0','1','true','garbage']){
  assert.equal(screen.tvPassiveChoice('1',saved),true);
  assert.equal(screen.tvPassiveChoice('0',saved),false);
  for(const query of [null,'','true','garbage'])assert.equal(screen.tvPassiveChoice(query,saved),saved==='1');
 }
});
test('V21 hide cannot discard an edit, unknown outcome or unresolved conflict',()=>{
 for(const phase of ['loading','local','synced','editing','saving','conflict','unknown','error']){
  assert.equal(screen.canHideTVTools(phase,true),false);
  assert.equal(screen.canHideTVTools(phase,false),!['editing','saving','conflict','unknown'].includes(phase));
 }
});
test('V21 passive header retains missing-profile and synchronization warnings',()=>{
 for(const phase of ['local','conflict','unknown','error'])assert.ok(screen.tvPassiveWarning(phase));
 for(const phase of ['loading','synced','editing','saving'])assert.equal(screen.tvPassiveWarning(phase),'');
});
test('V21 passive autoplay ignores parked mouse, never focused remote controls or hidden tabs',()=>{
 const p={autoplay:true,passive:false,resizing:false,hidden:false,hovered:true,focused:false,pages:3};
 assert.equal(screen.canAdvanceTV(p),false);
 assert.equal(screen.canAdvanceTV({...p,passive:true}),true);
 for(const change of [{focused:true},{hidden:true},{resizing:true},{autoplay:false},{pages:1}])assert.equal(screen.canAdvanceTV({...p,passive:true,...change}),false);
 assert.equal(screen.canAdvanceTV({...p,hovered:false}),true);
});

// Execute the actual singleton composable in isolated local browser stores.
// Sharing TV layout state must not share presentation or publish a layout POST.
function localScreen(href,store=new Map(),restricted=false){
 const prev={window:globalThis.window,document:globalThis.document};
 const writes=[],layoutCalls=[];const scope=Vue.effectScope();let value;
 const location=new URL(href);
 globalThis.window={location,history:{state:{back:null},replaceState(state,_unused,url){this.state=state;location.href=String(url);}}};
 globalThis.document={documentElement:{dataset:{},style:{setProperty(){}}}};
 const tv={state:Vue.reactive({value:{density:'10'}}),sync:{update:p=>layoutCalls.push(p)}};
 const deps={vue:Vue,'../tvScreen':screen,'../display':display,'./useTVProfile':{useTVProfile:()=>tv},'../presentation':{
  readPreference:(key,fallback)=>restricted?fallback:store.get(key)??fallback,
  savePreference:(key,v)=>{writes.push([key,v]);if(restricted)return false;store.set(key,v);return true;}
 }};
 try{scope.run(()=>{value=module(source('composables/useDisplay.ts'),deps).useDisplay()});
 return{value,writes,layoutCalls,location,store,dispose(){scope.stop();Object.assign(globalThis,prev);}};
 }catch(e){scope.stop();Object.assign(globalThis,prev);throw e;}
}
test('V21 actual local toggle persists/reverses explicit bookmark without changing shared profile',()=>{
 const s=localScreen('https://localhost/?display=tv&tvclean=1');
 try{
  assert.equal(s.value.mode.value,'tv');assert.equal(s.value.tvPassive.value,true);
  s.value.setTVPassive(false);assert.equal(s.location.searchParams.get('tvclean'),'0');
  assert.equal(s.store.get(screen.tvScreenKey),'0');assert.deepEqual(s.layoutCalls,[]);
  s.value.setTVPassive(true);s.value.choose('auto');assert.equal(s.value.tvPassive.value,true);assert.equal(s.value.mode.value,'auto');
  assert.equal(s.location.searchParams.get('display'),'auto');assert.deepEqual(s.layoutCalls,[]);
 }finally{s.dispose();}
 const pc=localScreen('https://localhost/?display=tv');
 try{assert.equal(pc.value.tvPassive.value,false);assert.deepEqual(pc.layoutCalls,[]);}finally{pc.dispose();}
});
test('V21 reload keeps screen-only choice; restricted storage remains usable and reports it',()=>{
 const store=new Map();let s=localScreen('https://localhost/?display=tv',store);
 try{s.value.setTVPassive(true);}finally{s.dispose();}
 s=localScreen('https://localhost/?display=tv',store);
 try{assert.equal(s.value.tvPassive.value,true);}finally{s.dispose();}
 s=localScreen('https://localhost/?display=tv',new Map(),true);
 try{s.value.setTVPassive(true);assert.equal(s.value.tvPassive.value,true);assert.match(s.value.notice.value,/не разрешил сохранить/);assert.deepEqual(s.layoutCalls,[]);}finally{s.dispose();}
});

// Compile and execute the real templates, not only source-text assertions.
// SSR is markup/lifecycle evidence, not measured Chromium/TV layout evidence.
const link={props:['to'],setup:(p,{slots,attrs})=>()=>Vue.h('a',{...attrs,href:p.to},slots.default?.())};
function fixture(passive=true,phase='synced',count=1){
 const model={tvPassive:Vue.ref(passive),mode:Vue.ref('tv'),density:Vue.ref('10'),choose(){},size(){},setTVPassive(){}};
 const tv={state:Vue.reactive({phase,inFlight:false,value:{widths:[9.5,3.5,8,6,8],density:'10',autoplay:true}}),sync:{canChange:()=>true,begin:()=>true,update(){},cancel(){}}};
 const layout={enabled:true,resizing:false,notice:'example notice',style:{'--overview-columns':'90px 35px 80px 60px 80px 250px 30px'},cancel(){},reset(){}};
 const deps={vue:Vue,'../tvScreen':screen,'../composables/useDisplay':{useDisplay:()=>model},'../composables/useTVProfile':{useTVProfile:()=>tv},
 '../composables/useOverviewColumns':{useOverviewColumns:()=>layout},'../overviewColumns':{columnLabels:['Машина','CPU','RAM','DISK','Ping','Сервисы'],columnDefaults:()=>[],keepVisibleOrder:cards=>cards},
 '../format':{pingDetail:()=>'',bytes:x=>String(x),number:x=>String(x),stateLabel:x=>x},'../presentation':{worstDisk:()=>null},'../display':display};
 const stubs={TVProfilePanel:{render:()=>Vue.h('div',{class:'tv-profile-panel'},'profile')},ColumnRemoteControl:{render:()=>Vue.h('div',{},'remote')},ColumnDividers:{render:()=>Vue.h('div',{class:'column-dividers',role:'separator'})},ServiceList:{render:()=>Vue.h('div',{},'service')}};
 function load(name){
  if(stubs[name])return stubs[name];
  const s=compileScript(parse(source('components/'+name+'.vue')).descriptor,{id:'v21-'+name,inlineTemplate:true}).content;
  const imports={...deps};for(const child of ['TVProfilePanel','ColumnRemoteControl','ColumnDividers','ServiceList','OverviewColumnHeader'])if(name!==child)imports['./'+child+'.vue']={default:loadChild(child)};
  return module(s,imports).default;
 }
 function loadChild(name){return stubs[name]||load(name);}
 const cards=Array.from({length:count},(_,i)=>({id:'v21-'+i,name:'Machine '+i,os:'linux',arch:'amd64',state:'ok',cpu:5,ram_total:100,ram_used:20,services:[{}]}));
 async function render(name,props={}){const previous=globalThis.document;globalThis.document={fullscreenElement:null};try{const app=Vue.createSSRApp(load(name),props);app.component('router-link',link);return await renderToString(app);}finally{globalThis.document=previous;}}
 return{render,cards,model,tv,layout};
}
test('V21 passive TV renders machine rows last, no footer/profile/resize targets, keeps two named actions',async()=>{
 const f=fixture(),html=await f.render('TVBoard',{cards:f.cards,fresh:()=>true,priority:()=>0});
 assert.doesNotMatch(html,/class="tv-profile-panel|class="tv-pager|class="column-dividers|column-notice/);
 assert.match(html,/tv-passive/);assert.match(html,/<article/);assert.match(html,/title="Открыть консоль"/);assert.match(html,/title="Графики и параметры"/);
 assert.equal((html.match(/class="tv-icon-action"/g)||[]).length,2);
 assert.match(html,/aria-label="Консоль Machine 0"/);assert.match(html,/aria-label="Открыть Machine 0"/);
 assert.match(html,/aria-hidden="true" focusable="false"/);
 assert.doesNotMatch(html.slice(html.lastIndexOf('</article>')+10),/<(footer|p|button|div|a)\b/);
});
test('V21 editor retains controls; passive multi-page strip moves above rows',async()=>{
 const normal=fixture(false),html=await normal.render('TVBoard',{cards:normal.cards,fresh:()=>true,priority:()=>0});
 assert.match(html,/tv-profile-panel/);assert.match(html,/tv-pager/);assert.match(html,/column-dividers/);
 const f=fixture(true,'synced',9),multi=await f.render('TVBoard',{cards:f.cards,fresh:()=>true,priority:()=>0});
 assert.ok(multi.indexOf('tv-page-strip')<multi.indexOf('<article'));assert.doesNotMatch(multi,/tv-pager/);assert.match(multi,/Страницы машин/);
});
test('V21 hidden-panel exit is visible at top; profile faults remain visible there',async()=>{
 for(const phase of ['synced','local','error','unknown']){
  const f=fixture(true,phase),html=await f.render('DisplayControls');
  assert.match(html,/Показать панель ТВ/);assert.doesNotMatch(html,/aria-label="Плотность ТВ"/);
  if(phase!=='synced')assert.match(html,/tv-sync-warning/);
 }
 const f=fixture(false,'editing'),html=await f.render('DisplayControls');
 assert.match(html,/Плотность ТВ/);assert.match(html,/class="tv-tools-toggle"[^>]*disabled/);
});
test('V21 omitted interactive still keeps ordinary overview resize handles; only explicit false hides them',async()=>{
 const f=fixture(false);
 const shown=await f.render('OverviewColumnHeader',{layout:f.layout});
 assert.match(shown,/column-dividers/);
 const hidden=await f.render('OverviewColumnHeader',{layout:f.layout,interactive:false});
 assert.doesNotMatch(hidden,/column-dividers/);
});
test('V21 compact TV actions have real 30px square boxes and focus outline, not browser-dependent text icons',()=>{
 const css=source('styles.css'),rule=css.match(/\.tv-machine-actions \.tv-icon-action\{([^}]+)\}/)?.[1]||'';
 assert.match(rule,/width:30px/);assert.match(rule,/height:30px/);assert.match(rule,/padding:0/);
 assert.match(css,/\.tv-machine-actions \.tv-icon-action:focus\{outline:3px/);
 assert.match(source('composables/useOverviewColumns.ts'),/mode\(\) === 'tv' \? 30 : 2 \* font\.value/);
});
