<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import InstallerDownload from "../components/InstallerDownload.vue";
import AdmissionPanel from "../components/AdmissionPanel.vue";
import { get, submitOp } from "../api";
import {discoveryProfile as makeDiscoveryProfile,normalizedProfileURL} from "../inventory";
const emit = defineEmits<{ toast: [string, string?] }>();
const info = ref<Record<string, unknown> | null>(null);
const created = ref<Record<string, unknown> | null>(null);
const pending = ref(false), error = ref(''), autoProfile = ref(false);
const installerBusy=ref(false);
const profileURL=ref(''), checking=ref(false), validation=ref<any>(null);
watch(profileURL,()=>validation.value=null);
function discoveryProfile(){
 try{return JSON.stringify(makeDiscoveryProfile(profileURL.value,String(info.value?.ca_cert_pem||'')),null,2);}catch{return '';}
}
async function checkAddress():Promise<boolean>{
 error.value='';checking.value=true;validation.value=null;
 try{
  const selected=normalizedProfileURL(profileURL.value);
  const result=await get<any>(`/api/v1/enrollment?controller_url=${encodeURIComponent(selected)}`);
  if(normalizedProfileURL(profileURL.value)!==selected){error.value='Адрес изменился во время проверки. Проверьте ещё раз.';return false;}
  if(result.ca_cert_pem!==info.value?.ca_cert_pem){error.value='Доверие контроллера изменилось. Обновите страницу и проверьте исходную CA перед сохранением профиля.';return false;}
  validation.value=result;
  if(!result.tls?.matches){error.value='Сертификат контроллера не подходит выбранному адресу. Подготовьте SAN командой tls-add-name на остановленном сервере. Проверка не меняет маршруты и не обращается к внешнему адресу.';return false;}
  return true;
 }catch(e){error.value=(e as Error).message;return false;}finally{checking.value=false;}
}
async function downloadProfile(){
 if(checking.value || !await checkAddress())return;
 const body=discoveryProfile();if(!body){error.value='Не удалось сформировать профиль';return;}
 const url=URL.createObjectURL(new Blob([body],{type:'application/json'}));
 const a=document.createElement('a');a.href=url;a.download='monik-discovery.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
 emit('toast','Профиль сохранён. Доступность маршрута проверяется с самой удалённой машины.');
}
onMounted(async () => {
  try{info.value = await get("/api/v1/enrollment");profileURL.value=String(info.value?.profile_url||info.value?.bootstrap_default||info.value?.advertised_url||'');}catch(e){error.value=(e as Error).message;}
});
async function makeCode() {
  if(pending.value || checking.value || !info.value)return;
  if(!await checkAddress())return;
  pending.value = true; error.value="";
  try {
    const r = await submitOp("enrollment.create", {});
    const op = r.op as Record<string, unknown> | null;
    const targets = (op?.targets as Array<Record<string, unknown>>) || [];
    const evidence=(targets[0]?.evidence as Record<string,unknown>);
    if(!evidence?.code){error.value='Операция сохранена, но создание кода ещё не подтверждено. Проверьте её в разделе «Операции».';return;}
    created.value=evidence;
    emit("toast", "Код регистрации создан на 10 минут", op ? `/operations/${op.operation_id}` : "");
  } catch(e){error.value=(e as Error).message;} finally {
    pending.value = false;
  }
}
const yaml = () => {
  const ev = created.value || {};
  let chosen:string;try{chosen=normalizedProfileURL(profileURL.value);}catch{return 'Укажите корректный HTTPS-адрес выше.';}
  return `schema_version: 3
controller_url: ${chosen}
ca_cert_pem: |
${String(ev.ca_cert_pem || info.value?.ca_cert_pem || "")
  .split("\n")
  .map((l) => "  " + l)
  .join("\n")}
enrollment_code: ${ev.code || ""}
`;
};
async function copyYaml() {
  if(checking.value || !await checkAddress())return;
  try {
    await navigator.clipboard.writeText(yaml());
    emit("toast", "Профиль скопирован");
  } catch {
    emit("toast", "Буфер недоступен: скопируйте текст вручную");
  }
}
</script>

<template>
  <div>
    <section class="panel profile-address"><h2>Адрес в профиле нового агента</h2>
    <label>Доступный агенту HTTPS-адрес <input v-model="profileURL" :disabled="pending||checking||installerBusy" type="url" autocomplete="off" spellcheck="false"/></label>
    <div class="row"><button :disabled="!info||checking||pending||installerBusy" @click="profileURL=String(info?.bootstrap_default||'');validation=null">Внешний адрес по умолчанию</button><button :disabled="!info||checking||pending||installerBusy" @click="profileURL=String(info?.advertised_url||'');validation=null">Адрес из настроек сервера</button><button :disabled="!info||checking||pending||installerBusy" @click="checkAddress">{{checking?'Проверяем сертификат…':'Проверить сертификат профиля'}}</button></div>
    <p class="muted">Настройки действующего контроллера: {{info?.advertised_url}}. Выбор выше меняет только новый профиль, не LAN-агентов и не маршрут работающего парка. Все профили используют прежнюю CA.</p>
    <p v-if="validation?.tls?.matches" role="status">Сертификат подходит адресу {{validation.profile_url}}. Внешний маршрут и firewall ещё не проверены.</p>
    <p v-if="validation && !validation.tls?.matches" class="data-warning">SAN: {{validation.tls?.ip_addresses?.join(', ')}} {{validation.tls?.dns_names?.join(', ')}}. Добавление имени не требует замены CA.</p>
    </section>
    <p v-if="error" class="panel err" role="alert">{{error}}</p>
    <InstallerDownload :controller-url="profileURL" @busy="installerBusy=$event"/>
    <details class="panel advanced-enrollment"><summary>Другие способы подключения: профиль, код, Windows</summary>
    <AdmissionPanel/>
    <section class="panel"><h2>Автоматическое появление машины</h2><p>Один доверенный профиль можно раздать своим машинам. Он не содержит кода регистрации или общего API-ключа. Откройте приём выше. Каждый агент создаёт собственную идентичность и после запуска появляется в «Машинах» для подтверждения.</p>
    <button :disabled="!info||checking||pending||installerBusy" @click="downloadProfile">Скачать профиль автообнаружения</button><button :disabled="!info" @click="autoProfile=!autoProfile">Показать профиль</button><pre v-if="autoProfile">{{discoveryProfile()}}</pre>
    <p>Linux, установка от администратора с парой бинарников agent + service-host:</p><pre>sudo ./monik-agent setup --profile monik-discovery.json
sudo ./monik-agent service install</pre>
    <p>Windows, терминал с полномочиями администратора:</p><pre>.\monik-agent.exe setup --profile monik-discovery.json
.\monik-agent.exe service install</pre>
    <p>После установки служба начинает маяковать сама. Одобрите отпечаток во вкладке <router-link to="/machines">Машины</router-link>. Для запуска без службы используйте <code>monik-agent run --config &lt;выданный путь&gt;</code>; этот режим не обеспечивает автозапуск.</p>
    <p class="muted">Профиль передаётся доверенным способом. Самоподписанному серверу агент не доверяет только по IP. Профиль должен содержать доступный машине адрес, а сертификат соответствовать ему. Новые машины не добавляются в обзор без вашей галочки.</p>
    </section>
    <section class="panel">
      <h2>Добавить машину</h2>
      <p>Адрес действующего контроллера: <code>{{ info?.advertised_url }}</code></p>
      <p class="muted">Агент проверяет TLS по CA контроллера. Не используйте -k.</p>
      <button class="primary" :disabled="pending || checking || !info || installerBusy" @click="makeCode">Создать код регистрации</button>
    </section>
    <section v-if="created" class="panel">
      <p>Код: <strong>{{ created.code }}</strong> до {{ created.expires_at }}</p>
      <pre v-if="profileURL">{{ yaml() }}</pre>
      <button @click="copyYaml">Копировать профиль</button>
      <p>Linux: <code>monik-agent setup --profile enrollment.yaml && monik-agent service install</code></p>
      <p>Windows: <code>monik-agent.exe setup --profile enrollment.yaml</code></p>
    </section>
    </details>
  </div>
</template>
