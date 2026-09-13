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
    emit("toast", "Снимок базы создан. Полное восстановление ключей и контроллера не проверено.", r.op ? `/operations/${r.op.operation_id}` : "");
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
      <p>TUF root: {{ st?.tuf_root_enrolled ? 'зачислен' : 'ещё не зачислен — первый импорт с отметкой enroll_root' }}</p>
      <p class="muted">Публичный CA контроллера (не ключ):</p>
      <pre class="muted">{{ st?.ca_cert_pem }}</pre>
    </section>
    <section class="panel">
      <h2>Диагностика</h2>
      <pre class="muted">{{ JSON.stringify(diag, null, 2) }}</pre>
      <p class="data-warning">Сейчас сохраняется только согласованный снимок SQLite. Это не полный комплект восстановления: отдельно сохраните каталог TLS, secret-master.key и конфигурацию. Статус проверки восстановления: не проверено.</p><button :disabled="pending" @click="backup">Создать снимок базы</button>
    </section>
    <section class="panel">
      <h2>Недавняя аутентификация</h2>
      <p class="muted">Нужна для отзыва, обновлений и смены контроллера.</p>
      <input v-model="pw" type="password" autocomplete="current-password" />
      <button @click="reauth">Подтвердить пароль</button>
    </section>
  </div>
</template>
