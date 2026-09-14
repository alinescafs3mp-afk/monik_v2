<script setup lang="ts">
import {computed,ref} from 'vue';
import {usePolling} from '../composables/usePolling';
import {get,submitOp} from '../api';
import {groupServices} from '../serviceGroups';
import {stateLabel} from '../format';
const emit=defineEmits<{toast:[string,string?]}>();
const rows=ref<any[]>([]),agents=ref<any[]>([]),query=ref(''),expanded=ref<Record<string,boolean>>({}),busy=ref<Record<string,boolean>>({}),feedback=ref<Record<string,string>>({});
const {loading,error:loadError,refresh}=usePolling(async()=>{const [s,a]=await Promise.all([get<any>('/api/v1/services'),get<any>('/api/v1/agents')]);rows.value=s.services||[];agents.value=a.agents||[];});
const groups=computed(()=>groupServices(rows.value,agents.value,query.value));
function expandAll(open:boolean){for(const g of groups.value)expanded.value[g.id]=open;}
async function pin(s:any){if(busy.value[s.id])return;busy.value[s.id]=true;feedback.value[s.id]='Сохраняем…';try{const r=await submitOp('service.pin',{service_id:s.id,pinned:!s.pinned});feedback.value[s.id]='Закрепление сохранено';emit('toast',feedback.value[s.id],r.op?`/operations/${r.op.operation_id}`:'');await refresh();}catch(e){feedback.value[s.id]=(e as Error).message;}finally{busy.value[s.id]=false;}}
</script>
<template><section>
 <div class="row"><input v-model="query" type="search" placeholder="Машина, сервис или HTTP-код" aria-label="Поиск сервисов"/><button @click="expandAll(true)">Развернуть все</button><button @click="expandAll(false)">Свернуть все</button></div>
 <p v-if="loadError" class="panel err" role="alert">{{loadError}} <button @click="refresh">Повторить чтение</button></p>
 <p v-if="loading" role="status">Загружаем сервисы…</p>
 <p v-else-if="!rows.length&&!loadError" class="panel">Сервисы пока не обнаружены. Проверьте подключение агента и повторите обнаружение.</p>
 <p v-else-if="!groups.length&&!loadError" class="panel">По этому запросу сервисов нет.</p>
 <div class="service-groups"><section v-for="g in groups" :key="g.id" class="service-group">
 <button type="button" class="service-group-header" :aria-expanded="!!expanded[g.id]" :aria-controls="`services-${g.id}`" @click="expanded[g.id]=!expanded[g.id]">
 <span aria-hidden="true">{{expanded[g.id]?'▾':'▸'}}</span><span class="dot" :class="g.state"/><span class="group-machine"><strong>{{g.name}}</strong><small class="muted">{{g.os}} / {{g.arch}}</small></span><span>{{stateLabel(g.state)}} · Сервисов {{g.services.length}}</span></button>
 <div v-if="expanded[g.id]" :id="`services-${g.id}`" class="service-group-body"><router-link :to="`/machines/${encodeURIComponent(g.id)}#services`">Открыть машину</router-link><div class="table-wrap"><table><thead><tr><th>Сервис</th><th>URL</th><th>Ответ</th><th>Действия</th></tr></thead><tbody>
 <tr v-for="s in g.services" :key="s.id"><td>{{s.display_name||s.url}}</td><td>{{s.url}}</td><td><span class="dot" :class="s.state"/> {{s.summary}}</td><td><router-link :to="{path:`/machines/${encodeURIComponent(g.id)}`,query:{service:s.id},hash:'#check-editor'}">Настроить запрос</router-link> <button :disabled="busy[s.id]" @click="pin(s)">{{s.pinned?'Открепить':'Закрепить'}}</button><small v-if="feedback[s.id]" role="status">{{feedback[s.id]}}</small></td></tr>
 </tbody></table></div></div></section></div>
</section></template>
