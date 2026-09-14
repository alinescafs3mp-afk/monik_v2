import {ref,watchEffect} from 'vue';
import {readPreference,savePreference} from '../presentation';
import {displayMode,tvDensity,type DisplayMode} from '../display';
const query=new URLSearchParams(window.location.search).get('display');
const mode=ref<DisplayMode>(displayMode(query||readPreference('monik:display','auto')));
const density=ref(tvDensity(readPreference('monik:tv-font:v8','10'))),notice=ref('');
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
function size(value:string){density.value=tvDensity(value);notice.value=savePreference('monik:tv-font:v8',density.value)?'':'Плотность изменена, но браузер не сохранил выбор.';}
export function useDisplay(){return{mode,density,notice,choose,size};}
