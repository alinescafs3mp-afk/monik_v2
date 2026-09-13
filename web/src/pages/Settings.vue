<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, post, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const st = ref<Record<string, unknown> | null>(null);
const diag = ref<Record<string, unknown> | null>(null);
const pw = ref("");
const pending = ref(false);
onMounted(async () => {
  st.value = await get("/api/v1/settings");
  diag.value = await get("/api/v1/diagnostics");
});
async function backup() {
  pending.value = true;
  try {
    const r = await submitOp("backup.create", {});
    emit("toast", "Резервная копия проверяется по sha256", r.op ? `/operations/${r.op.operation_id}` : "");
  } finally {
    pending.value = false;
  }
}
async function reauth() {
  await post("/api/v1/reauth", { password: pw.value });
  pw.value = "";
  emit("toast", "Недавняя аутентификация обновлена на 10 минут");
}
</script>

<template>
  <div>
    <section class="panel">
      <h2>Контроллер</h2>
      <p>URL: {{ st?.advertised_url }}</p>
      <p>Listen: {{ st?.listen }}</p>
      <p>ID: {{ st?.controller_id }}</p>
      <p>Срок TLS: {{ st?.tls_leaf_expiry }}</p>
      <p>Сырая история: {{ st?.retention_raw_hours }} ч</p>
      <p>Telegram: отложен</p>
    </section>
    <section class="panel">
      <h2>Диагностика</h2>
      <pre class="muted">{{ JSON.stringify(diag, null, 2) }}</pre>
      <button :disabled="pending" @click="backup">Создать резервную копию</button>
    </section>
    <section class="panel">
      <h2>Недавняя аутентификация</h2>
      <p class="muted">Нужна для отзыва, обновлений и смены контроллера.</p>
      <input v-model="pw" type="password" autocomplete="current-password" />
      <button @click="reauth">Подтвердить пароль</button>
    </section>
  </div>
</template>
