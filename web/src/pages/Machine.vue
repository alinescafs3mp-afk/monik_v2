<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { bytes, number, localDateValue, stateLabel, finite } from "../format";
import TimeBar from "../components/TimeBar.vue";
import Spark from "../components/Spark.vue";
import ServiceList from "../components/ServiceList.vue";
const route=useRoute(),router=useRouter(),emit=defineEmits<{toast:[string,string?]}>();
const detail=ref<any>(null), history=ref<any>(null), point=ref<any>(null), pending=ref('');
const mode=ref<'live'|'history'>(route.query.at?'history':'live');
const hours=ref([1,2,3,6,12,24].includes(Number(route.query.hours))?Number(route.query.hours):1);
const end=ref(localDateValue(route.query.at && Number.isFinite(Date.parse(String(route.query.at)))?new Date(String(route.query.at)):new Date()));
const cursor=ref(''), tab=ref(route.hash==='#services'?'services':'summary');
const zone=Intl.DateTimeFormat().resolvedOptions().timeZone;
let generation=0;
async function load() {
 const ticket=++generation,id=String(route.params.id),historical=mode.value==='history';
 const to=historical?new Date(end.value):new Date();if(!Number.isFinite(to.getTime()))throw new Error('Укажите корректные дату и время');
 const from=new Date(to.getTime()-hours.value*3600000),q=new URLSearchParams({agent_id:id,from:from.toISOString(),to:to.toISOString()});
 const [d,h,p]=await Promise.all([get(`/api/v1/agents/${encodeURIComponent(id)}`),get(`/api/v1/history/series?${q}`),historical?get(`/api/v1/history/point?${new URLSearchParams({agent_id:id,at:cursor.value || to.toISOString()})}`):Promise.resolve(null)]);
 if(ticket!==generation||id!==String(route.params.id))return;
 detail.value=d;history.value=h;point.value=p;if(!historical)end.value=localDateValue(to);
}
const {loading,refreshing,error,updated,refresh}=usePolling(load,()=>mode.value==='live');
watch([hours,mode,()=>route.params.id],(v,old)=>{if(v[0]!==old[0]||v[2]!==old[2])cursor.value='';void load().catch(e=>error.value=e.message);});
const host=computed(()=>mode.value==='history'?point.value?.host:detail.value?.host);
const rows=computed<any[]>(()=>history.value?.host || []);
const trialUrl=ref(''); const trialMethod=ref('GET'); const trialExpect=ref('200'); const trialService=ref('');
const secretName=ref(''); const secretHeader=ref('Authorization'); const secretValue=ref('');
function series(key:string,divisor=1){return rows.value.map(p=>({t:p.observed_at,v:finite(p[key])?p[key]/divisor:null,min:finite(p.min?.[key])?p.min[key]/divisor:undefined,max:finite(p.max?.[key])?p.max[key]/divisor:undefined}));}
const diskMounts=computed<string[]>(()=>[...new Set(rows.value.flatMap(p=>(p.payload?.disks || []).map((d:any)=>d.mount)))]);
const sensors=computed<string[]>(()=>[...new Set(rows.value.flatMap(p=>(p.payload?.temperatures || []).map((t:any)=>`${t.source}: ${t.label}`)))]);
function diskSeries(mount:string){return series('disk:'+mount);}
function tempSeries(sensor:string){return series('temperature:'+sensor);}
async function fixed(){cursor.value='';mode.value='history';await router.replace({query:{hours:hours.value,at:new Date(end.value).toISOString()}});await refresh();}
async function select(at:string){if(mode.value==='live')end.value=localDateValue(new Date());mode.value='history';cursor.value=at;try{point.value=await get(`/api/v1/history/point?${new URLSearchParams({agent_id:String(route.params.id),at})}`);}catch(e){error.value=(e as Error).message;}}
async function action(name:string, params:Record<string,unknown>={}){pending.value=name;try{const r=await submitOp(name,params,[String(route.params.id)]);emit('toast','Операция сохранена. Ждём результат агента.',`/operations/${r.op?.operation_id}`);}finally{pending.value='';}}
async function trial(){
  pending.value='check.trial';
  try{
    const expected=trialExpect.value.split(',').map(s=>Number(s.trim())).filter(n=>Number.isFinite(n));
    const r=await submitOp('check.trial',{url:trialUrl.value,method:trialMethod.value,expected_status:expected,service_id:trialService.value,kind:'http_health'},[String(route.params.id)]);
    emit('toast','Пробный запрос поставлен в очередь агента. Определение не сохранено.',`/operations/${r.op?.operation_id}`);
  } finally { pending.value=''; }
}
async function saveCheck(){
  pending.value='check.apply';
  try{
    const expected=trialExpect.value.split(',').map(s=>Number(s.trim())).filter(n=>Number.isFinite(n));
    const r=await submitOp('check.apply',{check:{id:crypto.randomUUID(),service_id:trialService.value,url:trialUrl.value,method:trialMethod.value,kind:'http_health',expected_status:expected,timeout_seconds:2}},[String(route.params.id)]);
    emit('toast','Проверка сохранена в желаемой конфигурации.',`/operations/${r.op?.operation_id}`);
  } finally { pending.value=''; }
}
async function replaceSecret(){
  pending.value='secret.replace';
  try{
    const r=await submitOp('secret.replace',{name:secretName.value,header:secretHeader.value,value:secretValue.value},[String(route.params.id)]);
    secretValue.value='';
    emit('toast','Секрет записан без журналирования значения.',`/operations/${r.op?.operation_id}`);
  } finally { pending.value=''; }
}
</script>
<template>
 <div>
  <p v-if="error" class="panel err" role="alert">{{ error }}. Последние показанные данные могут быть устаревшими. <button @click="refresh">Повторить чтение</button></p>
  <div v-if="loading" class="skeleton"/>
  <template v-if="detail">
   <header class="bar"><div><router-link to="/">← Обзор</router-link><h2>{{ detail.agent.display_name || detail.agent.hostname }}</h2><p class="muted">{{ detail.agent.os }} / {{ detail.agent.arch }} · <span class="dot" :class="detail.state"/> {{ stateLabel(detail.state) }} (сейчас)</p></div><div class="row"><button :disabled="!!pending || mode==='history'" @click="action('agent.collect_now')">{{ pending==='agent.collect_now'?'Отправляем…':'Собрать сейчас' }}</button><button :disabled="!!pending || mode==='history'" @click="action('agent.discover_now')">{{ pending==='agent.discover_now'?'Отправляем…':'Обнаружить сервисы' }}</button></div></header>
   <div class="row detail-tabs"><button v-for="[id,label] in [['summary','Параметры и графики'],['services','Сервисы'],['agent','Агент и конфигурация']]" :key="id" :aria-pressed="tab===id" @click="tab=id">{{ label }}</button></div>
   <TimeBar v-model:mode="mode" v-model:hours="hours"/>
   <form class="row time-form" @submit.prevent="fixed"><label>Конец интервала <input v-model="end" type="datetime-local" step="1" required/></label><button :disabled="refreshing">Открыть дату</button><small class="muted">{{ zone }} · {{ mode==='live'?'Окно обновляется':'HISTORY: окно закреплено' }}</small></form>
   <p v-if="mode==='history'" class="panel data-warning">Срез {{ cursor || end }}. {{ point?.found ? `Измерено ${point.age_seconds.toFixed(0)} с до выбранного момента` : 'Измерений нет' }}. {{ point?.fresh ? '' : 'Нет свежего подтверждения состояния в этой точке.' }} Сведения об агенте и список сервисов ниже относятся к текущему инвентарю, не к прошлому.</p>
   <section v-if="tab==='summary'" class="panel">
    <dl class="metric-grid"><div><dt>CPU</dt><dd>{{ number(host?.cpu_percent,'%',1) }}</dd><small>{{ host?.cpu_model }}</small></div><div><dt>RAM</dt><dd>{{ bytes(host?.ram_used_bytes) }}</dd><small>Всего {{ bytes(host?.ram_total_bytes) }} · доступно {{ bytes(host?.ram_available_bytes) }}</small></div><div><dt>Средний ping</dt><dd>{{ number(host?.ping?.mean_ms,' мс',1) }}</dd><small>Потери {{ number(host?.ping?.loss_percent,'%') }} · {{ host?.ping?.target || '8.8.8.8' }}</small></div><div><dt>Uptime машины</dt><dd>{{ number(host?.system_uptime_seconds ? host.system_uptime_seconds/3600 : host?.system_uptime_seconds,' ч',1) }}</dd></div></dl>
    <div class="chart-grid"><Spark :points="series('cpu_pct')" label="CPU" unit="%" :step="history?.step_seconds" @select="select"/><Spark :points="series('ram_used',1073741824)" label="Занятая RAM" unit=" GiB" :step="history?.step_seconds" @select="select"/><Spark :points="series('ping_mean_ms')" label="Средний ping за окно агента" unit=" мс" :step="history?.step_seconds" @select="select"/><Spark :points="series('ping_loss')" label="Потери ping за окно агента" unit="%" :step="history?.step_seconds" @select="select"/><Spark v-for="mount in diskMounts" :key="mount" :points="diskSeries(mount)" :label="`DISK ${mount}`" unit="%" :step="history?.step_seconds" @select="select"/><Spark v-for="sensor in sensors" :key="sensor" :points="tempSeries(sensor)" :label="sensor" unit=" °C" :step="history?.step_seconds" @select="select"/></div>
    <p v-if="!sensors.length" class="muted">Температурные датчики не предоставили измерения в этом интервале.</p><p class="muted">Разрешение графиков: {{ history?.step_seconds }} с. Вертикальные отметки сохраняют минимум/максимум параметра внутри каждого бакета. Линия показывает последнее измерение. Пропуски не соединяются. Клик по графику открывает срез.</p>
    <div class="table-wrap"><table><thead><tr><th>Диск</th><th>Занято</th><th>Всего</th><th>Доступно</th></tr></thead><tbody><tr v-for="d in host?.disks || []" :key="d.mount"><td>{{ d.mount }} ({{ d.fs }})</td><td>{{ bytes(d.used_bytes) }}</td><td>{{ bytes(d.total_bytes) }}</td><td>{{ bytes(d.available_bytes) }}</td></tr></tbody></table></div>
   </section>
   <section v-if="tab==='services'||tab==='summary'" id="services" class="panel"><h3>Сервисы: текущее состояние</h3><ServiceList :services="detail.services || []"/></section>
   <section v-if="tab==='services'" class="panel">
    <h3>Проверка HTTP</h3>
    <p class="muted">«Пробный запрос» выполняется один раз на агенте по локальной политике и не сохраняет определение. Сохранение — отдельная операция.</p>
    <label>Сервис <select v-model="trialService"><option value="">не выбран</option><option v-for="s in detail.services || []" :key="s.id" :value="s.id">{{ s.display_name || s.url }}</option></select></label>
    <label>URL <input v-model="trialUrl" autocomplete="off"/></label>
    <label>Метод <select v-model="trialMethod"><option>GET</option><option>HEAD</option></select></label>
    <label>Ожидаемые коды <input v-model="trialExpect"/></label>
    <div class="row"><button :disabled="!!pending || mode==='history' || !trialUrl" @click="trial">{{ pending==='check.trial'?'Отправляем…':'Пробный запрос' }}</button><button :disabled="!!pending || mode==='history' || !trialUrl || !trialService" @click="saveCheck">{{ pending==='check.apply'?'Отправляем…':'Сохранить проверку' }}</button></div>
   </section>
   <section v-if="tab==='agent'" class="panel"><h3>Агент</h3><p>{{ detail.agent.id }}</p><p>Worker: {{ detail.agent.worker_version }} · service-host: {{ detail.agent.service_host_version || 'не сообщил версию' }}</p><p>Желаемая / применённая ревизия: {{ detail.agent.desired_revision }} / {{ detail.agent.applied_revision }}</p><p>Совпадение конфигурации: {{ detail.agent.desired_revision===detail.agent.applied_revision && detail.agent.desired_hash===detail.agent.applied_hash ? 'подтверждено' : 'ожидаем применения' }}</p><p>Управляемая установка: {{ detail.agent.managed_ready?'подтверждена отчётом агента':'не подтверждена' }}. Без неё удалённый перезапуск и замена бинарника отклоняются.</p><p class="muted">Подписанная раскатка и безопасный ребинд доступны из разделов «Обновления» и «Машины». Неподтверждённая нативная служба Windows и суточный прогон по-прежнему не считаются принятыми.</p><div class="row"><button :disabled="!!pending || mode==='history' || !detail.agent.managed_ready" @click="action('agent.restart')">{{ pending==='agent.restart'?'Отправляем…':'Перезапустить агент' }}</button><button :disabled="!!pending || mode==='history'" @click="action('credential.rotate')">{{ pending==='credential.rotate'?'Отправляем…':'Сменить API-ключ агента' }}</button></div>
    <h3>Секрет для проверки</h3>
    <p class="muted">Значение не попадает в журнал операций. Нужна недавняя аутентификация.</p>
    <label>Имя <input v-model="secretName" autocomplete="off"/></label>
    <label>Заголовок <input v-model="secretHeader" autocomplete="off"/></label>
    <label>Значение <input v-model="secretValue" type="password" autocomplete="new-password"/></label>
    <button :disabled="!!pending || mode==='history' || !secretName || !secretHeader || !secretValue" @click="replaceSecret">{{ pending==='secret.replace'?'Отправляем…':'Записать секрет' }}</button>
   </section>
   <small class="muted">{{ updated ? `Последнее чтение ${updated.toLocaleTimeString()}` : '' }}</small>
  </template>
 </div>
</template>
