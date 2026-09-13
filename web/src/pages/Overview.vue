<script setup lang="ts">
import { computed, ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { bytes, number, stateLabel } from "../format";
import ServiceList from "../components/ServiceList.vue";
const emit = defineEmits<{ toast: [string, string?] }>();
const data = ref<any>(null), problemsOnly = ref(false), pinnedOnly = ref(false), pending = ref<string[]>([]);
const { loading, error, refreshing, updated, refresh } = usePolling(async () => { data.value = await get("/api/v1/overview"); });
const cards = computed(() => (data.value?.cards || []).filter((c: any) => (!problemsOnly.value || c.has_problem) && (!pinnedOnly.value || c.pinned)).sort((a: any,b: any) => Number(b.pinned)-Number(a.pinned) || a.name.localeCompare(b.name)));
function disk(c: any) { return [...(c.disks || [])].sort((a,b) => b.used_percent-a.used_percent)[0]; }
function breach(c: any, metric: string) { return c.metrics_fresh && (c.breaches || []).some((b: any) => b.metric === metric); }
async function pin(c: any) {
  if (pending.value.includes(c.id)) return; pending.value.push(c.id);
  try { const r = await submitOp("agent.pin", { agent_id: c.id, pinned: !c.pinned }); emit("toast", "Закрепление сохранено", `/operations/${r.op?.operation_id}`); await refresh(); }
  finally { pending.value = pending.value.filter(id => id !== c.id); }
}
</script>
<template>
  <div>
    <header class="bar"><div><h2>Состояние машин</h2><p class="muted">Ключевые параметры здесь. Графики и подробности открываются по машине.</p></div><button :disabled="refreshing" @click="refresh">{{ refreshing ? 'Обновляем…' : 'Обновить' }}</button></header>
    <div class="bar overview-counts"><span>На связи <b>{{ data?.agents_reporting ?? '…' }}/{{ data?.agents_total ?? '…' }}</b></span><span>Сервисов <b>{{ data?.services ?? '…' }}</b></span><router-link to="/problems">Инцидентов {{ data?.open_incidents ?? '…' }}</router-link><router-link to="/operations">Требуют внимания {{ data?.operations_attention ?? '…' }}</router-link></div>
    <div class="bar"><div class="row"><label><input v-model="problemsOnly" type="checkbox"/> Только проблемы</label><label><input v-model="pinnedOnly" type="checkbox"/> Только закреплённые</label></div><small class="muted">{{ updated ? `Данные получены ${updated.toLocaleTimeString()}` : 'Ожидаем данные сервера' }}</small></div>
    <p v-if="error" role="alert" class="panel err">{{ error }}. Показанные ранее данные могут быть устаревшими.</p>
    <div v-if="loading" class="skeleton" aria-label="Загрузка машин"/>
    <p v-else-if="data && !data.agents_total" class="panel">Машин пока нет. <router-link to="/add">Подключить первую</router-link></p>
    <p v-else-if="data && !cards.length" class="panel">Нет машин, соответствующих фильтрам.</p>
    <div class="cards host-cards">
      <article v-for="c in cards" :key="c.id" class="card host-card" :class="{ 'has-problem': c.has_problem }">
        <router-link class="card-main" :to="`/machines/${encodeURIComponent(c.id)}`">
          <header class="bar"><div><h3>{{ c.name }}</h3><small class="muted">{{ c.os }} / {{ c.arch }}</small></div><span class="badge"><span class="dot" :class="c.state"/>{{ stateLabel(c.state) }}</span></header>
          <dl class="metric-grid">
            <div :class="{ 'metric-problem': breach(c,'cpu') }"><dt>CPU</dt><dd>{{ number(c.cpu, '%', 1) }}</dd></div>
            <div :class="{ 'metric-problem': breach(c,'ram') }"><dt>RAM</dt><dd>{{ c.ram_total > 0 ? number(c.ram_used / c.ram_total * 100, '%', 1) : 'Нет данных' }}</dd><small>{{ bytes(c.ram_used) }} / {{ bytes(c.ram_total) }}</small></div>
            <div :class="{ 'metric-problem': breach(c,'disk') }"><dt>DISK <small>{{ disk(c)?.mount }}</small></dt><dd>{{ number(disk(c)?.used_percent, '%', 1) }}</dd><small>{{ bytes(disk(c)?.used_bytes) }} / {{ bytes(disk(c)?.total_bytes) }}</small><small v-if="c.disks?.length > 1">Самый заполненный из {{ c.disks.length }}</small></div>
            <div><dt>Средний ping</dt><dd>{{ number(c.ping?.mean_ms, ' мс', 1) }}</dd><small>{{ c.ping?.target || '8.8.8.8' }} · потери {{ number(c.ping?.loss_percent, '%') }}</small></div>
          </dl>
          <p v-if="!c.metrics_fresh" class="data-warning">Нет свежих измерений. Последние значения не означают нормальное состояние.</p>
          <p v-else class="muted">Измерено {{ number(c.age_seconds, ' с назад') }} · ping за {{ c.ping?.window_seconds || 60 }} с</p>
          <p v-for="b in (c.metrics_fresh ? c.breaches || [] : [])" :key="b.metric" class="err">{{ b.metric.toUpperCase() }}: {{ number(b.value, '%', 1) }} · {{ b.severity === 'critical' ? 'критическое превышение' : 'превышение порога' }}</p>
          <span class="detail-link">Открыть графики и параметры →</span>
        </router-link>
        <h4>Веб-сервисы <span class="muted">{{ c.services?.length || 0 }}</span></h4>
        <ServiceList :services="c.services || []" :limit="4"/>
        <footer><button :disabled="pending.includes(c.id)" @click="pin(c)">{{ pending.includes(c.id) ? 'Сохраняем…' : c.pinned ? 'Открепить' : 'Закрепить' }}</button></footer>
      </article>
    </div>
  </div>
</template>
