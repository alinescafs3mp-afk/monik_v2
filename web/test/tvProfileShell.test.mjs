import test from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
import {parse,compileScript} from '@vue/compiler-sfc';
import * as Vue from 'vue';

// Execute the compiled production SFC setup with actual Vue lifecycles, not a
// source substring assertion. Browser transport and unrelated children are stubs.
const src=await readFile(new URL('../src/components/AppShell.vue',import.meta.url),'utf8');
const script=compileScript(parse(src).descriptor,{id:'v19-shell'}).content;
const compiled=ts.transpileModule(script,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText;
const renderer=Vue.createRenderer({createElement:()=>({}),createText:()=>({}),createComment:()=>({}),setElementText(){},setText(){},parentNode(){},nextSibling(){},insert(){},remove(){},patchProp(){}});

test('V19 compiled AppShell initializes shared profile only after props and stops on logout',async()=>{
 const saved={window:globalThis.window,EventSource:globalThis.EventSource};
 const calls=[],errors=[];let closed=0;
 globalThis.window={matchMedia:()=>({matches:false,addEventListener(){},removeEventListener(){}}),addEventListener(){},removeEventListener(){},dispatchEvent(){},setInterval:()=>1,clearInterval(){}};
 globalThis.EventSource=class{addEventListener(){}close(){closed++;}};
 const me=Vue.ref({controller_id:'test-controller',username:'owner',role:'owner'});
 const modules={vue:Vue,'vue-router':{useRoute:()=>({fullPath:'/',meta:{}})},'../api':{get:async()=>({unread_attention:0,running:0}),setStream(){}},'../presentation':{readPreference:(_key,fallback)=>fallback,savePreference:()=>true},'../composables/useDisplay':{useDisplay:()=>({mode:Vue.ref('tv'),notice:Vue.ref('')})},'../composables/useTVProfile':{useTVProfile:()=>({start:(...args)=>calls.push(['start',...args]),stop:()=>calls.push(['stop'])})}};
 const exports={};
 new Function('require','exports',compiled)(name=>{if(name.endsWith('.vue'))return{default:{render:()=>null}};assert.ok(modules[name],name);return modules[name];},exports);
 const component={...exports.default,render:()=>null};
 const app=renderer.createApp({setup:()=>()=>Vue.h(component,{me:me.value,stream:'live'})});
 app.config.errorHandler=e=>errors.push(String(e));
 try{
  app.mount({});await Vue.nextTick();
  assert.deepEqual(errors,[],'production SFC setup threw before authentication/profile bootstrap');
  assert.deepEqual(calls[0],['start','test-controller','owner',true]);
  me.value={...me.value,role:'viewer'};await Vue.nextTick();assert.deepEqual(calls.at(-1),['start','test-controller','owner',false]);
  me.value=null;await Vue.nextTick();assert.deepEqual(calls.at(-1),['stop']);
 }finally{app.unmount();for(const[k,v]of Object.entries(saved))if(v===undefined)delete globalThis[k];else globalThis[k]=v;}
 assert.ok(closed>0);assert.deepEqual(errors,[]);
});
