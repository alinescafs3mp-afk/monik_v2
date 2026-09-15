import { ref,onUnmounted } from 'vue';
import {serverNow} from '../api';
import {serviceState,serviceTone,serviceOutcome} from '../display';
/** A render tick is reactive; the clock is read at render after API anchoring. */
export function useServiceDisplay(){
 const tick=ref(0);const timer=window.setInterval(()=>tick.value++,1000);
 onUnmounted(()=>clearInterval(timer));
 const now=()=>{void tick.value;return serverNow();};
 return {state:(s:any)=>serviceState(s,now()),tone:(s:any)=>serviceTone(s,now()),outcome:(s:any)=>serviceOutcome(s,now())};
}
