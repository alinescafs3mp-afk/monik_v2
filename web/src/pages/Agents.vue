<script setup lang="ts">
import { ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { stateLabel } from "../format";
const emit=defineEmits<{toast:[string,string?]}>(),rows=ref<any[]>([]),selected=ref<string[]>([]),pending=ref(false),candidate=ref(""),planId=ref(""),trustPem=ref(""),fingerprint=ref("");
const { loading,error,refresh }=usePolling(async()=>{rows.value=(await get<any>('/api/v1/agents')).agents || [];});
const migrationLabel:Record<string,string>={queued:'Ожидает доставки',prepared:'Подготовлен',armed:'Резервный переход разрешён',activating:'Проверяем новый адрес',confirmed:'Переход подтверждён',expired:'План истёк',retired:'Старый адрес выведен'};
async function run(action:string, params:Record<string,unknown>={}){const targets=[...selected.value];pending.value=true;try{const r=await submitOp(action,params,targets);emit('toast',`Операция ${action} создана для ${targets.length} машин.`,`/operations/${r.op?.operation_id}`);}finally{pending.value=false;}}
</script>
<template><div><div class="bar"><span>Выбрано {{ selected.length }} машин</span><button :disabled="!selected.length||pending" @click="run('agent.collect_now')">{{ pending?'Отправляем…':'Собрать данные выбранных' }}</button><button :disabled="!selected.length||pending" @click="run('agent.restart')">Перезапустить выбранных</button><router-link to="/add">Добавить машину</router-link></div>
<p class="muted">Выбор фиксируется при создании операции. Перезапуск и обновление требуют управляемой установки. Ребинд сначала только готовит план и не меняет адрес контроллера.</p>
<div class="row"><button :disabled="!selected.length||pending" @click="run('credential.rotate')">Сменить API-ключи выбранных</button></div>
<div class="row"><input v-model="candidate" placeholder="https://новый-контроллер:8777" /><button :disabled="!selected.length||pending||!candidate" @click="run('rebind.prepare',{candidate_url:candidate})">Подготовить ребинд</button></div>
<div class="row"><input v-model="planId" placeholder="plan_id" /><button :disabled="!selected.length||pending||!planId" @click="run('rebind.arm',{plan_id:planId})">Разрешить переход при потере связи</button><button :disabled="!selected.length||pending||!planId" @click="run('rebind.activate',{plan_id:planId})">Переключить</button><button :disabled="!selected.length||pending||!planId" @click="run('rebind.retire',{plan_id:planId})">Вывести старый адрес</button></div>
<div class="row"><textarea v-model="trustPem" rows="4" placeholder="-----BEGIN CERTIFICATE-----"></textarea></div>
<div class="row"><button :disabled="!selected.length||pending||!trustPem" @click="run('trust.stage',{trust_pem:trustPem})">Добавить доверие CA</button><input v-model="fingerprint" placeholder="sha256 отпечаток"/><button :disabled="!selected.length||pending||!fingerprint" @click="run('trust.retire',{fingerprint})">Снять доверие</button></div>
<p v-if="loading">Загрузка…</p><p v-if="error" class="panel err" role="alert">{{ error }} <button @click="refresh">Повторить</button></p>
<div class="table-wrap"><table><thead><tr><th>Выбор</th><th>Машина</th><th>ОС</th><th>Версия</th><th>Связь</th><th>Конфигурация</th><th>Установка</th><th>Ребинд</th></tr></thead>
<tbody><tr v-for="r in rows" :key="r.id"><td><input type="checkbox" :value="r.id" v-model="selected" :disabled="r.revoked||r.archived||pending" :aria-label="`Выбрать ${r.display_name}`"/></td>
<td><router-link :to="'/machines/'+encodeURIComponent(r.id)">{{ r.display_name }}</router-link></td><td>{{ r.os }}/{{ r.arch }}</td><td>{{ r.worker_version }}</td><td>{{ stateLabel(r.state) }}</td>
<td>{{ r.desired_revision }}/{{ r.applied_revision }} · {{ r.desired_hash===r.applied_hash?'применена':'ожидает применения' }}</td>
<td>{{ r.managed_ready?'управляемая':'неуправляемая' }}</td><td><span v-if="r.migration" :title="r.migration.reason">{{ migrationLabel[r.migration.state] || r.migration.state }}<small class="muted"> · {{ r.migration.plan_id }}</small></span><span v-else>Нет плана</span></td></tr></tbody></table></div></div></template>
