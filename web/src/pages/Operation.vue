<script setup lang="ts">
import { computed, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { exportDownload } from "../history";
import OperationReadButton from "../components/OperationReadButton.vue";
import { requiresAttention } from "../operations";
import { stateLabel } from "../format";
import RolloutProgress from "../components/RolloutProgress.vue";
import {type Rollout} from "../rollouts";
const rollout=ref<Rollout|null>(null),rolloutError=ref('');
const route=useRoute(),emit=defineEmits<{toast:[string,string?]}>(),op=ref<any>(null),pending=ref(false);
const { loading, error, refresh }=usePolling(async()=>{const id=String(route.params.id);const value=await get(`/api/v1/operations/${encodeURIComponent(id)}`);if(id===route.params.id){op.value=value;rollout.value=null;rolloutError.value='';if((value as any)?.action==='update.rollout'){try{const v=await get<Rollout>(`/api/v1/rollouts?operation_id=${encodeURIComponent(id)}`);if(id===route.params.id)rollout.value=v;}catch(e){if((e as any)?.status!==404)rolloutError.value='Не удалось прочитать план раскатки. Повторите чтение, не создавайте дублирующее обновление.';}}}});
watch(()=>route.params.id,()=>{op.value=null;void refresh();});
const cancellable=computed(()=>op.value?.targets?.some((t:any)=>['queued','waiting_offline'].includes(t.status)));
async function cancel(){pending.value=true;try{const r=await submitOp('operation.cancel_pending',{operation_id:String(route.params.id)});emit('toast','Отменены только ещё не отправленные цели. Уже доставленную команду нельзя отозвать без подтверждения.',`/operations/${r.op?.operation_id}`);await refresh();}catch{ /* submitOp publishes the operation error. */ }finally{pending.value=false;}}
</script>
<template>
 <p v-if="loading" role="status">Загрузка операции…</p><p v-if="error" role="alert" class="panel err">{{ error }} <button @click="refresh">Повторить чтение</button></p>
 <section v-if="op" class="panel"><router-link to="/operations">← Все операции</router-link><h2>{{ op.action }}</h2><h3 :class="{'err':requiresAttention(op)}">{{ stateLabel(op.status) }}</h3>
 <OperationReadButton :operation="op" @changed="refresh"/>
 <p class="muted">Отметка прочтения не меняет выполнение, доказательства результата и состояние машины.</p><p>{{ op.actor }} · {{ new Date(op.created_at).toLocaleString() }} · ревизия {{ op.revision }}</p><p class="muted">{{ op.operation_id }}</p>
 <p v-if="exportDownload(op)"><a :href="exportDownload(op)!" download>Скачать исторические данные JSON</a> · ссылка доступна 24 часа после создания.</p>
 <button v-if="cancellable" :disabled="pending" @click="cancel">{{ pending?'Сохраняем отмену…':'Отменить ещё не отправленные цели' }}</button>
 <div class="table-wrap"><table><thead><tr><th>Цель</th><th>Состояние</th><th>Этап</th><th>Результат</th><th>Подтверждение</th></tr></thead><tbody><tr v-for="t in op.targets || []" :key="t.agent_id"><td>{{ t.agent_id }}</td><td>{{ stateLabel(t.status) }}</td><td>{{ t.stage }}</td><td>{{ t.message }}<small v-if="t.error_code"> · {{ t.error_code }}</small></td><td><details v-if="t.evidence"><summary>Данные результата</summary><pre style="white-space:pre-wrap">{{ JSON.stringify(t.evidence,null,2) }}</pre></details></td></tr></tbody></table></div><p class="muted">Сохранено сервером, получено агентом и подтверждено выполнением: разные этапы. Ожидание и частичный результат не являются полным успехом.</p></section>
 <p v-if="rolloutError" role="alert">{{rolloutError}}</p>
 <RolloutProgress v-if="rollout" :rollout="rollout" @changed="refresh"/>
</template>
