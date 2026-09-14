<script setup lang="ts">
import {computed,ref} from 'vue';
import {submitOp} from '../api';
const props=defineProps<{agent:any;config:any;locked?:boolean}>();
const busy=ref(false),message=ref(''),failed=ref(false);
const config=computed(()=>props.config||{});
const confirmed=computed(()=>props.agent.desired_revision===props.agent.applied_revision&&props.agent.desired_hash===props.agent.applied_hash);
async function save(params:Record<string,unknown>){
 if(busy.value||props.locked)return;
 busy.value=true;message.value='Сохраняем настройку…';failed.value=false;
 try{await submitOp('profile.apply',{...params,base_revision:props.agent.desired_revision},[String(props.agent.id)]);message.value='Настройка сохранена; ждём подтверждения агента.';}
 catch(e){failed.value=true;message.value=(e as Error).message;}
 finally{busy.value=false;window.dispatchEvent(new Event('monik:refresh'));}
}
function toggle(event:Event){const el=event.target as HTMLInputElement;const value=el.checked;el.checked=!!config.value.auto_monitor_new;void save({auto_monitor_new:value});}
function all(paused:boolean){if(window.confirm(paused?'Выключить периодические проверки всех сервисов этой машины? Метрики машины и связь агента продолжатся.':'Включить проверки всех неигнорируемых сервисов этой машины?'))void save({pause_all_services:paused});}
</script>
<template><section class="monitoring-policy">
 <h3>Выборочный мониторинг</h3>
 <p class="muted">«Проверять» отправляет периодические запросы. «В обзоре» выбирает только отображение. Скрытые из обзора проблемы не исчезают из журнала.</p>
 <label class="check-choice"><input type="checkbox" :checked="!!config.auto_monitor_new" :disabled="busy||locked" @change="toggle"/> Автоматически проверять новые найденные сервисы</label>
 <p class="muted">Без галочки новые порты определяются ограниченной проверкой и остаются выключенными. Текущие проверки не меняются. Выключенные сервисы не опрашиваются повторным обнаружением после обновления агента до selective_monitor_v1.</p>
 <div class="row"><button :disabled="busy||locked" @click="all(true)">Выключить все проверки сервисов</button><button :disabled="busy||locked" @click="all(false)">Включить неигнорируемые сервисы</button></div>
 <p v-if="!confirmed" class="data-warning" role="status">Ожидаем применения конфигурации агентом. Уже начатый запрос может завершиться до своего таймаута.</p>
 <p v-if="message" :role="failed?'alert':'status'" :class="{err:failed}">{{message}}</p>
</section></template>
