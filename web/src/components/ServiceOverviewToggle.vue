<script setup lang="ts">
import {ref, watch} from 'vue';
import {submitOp} from '../api';
const props=defineProps<{service:any}>();
const busy=ref(false),notice=ref(''),failed=ref(false);
const checked=ref(!!props.service.pinned);
watch(()=>[props.service.id,props.service.pinned],()=>{if(!busy.value)checked.value=!!props.service.pinned;});
async function change(event:Event){
 const input=event.target as HTMLInputElement, desired=input.checked;
 input.checked=checked.value;
 if(busy.value)return;
 const id=String(props.service.id), expected=checked.value;
 busy.value=true;failed.value=false;notice.value='Сохраняем…';
 try {
  await submitOp('service.pin',{service_id:id,pinned:desired,expected_pinned:expected});
  if(String(props.service.id)===id){checked.value=desired;notice.value='Выбор сохранён';}
  window.dispatchEvent(new Event('monik:refresh'));
 }catch(e){failed.value=true;notice.value=(e as Error).message || 'Не удалось сохранить выбор';window.dispatchEvent(new Event('monik:refresh'));}
 finally {busy.value=false;}
}
</script>
<template><div class="service-overview-toggle">
 <label><input type="checkbox" :checked="checked" :disabled="busy" :aria-label="`Показывать в обзоре: ${service.display_name || service.url || service.id}`" @change="change"/> В обзоре</label>
 <small v-if="notice" :role="failed?'alert':'status'" :class="{err:failed}">{{notice}}</small>
 <small v-if="checked&&service.hidden" class="muted">Сервис скрыт. Снимите скрытие для показа.</small>
</div></template>
