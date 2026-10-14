<script setup lang="ts">
import {computed,nextTick,onBeforeUnmount,ref,watch} from 'vue';
import {useRoute} from 'vue-router';
import {get,serverNow,submitOp} from '../api';
import {usePolling} from '../composables/usePolling';
import {useSession} from '../session';
import {bytes,finite,stateLabel} from '../format';
import {actionReason,explainCode,freshAt,modelLabel,supervisorLabel,modelParams,type ModelAction,type ModelState,type Profile,type SparkDetail,type SparkSummary} from '../sparks';
import SparkObservation from '../components/SparkObservation.vue';
import ModelLogs from '../components/ModelLogs.vue';
import Spark from '../components/Spark.vue';
const route=useRoute(),emit=defineEmits<{toast:[string,string?]}>(),{owner}=useSession();
const detail=ref<(SparkDetail&{operations?:any[]})|null>(null),summary=ref<SparkSummary|null>(null),machine=ref<any>(null),history=ref<any>(null),historyError=ref('');
const sparkHistory=ref<{samples:Array<{observed_at:string;spark:SparkDetail['spark']}>;truncated?:boolean}|null>(null),selectedAt=ref('');
const tab=ref(['models','storage','diagnostics'].includes(route.hash.slice(1))?route.hash.slice(1):'overview'),selected=ref(''),pending=ref(''),actionError=ref(''),operation=ref<any>(null),now=ref(serverNow());
const confirmation=ref<{action:ModelAction;profile:Profile;state:ModelState;params:Record<string,unknown>}|null>(null),dialog=ref<HTMLDialogElement|null>(null);
let generation=0,disposed=false,returnFocus:HTMLElement|null=null;const abort=new AbortController();
const clock=window.setInterval(()=>now.value=serverNow(),1000);
async function load(){
 const ticket=++generation,id=String(route.params.id),isOwner=owner.value,current=()=>!disposed&&ticket===generation&&id===String(route.params.id)&&isOwner===owner.value;
 const outcomes=await Promise.allSettled([
  get<{sparks:SparkSummary[]}>('/api/v1/sparks',abort.signal),
  get<any>(`/api/v1/agents/${encodeURIComponent(id)}`,abort.signal),
  isOwner?get<SparkDetail&{operations?:any[]}>(`/api/v1/sparks/${encodeURIComponent(id)}`,abort.signal):Promise.resolve(null),
  tab.value==='overview'?get<any>(`/api/v1/history/series?${new URLSearchParams({agent_id:id,from:new Date(serverNow()-3600000).toISOString(),to:new Date(serverNow()).toISOString()})}`,abort.signal):Promise.resolve(history.value),
  isOwner&&tab.value==='overview'?get<any>(`/api/v1/sparks/${encodeURIComponent(id)}/history`,abort.signal):Promise.resolve(null),
 ]);
 if(!current())return;
 const [s,m,d,h,sh]=outcomes;
 if(s.status==='fulfilled')summary.value=s.value.sparks.find(x=>x.agent_id===id)||null;
 if(m.status==='fulfilled')machine.value=m.value;
 if(d.status==='fulfilled')detail.value=d.value;else detail.value=null;
 history.value=h.status==='fulfilled'?h.value:null;historyError.value=h.status==='rejected'?h.reason?.message||'История недоступна':'';
 sparkHistory.value=sh.status==='fulfilled'?sh.value:null;if(sh.status==='rejected')historyError.value+=(historyError.value?'; ':'')+(sh.reason?.message||'История Spark недоступна');
 if(detail.value?.operations?.length){const original=detail.value.operations.find(o=>o.params?.profile_id===selected.value);if(original)operation.value=original;}
 now.value=serverNow();
 const failed=[s,m,...(isOwner?[d]:[])].find(v=>v.status==='rejected');if(failed?.status==='rejected')throw failed.reason;
}
const {loading,error,refreshing,refresh,updated}=usePolling(load);
const online=computed(()=>{const current=detail.value||summary.value;return !!current?.online&&freshAt(current.received_at,now.value);});
const payload=computed(()=>detail.value?.spark);
const metrics=computed(()=>payload.value?.metrics||summary.value?.metrics||[]);
const profiles=computed(()=>payload.value?.inventory?.profiles||[]);
const profile=computed(()=>profiles.value.find(p=>p.profile_id===selected.value));
const state=computed(()=>payload.value?.model_states?.find(s=>s.profile_id===selected.value));
const name=computed(()=>summary.value?.display_name||summary.value?.hostname||machine.value?.agent?.display_name||String(route.params.id));
watch(profiles,list=>{if(!selected.value&&list.length)selected.value=list[0].profile_id;});
watch(selected,()=>{operation.value=detail.value?.operations?.find(o=>o.params?.profile_id===selected.value)||null;actionError.value='';});
watch(()=>route.params.id,()=>{++generation;detail.value=null;summary.value=null;machine.value=null;history.value=null;selected.value='';operation.value=null;closeConfirmation();void refresh();},{flush:'sync'});
watch(owner,()=>{++generation;detail.value=null;closeConfirmation();void refresh();},{flush:'sync'});
watch(tab,value=>{if(value==='overview')void refresh();});
function reason(p:Profile,a:ModelAction){return actionReason(detail.value,p,payload.value?.model_states?.find(s=>s.profile_id===p.profile_id),a,owner.value,now.value,!!pending.value||!!error.value);}
function stateFor(p:Profile){return payload.value?.model_states?.find(s=>s.profile_id===p.profile_id);}
const logReason=computed(()=>!owner.value?'Только для владельца':!profile.value?.log_allowed||!payload.value?.control_state?.enabled?'Локальное разрешение источника логов не подтверждено':!online.value||!freshAt(payload.value.control_state.observed_at,now.value)||state.value?.quality!=='ok'||!freshAt(state.value?.observed_at,now.value)?'Нет свежей привязки к модели':!payload.value.control_state.epoch||!detail.value?.agent_session?'Нет подтверждённого сеанса управления':'');
const logBinding=computed(()=>detail.value?.spark.control_state?.epoch&&detail.value.agent_session&&profile.value&&state.value?(({confirm_disruption,...rest})=>rest)(modelParams(detail.value,profile.value,state.value,false)):{});
function closeConfirmation(){confirmation.value=null;dialog.value?.close();if(returnFocus?.isConnected)returnFocus.focus();returnFocus=null;}
async function ask(p:Profile,a:ModelAction){
 const why=reason(p,a),s=stateFor(p);if(why||!s||!detail.value){actionError.value=why;return;}
 if(a==='start'){await execute(a,p,modelParams(detail.value,p,s,false));return;}
 returnFocus=document.activeElement as HTMLElement;confirmation.value={action:a,profile:{...p},state:{...s},params:modelParams(detail.value,p,s,true)};await nextTick();dialog.value?.showModal();
}
async function confirm(){
 const c=confirmation.value;if(!c)return;
 const p=profiles.value.find(p=>p.profile_id===c.profile.profile_id),s=p&&stateFor(p);
 if(!p||!s||!detail.value||reason(p,c.action)||JSON.stringify(modelParams(detail.value,p,s,true))!==JSON.stringify(c.params)){actionError.value='Состояние или разрешение изменилось. Сверьте данные и подтвердите заново.';closeConfirmation();return;}
 closeConfirmation();await execute(c.action,p,c.params);
}
async function execute(a:ModelAction,p:Profile,params:Record<string,unknown>){
 if(pending.value||reason(p,a))return;pending.value=p.profile_id;actionError.value='';
 try{const r=await submitOp(`model.${a}`,params,[String(route.params.id)]);operation.value=r.op;emit('toast','Операция сохранена. Готовность модели подтверждается отдельным наблюдением.',`/operations/${r.op.operation_id}`);await refresh();}
 catch(e){actionError.value=(e as Error).message||'Не удалось подтвердить действие';}
 finally{pending.value='';}
}
function series(key:string,divisor=1){return (history.value?.host||[]).slice(-1500).map((p:any)=>({t:p.observed_at,v:finite(p[key])?p[key]/divisor:null}));}
function sparkSeries(id:string,divisor=1){return (sparkHistory.value?.samples||[]).slice(0,500).map(p=>{const o=p.spark.metrics.find(m=>m.metric_id===id)?.observation;return {t:o?.observed_at||p.observed_at,v:o?.quality==='ok'&&finite(o.value)?o.value/divisor:null};});}
function exportDiagnostics(){
 const data={agent_id:String(route.params.id),observed_at:summary.value?.observed_at,received_at:summary.value?.received_at,online:online.value,classification:summary.value?.classification,memory_topology:summary.value?.memory_topology,metrics:metrics.value,models:summary.value?.profiles,worker_version:machine.value?.agent?.worker_version};
 const url=URL.createObjectURL(new Blob([JSON.stringify(data,null,2)],{type:'application/json'})),a=document.createElement('a');a.href=url;a.download='monik-spark-diagnostics.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}
onBeforeUnmount(()=>{disposed=true;++generation;abort.abort();window.clearInterval(clock);closeConfirmation();});
</script>
<template><div class="spark-detail">
 <header class="bar"><div><router-link to="/sparks">← Спарки</router-link><h2>{{name}}</h2><p><span class="dot" :class="online?'ok':'stale'"/> {{online?'На связи':'Нет свежей связи'}} · {{(detail?.received_at||summary?.received_at)?new Date(detail?.received_at||summary?.received_at||'').toLocaleString('ru-RU'):'Наблюдения нет'}}</p></div><router-link :to="`/machines/${encodeURIComponent(String(route.params.id))}`">Общая машина, сервисы и история</router-link></header>
 <p v-if="error" class="panel err" role="alert">{{error}}. Последние значения могут быть устаревшими; управление недоступно. <button @click="refresh">Повторить чтение</button></p><p v-if="loading" role="status">Загружаем Spark…</p>
 <p v-if="!loading&&!summary&&!error" class="panel">Spark не найден. <router-link to="/machines">Открыть общий список</router-link></p>
 <template v-if="summary||machine">
 <nav class="row detail-tabs" aria-label="Разделы Spark"><button v-for="[id,label] in [['overview','Обзор'],['models','Модели'],['storage','Хранилище'],['diagnostics','Диагностика']]" :key="id" :aria-pressed="tab===id" @click="tab=id">{{label}}</button><button :disabled="refreshing" @click="refresh">Обновить</button></nav>
 <section v-if="tab==='overview'" class="panel"><h3>Показатели и их источники</h3><p class="data-warning">{{(payload?.memory_topology||summary?.memory_topology)==='unified'?'UMA: одна общая физическая память CPU и GPU. Значения драйвера не прибавляются к RAM.':'Топология памяти: '+(payload?.memory_topology||summary?.memory_topology||'неизвестна')}} Доступная память — запас ОС MemAvailable, а не гарантия запуска модели.</p>
  <dl class="metric-grid spark-metrics"><SparkObservation v-for="m in metrics" :key="m.metric_id" :metric="m" :now="now" :online="online"/></dl><p v-if="!metrics.length">Расширенные измерения ещё не получены. Отсутствие данных не означает нулевую загрузку.</p>
  <p v-if="historyError" class="err" role="alert">{{historyError}}</p><div class="chart-grid"><Spark :points="series('cpu_pct')" label="История CPU" unit="%" :step="history?.step_seconds" :from="history?.from" :to="history?.to" @select="selectedAt=$event"/><Spark :points="series('ram_used',1073741824)" label="История RAM (обычное определение агента)" unit=" GiB" :step="history?.step_seconds" :from="history?.from" :to="history?.to" @select="selectedAt=$event"/><template v-if="owner"><Spark :points="sparkSeries('gpu.0.utilization_percent')" label="История загрузки GPU 0" unit="%" @select="selectedAt=$event"/><Spark :points="sparkSeries('memory.available_bytes',1073741824)" label="История MemAvailable" unit=" GiB" @select="selectedAt=$event"/></template></div><p v-if="selectedAt" role="status">Выбрано измерение {{new Date(selectedAt).toLocaleString('ru-RU')}}. <router-link :to="{path:`/machines/${encodeURIComponent(String(route.params.id))}`,query:{at:selectedAt,hours:1}}">Открыть общий исторический срез</router-link>. Модельное управление использует текущее состояние.</p><p v-if="sparkHistory?.truncated" class="data-warning">Расширенная история ограничена последними 500 сохранёнными отчётами; это не полный архив.</p><p class="muted">История за последний час. Пропуски не соединяются. Полный выбор интервала доступен на общей странице машины.</p>
 </section>
 <section v-if="tab==='models'" class="panel"><h3>Модели и профили</h3><p>Движок, супервизор и готовность показаны отдельно. Наличие процесса не подтверждает готовность к запросам.</p>
  <template v-if="!owner"><p class="data-warning">Режим просмотра: параметры, управление и логи доступны владельцу.</p><article v-for="p in summary?.profiles||[]" :key="p.profile_id" class="model-summary"><h4>{{p.display_name||p.profile_id}}</h4><p>{{p.engine}} · {{supervisorLabel(p.supervisor)}} · {{modelLabel({...p,supervisor_state:p.state},online,now)}}</p></article><p v-if="!summary?.profiles.length">Модельных профилей пока нет в полученных наблюдениях.</p></template>
  <template v-else><p v-if="payload?.inventory&&!payload.inventory.complete" class="data-warning">Инвентарь неполный. Отсутствие профиля не подтверждает его удаление.</p><p v-if="!profiles.length">Настроенные и обнаруженные профили пока не сообщены. Установка агента не создаёт и не запускает модели.</p>
   <label v-if="profiles.length">Профиль <select v-model="selected"><option v-for="p in profiles" :key="p.profile_id" :value="p.profile_id">{{p.display_name||p.profile_id}}</option></select></label>
   <p v-if="selected&&!profile" class="data-warning">Выбранный профиль отсутствует в текущем снимке. Автоматически другой профиль не выбран; сверьте инвентарь и выберите явно.</p>
   <article v-if="profile" class="model-card"><h4>{{profile.display_name||profile.profile_id}}</h4><p>{{modelLabel(state,online,now)}}</p><p v-if="state?.reason" class="data-warning">{{explainCode(state.reason)}}</p><dl class="model-properties"><dt>Движок</dt><dd>{{profile.engine}} · {{profile.engine_version||'версия неизвестна'}}</dd><dt>Супервизор</dt><dd>{{supervisorLabel(profile.supervisor)}} · {{profile.supervisor_version||'версия неизвестна'}}</dd><dt>Модель / файлы</dt><dd>{{profile.configured_model_ref||'Не сообщено'}}</dd><dt>Эндпоинт (сведения, без опроса браузером)</dt><dd>{{profile.endpoint_hint||'Не сообщён'}}</dd><dt>Псевдонимы</dt><dd>{{profile.model_aliases?.join(', ')||'Не сообщены'}}</dd><dt>Наблюдение</dt><dd>{{state?.observed_at?new Date(state.observed_at).toLocaleString('ru-RU'):'Нет данных'}} · поколение {{state?.generation??'неизвестно'}}</dd></dl>
    <div class="model-actions"><div v-for="[a,label] in [['start','Старт'],['stop','Стоп'],['restart','Рестарт']]" :key="a"><button :disabled="!!reason(profile,a as ModelAction)" :aria-describedby="`why-${a}`" @click="ask(profile,a as ModelAction)">{{pending===profile.profile_id?'Отправляем…':label}}</button><small :id="`why-${a}`">{{reason(profile,a as ModelAction)}}</small></div></div>
    <p v-if="!profile.control_allowed||!payload?.control_state?.enabled" class="data-warning">{{explainCode(profile.control_reason||payload?.control_state?.reason||'control_disabled')}}. Владелец локально разрешает конкретный профиль и источник логов. Мониторинг не выдаёт это разрешение автоматически.</p>
    <details class="model-settings"><summary>Параметры запуска (только чтение)</summary><p>«Настроено» и «Подтверждено при работе» — разные источники. Изменения команд из браузера не отправляются.</p><div class="table-wrap"><table><thead><tr><th>Параметр</th><th>Значение</th><th>Источник</th></tr></thead><tbody><tr v-for="setting in profile.settings" :key="setting.name"><td>{{setting.name}}</td><td>{{setting.value===null?'Неизвестно':setting.value}}</td><td>{{setting.provenance==='configured'?'Настроено':setting.provenance==='observed-effective'?'Подтверждено при работе':'Неизвестно'}}</td></tr></tbody></table></div><p v-if="!profile.settings.length">Безопасные параметры не предоставлены.</p></details>
    <p v-if="operation" class="panel" role="status">Операция {{operation.action}} · {{stateLabel(operation.status)}}. <router-link :to="`/operations/${encodeURIComponent(operation.operation_id)}`">Результат и этапы</router-link></p>
    <p v-if="actionError" class="err" role="alert">{{actionError}}</p><p v-if="logReason" class="muted">Логи: {{logReason}}</p><ModelLogs :key="profile.profile_id" :agent-id="String(route.params.id)" :binding="logBinding" :available="!logReason&&!error"/>
   </article>
  </template>
 </section>
 <section v-if="tab==='storage'" class="panel"><h3>Файловые системы и хранилище моделей</h3><p class="muted">Свободное место относится к конкретной файловой системе. Оно не суммируется с ёмкостью физического диска; файлы и кэш здесь не удаляются.</p><div class="table-wrap"><table><thead><tr><th>Раздел / точка подключения</th><th>Всего</th><th>Занято</th><th>Доступно</th></tr></thead><tbody><tr v-for="disk in machine?.host?.disks||[]" :key="disk.mount"><td>{{disk.mount}} · {{disk.fs}}</td><td>{{bytes(disk.total_bytes)}}</td><td>{{bytes(disk.used_bytes)}}</td><td>{{bytes(disk.available_bytes)}}</td></tr></tbody></table></div><p v-if="!machine?.host?.disks?.length">Файловые системы не сообщены.</p><p v-if="!online" class="data-warning">Последний известный снимок, не подтверждение текущего свободного места.</p><p>Имена моделей в профилях — сведения конфигурации; наличие весов и их размещение проверяются локально перед новым запуском.</p><dl class="metric-grid"><SparkObservation v-for="m in metrics.filter(m=>m.metric_id.startsWith('disk.')||m.metric_id.includes('nvme'))" :key="m.metric_id" :metric="m" :now="now" :online="online"/></dl></section>
 <section v-if="tab==='diagnostics'" class="panel"><h3>Диагностика наблюдений</h3><p>ID {{String(route.params.id)}} · классификация {{summary?.classification||'неизвестна'}}</p><p>Агент {{machine?.agent?.worker_version||'версия не сообщена'}} · {{machine?.agent?.os}} / {{machine?.agent?.arch}}</p><p>Последнее наблюдение {{summary?.observed_at?new Date(summary.observed_at).toLocaleString('ru-RU'):'нет'}}. Отчёт получен {{summary?.received_at?new Date(summary.received_at).toLocaleString('ru-RU'):'неизвестно'}}.</p><p>CPU агента нормализован на одно ядро. RSS/PSS и счётчики показаны только когда доступны; отсутствие PSS не заменяется нулём.</p><dl class="metric-grid"><SparkObservation v-for="m in metrics.filter(m=>m.metric_id.startsWith('agent.')||m.observation.quality!=='ok')" :key="m.metric_id" :metric="m" :now="now" :online="online"/></dl><button @click="exportDiagnostics">Скачать безопасную диагностику</button><p class="muted">В экспорте: наблюдения, версии и сводка моделей. Команды, окружение, настройки управления и логи не включаются.</p></section>
 <p class="muted"><small>{{updated?'Последнее чтение '+updated.toLocaleTimeString():''}}</small></p>
 </template>
 <dialog v-if="confirmation" ref="dialog" class="reauth-dialog" aria-labelledby="disruption-title" @cancel.prevent="closeConfirmation"><h2 id="disruption-title">{{confirmation.action==='stop'?'Остановить':'Перезапустить'}} модель?</h2><p>{{confirmation.profile.display_name||confirmation.profile.profile_id}} на {{name}}. Активные запросы неизвестны: действие может прервать инференс.</p><p>Остановка относится только к этому разрешённому профилю. Завершение и допустимое усиление остановки определяет локальная политика супервизора. При рестарте готовность проверяется заново.</p><div class="row"><button class="primary" @click="confirm">Подтвердить {{confirmation.action==='stop'?'Стоп':'Рестарт'}}</button><button @click="closeConfirmation">Отмена</button></div></dialog>
</div></template>
