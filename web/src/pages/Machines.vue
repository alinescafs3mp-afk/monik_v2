<script setup lang="ts">
import { computed, onMounted, ref } from "vue";
import { useRoute } from "vue-router";
import { get } from "../api";

const route = useRoute();
const rows = ref<Array<Record<string, unknown>>>([]);
const err = ref("");
onMounted(async () => {
  try {
    const d = await get<{ agents: Array<Record<string, unknown>> }>("/api/v1/agents");
    rows.value = d.agents || [];
  } catch (e) {
    err.value = (e as { message?: string }).message || "ошибка";
  }
});
const filtered = computed(() => {
  const q = String(route.query.q || "").toLowerCase();
  if (!q) return rows.value;
  return rows.value.filter((r) => JSON.stringify(r).toLowerCase().includes(q));
});
</script>

<template>
  <div>
    <p v-if="err" class="err">{{ err }}</p>
    <p v-if="!rows.length" class="panel">Нет машин. <router-link to="/add">Добавить машину</router-link></p>
    <div class="table-wrap">
      <table>
        <thead>
          <tr>
            <th>Имя</th><th>ОС</th><th>Состояние</th><th>Версия</th><th>Управляемый</th><th>Последний live</th>
          </tr>
        </thead>
        <tbody>
          <tr v-for="r in filtered" :key="String(r.id)">
            <td><router-link :to="'/machines/' + r.id">{{ r.display_name }}</router-link></td>
            <td>{{ r.os }}/{{ r.arch }}</td>
            <td><span class="dot" :class="String(r.state)" /> {{ r.state }} <span class="muted">{{ r.reason }}</span></td>
            <td>{{ r.worker_version }}</td>
            <td>{{ r.managed_ready ? "да" : "нет" }}</td>
            <td>{{ r.last_live_at || "—" }}</td>
          </tr>
        </tbody>
      </table>
    </div>
  </div>
</template>
