<script setup lang="ts">
import {computed,ref,watch} from 'vue';
import {saveConsoleConfiguration} from '../api';
const props=defineProps<{id:string;config:any;locked:boolean}>();
const emit=defineEmits<{saved:[]}>();
const host=ref(''),port=ref(22),username=ref(''),fingerprint=ref(''),keyType=ref('ssh-ed25519'),verified=ref(false),revision=ref(''),busy=ref(false),error=ref(''),notice=ref(''),dirty=ref(false);
function read(){const t=props.config?.target;host.value=t?.host||'';port.value=t?.port||22;username.value=t?.username||'';fingerprint.value=t?.host_key_sha256||'';keyType.value=t?.host_key_type||(!t?'ssh-ed25519':'');revision.value=props.config?.config_revision||'';verified.value=false;dirty.value=false;}
watch(()=>[props.id,props.config?.config_revision],()=>{if(!dirty.value)read();},{immediate:true});
const valid=computed(()=>host.value.trim()&&username.value.trim()&&/^SHA256:[A-Za-z0-9+/]{43}$/.test(fingerprint.value.trim())&&Number.isInteger(port.value)&&port.value>=1&&port.value<=65535&&verified.value);
async function save(enabled:boolean){
 if(busy.value||props.locked)return;
 if(!enabled&&!window.confirm('Выключить SSH-консоль этой машины? Активный сеанс будет закрыт. Агент и мониторинг продолжат работу.'))return;
 const requestID=props.id;busy.value=true;error.value='';notice.value='Сохраняем SSH-настройки…';
 try{
  await saveConsoleConfiguration(requestID,{base_revision:revision.value,enabled,...(enabled?{target:{host:host.value.trim(),port:port.value,username:username.value.trim(),host_key_sha256:fingerprint.value.trim(),host_key_type:keyType.value},fingerprint_verified:verified.value}:{} )});
  if(requestID!==props.id)return;dirty.value=false;verified.value=false;notice.value=enabled?'SSH-настройки сохранены. Подключение запускается отдельно, кнопкой ниже.':'SSH-консоль выключена. Мониторинг не изменён.';emit('saved');
 }catch(e){if(requestID===props.id){notice.value='';error.value=(e as Error).message||'Сохранение не подтверждено. Перечитайте настройки перед повторной попыткой.';}}
 finally{if(requestID===props.id)busy.value=false;}
}
function reload(){if(dirty.value&&!window.confirm('Заменить несохранённый черновик текущими настройками сервера?'))return;dirty.value=false;emit('saved');}
</script>
<template><details class="panel console-setup" :open="!config.enabled"><summary>Настроить SSH-подключение</summary>
 <form @submit.prevent="save(true)" @input="dirty=true">
  <p>Укажите SSH машины, а не адрес веб-панели Monik. Контроллер должен видеть этот IP и порт. Эти настройки не устанавливают SSH-сервер и не открывают firewall.</p>
  <div class="console-target-grid"><label>IP машины<input v-model="host" name="ssh_host" required maxlength="64" placeholder="192.0.2.10" :disabled="busy||locked" autocomplete="off"/></label><label>SSH-порт<input v-model.number="port" name="ssh_port" type="number" min="1" max="65535" step="1" required :disabled="busy||locked"/></label><label>SSH-логин<input v-model="username" name="ssh_username" required maxlength="128" :disabled="busy||locked" autocomplete="off"/></label></div>
  <label>Тип ключа сервера<select v-model="keyType" :disabled="busy||locked"><option value="ssh-ed25519">Ed25519</option><option value="ecdsa-sha2-nistp256">ECDSA P-256</option><option value="ecdsa-sha2-nistp384">ECDSA P-384</option><option value="ecdsa-sha2-nistp521">ECDSA P-521</option><option value="ssh-rsa">RSA (подпись SHA-2)</option><option value="">Автовыбор, для прежних настроек</option></select></label>
  <label>Отпечаток ключа SSH-сервера<input v-model="fingerprint" name="ssh_fingerprint" required maxlength="64" placeholder="SHA256:…" :disabled="busy||locked" autocomplete="off" spellcheck="false"/></label>
  <p class="muted">Получите отпечаток через уже доверенный SSH или консоль провайдера на этой машине, например:</p>
  <code class="ssh-fingerprint-command">sudo ssh-keygen -lf /etc/ssh/{{keyType==='ssh-rsa'?'ssh_host_rsa_key.pub':keyType.startsWith('ecdsa')?'ssh_host_ecdsa_key.pub':'ssh_host_ed25519_key.pub'}} -E sha256</code>
  <p class="muted">Скопируйте только часть SHA256:… для действительного ключа SSH-сервера. Самостоятельное сканирование неизвестного сервера не доказывает его подлинность. Не вставляйте приватный ключ в это поле.</p>
  <label class="row"><input v-model="verified" type="checkbox" :disabled="busy||locked"/> Я независимо сверил отпечаток этой машины</label>
  <div class="row"><button type="submit" :disabled="busy||locked||!valid">{{busy?'Сохраняем…':config.enabled?'Сохранить SSH-настройки':'Включить SSH-консоль'}}</button><button v-if="config.enabled" type="button" :disabled="busy||locked" @click="save(false)">Выключить консоль</button><button type="button" :disabled="busy||locked" @click="reload">Перечитать настройки</button></div>
  <p v-if="locked" class="muted">Сначала отключите текущий сеанс.</p><p v-if="notice" role="status">{{notice}}</p><p v-if="error" role="alert" class="err">{{error}}</p>
 </form>
</details></template>
<style scoped>
.console-setup {max-width:54rem;}.console-setup form{display:flex;flex-direction:column;gap:.65rem;margin-top:.75rem}.console-setup label:not(.row){display:flex;flex-direction:column;gap:.3rem;min-width:0}.console-setup input:not([type=checkbox]){width:100%;min-width:0}.console-target-grid{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(5rem,.6fr) minmax(0,1fr);gap:.6rem}.ssh-fingerprint-command{display:block;overflow-wrap:anywhere;white-space:normal;padding:.5rem;background:var(--bg);font-size:.85rem}.console-setup p{margin:.2rem 0;}@media(max-width:600px){.console-target-grid{grid-template-columns:minmax(0,1fr);}}
</style>
