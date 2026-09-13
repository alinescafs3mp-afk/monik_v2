<script setup lang="ts">
import { onMounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { get, submitOp } from "../api";
import TimeBar from "../components/TimeBar.vue";
import Spark from "../components/Spark.vue";

const route = useRoute();
const emit = defineEmits<{ toast: [string, string?] }>();
const tab = ref("summary");
const detail = ref<Record<string, unknown> | null>(null);
const mode = ref<"live" | "history">("live");
const hours = ref(1);
const series = ref<Array<{ t: string; v: number | null }>>([]);
const pending = ref(false);

async function load() {
  detail.value = await get("/api/v1/agents/" + route.params.id);
  const to = new Date();
  const from = new Date(to.getTime() - hours.value * 3600 * 1000);
  const s = await get<{ host: Array<Record<string, unknown>> }>(
    `/api/v1/history/series?agent_id=${route.params.id}&from=${from.toISOString()}&to=${to.toISOString()}`
  );
  series.value = (s.host || []).map((p) => ({ t: String(p.observed_at), v: (p.cpu_pct as number) ?? null }));
}
onMounted(load);
watch([hours, () => route.params.id], load);

async function collect() {
  pending.value = true;
  try {
    const r = await submitOp("agent.collect_now", {}, [String(route.params.id)]);
    emit("toast", r.unknown ? "Результат ещё неизвестен; дубликат не отправлен" : "Задание создано", r.op ? `/operations/${r.op.operation_id}` : "");
  } finally {
    pending.value = false;
  }
}
async function discover() {
  pending.value = true;
  try {
    const r = await submitOp("agent.discover_now", {}, [String(route.params.id)]);
    emit("toast", "Обнаружение поставлено в очередь", r.op ? `/operations/${r.op.operation_id}` : "");
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <div v-if="detail">
    <header class="bar">
      <div>
        <h2>{{ (detail.agent as Record<string, unknown>)?.display_name || (detail.agent as Record<string, unknown>)?.hostname }}</h2>
        <p class="muted">{{ (detail.agent as Record<string, unknown>)?.id }} · {{ detail.state }} · {{ detail.reason }}</p>
      </div>
      <div class="row">
        <button :disabled="pending" @click="collect">Собрать сейчас</button>
        <button :disabled="pending" @click="discover">Обнаружить сейчас</button>
      </div>
    </header>
    <div class="row">
      <button v-for="t in ['summary','metrics','services','agent','config']" :key="t" :aria-pressed="tab===t" @click="tab=t">{{ t }}</button>
    </div>
    <TimeBar v-model:mode="mode" v-model:hours="hours" />
    <section v-if="tab==='summary' || tab==='metrics'" class="panel">
      <Spark :points="series" label="CPU %" />
      <pre class="muted" style="white-space:pre-wrap">{{ JSON.stringify(detail.host, null, 2) }}</pre>
    </section>
    <section v-if="tab==='services'" class="panel">
      <p v-if="!(detail.services as unknown[])?.length">Сервисы пока не обнаружены.</p>
      <ul>
        <li v-for="s in (detail.services as Array<Record<string, unknown>> || [])" :key="String(s.id)">
          {{ s.display_name || s.url }} · {{ s.dial_target }}
        </li>
      </ul>
    </section>
    <section v-if="tab==='agent'" class="panel">
      <p>Версия работника: {{ (detail.agent as Record<string, unknown>)?.worker_version }}</p>
      <p>Желаемая ревизия: {{ (detail.agent as Record<string, unknown>)?.desired_revision }} / применена {{ (detail.agent as Record<string, unknown>)?.applied_revision }}</p>
      <p>Управляемый: {{ (detail.agent as Record<string, unknown>)?.managed_ready ? "да" : "нет" }}</p>
    </section>
    <section v-if="tab==='config'" class="panel">
      <p class="muted">Изменение профиля создаёт желаемую ревизию; применение доказывается совпадением hash на агенте.</p>
    </section>
  </div>
</template>
