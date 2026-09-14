<script setup lang="ts">
import { onMounted, ref } from "vue";
import AdmissionPanel from "../components/AdmissionPanel.vue";
import { get, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const info = ref<Record<string, unknown> | null>(null);
const created = ref<Record<string, unknown> | null>(null);
const pending = ref(false), error = ref(''), autoProfile = ref(false);
function discoveryProfile(){return JSON.stringify({controller_url:info.value?.advertised_url||'',ca_cert_pem:info.value?.ca_cert_pem||'',auto_discover:true},null,2);}
function downloadProfile(){
 const url=URL.createObjectURL(new Blob([discoveryProfile()],{type:'application/json'}));
 const a=document.createElement('a');a.href=url;a.download='monik-discovery.json';a.click();setTimeout(()=>URL.revokeObjectURL(url),1000);
}
onMounted(async () => {
  try{info.value = await get("/api/v1/enrollment");}catch(e){error.value=(e as Error).message;}
});
async function makeCode() {
  if(pending.value || !info.value)return;
  pending.value = true; error.value="";
  try {
    const r = await submitOp("enrollment.create", {});
    const op = r.op as Record<string, unknown> | null;
    const targets = (op?.targets as Array<Record<string, unknown>>) || [];
    created.value = (targets[0]?.evidence as Record<string, unknown>) || op;
    emit("toast", "Код регистрации создан на 10 минут", op ? `/operations/${op.operation_id}` : "");
  } catch(e){error.value=(e as Error).message;} finally {
    pending.value = false;
  }
}
const yaml = () => {
  const ev = created.value || {};
  return `schema_version: 3
controller_url: ${ev.advertised_url || info.value?.advertised_url || ""}
ca_cert_pem: |
${String(ev.ca_cert_pem || info.value?.ca_cert_pem || "")
  .split("\n")
  .map((l) => "  " + l)
  .join("\n")}
enrollment_code: ${ev.code || ""}
`;
};
async function copyYaml() {
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
    <AdmissionPanel/>
    <p v-if="error" class="panel err" role="alert">{{error}}</p>
    <section class="panel"><h2>Автоматическое появление машины</h2><p>Один доверенный профиль можно раздать своим машинам. Он не содержит кода регистрации или общего API-ключа. Откройте приём выше. Каждый агент создаёт собственную идентичность и после запуска появляется в «Машинах» для подтверждения.</p>
    <button :disabled="!info" @click="downloadProfile">Скачать профиль автообнаружения</button><button :disabled="!info" @click="autoProfile=!autoProfile">Показать профиль</button><pre v-if="autoProfile">{{discoveryProfile()}}</pre>
    <p>Linux, установка от администратора с парой бинарников agent + service-host:</p><pre>sudo ./monik-agent setup --profile monik-discovery.json
sudo ./monik-agent service install</pre>
    <p>Windows, терминал с полномочиями администратора:</p><pre>.\monik-agent.exe setup --profile monik-discovery.json
.\monik-agent.exe service install</pre>
    <p>После установки служба начинает маяковать сама. Одобрите отпечаток во вкладке <router-link to="/machines">Машины</router-link>. Для запуска без службы используйте <code>monik-agent run --config &lt;выданный путь&gt;</code>; этот режим не обеспечивает автозапуск.</p>
    <p class="muted">Профиль передаётся доверенным способом. Самоподписанному серверу агент не доверяет только по IP. Профиль должен содержать доступный машине адрес, а сертификат соответствовать ему. Новые машины не добавляются в обзор без вашей галочки.</p>
    </section>
    <section class="panel">
      <h2>Добавить машину</h2>
      <p>Рекламируемый адрес: <code>{{ info?.advertised_url }}</code></p>
      <p class="muted">Агент проверяет TLS по CA контроллера. Не используйте -k.</p>
      <button class="primary" :disabled="pending || !info" @click="makeCode">Создать код регистрации</button>
    </section>
    <section v-if="created" class="panel">
      <p>Код: <strong>{{ created.code }}</strong> до {{ created.expires_at }}</p>
      <pre>{{ yaml() }}</pre>
      <button @click="copyYaml">Копировать профиль</button>
      <p>Linux: <code>monik-agent setup --profile enrollment.yaml && monik-agent service install</code></p>
      <p>Windows: <code>monik-agent.exe setup --profile enrollment.yaml</code></p>
    </section>
  </div>
</template>
