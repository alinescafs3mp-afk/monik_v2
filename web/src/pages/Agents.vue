<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, submitOp } from "../api";
const emit = defineEmits<{ toast: [string, string?] }>();
const rows = ref<Array<Record<string, unknown>>>([]);
const selected = ref<string[]>([]);
const pending = ref(false);
onMounted(async () => {
  const d = await get<{ agents: Array<Record<string, unknown>> }>("/api/v1/agents");
  rows.value = d.agents || [];
});
function toggle(id: string) {
  selected.value = selected.value.includes(id) ? selected.value.filter((x) => x !== id) : [...selected.value, id];
}
async function restart() {
  pending.value = true;
  try {
    const r = await submitOp("agent.restart", {}, selected.value);
    emit("toast", r.unknown ? "Результат ещё неизвестен" : "Перезапуск поставлен в очередь", r.op ? `/operations/${r.op.operation_id}` : "");
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <div>
    <div class="bar">
      <span>Выбрано: {{ selected.length }}</span>
      <button :disabled="!selected.length || pending" @click="restart">Перезапустить выбранные</button>
      <router-link to="/add">Добавить машину</router-link>
    </div>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th></th><th>Имя</th><th>ОС</th><th>Worker</th><th>Host</th><th>Связь</th><th>Желаемая/применённая</th><th>Управляемый</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in rows" :key="String(r.id)">
            <td><input type="checkbox" :checked="selected.includes(String(r.id))" @change="toggle(String(r.id))" /></td>
            <td><router-link :to="'/machines/' + r.id">{{ r.display_name }}</router-link></td>
            <td>{{ r.os }}/{{ r.arch }}</td>
            <td>{{ r.worker_version }}</td>
            <td>{{ r.service_host_version || "—" }}</td>
            <td><span class="dot" :class="String(r.state)" /> {{ r.state }}</td>
            <td>{{ r.desired_revision }}/{{ r.applied_revision }}</td>
            <td>{{ r.managed_ready ? "да" : "нет" }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
