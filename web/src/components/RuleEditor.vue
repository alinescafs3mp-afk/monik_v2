<script setup lang="ts">
import {computed,ref} from 'vue';
import {get,submitOp} from '../api';
import {usePolling} from '../composables/usePolling';
import {thresholdError,type Threshold} from '../monitoring';
const draft=ref<Threshold[]>([]),base=ref(0),latest=ref(0),original=ref(''),busy=ref(false),notice=ref('');
const dirty=computed(()=>JSON.stringify(draft.value)!==original.value),validation=computed(()=>thresholdError(draft.value));
const labels:Record<string,string>={cpu:'CPU',ram:'RAM (занято)',disk:'DISK (самый заполненный)'};
const {loading,error,refresh}=usePolling(async()=>{
 const data=await get<any>('/api/v1/monitoring'),rules=data.host_rules;
 latest.value=rules.revision;
 if(!dirty.value || !draft.value.length){draft.value=rules.rules;base.value=rules.revision;original.value=JSON.stringify(draft.value);}
});
async function reload(){if(dirty.value&&!confirm('Отбросить несохранённые пороги и прочитать текущие?'))return;draft.value=[];original.value='[]';await refresh();}
async function save(){
 if(busy.value||validation.value||!dirty.value)return;
 busy.value=true;notice.value='Сохраняем правила…';
 try{await submitOp('rule.save',{base_revision:base.value,rules:JSON.parse(JSON.stringify(draft.value))});original.value=JSON.stringify(draft.value);await refresh();notice.value='Правила сохранены. Применяются к следующим измерениям, старая история не пересчитывается.';window.dispatchEvent(new Event('monik:refresh'));}
 catch(e){notice.value=(e as Error).message;}finally{busy.value=false;}
}
</script>
<template><section class="panel rule-editor"><h2>Пороги CPU, RAM и DISK</h2>
 <p class="muted">Общие правила для парка. RAM: занятые байты / всего; DISK: процент самого заполненного локального раздела. Изменение политики не считается восстановлением машины.</p>
 <p v-if="loading" role="status">Читаем правила…</p><p v-if="error" class="err" role="alert">{{error}} <button @click="refresh">Повторить</button></p>
 <form v-if="draft.length" @submit.prevent="save"><fieldset :disabled="busy"><legend>Ревизия {{base}}</legend>
  <div class="table-wrap"><table><thead><tr><th>Метрика</th><th>Предупреждение, %</th><th>Критический, %</th><th>Восстановление ≤, %</th><th>Превышение, с</th><th>Восстановление, с</th></tr></thead><tbody><tr v-for="r in draft" :key="r.metric"><td>{{labels[r.metric]}}</td>
   <td><input v-model.number="r.warning" type="number" min="1" max="99" step="0.1" :aria-label="`${r.metric}: предупреждение`"/></td>
   <td><input v-model.number="r.critical" type="number" min="1" max="100" step="0.1" :aria-label="`${r.metric}: критический`"/></td>
   <td><input v-model.number="r.recovery" type="number" min="0" max="99" step="0.1" :aria-label="`${r.metric}: восстановление`"/></td>
   <td><input v-model.number="r.persist_seconds" type="number" min="5" max="3600" :aria-label="`${r.metric}: выдержка превышения`"/></td>
   <td><input v-model.number="r.recover_seconds" type="number" min="5" max="3600" :aria-label="`${r.metric}: выдержка восстановления`"/></td>
  </tr></tbody></table></div>
  <p v-if="latest!==base" role="alert" class="data-warning">Правила изменены в другой вкладке. Ваш черновик сохранён на экране; перечитайте актуальную ревизию перед заменой.</p>
  <p v-if="validation" class="err">{{validation}}</p><div class="row"><button type="submit" :disabled="!!validation||!dirty||latest!==base">{{busy?'Сохраняем…':'Сохранить пороги'}}</button><button type="button" @click="reload">Перечитать правила</button><small v-if="dirty">Есть несохранённые изменения</small></div>
 </fieldset></form><p v-if="notice" role="status">{{notice}}</p>
</section></template>
