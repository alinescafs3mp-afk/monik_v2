<script setup lang="ts">
import {ref} from 'vue';
import {get,submitOp} from '../api';
import {usePolling} from '../composables/usePolling';
const rows=ref<any[]>([]),limited=ref(false),busy=ref(false),message=ref('');
const {error,refresh}=usePolling(async()=>{const r=await get<any>('/api/v1/sessions');rows.value=r.sessions;limited.value=r.truncated;});
async function revoke(){if(busy.value||!confirm('Завершить все другие входы, включая сохранённые на телевизоре? Текущий вход и агенты останутся подключены.'))return;busy.value=true;message.value='';try{await submitOp('session.revoke_others');message.value='Остальные браузерные сессии завершены.';await refresh();}catch(e){message.value=(e as Error).message;}finally{busy.value=false;}}
function date(s:string){return new Date(s).toLocaleString();}
</script>
<template><section class="panel"><h2>Сохранённые входы</h2><p>Сеанс «Запомнить вход» действует до 30 дней. Выход и смена пароля отзывают его на сервере. Не сохраняйте вход владельца на общедоступном экране.</p><p v-if="error" role="alert" class="err">{{error}}</p><ul class="session-list"><li v-for="(s,index) in rows" :key="index">{{s.current?'Этот браузер':'Другой вход'}} · создан {{date(s.created_at)}} · истекает {{date(s.expires_at)}}</li></ul><p v-if="limited">Показаны первые 100 сеансов.</p><button :disabled="busy" @click="revoke">{{busy?'Завершаем…':'Выйти на остальных устройствах'}}</button><p v-if="message" role="status">{{message}}</p></section></template>
