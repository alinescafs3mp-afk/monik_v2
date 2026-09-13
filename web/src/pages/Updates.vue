<script setup lang="ts">
import { ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
const emit = defineEmits<{ toast: [string, string?] }>();
const rows = ref<any[]>([]);
const agents = ref<any[]>([]);
const selected = ref<string[]>([]);
const bundle = ref("");
const enroll = ref(false);
const pending = ref("");
const { loading, error, refresh } = usePolling(async () => {
  const [rel, ag] = await Promise.all([get<any>("/api/v1/releases"), get<any>("/api/v1/agents")]);
  rows.value = rel.releases || [];
  agents.value = ag.agents || [];
});
async function importBundle() {
  pending.value = "import";
  try {
    const r = await submitOp("update.import", { bundle_path: bundle.value, enroll_root: enroll.value });
    emit("toast", "Комплект проверен по доверенному TUF root и записан в каталог.", `/operations/${r.op?.operation_id}`);
    await refresh();
  } finally { pending.value = ""; }
}
async function rollout(id: string) {
  const targets = [...selected.value];
  pending.value = id;
  try {
    const r = await submitOp("update.rollout", { release_id: id }, targets);
    emit("toast", "Раскатка создана только для выбранных управляемых машин.", `/operations/${r.op?.operation_id}`);
  } finally { pending.value = ""; }
}
</script>
<template>
  <section class="panel">
    <h2>Обновления агентов</h2>
    <p>Импорт проверяет комплект против независимо зачисленного TUF root. Первый импорт может зачислить root владельца. Раскатка идёт только на выбранные машины с подтверждённой управляемой установкой; неподписанный или чужой root отклоняется.</p>
    <label>Путь к комплекту на контроллере <input v-model="bundle" autocomplete="off"/></label>
    <label><input type="checkbox" v-model="enroll"/> Зачислить TUF root из этого комплекта (только первый доверенный репозиторий)</label>
    <button :disabled="!bundle || !!pending" @click="importBundle">{{ pending==='import' ? 'Импорт…' : 'Импортировать комплект' }}</button>
    <router-link to="/agents">Выбор машин</router-link>
  </section>
  <section class="panel">
    <h3>Цели раскатки</h3>
    <p class="muted">Нет кнопки «Обновить всех». Неуправляемые агенты сервер пометит как неподдерживаемые.</p>
    <label v-for="ag in agents" :key="ag.id"><input type="checkbox" :value="ag.id" v-model="selected" :disabled="ag.revoked||ag.archived"/> {{ ag.display_name }} · {{ ag.os }}/{{ ag.arch }} · {{ ag.managed_ready ? 'управляемый' : 'неуправляемый' }}</label>
  </section>
  <section class="panel">
    <h3>Каталог</h3>
    <p v-if="loading">Загрузка…</p>
    <p v-if="error" role="alert">{{ error }} <button @click="refresh">Повторить</button></p>
    <p v-else-if="!rows.length&&!loading">Каталог пуст. Импортируйте подписанный комплект.</p>
    <p v-for="r in rows" :key="r.id">{{ r.version }} · {{ r.imported_at }} · {{ r.trust_ok ? 'root совпал' : 'не доверен' }}
      <button :disabled="!selected.length || !!pending || !r.trust_ok" @click="rollout(r.id)">{{ pending===r.id ? 'Отправляем…' : 'Раскатить выбранным' }}</button>
    </p>
  </section>
</template>
