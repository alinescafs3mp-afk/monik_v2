<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const rels = ref<Array<Record<string, unknown>>>([]);
const path = ref("");
const selected = ref("");
const agents = ref<string[]>([]);
const pending = ref(false);
onMounted(async () => {
  rels.value = (await get<{ releases: Array<Record<string, unknown>> }>("/api/v1/releases")).releases || [];
});
async function imp() {
  pending.value = true;
  try {
    const r = await submitOp("update.import", { bundle_path: path.value });
    emit("toast", "Импорт — это каталог, не установка", r.op ? `/operations/${r.op.operation_id}` : "");
    rels.value = (await get<{ releases: Array<Record<string, unknown>> }>("/api/v1/releases")).releases || [];
  } finally {
    pending.value = false;
  }
}
async function rollout() {
  pending.value = true;
  try {
    const r = await submitOp("update.rollout", { release_id: selected.value }, agents.value, agents.value.length ? "selected" : "all");
    emit("toast", "Выкат создан; канарейка и пакеты видны в операции", r.op ? `/operations/${r.op.operation_id}` : "");
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <div>
    <section class="panel">
      <h2>Импорт подписанного комплекта</h2>
      <p class="muted">Ключи root/targets на сервере не хранятся. Импорт проверяет TUF и только после этого попадает в каталог.</p>
      <input v-model="path" placeholder="/path/to/monik-release.tgz" style="width:100%" />
      <p><button class="primary" :disabled="pending || !path" @click="imp">Импортировать</button></p>
    </section>
    <section class="panel">
      <h2>Каталог</h2>
      <p v-if="!rels.length">Подписанных релизов нет.</p>
      <table>
        <thead><tr><th></th><th>Версия</th><th>Digest</th><th>Доверие</th><th>Когда</th></tr></thead>
        <tbody>
          <tr v-for="r in rels" :key="String(r.id)">
            <td><input type="radio" name="rel" :value="r.id" v-model="selected" /></td>
            <td>{{ r.version }}</td>
            <td class="muted">{{ r.digest }}</td>
            <td>{{ r.trust_ok ? "проверен" : "нет" }}</td>
            <td>{{ r.imported_at }}</td>
          </tr>
        </tbody>
      </table>
      <p><button :disabled="!selected || pending" @click="rollout">Выкатить (канарейка по умолчанию)</button></p>
    </section>
  </div>
</template>
