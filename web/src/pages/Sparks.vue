<script setup lang="ts">
import {computed,onUnmounted,ref} from 'vue';
import {get,serverNow} from '../api';
import {usePolling} from '../composables/usePolling';
import {useSession} from '../session';
import {bytes} from '../format';
import {formatObservation,modelLabel,freshAt,type SparkSummary} from '../sparks';
const {owner}=useSession();
const rows=ref<SparkSummary[]>([]),query=ref(''),status=ref('all'),now=ref(serverNow());
const clock=window.setInterval(()=>now.value=serverNow(),1000);onUnmounted(()=>window.clearInterval(clock));
const {loading,error,refreshing,refresh,updated}=usePolling(async()=>{const d=await get<{sparks:SparkSummary[]}>('/api/v1/sparks');rows.value=d.sparks||[];now.value=serverNow();});
function online(r:SparkSummary){return r.online&&freshAt(r.received_at,now.value);}
const filtered=computed(()=>rows.value.filter(r=>(status.value==='all'||(status.value==='online')===online(r))&&[r.display_name,r.hostname,r.agent_id].join(' ').toLocaleLowerCase().includes(query.value.toLocaleLowerCase())));
function observation(r:SparkSummary,ids:string[]){const m=r.metrics.find(m=>ids.includes(m.metric_id));return m?formatObservation(m.observation,now.value,online(r)):'Нет данных';}
function state(p:SparkSummary['profiles'][number],r:SparkSummary){return modelLabel({...p,supervisor_state:p.state},online(r),now.value);}
function ready(r:SparkSummary){return r.profiles.filter(p=>online(r)&&p.state==='running'&&p.readiness==='ready'&&p.quality==='ok'&&freshAt(p.observed_at,now.value)).length;}
function warnings(r:SparkSummary){return r.metrics.filter(m=>m.observation.quality!=='ok'||!freshAt(m.observation.observed_at,now.value)).length;}
</script>
<template><div>
 <header class="bar"><div><h2>Спарки</h2><p class="muted">Та же машина и история, что в общем списке.</p></div><router-link v-if="owner" class="primary" to="/add?flavor=spark-light">Добавить Spark</router-link></header>
 <div class="row"><input v-model="query" type="search" aria-label="Поиск Spark" placeholder="Имя или ID машины"/><label>Связь <select v-model="status"><option value="all">Все</option><option value="online">На связи</option><option value="offline">Нет свежей связи</option></select></label><button :disabled="refreshing" @click="refresh">Обновить</button></div>
 <p v-if="error" class="panel err" role="alert">{{error}}. Показаны последние полученные данные. <button @click="refresh">Повторить чтение</button></p>
 <p v-if="loading" role="status">Загружаем Спарки…</p>
 <p v-else-if="!rows.length&&!error" class="panel">Спарков пока нет. Подключите агент Spark light для Linux ARM64; установка мониторинга не запускает модели.</p>
 <p v-else-if="!filtered.length&&!error" class="panel">Нет машин по выбранным условиям.</p>
 <div class="cards spark-cards"><article v-for="r in filtered" :key="r.agent_id" class="card spark-card" :class="{'measurements-stale':!online(r)}">
  <header><router-link :to="`/sparks/${encodeURIComponent(r.agent_id)}`"><h3>{{r.display_name||r.hostname||r.agent_id}}</h3></router-link><p><span class="dot" :class="online(r)?'ok':'stale'"/> {{online(r)?'На связи':'Нет свежей связи'}} · {{new Date(r.received_at).toLocaleString()}}</p></header>
  <dl class="metric-grid"><div><dt>GPU</dt><dd>{{observation(r,['gpu.0.utilization_percent'])}}</dd></div><div><dt>Доступно общей памяти</dt><dd>{{observation(r,['memory.available_bytes','host.memory.available_bytes'])}}</dd><small>{{r.memory_topology==='unified'?'UMA · одна физическая память':'Топология: '+r.memory_topology}}</small></div><div><dt>Температура GPU</dt><dd>{{observation(r,['gpu.0.temperature_celsius'])}}</dd></div><div><dt>Готовы к запросам</dt><dd>{{ready(r)}} / {{r.profiles.length}}</dd><small>Готовность проверяется отдельно от процесса</small></div></dl>
  <p v-for="p in r.profiles.slice(0,3)" :key="p.profile_id">{{p.display_name||p.profile_id}} · {{state(p,r)}}</p>
  <p v-for="disk in r.host?.disks||[]" :key="disk.mount">Хранилище {{disk.mount}}: доступно {{bytes(disk.available_bytes)}}<small v-if="!online(r)||!freshAt(r.host_observed_at,now)" class="data-warning"> · последнее наблюдение</small></p><p v-if="!r.host?.disks?.length" class="muted">Свободное место хранилища не сообщено.</p>
  <p v-if="warnings(r)" class="data-warning">Неактуальных или недоступных показателей: {{warnings(r)}}. Источники и причины — в обзоре.</p>
  <footer class="row"><router-link :to="`/sparks/${encodeURIComponent(r.agent_id)}`">Открыть Spark</router-link><router-link :to="`/machines/${encodeURIComponent(r.agent_id)}`">Общая машина</router-link></footer>
 </article></div><p class="muted"><small>{{updated?'Последнее чтение '+updated.toLocaleTimeString():''}}</small></p>
</div></template>
