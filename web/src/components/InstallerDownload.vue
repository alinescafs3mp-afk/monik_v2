<script setup lang="ts">
import {computed,ref,watch} from 'vue';
import {get,downloadInstaller} from '../api';
import {useSession} from '../session';
const {owner}=useSession();
const props=defineProps<{controllerUrl:string;initialFlavor?:string}>();
const emit=defineEmits<{busy:[boolean]}>();
const rows=ref<Array<{platform:string;agent_flavor?:'general'|'spark-light';ready:boolean;reason?:string;build?:string}>>([]);
const flavor=ref<'general'|'spark-light'>(props.initialFlavor==='spark-light'?'spark-light':'general');
const availableRows=computed(()=>rows.value.filter(r=>(r.agent_flavor||'general')===flavor.value));
const enableConsole=ref(false);
watch(flavor,v=>{success.value='';if(v==='spark-light')enableConsole.value=false;});
const loading=ref(false),pending=ref(''),error=ref(''),success=ref('');
async function reload(){loading.value=true;error.value='';try{const r=await get<{installers:typeof rows.value}>('/api/v1/installers');if(!Array.isArray(r.installers)||r.installers.some(x=>!['linux-amd64','linux-arm64'].includes(x.platform)||typeof x.ready!=='boolean'||x.agent_flavor&&!['general','spark-light'].includes(x.agent_flavor)))throw new Error('Сервер вернул некорректный список установщиков.');rows.value=r.installers;}catch(e){error.value=(e as Error).message;}finally{loading.value=false;}}
watch(owner,value=>{if(value)void reload();else rows.value=[];},{immediate:true});
async function download(platform:string){
 if(pending.value||!owner.value)return;const chosen=props.controllerUrl,selectedFlavor=flavor.value;pending.value=platform;error.value='';success.value='';emit('busy',true);
 try{
  const result=await downloadInstaller(platform,chosen,enableConsole.value,selectedFlavor);
  const url=URL.createObjectURL(result.blob),link=document.createElement('a');link.href=url;link.download='monik-agent';document.body.appendChild(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),60000);
  success.value=`Файл ${selectedFlavor==='spark-light'?'Spark light':'обычного агента'} подготовлен для ${chosen}. Регистрация одной новой машины до ${new Date(result.expires).toLocaleString()}. Это не подтверждение установки: запустите файл на нужном сервере.`;
 }catch(e){error.value=(e as Error).message||'Не удалось скачать установщик.';}finally{pending.value='';emit('busy',false);}
}
</script>
<template>
 <section class="panel installer-panel" aria-labelledby="installer-title">
  <h2 id="installer-title">Один файл. Один запуск.</h2>
  <label>Вариант агента <select v-model="flavor" :disabled="!!pending"><option value="general">Обычный агент</option><option value="spark-light">Spark light · Linux ARM64</option></select></label>
  <p>Скачайте подготовленный агент, перенесите на Linux-сервер с systemd и запустите:</p>
  <pre>chmod +x monik-agent &amp;&amp; sudo ./monik-agent</pre>
  <p v-if="flavor==='general'">Внутри уже есть агент, супервизор, адрес и доверие контроллера. Служба включается автоматически; «ГОТОВО» появляется только после проверки запуска и свежих отчётов. После этого консоль можно закрыть.</p>
  <p v-else>Отдельный лёгкий агент Spark для Linux ARM64. Подключает мониторинг и прежнее доверие контроллера. Модели, скрипты запуска и их автозапуск не меняются; модельное управление требует отдельного локального разрешения.</p>
  <p class="data-warning">Каждый файл разрешает регистрацию только одной новой машины, в течение часа. Для следующей скачайте новый. Не публикуйте установщик и не пересылайте посторонним: до использования он содержит одноразовое разрешение на подключение.</p>
  <label v-if="flavor==='general'" class="console-opt-in"><input v-model="enableConsole" type="checkbox" :disabled="!!pending||!owner"/> Разрешить консоль через агент. Отдельный пользователь monik-console, без root/sudo; без входящих сетевых портов.</label>
  <p v-if="loading" role="status">Проверяем наличие собранных установщиков…</p>
  <div v-for="r in availableRows" :key="`${r.agent_flavor||'general'}:${r.platform}`" class="installer-option">
   <button class="primary" :disabled="!owner||!r.ready||!!pending||loading||!controllerUrl" @click="download(r.platform)">{{pending===r.platform?'Подготавливаем и скачиваем…':`Скачать ${flavor==='spark-light'?'Spark light':'агент'}: ${r.platform}`}}</button>
   <small v-if="!r.ready" class="muted">{{r.reason}}</small><small v-else class="muted">Сборка {{r.build}}</small>
  </div>
  <p v-if="!loading&&!availableRows.length" class="data-warning">Установщик выбранного варианта не опубликован на этом контроллере. Перепроверьте доступность после установки соответствующего шаблона.</p>
  <p v-if="!owner" class="muted">Скачивание установщика доступно владельцу.</p>
  <p v-if="error" class="err" role="alert">{{error}}</p><p v-if="success" role="status">{{success}}</p>
  <button :disabled="loading||!!pending" @click="reload">Перепроверить доступность</button>
  <p class="muted">Разрешение выдано при скачивании владельцем, отдельное открытие приёма и одобрение не нужны. В обзоре машина по-прежнему показывается по вашей галочке. Windows и системы без systemd этим установщиком пока не поддерживаются.</p>
 </section>
</template>
<style scoped>
.installer-option{display:flex;align-items:center;flex-wrap:wrap;gap:.6rem;margin:.6rem 0}.installer-option small{overflow-wrap:anywhere;min-width:0;flex:1 1 14rem}.installer-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;max-width:100%}.installer-panel button{max-width:100%;white-space:normal}
</style>
