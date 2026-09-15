<script setup lang="ts">
import {computed,nextTick,onMounted,onUnmounted,ref,watch} from 'vue';
import {useRoute,onBeforeRouteLeave,onBeforeRouteUpdate} from 'vue-router';
import {Terminal} from '@xterm/xterm';
import {FitAddon} from '@xterm/addon-fit';
import '@xterm/xterm/css/xterm.css';
import {get,consoleTicket} from '../api';
import {authFrame,inputChunks,terminalOutput,MAX_TERMINAL_BACKLOG} from '../terminalTransport';
import ConsoleSetup from '../components/ConsoleSetup.vue';
import PasswordInput from '../components/PasswordInput.vue';
const route=useRoute(),id=computed(()=>String(route.params.id));
const transport=ref<'agent'|'ssh'>('agent');
const agent=ref<any>(null),ssh=ref<any>(null),config=computed(()=>transport.value==='agent'?agent.value:ssh.value);
const error=ref(''),status=ref('Загрузка настроек…'),loading=ref(false);
const password=ref(''),privateKey=ref(''),passphrase=ref(''),auth=ref('password'),busy=ref(false),connected=ref(false);
const area=ref<HTMLElement|null>(null);
let ws:WebSocket|null=null,term:Terminal|null=null,fit:FitAddon|null=null,observer:ResizeObserver|null=null;
let generation=0,loadGeneration=0,seq=0,poll=0,pendingOutput=0;
function credentialsClear(){password.value='';privateKey.value='';passphrase.value='';}
async function load(){
 const requestID=id.value,request=++loadGeneration;loading.value=true;
 const results=await Promise.allSettled([get(`/api/v1/agents/${encodeURIComponent(requestID)}/agent-console`),get(`/api/v1/agents/${encodeURIComponent(requestID)}/console`)]);
 if(request!==loadGeneration||requestID!==id.value)return;
 agent.value=results[0].status==='fulfilled'?results[0].value:{enabled:false,reason:results[0].reason?.message||'Канал агента недоступен.'};
 ssh.value=results[1].status==='fulfilled'?results[1].value:{enabled:false,reason:results[1].reason?.message||'SSH-настройки недоступны.'};
 loading.value=false;if(!busy.value&&!connected.value)status.value=config.value?.enabled?'Готово к ручному подключению':'Подключение пока недоступно';
}
function closeSocket(reason:string){if(ws)ws.close();connected.value=false;busy.value=false;error.value=reason;credentialsClear();}
function send(value:Record<string,unknown>){
 if(ws?.readyState!==WebSocket.OPEN)return;
 if(ws.bufferedAmount>MAX_TERMINAL_BACKLOG){closeSocket('Канал перегружен. Сеанс закрыт, ввод не будет повторён.');return;}
 if(transport.value==='agent'&&['input','resize','close'].includes(String(value.type)))value={...value,seq:++seq};
 ws.send(JSON.stringify(value));
}
function disconnect(){generation++;send({type:'close'});ws?.close();ws=null;connected.value=false;busy.value=false;credentialsClear();observer?.disconnect();observer=null;term?.dispose();term=null;fit=null;pendingOutput=0;status.value='Отключено. Повторный вход только вручную.';}
function resized(){if(!term||!fit)return;try{fit.fit();}catch{return;}if(connected.value)send({type:'resize',cols:Math.min(300,Math.max(20,term.cols)),rows:Math.min(120,Math.max(5,term.rows))});}
async function connect(){
 if(busy.value||connected.value||!config.value?.enabled)return;
 ws?.close();ws=null;observer?.disconnect();observer=null;error.value='';busy.value=true;seq=0;pendingOutput=0;status.value='Проверяем разрешение…';
 const attempt=++generation,kind=transport.value,machine=id.value;
 const current=()=>attempt===generation&&machine===id.value&&kind===transport.value;
 const prefix=`/api/v1/agents/${encodeURIComponent(machine)}/${kind==='agent'?'agent-console':'console'}`;
 try{
  const {ticket}=await consoleTicket(prefix+'-ticket',kind==='agent'?config.value.channel_revision:config.value.target_revision);
  if(!current())return;
  await nextTick();if(!current())return;term?.dispose();term=new Terminal({cursorBlink:true,scrollback:1000,fontSize:14,linkHandler:{activate:()=>{}}});fit=new FitAddon();term.loadAddon(fit);
  // Terminal output cannot write the browser clipboard or activate remote links.
  term.parser.registerOscHandler(52,()=>true);
  term.open(area.value!);fit.fit();
  const url=new URL(prefix+'-stream',window.location.href);url.protocol='wss:';
  const socket=new WebSocket(url);ws=socket;status.value=kind==='agent'?'Открываем терминал через агент…':'Подключаем SSH…';
  socket.onopen=()=>{
   if(!current()){socket.close();return;}
   socket.send(JSON.stringify(authFrame(kind,ticket,term!.cols,term!.rows,{password:auth.value==='password'?password.value:'',private_key:auth.value==='key'?privateKey.value:'',passphrase:auth.value==='key'?passphrase.value:''})));credentialsClear();
  };
  socket.onmessage=event=>{
   if(!current())return;
   try{
    if(typeof event.data!=='string'||event.data.length>24576)throw new Error('Некорректный размер сообщения терминала.');
    const msg=JSON.parse(event.data);
    if(msg.type==='ping'){send({type:'pong'});return;}
    if(msg.type==='output'){
     if(kind==='agent'&&!connected.value)throw new Error('Вывод до подтверждения терминала.');
     const bytes=terminalOutput(msg.data);pendingOutput+=bytes.length;
     if(pendingOutput>MAX_TERMINAL_BACKLOG)throw new Error('Вывод не успевает отображаться. Канал закрыт без повтора ввода.');
     term?.write(bytes,()=>{if(current())pendingOutput-=bytes.length;});
    }
    else if(msg.type==='ready'){if(connected.value)throw new Error('Повторное открытие запрещено.');connected.value=true;busy.value=false;status.value=msg.message||'Подключено';term?.focus();resized();}
    else if(msg.type==='error'){error.value=String(msg.message||'Ошибка терминала');socket.close();}
    else if(msg.type==='closed'){status.value=String(msg.message||'Сеанс завершён');socket.close();}
    else throw new Error('Неизвестное сообщение терминала.');
   }catch(e){error.value=(e as Error).message||'Некорректное сообщение терминала';socket.close();}
  };
  socket.onerror=()=>{if(current())error.value='Соединение терминала не установлено или потеряно. Автоповтора нет.';};
  socket.onclose=()=>{if(current()){busy.value=false;connected.value=false;credentialsClear();observer?.disconnect();if(!error.value)status.value='Сеанс закрыт. Неподтверждённые команды не повторяются.';}};
  term.onData(data=>{if(!connected.value)return;try{for(const chunk of inputChunks(data))send({type:'input',data:chunk});}catch(e){closeSocket((e as Error).message);}});
  if(typeof ResizeObserver!=='undefined'){observer=new ResizeObserver(resized);observer.observe(area.value!);}
 }catch(e){if(current()){error.value=(e as Error).message||'Подключение не удалось';busy.value=false;credentialsClear();}}
}
function paste(event:ClipboardEvent){const text=event.clipboardData?.getData('text')||'';if(/[\r\n]/.test(text)&&!window.confirm('Вставка содержит несколько строк и может выполнить команды. Продолжить?')){event.preventDefault();event.stopImmediatePropagation();}}
function leaving(e:BeforeUnloadEvent){if(connected.value||busy.value){e.preventDefault();e.returnValue='';}}
watch(transport,()=>{disconnect();error.value='';status.value=config.value?.enabled?'Готово к ручному подключению':'Подключение пока недоступно';});
onBeforeRouteUpdate(()=>!(connected.value||busy.value)||window.confirm('Закрыть консоль текущей машины перед переходом?'));
watch(id,()=>{disconnect();agent.value=null;ssh.value=null;error.value='';void load();});
onBeforeRouteLeave(()=>!(connected.value||busy.value)||window.confirm('Закрыть активное подключение? Уже выполненные действия не отменятся.'));
onMounted(()=>{void load();window.addEventListener('beforeunload',leaving);poll=window.setInterval(()=>{if(!connected.value&&!busy.value&&!loading.value&&transport.value==='agent')void load();},10000);});
onUnmounted(()=>{++loadGeneration;clearInterval(poll);disconnect();window.removeEventListener('beforeunload',leaving);});
</script>
<template><section class="console-page">
 <header class="row"><router-link :to="`/machines/${encodeURIComponent(id)}`">← Машина</router-link><h2>Консоль машины</h2></header>
 <div class="row" role="group" aria-label="Способ подключения"><button :disabled="busy||connected" :aria-pressed="transport==='agent'" @click="transport='agent'">Через агент</button><button :disabled="busy||connected" :aria-pressed="transport==='ssh'" @click="transport='ssh'">Прямое SSH</button><button :disabled="busy||connected||loading" @click="load">Проверить подключение</button></div>
 <p v-if="transport==='agent'" class="muted">Исходящий защищённый канал агента. Терминал работает под отдельным пользователем monik-console, без root/sudo и без доступа к ключам мониторинга. Входящий SSH-порт не нужен.</p>
 <p v-else class="muted">Отдельный SSH-доступ. Правами управляет SSH-учётная запись. Закрытие вкладки не гарантирует остановку уже запущенных программ.</p>
 <p role="status">{{status}}</p><p v-if="error" class="panel err" role="alert">{{error}}</p>
 <div v-if="config&&!config.enabled" class="panel"><p>{{config.reason}}</p><p v-if="transport==='agent'">Для существующей стандартной Linux-установки после обновления пары agent + service-host разрешите консоль локально: <code>sudo /usr/lib/monik/monik-service-host console-enable</code>. Это отдельное разрешение, не команда через мониторинг. Windows пока использует прямое SSH.</p></div>
 <ConsoleSetup v-if="transport==='ssh'&&ssh?.configurable" :key="id" :id="id" :config="ssh" :locked="connected||busy" @saved="load"/>
 <form v-if="config?.enabled&&!connected" class="panel console-auth" @submit.prevent="connect">
  <template v-if="transport==='ssh'">
   <p><b>{{config.target.username}}@{{config.target.host}}:{{config.target.port}}</b></p><p class="fingerprint">{{config.target.host_key_sha256}}</p>
   <label>Способ входа <select v-model="auth" :disabled="busy"><option value="password">Пароль SSH</option><option value="key">Приватный ключ</option></select></label>
   <PasswordInput v-if="auth==='password'" id="ssh-password" v-model="password" label="Пароль SSH" autocomplete="off" :disabled="busy"/>
   <template v-else><label>Приватный ключ <textarea v-model="privateKey" :disabled="busy" rows="5" maxlength="32768" autocomplete="off" spellcheck="false"/></label><PasswordInput id="ssh-key-password" v-model="passphrase" label="Пароль ключа (если есть)" :required="false" autocomplete="off" :disabled="busy"/></template>
  </template>
  <p v-else><b>Через агент · пользователь ОС: {{config.username}}</b>. Никаких дополнительных сетевых реквизитов.</p>
  <p class="muted">Сеанс до часа, без ввода до 10 минут, вывод до 16 МиБ. Требуется недавнее подтверждение владельца. Ввод не записывается в очередь и не повторяется после обрыва.</p>
  <button type="submit" :disabled="busy||(transport==='ssh'&&(auth==='password'?!password:!privateKey))">{{busy?'Подключаем…':'Подключиться'}}</button><button v-if="busy" type="button" @click="disconnect">Отменить</button>
 </form>
 <button v-if="connected" type="button" @click="disconnect">Отключиться</button>
 <div ref="area" class="console-terminal" aria-label="Интерактивный терминал" @paste.capture="paste"/>
</section></template>
<style scoped>
.console-page{max-width:100%;min-width:0}.console-auth{max-width:42rem;display:flex;flex-direction:column;gap:.7rem}.console-terminal{height:clamp(240px,58vh,850px);background:#000;margin-top:1rem;padding:8px;min-width:0;overflow:hidden}.fingerprint{overflow-wrap:anywhere;font-family:monospace}.console-auth textarea{font-family:monospace}.console-page code{overflow-wrap:anywhere}.console-page>.row{flex-wrap:wrap}
</style>
