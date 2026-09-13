<script setup lang="ts">
import { onMounted, ref } from "vue";
import { usePolling } from "../composables/usePolling";
import { get, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const rows = ref<Array<Record<string, unknown>>>([]);
const { loading, error: loadError, refresh } = usePolling(async () => {
  const d = await get<{ services: Array<Record<string, unknown>> }>("/api/v1/services");
  rows.value = d.services || [];
});
async function pin(id: string, pinned: boolean) {
  const r = await submitOp("service.pin", { service_id: id, pinned: !pinned });
  emit("toast", "Закрепление сохранено", r.op ? `/operations/${r.op.operation_id}` : "");
 await refresh();
}
</script>

<template>
  <div>
    <p v-if="loadError" class="panel err" role="alert">{{ loadError }} <button @click="refresh">Повторить чтение</button></p>
    <p v-if="loading" role="status">Загружаем данные…</p>
    <p v-if="!rows.length" class="panel">Сервисы не обнаружены. Запустите агент и «Обнаружить сейчас».</p>
    <div class="table-wrap">
      <table>
        <thead><tr><th>Сервис</th><th>Агент</th><th>URL</th><th>Результат запроса</th><th>Состояние агента</th><th></th></tr></thead>
        <tbody>
          <tr v-for="s in rows" :key="String(s.id)">
            <td>{{ s.display_name || s.url }}</td>
            <td><router-link :to="'/machines/' + s.agent_id">{{ s.agent_id }}</router-link></td>
            <td>{{ s.url }}</td>
            <td><span class="dot" :class="String(s.state)"/> {{ s.summary }}</td><td>{{ s.agent_state }}</td>
            <td><button @click="pin(String(s.id), !!s.pinned)">{{ s.pinned ? "Открепить" : "Закрепить" }}</button></td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
