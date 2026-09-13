<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const info = ref<Record<string, unknown> | null>(null);
const created = ref<Record<string, unknown> | null>(null);
const pending = ref(false);
onMounted(async () => {
  info.value = await get("/api/v1/enrollment");
});
async function makeCode() {
  pending.value = true;
  try {
    const r = await submitOp("enrollment.create", {});
    const op = r.op as Record<string, unknown> | null;
    const targets = (op?.targets as Array<Record<string, unknown>>) || [];
    created.value = (targets[0]?.evidence as Record<string, unknown>) || op;
    emit("toast", "Код регистрации создан на 10 минут", op ? `/operations/${op.operation_id}` : "");
  } finally {
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
    emit("toast", "Буфер недоступен — скопируйте текст вручную");
  }
}
</script>

<template>
  <div>
    <section class="panel">
      <h2>Добавить машину</h2>
      <p>Рекламируемый адрес: <code>{{ info?.advertised_url }}</code></p>
      <p class="muted">Агент проверяет TLS по CA контроллера. Не используйте -k.</p>
      <button class="primary" :disabled="pending" @click="makeCode">Создать код регистрации</button>
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
