<script setup lang="ts">
import {computed,ref} from 'vue';
import {submitOp} from '../api';
import {canResume,controlResult,rolloutCounts,rolloutLabel,type Rollout} from '../rollouts';
import {stateLabel} from '../format';
const props=defineProps<{rollout:Rollout}>();
const emit=defineEmits<{changed:[]}>();
const busy=ref(''),message=ref(''),error=ref(''),expanded=ref(false);
const counts=computed(()=>rolloutCounts(props.rollout));
async function control(pause:boolean){
 busy.value=pause?'pause':'resume';error.value='';message.value='';
 try{
  const r=await submitOp(pause?'update.pause':'update.resume',{operation_id:props.rollout.operation_id,rollout_revision:props.rollout.revision});
  if(controlResult(r.op))message.value=pause?'Пауза сохранена. Уже выданные задания могут завершиться.':'Продолжается исходная раскатка. Завершённые задания не повторяются.';
  else error.value=r.op?.targets?.find((t:any)=>t.status!=='succeeded')?.message||'Изменение не подтверждено. Проверьте исходную операцию.';
  emit('changed');
 }catch(e){error.value=(e as any)?.message||'Результат запроса неизвестен. Обновите состояние, не запускайте раскатку повторно.';emit('changed');}
 finally{busy.value='';}
}
</script>
<template>
 <article class="rollout-progress panel" :data-rollout="rollout.operation_id">
  <div class="rollout-heading"><strong>{{rolloutLabel(rollout.state)}}</strong><router-link :to="`/operations/${rollout.operation_id}`">Открыть операцию</router-link></div>
  <small class="muted">{{rollout.operation_id}} · ревизия {{rollout.revision}}</small>
  <p>Подтверждено {{counts.confirmed}}/{{counts.total}} · ждут своей очереди {{counts.held}} · проблем {{counts.failed}} · отменено {{counts.cancelled}}</p>
  <p v-if="rollout.state==='running'">{{rollout.wave < rollout.canary_waves?'Пробная машина':'Партия'}} {{rollout.wave+1}}. Наблюдение свежей связи после подтверждения: {{rollout.observe_seconds}} с.</p>
  <p v-if="rollout.stable_since">Начало текущего наблюдения: {{new Date(rollout.stable_since).toLocaleString()}}. Пропуск наблюдений начинает отсчёт заново.</p>
  <p v-if="rollout.reason">{{rollout.reason}}</p>
  <p v-if="rollout.state==='blocked'" class="muted">Новые задания не выдаются. Прочтение ошибки не продолжит раскатку. Отмените неотправленные цели в операции и проверьте результат перед новой попыткой.</p>
  <div class="rollout-controls">
   <button v-if="rollout.state==='running'" :disabled="!!busy" @click="control(true)">{{busy==='pause'?'Сохраняем паузу…':'Пауза раскатки'}}</button>
   <button v-if="canResume(rollout)" :disabled="!!busy" @click="control(false)">{{busy==='resume'?'Подтверждаем…':'Продолжить раскатку'}}</button>
   <button :aria-expanded="expanded" @click="expanded=!expanded">{{expanded?'Скрыть цели':'Показать цели'}}</button>
  </div>
  <p v-if="message" role="status">{{message}}</p><p v-if="error" role="alert">{{error}}</p>
  <div v-if="expanded" class="table-wrap"><table><thead><tr><th>Машина</th><th>Платформа</th><th>Партия</th><th>Результат</th></tr></thead><tbody><tr v-for="m in rollout.members" :key="m.agent_id"><td><router-link :to="`/machines/${m.agent_id}`">{{m.agent_id}}</router-link></td><td>{{m.platform}}</td><td>{{m.wave+1}}</td><td>{{!m.released_at&&m.status==='queued'?'Ждёт очереди':stateLabel(m.status)}}<small>{{m.message}}</small></td></tr></tbody></table></div>
 </article>
</template>
<style scoped>
.rollout-progress{min-width:0;overflow-wrap:anywhere}.rollout-heading,.rollout-controls{display:flex;gap:.65rem;align-items:center;flex-wrap:wrap}.rollout-progress small{display:block}.rollout-progress td{vertical-align:top}.rollout-progress button{max-width:100%}
</style>
