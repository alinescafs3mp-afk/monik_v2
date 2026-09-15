import { reactive } from 'vue';
import { get, post, newKey } from '../api';
import { createTVProfileSync, initialTVValue, type TVSyncState } from '../tvProfile';
import {decodeColumns} from '../overviewColumns';
import {tvDensity} from '../display';
const state=reactive<TVSyncState>({profile:null,value:initialTVValue(),phase:'loading',message:'',canEdit:false,inFlight:false,controller:''});
const engine=createTVProfileSync({read:()=>get('/api/v1/display/tv'),write:body=>post('/api/v1/display/tv',body),key:newKey},s=>Object.assign(state,s));
const canChange=engine.canChange;
const sync=engine;
// Track the published Vue state without flattening the engine's live getters.
sync.canChange=()=>{void state.phase;void state.inFlight;void state.canEdit;void state.profile;return canChange();};
let timer=0,running=false;
function localDraft(){try{return {widths:decodeColumns(localStorage.getItem('monik:overview-columns:v1:tv'),'tv'),density:tvDensity(localStorage.getItem('monik:tv-font:v8')),autoplay:false};}catch{return initialTVValue();}}
function hint(){if(!document.hidden)void sync.read();}
function start(controller:string,username:string,writable:boolean){
 sync.start(controller+'/'+username,controller,writable,localDraft());
 if(running)return;running=true;timer=window.setInterval(hint,5000);
 window.addEventListener('monik:display-refresh',hint);window.addEventListener('online',hint);document.addEventListener('visibilitychange',hint);
}
function stop(){if(running){clearInterval(timer);window.removeEventListener('monik:display-refresh',hint);window.removeEventListener('online',hint);document.removeEventListener('visibilitychange',hint);}running=false;sync.stop();}
export function useTVProfile(){return{state,sync,start,stop};}
