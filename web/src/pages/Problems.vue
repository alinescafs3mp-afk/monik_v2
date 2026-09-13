<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, submitOp } from "../api";
import TimeBar from "../components/TimeBar.vue";
const emit = defineEmits<{ toast: [string, string?] }>();
const incs = ref<Array<Record<string, unknown>>>([]);
const mode = ref<"live" | "history">("live");
const hours = ref(6);
onMounted(async () => {
  const d = await get<{ incidents: Array<Record<string, unknown>> }>("/api/v1/incidents");
  incs.value = d.incidents || [];
});
async function ack(id: string) {
  const r = await submitOp("incident.acknowledge", { incident_id: id });
  emit("toast", "Инцидент подтверждён, это не восстановление", r.op ? `/operations/${r.op.operation_id}` : "");
}
</script>

<template>
  <div>
    <TimeBar v-model:mode="mode" v-model:hours="hours" />
    <p v-if="!incs.length" class="panel">Открытых инцидентов нет.</p>
    <table>
      <thead><tr><th>Когда</th><th>Объект</th><th>Метрика</th><th>Серьёзность</th><th>Причина</th><th></th></tr></thead>
      <tbody>
        <tr v-for="i in incs" :key="String(i.id)">
          <td>{{ i.opened_at }}</td>
          <td>{{ i.entity_type }} {{ i.entity_id }}</td>
          <td>{{ i.metric }}</td>
          <td>{{ i.severity }}</td>
          <td>{{ i.reason }}</td>
          <td><button @click="ack(String(i.id))">Подтвердить</button></td>
        </tr>
      </tbody>
    </table>
  </div>
</template>
