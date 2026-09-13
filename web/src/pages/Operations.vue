<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { usePolling } from "../composables/usePolling";
import { get, pendingRequests, reconcileOperation } from "../api";
const tab = ref("all");
const pending = ref(pendingRequests());
async function reconcile(key:string){try{await reconcileOperation(key);pending.value=pendingRequests();await refresh();}catch(e){loadError.value=(e as Error).message;}}
const rows = ref<Array<Record<string, unknown>>>([]);
const { loading, error: loadError, refresh } = usePolling(async () => {
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
    <p v-if="loadError" class="panel err" role="alert">{{ loadError }} <button @click="refresh">Повторить чтение</button></p>
    <p v-if="loading" role="status">Загружаем данные…</p>
    <section v-if="pending.length" class="panel data-warning"><h3>Запросы с неизвестным результатом</h3><p v-for="p in pending" :key="p.key">{{ p.action }} · {{ p.created }} <button @click="reconcile(p.key)">Проверить исходный запрос</button><small>{{ p.key }}</small></p><p>Это чтение результата, не повторное выполнение. Отсутствие ответа не означает отмену.</p></section>
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
