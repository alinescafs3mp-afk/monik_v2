<script setup lang="ts">
import {ref} from 'vue';
import {get,post,submitOp} from '../api';
import {usePolling} from '../composables/usePolling';
const emit=defineEmits<{changed:[]}>();
const candidates=ref<any[]>([]),busy=ref(''),feedback=ref(''),auth=ref(false),password=ref('');
const {loading,error,refresh}=usePolling(async()=>{const data=await get<any>('/api/v1/enrollment/pending');candidates.value=data.candidates||[];});
async function reauth(){if(busy.value)return;busy.value='auth';error.value='';try{await post('/api/v1/reauth',{password:password.value});auth.value=false;feedback.value='Личность подтверждена. Повторите нужное решение.';}catch(e){error.value=(e as Error).message;}finally{password.value='';busy.value='';}}
async function decide(c:any,approve:boolean){
 if(busy.value)return;
 if(!confirm(`${approve?'Разрешить мониторинг':'Отклонить подключение'} машины «${c.display_name||c.hostname}»? Сверьте отпечаток регистрации с выводом установки: ${c.fingerprint}`))return;
 busy.value=c.id;feedback.value=approve?'Сохраняем разрешение…':'Сохраняем отказ…';
 try{const result=await submitOp(approve?'enrollment.approve':'enrollment.reject',{agent_id:c.id,fingerprint:c.fingerprint});if(result.op?.status!=='completed')throw new Error('Решение ещё не подтверждено. Проверьте исходную операцию в Центре операций.');feedback.value=approve?'Подключение разрешено. Ждём первый отчёт агента. Обзор включается отдельно галочкой.':'Подключение отклонено.';await refresh();emit('changed');window.dispatchEvent(new Event('monik:refresh'));}
 catch(e){const err=e as any;feedback.value=err.message;if(err.error==='recent_auth_required')auth.value=true;}
 finally{busy.value='';}
}
</script>
<template><section v-if="candidates.length||error||feedback" class="panel pending-agents">
 <p><router-link to="/add">Управление окном приёма новых агентов</router-link></p>
 <h2>Ожидают подтверждения: {{candidates.length}}</h2><p class="muted">Машины обнаружены по исходящему сигналу агента. Имена и ОС пока не проверены. Сверьте отпечаток с установкой, прежде чем разрешить доступ. Метрики и команды недоступны до одобрения.</p>
 <p v-if="error" class="err" role="alert">{{error}} <button @click="refresh">Повторить</button></p><p v-if="feedback" role="status">{{feedback}}</p>
 <form v-if="auth" @submit.prevent="reauth"><label>Подтвердите пароль владельца <input v-model="password" type="password" autocomplete="current-password" required/></label><button :disabled="!!busy">Подтвердить</button></form>
 <article v-for="c in candidates" :key="c.id" class="pending-agent"><strong>{{c.display_name||c.hostname}}</strong><p>{{c.os}} / {{c.arch}} · {{c.hostname}}</p><code>{{c.fingerprint}}</code><p>Последний сигнал: {{new Date(c.last_seen_at).toLocaleString()}}</p><div class="row"><button :disabled="!!busy" @click="decide(c,true)">Разрешить подключение</button><button :disabled="!!busy" @click="decide(c,false)">Отклонить</button></div></article>
 </section></template>
