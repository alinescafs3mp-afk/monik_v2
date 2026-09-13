<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { get, submitOp } from "../api";
const route = useRoute();
const emit = defineEmits<{ toast: [string, string?] }>();
const op = ref<Record<string, unknown> | null>(null);
onMounted(async () => {
  op.value = await get("/api/v1/operations/" + route.params.id);
});
async function cancel() {
  const r = await submitOp("operation.cancel_pending", { operation_id: String(route.params.id) });
  emit("toast", "Отмена незапущенных целей записана", r.op ? `/operations/${r.op.operation_id}` : "");
  op.value = await get("/api/v1/operations/" + route.params.id);
}
</script>

<template>
  <div v-if="op" class="panel">
    <h2>{{ op.action }}</h2>
    <p>{{ op.status }} · {{ op.actor }} · {{ op.created_at }}</p>
    <p class="muted">{{ op.summary }}</p>
    <button @click="cancel">Отменить незапущенные</button>
    <table>
      <thead><tr><th>Цель</th><th>Статус</th><th>Стадия</th><th>Сообщение</th></tr></thead>
      <tbody>
        <tr v-for="t in (op.targets as Array<Record<string, unknown>> || [])" :key="String(t.agent_id)">
          <td>{{ t.agent_id }}</td>
          <td>{{ t.status }}</td>
          <td>{{ t.stage }}</td>
          <td>{{ t.message }}</td>
        </tr>
      </tbody>
    </table>
    <p class="muted">Успех сервера ≠ чек агента ≠ проверка применения. Ожидание подтверждения не считается успехом.</p>
  </div>
</template>
