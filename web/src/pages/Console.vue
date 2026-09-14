<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue';
import {useRoute,onBeforeRouteLeave,onBeforeRouteUpdate} from 'vue-router';
import {Terminal} from '@xterm/xterm';
import {FitAddon} from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import {get,consoleTicket} from '../api';
import PasswordInput from '../components/PasswordInput.vue';
const route=useRoute(),id=computed(()=>String(route.params.id));
const config=ref<any>(null),error=ref(''),status=ref('Загрузка настроек…');
const password=ref(''),privateKey=ref(''),passphrase=ref(''),auth=ref('password'),busy=ref(false),connected=ref(false);
const area=ref<HTMLElement|null>(null);
let ws:WebSocket|null=null,term:Terminal|null=null,fit:FitAddon|null=null,observer:ResizeObserver|null=null;
let generation=0;
function credentialsClear(){password.value='';privateKey.value='';passphrase.value='';}
async function load(){const requestID=id.value;try{const value=await get(`/api/v1/agents/${encodeURIComponent(requestID)}/console`);if(requestID!==id.value)return;config.value=value;status.value=config.value.enabled?'Готово к ручному подключению':'Консоль не настроена';}catch(e){if(requestID===id.value)error.value=(e as Error).message;}}
function send(value:unknown){if(ws?.readyState===WebSocket.OPEN)ws.send(JSON.stringify(value));}
function disconnect(){generation++;send({type:'close'});ws?.close();ws=null;connected.value=false;busy.value=false;credentialsClear();observer?.disconnect();observer=null;term?.dispose();term=null;fit=null;status.value='Отключено. Повторный вход только вручную.';}
function resized(){if(!term||!fit)return;fit.fit();if(connected.value)send({type:'resize',cols:Math.min(300,Math.max(20,term.cols)),rows:Math.min(120,Math.max(5,term.rows))});}
async function connect(){
 if(busy.value||connected.value)return;error.value='';busy.value=true;status.value='Проверяем разрешение…';const attempt=++generation;
 try{
  const {ticket}=await consoleTicket(`/api/v1/agents/${encodeURIComponent(id.value)}/console-ticket`);
  if(attempt!==generation)return;
  await nextTick();term?.dispose();term=new Terminal({cursorBlink:true,scrollback:1000,fontSize:14,linkHandler:{activate:()=>{}}});fit=new FitAddon();term.loadAddon(fit);
  // Do not allow terminal escape sequences to write into the browser clipboard.
  term.parser.registerOscHandler(52,()=>true);
  term.open(area.value!);fit.fit();
  const url=new URL(`/api/v1/agents/${encodeURIComponent(id.value)}/console-stream`,window.location.href);url.protocol='wss:';
  const socket=new WebSocket(url);ws=socket;status.value='Подключаем SSH…';
  socket.onopen=()=>{
   if(attempt!==generation){socket.close();return;}
   socket.send(JSON.stringify({type:'authenticate',ticket,password:auth.value==='password'?password.value:'',private_key:auth.value==='key'?privateKey.value:'',passphrase:auth.value==='key'?passphrase.value:'',cols:Math.min(300,Math.max(20,term!.cols)),rows:Math.min(120,Math.max(5,term!.rows))}));credentialsClear();
  };
  socket.onmessage=event=>{
   if(attempt!==generation)return;
   try{const msg=JSON.parse(event.data);
    if(msg.type==='output'){const bytes=Uint8Array.from(atob(msg.data),c=>c.charCodeAt(0));term?.write(bytes);}
    else if(msg.type==='ready'){connected.value=true;busy.value=false;status.value=msg.message;term?.focus();resized();}
    else if(msg.type==='error'){error.value=msg.message;busy.value=false;connected.value=false;}
    else if(msg.type==='closed'){status.value=msg.message;busy.value=false;connected.value=false;}
   }catch{error.value='Некорректное сообщение терминала';socket.close();}
  };
  socket.onerror=()=>{if(attempt===generation)error.value='Соединение терминала не установлено или потеряно. Автоповтора нет.';};
  socket.onclose=()=>{if(attempt===generation){busy.value=false;connected.value=false;credentialsClear();if(!error.value)status.value='SSH-сеанс закрыт. Неподтверждённые команды не повторяются.';}};
  term.onData(data=>{if(!connected.value)return;for(let i=0;i<data.length;){let end=Math.min(i+4096,data.length);const last=data.charCodeAt(end-1);if(end<data.length&&last>=0xD800&&last<=0xDBFF)end--;send({type:'input',data:data.slice(i,end)});i=end;}});
  observer?.disconnect();if(typeof ResizeObserver!=='undefined'){observer=new ResizeObserver(resized);observer.observe(area.value!);}
 }catch(e){if(attempt===generation){error.value=(e as Error).message||'Подключение не удалось';busy.value=false;credentialsClear();}}
}
function paste(event:ClipboardEvent){const text=event.clipboardData?.getData('text')||'';if(/[\r\n]/.test(text)&&!window.confirm('Вставка содержит несколько строк и может выполнить команды. Продолжить?')){event.preventDefault();event.stopImmediatePropagation();}}
function leaving(e:BeforeUnloadEvent){if(connected.value||busy.value){e.preventDefault();e.returnValue='';}}
onBeforeRouteUpdate(()=>!(connected.value||busy.value)||window.confirm('Закрыть консоль текущей машины перед переходом?'));
watch(id,()=>{disconnect();config.value=null;error.value='';void load();});
onBeforeRouteLeave(()=>!(connected.value||busy.value)||window.confirm('Закрыть активное подключение? Запущенные удалённые процессы могут продолжить работу.'));
onMounted(()=>{void load();window.addEventListener('beforeunload',leaving);});onUnmounted(()=>{disconnect();window.removeEventListener('beforeunload',leaving);});
</script>
<template><section class="console-page">
 <header class="row"><router-link :to="`/machines/${encodeURIComponent(id)}`">← Машина</router-link><h2>SSH-консоль</h2></header>
 <p class="muted">Отдельный SSH-доступ, не оболочка агента. Правами управляет SSH-учётная запись. Закрытие вкладки не гарантирует остановку уже запущенных программ.</p>
 <p role="status">{{status}}</p><p v-if="error" class="panel err" role="alert">{{error}}</p>
 <div v-if="config&&!config.enabled" class="panel"><p>{{config.reason}}</p><p>На контроллере настройте защищённый <code>console-targets.json</code> для машины <code>{{id}}</code>: IP, порт, SSH-логин и независимо проверенный отпечаток ключа. Агенту дополнительные права не нужны.</p><button @click="load">Проверить настройки</button></div>
 <form v-if="config?.enabled&&!connected" class="panel console-auth" @submit.prevent="connect">
  <p><b>{{config.target.username}}@{{config.target.host}}:{{config.target.port}}</b></p><p class="fingerprint">{{config.target.host_key_sha256}}</p>
  <label>Способ входа <select v-model="auth" :disabled="busy"><option value="password">Пароль SSH</option><option value="key">Приватный ключ</option></select></label>
  <PasswordInput v-if="auth==='password'" id="ssh-password" v-model="password" label="Пароль SSH" autocomplete="off" :disabled="busy"/>
  <template v-else><label>Приватный ключ <textarea v-model="privateKey" :disabled="busy" rows="5" maxlength="32768" autocomplete="off" spellcheck="false"/></label><PasswordInput id="ssh-key-password" v-model="passphrase" label="Пароль ключа (если есть)" :required="false" autocomplete="off" :disabled="busy"/></template>
  <p class="muted">Данные входа не сохраняются в Monik. Сеанс: до часа, без ввода: до 10 минут, вывод до 16 МиБ. Требуется недавнее подтверждение входа владельца.</p>
  <button type="submit" :disabled="busy||(auth==='password'?!password:!privateKey)">{{busy?'Подключаем…':'Подключиться'}}</button><button v-if="busy" type="button" @click="disconnect">Отменить</button>
 </form>
 <button v-if="connected" type="button" @click="disconnect">Отключиться</button>
 <div ref="area" class="console-terminal" aria-label="Интерактивный SSH-терминал" @paste.capture="paste"/>
</section></template>
<style scoped>
.console-page{max-width:100%;min-width:0;}.console-auth{max-width:42rem;display:flex;flex-direction:column;gap:.7rem}.console-terminal{height:clamp(240px,58vh,850px);background:#000;margin-top:1rem;padding:8px;min-width:0;overflow:hidden}.fingerprint{overflow-wrap:anywhere;font-family:monospace}.console-auth textarea{font-family:monospace;}
</style>
