<script setup lang="ts">
import { ref, watch } from 'vue';
import { get, submitOp } from '../api';
import { usePolling } from '../composables/usePolling';
import { localDateValue } from '../format';
import { historyRange, incidentStateLabel } from '../history';
import TimeBar from '../components/TimeBar.vue';
const emit=defineEmits<{toast:[string,string?]}>();
const incs=ref<any[]>([]),mode=ref<'live'|'history'>('live'),hours=ref(6),end=ref(localDateValue(new Date()));
const state=ref('open'),severity=ref(''),metric=ref(''),entity=ref(''),next=ref(''),pending=ref(''),paging=ref(false);
const zone=Intl.DateTimeFormat().resolvedOptions().timeZone;
let query=new URLSearchParams(),generation=0;
async function load(){
 const ticket=++generation;
 const range=historyRange(hours.value,mode.value==='live'?new Date():end.value);
 const params=new URLSearchParams({...range,state:state.value,severity:severity.value,metric:metric.value,entity_id:entity.value,limit:'100'});
 const data=await get<any>(`/api/v1/incidents?${params}`);
 if(ticket!==generation)return;
 query=params;incs.value=data.incidents || [];next.value=data.next_cursor || '';
}
const {loading,refreshing,error,updated,refresh}=usePolling(load,()=>mode.value==='live');
watch([mode,hours,state,severity,metric],()=>{++generation;void refresh();});
async function fixed(){mode.value='history';await refresh();}
async function more(){
 if(paging.value||!next.value)return;
 paging.value=true;const ticket=generation,params=new URLSearchParams(query);params.set('cursor',next.value);
 try{const data=await get<any>(`/api/v1/incidents?${params}`);if(ticket===generation){const known=new Set(incs.value.map(i=>i.id));incs.value.push(...(data.incidents||[]).filter((i:any)=>!known.has(i.id)));next.value=data.next_cursor||'';}}
 catch(e){error.value=(e as Error).message;}
 finally{paging.value=false;}
}
async function ack(id:string){
 if(pending.value||mode.value==='history')return;pending.value=id;
 try{const r=await submitOp('incident.acknowledge',{incident_id:id});emit('toast','Прочтение подтверждено. Это не означает восстановление.',`/operations/${r.op?.operation_id}`);await refresh();}
 catch{/* Global operation error remains visible. */}finally{pending.value='';}
}
function instant(value:string){return value?new Date(value).toLocaleString():'Нет данных';}
</script>
<template>
 <div>
  <h2>Проблемы и история</h2>
  <TimeBar v-model:mode="mode" v-model:hours="hours"/>
  <div class="row"><label>Конец интервала <input v-model="end" type="datetime-local" step="1" aria-label="Конец интервала"/></label><button :disabled="refreshing" @click="fixed">Показать историю</button><span class="muted">{{ zone }}</span></div>
  <div class="row">
   <label>Состояние <select v-model="state" aria-label="Состояние"><option value="open">Открытые</option><option value="all">Все</option><option value="resolved">Восстановленные</option><option value="interrupted">Наблюдение прервано</option><option value="confirmed">Подтверждённые</option><option value="pending">Ожидают подтверждения</option></select></label>
   <label>Серьёзность <select v-model="severity" aria-label="Серьёзность"><option value="">Любая</option><option value="warning">Предупреждение</option><option value="critical">Критическая</option></select></label>
   <label>Метрика <select v-model="metric" aria-label="Метрика"><option value="">Любая</option><option value="cpu">CPU</option><option value="ram">RAM</option><option value="disk">DISK</option><option value="http">HTTP</option></select></label>
   <label>ID машины или сервиса <input v-model="entity" placeholder="Все объекты" aria-label="ID машины или сервиса" @keyup.enter="refresh"/></label><button :disabled="refreshing" @click="refresh">{{ refreshing?'Читаем…':'Применить фильтры' }}</button>
  </div>
  <p class="muted">Инциденты, пересекающие выбранный интервал. Подтверждение прочтения не меняет здоровье. HISTORY закрепляет время; данные отражают известные сейчас результаты, а не точную копию тогдашнего экрана.</p>
  <p v-if="error" role="alert" class="panel err">{{ error }} <button @click="refresh">Повторить чтение</button></p>
  <p v-if="loading" role="status">Загружаем инциденты…</p>
  <p v-else-if="!incs.length&&!error" class="panel">Инцидентов по выбранным условиям нет.</p>
  <div v-if="incs.length" class="table-wrap"><table><thead><tr><th>Начало / завершение</th><th>Объект</th><th>Метрика</th><th>Состояние</th><th>Причина</th><th>Прочтение</th></tr></thead><tbody>
   <tr v-for="i in incs" :key="i.id"><td>{{ instant(i.opened_at) }}<small v-if="i.resolved_at"> → {{ instant(i.resolved_at) }}</small></td>
    <td><router-link v-if="i.entity_type==='agent'" :to="{path:'/machines/'+encodeURIComponent(i.entity_id),query:{at:i.opened_at,hours:1}}">{{ i.entity_id }}</router-link><span v-else>{{ i.entity_type }} {{ i.entity_id }}</span></td>
    <td>{{ i.metric }}</td><td>{{ incidentStateLabel(i.status) }} · {{ i.severity==='critical'?'Критическая':'Предупреждение' }}</td><td>{{ i.reason }}</td>
    <td><span v-if="i.acked_at">Прочитан {{ instant(i.acked_at) }}</span><button v-else :disabled="!!pending||mode==='history'" @click="ack(i.id)">{{ pending===i.id?'Сохраняем…':'Прочитано' }}</button></td>
   </tr></tbody></table></div>
  <button v-if="next" :disabled="paging||refreshing" @click="more">{{ paging?'Загружаем…':'Ещё 100' }}</button>
  <p class="muted">{{ incs.length }} записей · {{ updated?`Данные прочитаны ${updated.toLocaleTimeString()}`:'' }}</p>
 </div>
</template>
