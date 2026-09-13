<script setup lang="ts">
import { ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { stateLabel } from "../format";
const emit=defineEmits<{toast:[string,string?]}>(),rows=ref<any[]>([]),selected=ref<string[]>([]),pending=ref(false);
const { loading,error,refresh }=usePolling(async()=>{rows.value=(await get<any>('/api/v1/agents')).agents || [];});
async function collect(){const targets=[...selected.value];pending.value=true;try{const r=await submitOp('agent.collect_now',{},targets);emit('toast',`Операция создана для ${targets.length} выбранных машин. Ожидаем результаты.`,`/operations/${r.op?.operation_id}`);}finally{pending.value=false;}}
</script>
<template><div><div class="bar"><span>Выбрано {{ selected.length }} машин</span><button :disabled="!selected.length||pending" @click="collect">{{ pending?'Отправляем…':'Собрать данные выбранных' }}</button><router-link to="/add">Добавить машину</router-link></div><p class="muted">Выбор фиксируется при создании операции. Перезапуск, обновления и ребинд пока заблокированы до безопасной реализации.</p><p v-if="loading">Загрузка…</p><p v-if="error" class="panel err" role="alert">{{ error }} <button @click="refresh">Повторить</button></p><div class="table-wrap"><table><thead><tr><th>Выбор</th><th>Машина</th><th>ОС</th><th>Версия</th><th>Связь</th><th>Конфигурация</th></tr></thead><tbody><tr v-for="r in rows" :key="r.id"><td><input type="checkbox" :value="r.id" v-model="selected" :disabled="r.revoked||r.archived||pending" :aria-label="`Выбрать ${r.display_name}`"/></td><td><router-link :to="'/machines/'+encodeURIComponent(r.id)">{{ r.display_name }}</router-link></td><td>{{ r.os }}/{{ r.arch }}</td><td>{{ r.worker_version }}</td><td>{{ stateLabel(r.state) }}</td><td>{{ r.desired_revision }}/{{ r.applied_revision }} · {{ r.desired_hash===r.applied_hash?'применена':'ожидает применения' }}</td></tr></tbody></table></div></div></template>
