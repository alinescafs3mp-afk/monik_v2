import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import {createRenderer,ref,nextTick} from 'vue';
import ts from 'typescript';
const compile=s=>ts.transpileModule(s,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const uri=s=>'data:text/javascript;base64,'+Buffer.from(compile(s)).toString('base64');
const geometryURL=uri(await readFile(new URL('../src/overviewColumns.ts',import.meta.url),'utf8'));
const c=await import(geometryURL);
test('V17 shared column fitting never exceeds row width and reserves service space',()=>{
 for(const mode of ['auto','compact','tv'])for(const font of [10,12,13.3333,14,16,17.5,20,32])for(let space=320;space<3600;space+=17){
  const min=c.columnMinima(mode).map(n=>n*font),w=c.fitColumns(c.columnDefaults(mode).map(n=>n*font),min,space);
  if(!w){assert.ok(space<min.reduce((a,b)=>a+b,0));continue;}
  assert.ok(Math.abs(w.reduce((a,b)=>a+b,0)-space)<1e-6);
  w.forEach((v,i)=>assert.ok(v>=min[i]-1e-7));
 }
});
test('V17 each separator moves only its two neighbours and cannot collapse either',()=>{
 const min=[100,48,80,80,80,160],w=c.fitColumns([152,72,125,125,128],min,1100);
 for(let i=0;i<5;i++)for(const delta of [-10000,-100,-1,0,1,100,10000]){
  const out=c.moveColumn(w,min,i,delta);assert.ok(Math.abs(out.reduce((a,b)=>a+b,0)-1100)<1e-7);
  out.forEach((v,j)=>{assert.ok(v>=min[j]-1e-7);if(j!==i&&j!==i+1)assert.equal(v,w[j]);});
 }
 assert.deepEqual(w,c.fitColumns([152,72,125,125,128],min,1100),'input mutated');
});
test('V17 malformed preferences and arbitrary CSS cannot enter grid styles',()=>{
 for(const raw of ['null','{}','[]','bad',JSON.stringify({version:1,widths:[3,4,5,6,'url(evil)']}),JSON.stringify({version:1,widths:[3,4,5,6,-1]}),JSON.stringify({version:2,widths:[3,4,5,6,7]}),' '.repeat(600)])
  assert.deepEqual(c.decodeColumns(raw,'auto'),c.columnDefaults('auto'));
 assert.deepEqual(c.decodeColumns(JSON.stringify({version:1,widths:[8,6,9,8,7]}),'auto'),[8,6,9,8,7]);
});
test('V17 shrink-to-mobile leaves saved desktop widths unchanged and restores them later',()=>{
 const saved=[240,160,180,190,160],min=[112,48,80,80,80,160];const copy=[...saved];
 assert.equal(c.fitColumns(saved,min,300),null);c.fitColumns(saved,min,700);assert.deepEqual(saved,copy);
 assert.deepEqual(c.fitColumns(saved,min,1400).slice(0,5),saved);
});
test('V17 frozen rows keep their order but receive current telemetry',()=>{
 const fresh=[{id:'b',cpu:90},{id:'a',cpu:4},{id:'c',cpu:8}];const rows=c.keepVisibleOrder(fresh,['a','b']);
 assert.deepEqual(rows.map(x=>x.id),['a','b','c']);assert.equal(rows[0],fresh[1]);assert.equal(rows[0].cpu,4);assert.equal(fresh[0].id,'b');
});
test('V17 geometry safely rejects nonfinite and malformed numeric inputs',()=>{
 for(const n of [NaN,Infinity,-1])assert.equal(c.fitColumns([1,2,3,4,5],[1,2,3,4,5,6],n),null);
 assert.deepEqual(c.moveColumn([1,2,3,4,5,6],[1,1,1,1,1,1],99,10),[1,2,3,4,5,6]);
});

// Execute the actual composable with real Vue lifecycles. Only browser layout,
// pointer capture and storage are simulated, not its event handling or state.
const hookSrc=await readFile(new URL('../src/composables/useOverviewColumns.ts',import.meta.url),'utf8');
const vueURL=new URL('../node_modules/vue/dist/vue.runtime.esm-bundler.js',import.meta.url).href;
const hook=await import(uri(hookSrc.replace("from 'vue'",`from '${vueURL}'`).replace("from '../overviewColumns'",`from '${geometryURL}'`)));
const renderer=createRenderer({createElement:()=>({}),createText:()=>({}),createComment:()=>({}),setElementText(){},setText(){},parentNode(){},nextSibling(){},insert(){},remove(){},patchProp(){}});
async function harness(run){
 const old={};for(const name of ['window','document','localStorage','getComputedStyle','requestAnimationFrame','cancelAnimationFrame','ResizeObserver'])old[name]=globalThis[name];
 const saved=new Map(),listeners=new Map(),frames=new Map();let frame=0,font=16,rect=1300,writes=0,throws=false;
 globalThis.window={innerWidth:1550,addEventListener:(k,fn)=>listeners.set(k,fn),removeEventListener:k=>listeners.delete(k)};
 globalThis.document={documentElement:{},hidden:false,addEventListener(){},removeEventListener(){}};
 globalThis.localStorage={getItem:k=>saved.get(k)||null,setItem:(k,v)=>{if(throws)throw Error('restricted');writes++;saved.set(k,v);}};
 globalThis.getComputedStyle=()=>({fontSize:String(font)});
 globalThis.requestAnimationFrame=fn=>{frames.set(++frame,fn);return frame;};globalThis.cancelAnimationFrame=id=>frames.delete(id);
 globalThis.ResizeObserver=class{observe(){}unobserve(){}disconnect(){}};
 let layout;const mode=ref('auto'),active=ref(true),root=ref({getBoundingClientRect:()=>({width:rect})});
 const app=renderer.createApp({setup(){layout=hook.useOverviewColumns(root,()=>mode.value,()=>active.value);return()=>null;}});app.mount({});
 const flush=async()=>{for(const [id,fn] of [...frames]){frames.delete(id);fn();}await nextTick();};await flush();
 let captured=null;const element={isConnected:true,focus(){},setPointerCapture:id=>{captured=id;},hasPointerCapture:id=>captured===id,releasePointerCapture(){captured=null;}};
 const event=(x,pointer=1)=>({isPrimary:true,button:0,pointerId:pointer,clientX:x,currentTarget:element,preventDefault(){},stopPropagation(){}});
 try{await run({layout,mode,active,saved,event,flush,element,listeners,get writes(){return writes;},restrict:()=>{throws=true;},resize:async(w,v=1550)=>{rect=w;window.innerWidth=v;listeners.get('resize')();await flush();},key:(key,index=1,shiftKey=false)=>layout.key({key,shiftKey,preventDefault(){}},index)});}
 finally{app.unmount();for(const [key,value] of Object.entries(old))if(value===undefined)delete globalThis[key];else globalThis[key]=value;}
}
test('V17 actual drag resizes live, writes only on release, ignores other pointers',()=>harness(async h=>{
 const before=[...h.layout.widths];h.layout.start(h.event(100),1);h.layout.move(h.event(125,2));assert.deepEqual(h.layout.widths,before);
 h.layout.move(h.event(125));assert.equal(h.layout.widths[1],before[1]+25);assert.equal(h.writes,0);
 h.layout.finish(h.event(125));assert.equal(h.writes,1);assert.equal(h.layout.resizing,false);
}));
test('V17 Escape, pointer cancellation and window resize undo unfinished gestures',()=>harness(async h=>{
 for(const kind of ['escape','cancel','resize']){const before=[...h.layout.widths];h.layout.start(h.event(0),1);h.layout.move(h.event(10));
  if(kind==='escape')h.key('Escape');else if(kind==='cancel')h.layout.finish(h.event(10),true);else{await h.resize(1100);await h.resize(1300);}
  assert.deepEqual(h.layout.widths,before);assert.equal(h.layout.resizing,false);assert.equal(h.writes,0);
 }
}));
test('V17 keyboard changes widths, mode preferences stay isolated, reset is explicit',()=>harness(async h=>{
 const initial=[...h.layout.widths];h.key('ArrowRight');const custom=[...h.layout.widths];assert.equal(custom[1],initial[1]+8);
 h.mode.value='tv';await h.flush();assert.ok(h.saved.has('monik:overview-columns:v1:auto'));assert.ok(!h.saved.has('monik:overview-columns:v1:tv'));
 h.key('ArrowLeft',3);h.mode.value='auto';await h.flush();assert.deepEqual(h.layout.widths,custom);
 h.layout.reset();assert.deepEqual(h.layout.widths,initial);
}));
test('V17 denied storage never breaks resizing or silently claims persistence',()=>harness(async h=>{
 h.restrict();h.key('ArrowRight');assert.match(h.layout.notice,/не сохранена/);assert.equal(h.layout.enabled,true);
}));

// These are narrow source-structure guards, not browser layout evidence.
test('V17 CSS cannot move the overlay into the content-only grid area',async()=>{
 const css=await readFile(new URL('../src/styles.css',import.meta.url),'utf8');
 const rule=css.match(/\.column-dividers\s*\{([^}]+)\}/)?.[1]||'';
 assert.match(rule,/position:absolute/);assert.match(rule,/inset:0/);
 assert.match(rule,/grid-column:auto/);assert.match(rule,/grid-row:auto/);
 assert.ok(css.indexOf(':root .columns-adjustable .overview-column-header')>css.lastIndexOf('.tv-mode .tv-columns,.tv-mode .tv-machine'));
 assert.ok(css.indexOf(':root .columns-adjustable .host-line')>css.indexOf(':root[data-display="compact"] .host-line'));
});
test('V17 splitter coordinate uses padding edge once and matches adjacent grid tracks',()=>harness(async h=>{
 const expected=12+h.layout.widths[0]+9.6/2;
 assert.ok(Math.abs(h.layout.positions[0]-expected)<1e-6);
 h.key('ArrowRight',0);assert.ok(Math.abs(h.layout.positions[0]-expected-8)<1e-6);
}));

test('V17 a remotely removed captured row cannot leave the layout stuck dragging',()=>harness(async h=>{
 const before=[...h.layout.widths];h.layout.start(h.event(0),1);h.layout.move(h.event(8));
 h.element.isConnected=false;h.listeners.get('pointerup')(h.event(8));
 assert.deepEqual(h.layout.widths,before);assert.equal(h.layout.resizing,false);assert.equal(h.writes,0);
}));
test('V17 wide TV preferences survive reload and fit back into smaller content width',()=>{
 const raw=JSON.stringify({version:1,widths:[350,5,8,7,8]});
 assert.equal(c.decodeColumns(raw,'tv')[0],350);
 const pixels=c.decodeColumns(raw,'tv').map(n=>n*10),min=c.columnMinima('tv').map(n=>n*10);
 const fitted=c.fitColumns(pixels,min,900);assert.ok(fitted);assert.ok(fitted[5]>=100-1e-6);
 assert.ok(Math.abs(fitted.reduce((a,b)=>a+b,0)-900)<1e-6);
});
