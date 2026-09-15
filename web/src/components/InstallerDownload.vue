<script setup lang="ts">
import {onMounted,ref} from 'vue';
import {get,downloadInstaller} from '../api';
const props=defineProps<{controllerUrl:string}>();
const emit=defineEmits<{busy:[boolean]}>();
const rows=ref<Array<{platform:string;ready:boolean;reason?:string;build?:string}>>([]);
const loading=ref(false),pending=ref(''),error=ref(''),success=ref('');
async function reload(){loading.value=true;error.value='';try{const r=await get<{installers:typeof rows.value}>('/api/v1/installers');if(!Array.isArray(r.installers)||r.installers.some(x=>!['linux-amd64','linux-arm64'].includes(x.platform)||typeof x.ready!=='boolean'))throw new Error('Сервер вернул некорректный список установщиков.');rows.value=r.installers;}catch(e){error.value=(e as Error).message;}finally{loading.value=false;}}
onMounted(reload);
async function download(platform:string){
 if(pending.value)return;const chosen=props.controllerUrl;pending.value=platform;error.value='';success.value='';emit('busy',true);
 try{
  const result=await downloadInstaller(platform,chosen);
  const url=URL.createObjectURL(result.blob),link=document.createElement('a');link.href=url;link.download='monik-agent';document.body.appendChild(link);link.click();link.remove();setTimeout(()=>URL.revokeObjectURL(url),60000);
  success.value=`Файл подготовлен для ${chosen}. Регистрация одной новой машины до ${new Date(result.expires).toLocaleString()}. Это не подтверждение установки: запустите файл на нужном сервере.`;
 }catch(e){error.value=(e as Error).message||'Не удалось скачать установщик.';}finally{pending.value='';emit('busy',false);}
}
</script>
<template>
 <section class="panel installer-panel" aria-labelledby="installer-title">
  <h2 id="installer-title">Один файл. Один запуск.</h2>
  <p>Скачайте подготовленный агент, перенесите на Linux-сервер с systemd и запустите:</p>
  <pre>chmod +x monik-agent &amp;&amp; sudo ./monik-agent</pre>
  <p>Внутри уже есть агент, супервизор, адрес и доверие контроллера. Служба включается автоматически; «ГОТОВО» появляется только после проверки запуска и свежих отчётов. После этого консоль можно закрыть.</p>
  <p class="data-warning">Каждый файл разрешает регистрацию только одной новой машины, в течение часа. Для следующей скачайте новый. Не публикуйте установщик и не пересылайте посторонним: до использования он содержит одноразовое разрешение на подключение.</p>
  <p v-if="loading" role="status">Проверяем наличие собранных установщиков…</p>
  <div v-for="r in rows" :key="r.platform" class="installer-option">
   <button class="primary" :disabled="!r.ready||!!pending||loading||!controllerUrl" @click="download(r.platform)">{{pending===r.platform?'Подготавливаем и скачиваем…':`Скачать агент: ${r.platform}`}}</button>
   <small v-if="!r.ready" class="muted">{{r.reason}}</small><small v-else class="muted">Сборка {{r.build}}</small>
  </div>
  <p v-if="error" class="err" role="alert">{{error}}</p><p v-if="success" role="status">{{success}}</p>
  <button :disabled="loading||!!pending" @click="reload">Перепроверить доступность</button>
  <p class="muted">Разрешение выдано при скачивании владельцем, отдельное открытие приёма и одобрение не нужны. В обзоре машина по-прежнему показывается по вашей галочке. Windows и системы без systemd этим установщиком пока не поддерживаются.</p>
 </section>
</template>
<style scoped>
.installer-option{display:flex;align-items:center;flex-wrap:wrap;gap:.6rem;margin:.6rem 0}.installer-option small{overflow-wrap:anywhere;min-width:0;flex:1 1 14rem}.installer-panel pre{white-space:pre-wrap;overflow-wrap:anywhere;max-width:100%}.installer-panel button{max-width:100%;white-space:normal}
</style>
