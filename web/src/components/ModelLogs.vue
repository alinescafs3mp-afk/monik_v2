<script setup lang="ts">
import {computed,nextTick,onBeforeUnmount,ref,watch} from 'vue';
import {api,postRecentAuth} from '../api';
import {boundedLogs,explainCode,type LogRecord} from '../sparks';
const props=defineProps<{agentId:string;binding:Record<string,unknown>;available:boolean}>();
const rows=ref<LogRecord[]>([]),paused=ref(false),follow=ref(true),busy=ref(false),status=ref('Логи не открыты'),error=ref(''),truncated=ref(false),gap=ref(false),viewport=ref<HTMLElement|null>(null);
let stream='',streamRoot='',source:EventSource|null=null,generation=0,disposed=false;
let buffered:LogRecord[]=[],flushTimer=0;
function flush(){
 if(disposed)return;
 if(window.getSelection()?.toString()){flushTimer=window.setTimeout(flush,250);return;}
 const result=boundedLogs(rows.value,buffered);buffered=[];rows.value=result.rows;truncated.value ||= result.truncated;flushTimer=0;
 if(follow.value)void nextTick(()=>{if(viewport.value)viewport.value.scrollTop=viewport.value.scrollHeight;});
}
const root=computed(()=>`/api/v1/sparks/${encodeURIComponent(props.agentId)}/logs`);
const text=computed(()=>rows.value.map(r=>`${r.at?new Date(r.at).toLocaleString():'Время не сообщено'}${r.gap?' [пропуск]':''}  ${r.text}`).join('\n'));
async function release(){
 ++generation;source?.close();source=null;const previous=stream,previousRoot=streamRoot;stream='';streamRoot='';
 if(previous){try{await api(`${previousRoot}/${encodeURIComponent(previous)}`,{method:'DELETE'});}catch{/* The server also expires disconnected streams. */}}
}
function connect(id:string,ticket:number){
 source?.close();source=new EventSource(`${root.value}/${encodeURIComponent(id)}/events`);
 source.onopen=()=>{if(ticket===generation)status.value='LIVE: получаем очищенные записи';};
 source.onerror=()=>{if(ticket===generation){source?.close();source=null;gap.value=true;status.value='Связь с логами потеряна. Продолжение — после явного открытия.';void release();}};
 source.addEventListener('log',event=>{
  if(ticket!==generation||paused.value)return;
  try{
   const raw=(event as MessageEvent).data;if(raw.length>32768)throw new Error('Запись слишком велика');
   const record=JSON.parse(raw) as LogRecord;
   if(typeof record.at!=='string'||typeof record.text!=='string'||!Number.isSafeInteger(record.sequence))throw new Error('Некорректная запись');
   const previous=rows.value.at(-1)?.sequence;if(record.gap||previous!==undefined&&record.sequence>previous+1)gap.value=true;
   const result=boundedLogs(buffered,[record]);buffered=result.rows;truncated.value ||= result.truncated;
   // Batch a flood. Keep selected text intact until selection is released.
   if(!flushTimer)flushTimer=window.setTimeout(flush,200);
  }catch{error.value='Некорректная запись лога пропущена';gap.value=true;}
 });
 source.addEventListener('closed',event=>{if(ticket!==generation)return;try{status.value='Поток закрыт: '+explainCode(JSON.parse((event as MessageEvent).data).reason);}catch{status.value='Поток закрыт';}void release();});
}
async function open(){
 if(busy.value||!props.available)return;busy.value=true;error.value='';
 await release();const ticket=generation,selectedRoot=root.value;
 try{
  const r=await postRecentAuth<{stream_id:string;expires_at:string}>(selectedRoot,props.binding,'Открыть логи модели');
  if(!r.stream_id||!Number.isFinite(Date.parse(r.expires_at)))throw new Error('Сервер не подтвердил поток логов');
  if(disposed||ticket!==generation||!props.available){try{await api(`${selectedRoot}/${encodeURIComponent(r.stream_id)}`,{method:'DELETE'});}catch{}return;}
  stream=r.stream_id;streamRoot=selectedRoot;paused.value=false;status.value='Открываем ограниченный хвост и LIVE';connect(stream,ticket);
 }catch(e){if(!disposed&&ticket===generation){error.value=(e as Error).message;status.value='Логи не открыты';}}
 finally{if(ticket===generation)busy.value=false;}
}
async function pause(){paused.value=true;status.value='Пауза. Подписка освобождена, последние записи сохранены';busy.value=false;await release();}
function scroll(){if(viewport.value&&viewport.value.scrollHeight-viewport.value.scrollTop-viewport.value.clientHeight>32)follow.value=false;}
function clear(){buffered=[];rows.value=[];truncated.value=false;gap.value=false;}
watch(()=>[props.agentId,JSON.stringify(props.binding)],()=>{void release();window.clearTimeout(flushTimer);flushTimer=0;clear();busy.value=false;status.value='Привязка модели изменилась. Откройте логи снова';});
watch(()=>props.available,value=>{if(!value){busy.value=false;void release();status.value='Нет свежего разрешения на логи';}});
onBeforeUnmount(()=>{disposed=true;window.clearTimeout(flushTimer);buffered=[];void release();});
</script>
<template><section class="panel model-logs" aria-labelledby="model-logs-title">
 <h4 id="model-logs-title">Логи модели</h4><p class="muted">Ограниченный хвост и LIVE из разрешённого локального источника. Только очищенные записи; это не терминал.</p>
 <div class="row"><button :disabled="busy||!available" @click="open">{{busy?'Открываем…':paused?'Продолжить чтение':'Открыть / переподключить'}}</button><button :disabled="!stream&&!busy" @click="pause">Пауза</button><label><input v-model="follow" type="checkbox"/> Следить за концом</label><button @click="clear">Очистить экран</button></div>
 <p role="status">{{status}}</p><p v-if="error" class="err" role="alert">{{error}}</p><p v-if="gap||truncated" class="data-warning">{{gap?'Есть пропуски или разрыв соединения. ':''}}{{truncated?'Старые записи удалены из ограниченного буфера.':''}}</p>
 <pre ref="viewport" tabindex="0" class="log-view" aria-label="Очищенные записи лога модели" @scroll="scroll">{{text||'Записей пока нет'}}</pre>
</section></template>
