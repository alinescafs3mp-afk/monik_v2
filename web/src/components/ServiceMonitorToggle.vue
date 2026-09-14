<script setup lang="ts">
import {ref,watch} from 'vue';
import {get,submitOp} from '../api';
import {monitoringCheck,monitoringLabel} from '../serviceSelection';
const props=defineProps<{service:any;locked?:boolean}>();
const busy=ref(false),notice=ref(''),failed=ref(false),checked=ref(!!props.service.monitoring_enabled);
watch(()=>[props.service.id,props.service.monitoring_enabled,props.service.monitoring_applied],()=>{if(!busy.value)checked.value=!!props.service.monitoring_enabled;});
async function change(event:Event){
 const input=event.target as HTMLInputElement,enabled=input.checked;input.checked=checked.value;
 if(busy.value || props.locked)return;
 busy.value=true;failed.value=false;notice.value='Читаем актуальную конфигурацию…';
 try{
  const id=String(props.service.id),agent=String(props.service.agent_id);
  const result=await get<any>(`/api/v1/agents/${encodeURIComponent(agent)}`);
  const cfg=result.desired_config;
  const current=(cfg?.checks||[]).find((c:any)=>c.service_id===id);
  // Do not overwrite a concurrent toggle from another tab while loading.
  if(current && (!current.paused&&!current.ignored)!==checked.value)throw new Error('Настройка изменилась. Обновите сведения перед повтором.');
  const check=monitoringCheck(current,enabled);
  notice.value='Сохраняем…';
  await submitOp('check.apply',{check,base_revision:result.agent.desired_revision},[agent]);
  checked.value=enabled;notice.value='Настройка сохранена. Ожидаем подтверждение агента.';
 }catch(e){failed.value=true;notice.value=(e as Error).message||'Не удалось изменить мониторинг';}
 finally{busy.value=false;window.dispatchEvent(new Event('monik:refresh'));}
}
</script>
<template><div class="service-monitor-toggle">
 <label><input type="checkbox" :checked="checked" :disabled="busy||locked||!service.has_check" :aria-label="`Проверять сервис: ${service.display_name||service.url||service.id}`" @change="change"/> Проверять</label>
 <small :class="{'data-warning':!service.monitoring_applied}" role="status">{{monitoringLabel(service)}}</small>
 <small v-if="notice" :role="failed?'alert':'status'" :class="{err:failed}">{{notice}}</small>
</div></template>
