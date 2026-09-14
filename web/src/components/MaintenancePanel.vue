<script setup lang="ts">
import {computed,ref} from 'vue';
import {get,submitOp} from '../api';
import {usePolling} from '../composables/usePolling';
import {maintenanceState} from '../monitoring';
const props=withDefaults(defineProps<{kind?:string;entityId?:string;locked?:boolean}>(),{kind:'fleet',entityId:'',locked:false});
const windows=ref<any[]>([]),purpose=ref(''),minutes=ref(60),start=ref(''),busy=ref(''),notice=ref(''),truncated=ref(false),offset=ref(0),now=ref(new Date());
const {loading,error,refresh}=usePolling(async()=>{const d=await get<any>('/api/v1/monitoring');windows.value=d.maintenance||[];truncated.value=d.truncated;offset.value=Date.parse(d.server_time)-Date.now();now.value=new Date(Date.now()+offset.value);});
const visible=computed(()=>props.kind==='fleet'?windows.value:windows.value.filter(w=>w.entity_type==='fleet'||w.entity_type===props.kind&&w.entity_id===props.entityId));
const title=computed(()=>props.kind==='fleet'?'Обслуживание парка':props.kind==='service'?'Обслуживание сервиса':'Обслуживание машины и её сервисов');
function canCancel(w:any){return !props.locked && !w.cancelled_at && Date.parse(w.end_at)>now.value.getTime() && (props.kind==='fleet' || w.entity_type===props.kind&&w.entity_id===props.entityId);}
async function create(){
 if(busy.value||props.locked)return;
 busy.value='create';notice.value='Сохраняем интервал…';
 try{
  if(!purpose.value.trim()||!Number.isInteger(minutes.value)||minutes.value<1||minutes.value>10080)throw new Error('Укажите причину и целое число минут от 1 до 10080.');
  const stamp=start.value?new Date(start.value):new Date(Date.now()+offset.value);
  if(!Number.isFinite(stamp.getTime()))throw new Error('Некорректное время начала.');
  const params:any={entity_type:props.kind,entity_id:props.entityId,purpose:purpose.value,end_at:new Date(stamp.getTime()+minutes.value*60000).toISOString()};
  if(start.value)params.start_at=stamp.toISOString();
  await submitOp('maintenance.set',params);purpose.value='';start.value='';notice.value='Обслуживание сохранено. Измерения и реальные проблемы продолжают записываться.';await refresh();window.dispatchEvent(new Event('monik:refresh'));
 }catch(e){notice.value=(e as Error).message;}finally{busy.value='';}
}
async function cancel(w:any){if(busy.value||!canCancel(w))return;if(!confirm(`Завершить обслуживание «${w.purpose}»? История сохранится.`))return;busy.value=w.id;
 try{await submitOp('maintenance.cancel',{window_id:w.id});notice.value='Обслуживание завершено. Исторический интервал сохранён.';await refresh();window.dispatchEvent(new Event('monik:refresh'));}catch(e){notice.value=(e as Error).message;}finally{busy.value='';}}
function instant(t:string){return new Date(t).toLocaleString();}
</script>
<template><section class="panel maintenance-panel"><h2>{{title}}</h2>
 <p class="muted">Плановые работы отмечаются отдельно от здоровья. Мониторинг не выключается, красный сервис не становится зелёным. В обзоре отдельно указан счётчик проблем вне обслуживания.</p>
 <p v-if="locked" class="data-warning">Сейчас открыт исторический вид. Вернитесь в LIVE для изменения текущего обслуживания.</p>
 <form @submit.prevent="create"><fieldset :disabled="!!busy||locked"><legend>{{kind==='fleet'?'Новый интервал для всего парка':'Новый интервал для выбранного объекта'}}</legend><div class="form-grid">
  <label>Причина <input v-model="purpose" required maxlength="500" placeholder="Например, замена диска"/></label>
  <label>Начало (пусто = сейчас) <input v-model="start" type="datetime-local"/></label>
  <label>Длительность, минут <input v-model.number="minutes" type="number" min="1" max="10080" required/></label>
 </div><button type="submit">{{busy==='create'?'Сохраняем…':'Запланировать обслуживание'}}</button></fieldset></form>
 <p v-if="notice" role="status">{{notice}}</p><p v-if="error" role="alert" class="err">{{error}} <button @click="refresh">Повторить чтение</button></p><p v-if="loading" role="status">Читаем интервалы…</p><p v-if="truncated" class="data-warning">Список ограничен 200 интервалами; это не полный архив обслуживания.</p>
 <p v-if="!loading&&!visible.length&&!error" class="muted">Интервалов обслуживания нет.</p>
 <div v-if="visible.length" class="table-wrap"><table><thead><tr><th>Объект / причина</th><th>Интервал</th><th>Состояние</th><th>Действие</th></tr></thead><tbody><tr v-for="w in visible" :key="w.id"><td>{{w.entity_type==='fleet'?'Весь парк':w.entity_id}}<p>{{w.purpose}}</p></td><td>{{instant(w.start_at)}} → {{instant(w.end_at)}}<small v-if="w.cancelled_at">Завершено: {{instant(w.cancelled_at)}} ({{w.cancelled_by}})</small></td><td>{{maintenanceState(w,now)}}</td><td><button v-if="canCancel(w)" :disabled="!!busy" @click="cancel(w)">{{busy===w.id?'Завершаем…':'Завершить сейчас'}}</button><span v-else-if="w.entity_type==='fleet'&&kind!=='fleet'">Общее окно: управление в настройках</span></td></tr></tbody></table></div>
</section></template>
