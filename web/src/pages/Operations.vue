<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { get } from "../api";
const tab = ref("running");
const rows = ref<Array<Record<string, unknown>>>([]);
onMounted(async () => {
  const d = await get<{ operations: Array<Record<string, unknown>> }>("/api/v1/operations");
  rows.value = d.operations || [];
});
const filtered = computed(() => {
  if (tab.value === "all") return rows.value;
  if (tab.value === "running") return rows.value.filter((o) => o.status === "queued" || o.status === "running");
  if (tab.value === "attention") return rows.value.filter((o) => o.status === "attention_required" || o.status === "completed_with_errors");
  return rows.value.filter((o) => o.status === "completed" || o.status === "cancelled");
});
</script>

<template>
  <div>
    <div class="row">
      <button :aria-pressed="tab==='running'" @click="tab='running'">Выполняются</button>
      <button :aria-pressed="tab==='attention'" @click="tab='attention'">Требуют внимания</button>
      <button :aria-pressed="tab==='completed'" @click="tab='completed'">Завершённые</button>
      <button :aria-pressed="tab==='all'" @click="tab='all'">Все</button>
    </div>
    <p v-if="!filtered.length" class="panel">Нет операций в этом фильтре.</p>
    <table>
      <thead><tr><th>Создана</th><th>Действие</th><th>Актор</th><th>Статус</th><th>Целей</th></tr></thead>
      <tbody>
        <tr v-for="o in filtered" :key="String(o.operation_id)">
          <td>{{ o.created_at }}</td>
          <td><router-link :to="'/operations/' + o.operation_id">{{ o.action }}</router-link></td>
          <td>{{ o.actor }}</td>
          <td>{{ o.status }}</td>
          <td>{{ (o.targets as unknown[] | undefined)?.length || 0 }}</td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
