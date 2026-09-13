<script setup lang="ts">
import { onMounted, ref } from "vue";
import { get, submitOp } from "../api";

const emit = defineEmits<{ toast: [string, string?] }>();
const data = ref<Record<string, unknown> | null>(null);
const err = ref("");
const problemsOnly = ref(false);

async function load() {
  err.value = "";
  try {
    data.value = await get("/api/v1/overview");
  } catch (e) {
    err.value = (e as { message?: string }).message || "ошибка";
  }
}
onMounted(load);

function cards() {
  const list = ((data.value?.cards as Array<Record<string, unknown>>) || []).slice();
  if (problemsOnly.value) return list.filter((c) => c.state !== "ok");
  return list;
}
function fmtBytes(n: unknown) {
  const v = Number(n || 0);
  if (v > 1 << 30) return (v / (1 << 30)).toFixed(1) + " GiB";
  if (v > 1 << 20) return (v / (1 << 20)).toFixed(0) + " MiB";
  return v + " B";
}
async function pin(id: string, pinned: boolean) {
  const r = await submitOp("agent.pin", { agent_id: id, pinned: !pinned });
  emit("toast", "Предпочтение сохранено на сервере", r.op ? `/operations/${r.op.operation_id}` : "");
  await load();
}
</script>

<template>
  <div>
    <div class="bar">
      <div class="row">
        <span>Агенты: {{ data?.agents_reporting }}/{{ data?.agents_total }}</span>
        <span>Сервисы: {{ data?.services }}</span>
        <span>Инциденты: {{ data?.open_incidents }}</span>
        <span>Операции: {{ data?.operations_attention }}</span>
      </div>
      <label><input v-model="problemsOnly" type="checkbox" /> Только проблемы</label>
    </div>
    <p v-if="err" class="err">{{ err }}</p>
    <p v-if="data && !(data.agents_total as number)" class="panel">
      Установка пуста. <router-link to="/add">Добавить машину</router-link>
    </p>
    <div class="cards">
      <article v-for="c in cards()" :key="String(c.id)" class="card">
        <header class="row">
          <span class="dot" :class="String(c.state)" />
          <router-link :to="'/machines/' + c.id"><strong>{{ c.name }}</strong></router-link>
          <span class="muted">{{ c.os }}/{{ c.arch }}</span>
        </header>
        <p v-if="c.cpu != null">CPU {{ Number(c.cpu).toFixed(0) }}%</p>
        <p v-if="c.ram_total">RAM {{ fmtBytes(c.ram_used) }} / {{ fmtBytes(c.ram_total) }}</p>
        <p class="muted">{{ c.reason || "без замечаний" }} · {{ c.age_seconds != null ? Number(c.age_seconds).toFixed(0) + " с назад" : "нет выборки" }}</p>
        <button type="button" @click="pin(String(c.id), !!c.pinned)">{{ c.pinned ? "Открепить" : "Закрепить" }}</button>
      </article>
    </div>
  </div>
</template>
