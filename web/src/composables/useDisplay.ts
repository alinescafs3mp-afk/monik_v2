import {computed,ref,watchEffect} from 'vue';
import {tvScreenKey,tvPassiveChoice} from '../tvScreen';
import {useTVProfile} from './useTVProfile';
import {readPreference,savePreference} from '../presentation';
import {displayMode,tvDensity,type DisplayMode} from '../display';
const query=new URLSearchParams(window.location.search).get('display');
const mode=ref<DisplayMode>(displayMode(query||readPreference('monik:display','auto')));
const notice=ref('');
const passiveQuery=new URLSearchParams(window.location.search).get('tvclean');
const tvPassive=ref(tvPassiveChoice(passiveQuery,readPreference(tvScreenKey,'0')));
if(passiveQuery==='1'||passiveQuery==='0')savePreference(tvScreenKey,tvPassive.value?'1':'0');
const tv=useTVProfile();
const density=computed(()=>tv.state.value.density);
if(query && ['auto','compact','tv'].includes(query))savePreference('monik:display',mode.value);
watchEffect(()=>{
 document.documentElement.dataset.display=mode.value;
 document.documentElement.style.setProperty('--tv-font-size',density.value+'px');
});
function choose(value:string){
 mode.value=displayMode(value);
 notice.value=savePreference('monik:display',mode.value)?'':'Выбор действует до закрытия страницы: хранилище недоступно.';
 // An explicit deep link must not undo a subsequent user choice on reload.
 try{const url=new URL(window.location.href);if(url.searchParams.has('display')){url.searchParams.set('display',mode.value);window.history.replaceState(window.history.state,'',url);}}catch{/* The in-memory choice remains usable in restricted browsers. */}
}
function setTVPassive(value:boolean){
 tvPassive.value=value;
 notice.value=savePreference(tvScreenKey,value?'1':'0')?'':'Режим показа действует в этой вкладке: браузер не разрешил сохранить настройку.';
 // Keep an explicit wallboard bookmark from undoing the user's local choice.
 try{const url=new URL(window.location.href);if(url.searchParams.has('tvclean')){url.searchParams.set('tvclean',value?'1':'0');window.history.replaceState(window.history.state,'',url);}}catch{/* This screen still changes immediately. */}
}
function size(value:string){void tv.sync.update({density:tvDensity(value)});}
export function useDisplay(){return{mode,density,notice,choose,size,tvPassive,setTVPassive};}
