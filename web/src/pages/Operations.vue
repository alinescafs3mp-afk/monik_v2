<script setup lang="ts">
import {computed,ref,watch} from 'vue';
import {useRoute,useRouter} from 'vue-router';
import {usePolling} from '../composables/usePolling';
import {get,markOperationsRead,pendingRequests,reconcileOperation,type ApiError} from '../api';
import {operationQuery,readTarget,requiresAttention,type OperationRecord,type OperationReadTarget,type OperationPage,type OperationCounts} from '../operations';
import {stateLabel} from '../format';
import OperationReadButton from '../components/OperationReadButton.vue';
const route=useRoute(),router=useRouter();
const filter=ref(String(route.query.filter||'all')),read=ref(String(route.query.read||'all')),search=ref(String(route.query.q||''));
const searchDraft=ref(search.value);
const cursor=ref(''),previous=ref<string[]>([]),next=ref('');
const rows=ref<OperationRecord[]>([]),counts=ref<OperationCounts|null>(null);
const selected=ref<Record<string,OperationReadTarget>>({}),saving=ref(false),actionError=ref(''),notice=ref('');
const pending=ref(pendingRequests());
const {loading,refreshing,error:loadError,refresh}=usePolling(async()=>{
 const url=operationQuery(filter.value,read.value,search.value,cursor.value);
 const d=await get<OperationPage>(url);
 if(url!==operationQuery(filter.value,read.value,search.value,cursor.value))return;
 rows.value=d.operations||[];next.value=d.next_cursor||'';counts.value=d.counts;pending.value=pendingRequests();
});
watch(()=>[route.query.filter,route.query.read,route.query.q],()=>{
 filter.value=String(route.query.filter||'all');read.value=String(route.query.read||'all');search.value=String(route.query.q||'');searchDraft.value=search.value;reset();
});
function reset(){cursor.value='';previous.value=[];selected.value={};void refresh();}
async function choose(value:string){filter.value=value;if(value==='attention')read.value='unread';await filtersChanged();}
async function filtersChanged(){if(filter.value==='attention'&&read.value==='read')filter.value='errors';search.value=searchDraft.value;await router.replace({path:'/operations',query:{filter:filter.value,read:read.value,...(search.value?{q:search.value}:{})}});reset();}
function turn(forward:boolean){if(forward&&next.value){previous.value.push(cursor.value);cursor.value=next.value;}else if(!forward&&previous.value.length){cursor.value=previous.value.pop()!;}selected.value={};void refresh();}
const selectedCount=computed(()=>Object.keys(selected.value).length);
const pageSelected=computed(()=>rows.value.length>0&&rows.value.every(o=>!!selected.value[o.operation_id]));
function selectRow(o:OperationRecord,checked:boolean){const values={...selected.value};if(checked)values[o.operation_id]=readTarget(o);else delete values[o.operation_id];selected.value=values;}
function selectPage(checked:boolean){if(saving.value)return;for(const o of rows.value)selectRow(o,checked);}
async function bulk(value:boolean){
 if(saving.value||!selectedCount.value)return;saving.value=true;actionError.value='';notice.value='';
 const frozen=Object.values(selected.value).map(t=>({...t}));
 try{await markOperationsRead(frozen,value);notice.value=`${value?'Прочитано':'Отметка снята'}: ${frozen.length}. Выполнение операций не изменялось.`;selected.value={};}
 catch(e){const err=e as ApiError;actionError.value=err.status===0||err.status>=500?'Подтверждение не получено. Перечитайте список; отметки могли сохраниться.':err.message;}
 finally{saving.value=false;await refresh();}
}
async function reconcile(key:string){try{await reconcileOperation(key);pending.value=pendingRequests();await refresh();}catch(e){actionError.value=(e as Error).message;}}
</script>
<template>
 <div class="operations-page">
  <p v-if="loadError" class="panel err" role="alert">{{loadError}} <button @click="refresh">Повторить чтение</button></p>
  <p v-if="loading" role="status">Загружаем операции…</p>
  <section v-if="pending.length" class="panel data-warning"><h3>Запросы с неизвестным результатом</h3><p v-for="p in pending" :key="p.key">{{p.action}} · {{p.created}} <button @click="reconcile(p.key)">Проверить исходный запрос</button><small>{{p.key}}</small></p><p>Это чтение результата, не повторное выполнение. Отсутствие ответа не означает отмену.</p></section>
  <nav class="row operation-filters" aria-label="Фильтр операций">
   <button :aria-pressed="filter==='running'" @click="choose('running')">Выполняются {{counts?.running??''}}</button>
   <button :aria-pressed="filter==='attention'" @click="choose('attention')">Требуют внимания {{counts?.unread_attention??''}}</button>
   <button :aria-pressed="filter==='errors'" @click="choose('errors')">С проблемами, включая прочитанные</button>
   <button :aria-pressed="filter==='completed'" @click="choose('completed')">Завершённые</button>
   <button :aria-pressed="filter==='all'" @click="choose('all')">Все {{counts?.total??''}}</button>
  </nav>
  <form class="row operation-filters" @submit.prevent="filtersChanged">
   <label>Прочтение <select v-model="read" aria-label="Прочтение операций" @change="filtersChanged"><option value="all">Все</option><option value="unread">Непрочитанные</option><option value="read">Прочитанные</option></select></label>
   <label>Поиск <input v-model="searchDraft" maxlength="120" type="search" placeholder="Действие, пользователь или ID" aria-label="Поиск операций"/></label><button type="submit">Найти</button><button type="button" :disabled="refreshing" @click="refresh">Перечитать</button>
  </form>
  <div class="row operation-selection" aria-live="polite"><span>Выбрано: {{selectedCount}}</span><button :disabled="saving||!selectedCount" @click="bulk(true)">{{saving?'Сохраняем…':'Прочитать выбранные'}}</button><button :disabled="saving||!selectedCount" @click="bulk(false)">Считать выбранные непрочитанными</button><button :disabled="saving||!selectedCount" @click="selected={}">Снять выбор</button></div>
  <p role="status">{{notice}}</p><p v-if="actionError" role="alert" class="panel err">{{actionError}}</p>
  <p class="muted">Прочтение убирает красное уведомление, но не исправляет и не повторяет операцию. Новый сбой снова потребует внимания. Выбор действует только на отмеченные строки этой страницы.</p>
  <p v-if="!loading&&!rows.length" class="panel">Нет операций в этом фильтре.</p>
  <div v-else class="table-wrap"><table class="operation-table"><thead><tr><th><input type="checkbox" aria-label="Выбрать операции на странице" :checked="pageSelected" :disabled="saving" @change="selectPage(($event.target as HTMLInputElement).checked)"/></th><th>Создана</th><th>Действие</th><th>Пользователь</th><th>Результат выполнения</th><th>Целей</th><th>Прочтение</th></tr></thead><tbody>
   <tr v-for="o in rows" :key="o.operation_id" :class="{'operation-needs-attention':requiresAttention(o),'operation-read':o.read}" :data-operation-id="o.operation_id">
    <td><input type="checkbox" :aria-label="`Выбрать операцию ${o.operation_id}`" :checked="!!selected[o.operation_id]" :disabled="saving" @change="selectRow(o,($event.target as HTMLInputElement).checked)"/></td>
    <td>{{new Date(o.created_at).toLocaleString()}}</td><td><router-link :to="'/operations/'+encodeURIComponent(o.operation_id)">{{o.action}}</router-link></td><td>{{o.actor}}</td>
    <td><span :class="{'err':requiresAttention(o)}">{{stateLabel(o.status)}}</span><small v-if="requiresAttention(o)&&o.status==='running'">Есть проблема у части целей</small></td><td>{{o.targets?.length||0}}</td>
    <td><OperationReadButton :operation="o" @changed="refresh"/></td>
   </tr>
  </tbody></table></div>
  <div class="row operation-pagination"><button :disabled="refreshing||!previous.length" @click="turn(false)">Назад</button><span>Страница {{previous.length+1}} · {{rows.length}} строк · счётчики по всему журналу</span><button :disabled="refreshing||!next" @click="turn(true)">Далее</button></div>
 </div>
</template>
