<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { bytes, number, stateLabel } from "../format";
import { worstDisk, readPreference, savePreference, matchesMachine } from "../presentation";
import ServiceList from "../components/ServiceList.vue";
const emit = defineEmits<{ toast: [string, string?] }>();
const data = ref<any>(null), problemsOnly = ref(false), pending = ref<string[]>([]), query = ref("");
const layout = ref(readPreference('monik:overview-layout', 'rows') === 'cards' ? 'cards' : 'rows');
const now = ref(Date.now()); let fetchedAt = Date.now();
const timer = window.setInterval(() => { now.value = Date.now(); }, 2000); onUnmounted(() => clearInterval(timer));
const { loading, error, refreshing, updated, refresh } = usePolling(async () => { data.value = await get("/api/v1/overview"); fetchedAt = Date.now(); now.value = fetchedAt; });
function fresh(c: any) { return c.metrics_fresh && (c.age_seconds || 0) + Math.max(0, now.value-fetchedAt)/1000 <= 15; }
const cards = computed(() => (data.value?.cards || []).filter((c: any) => (!problemsOnly.value || c.has_problem || !fresh(c)) && c.pinned && matchesMachine(c, query.value)).sort((a: any,b: any) => Number(b.pinned)-Number(a.pinned) || a.name.localeCompare(b.name)));
function breach(c: any, metric: string) { return fresh(c) && (c.breaches || []).some((b: any) => b.metric === metric); }
function chooseLayout(value: string) { layout.value = value; if (!savePreference('monik:overview-layout', value)) emit('toast', 'Вид изменён. Браузер не разрешил сохранить выбор.'); }
async function pin(c: any) {
  if (pending.value.includes(c.id)) return; pending.value.push(c.id);
  try { const r = await submitOp("agent.pin", { agent_id: c.id, pinned: !c.pinned }); emit("toast", "Выбор для обзора сохранён", `/operations/${r.op?.operation_id}`); await refresh(); }
  catch { /* submitOp publishes an actionable global error. */ }
  finally { pending.value = pending.value.filter(id => id !== c.id); }
}
</script>
<template>
  <div class="overview">
    <header class="bar"><div><h2>Состояние машин</h2></div><button :disabled="refreshing" @click="refresh">{{ refreshing ? 'Обновляем…' : 'Обновить' }}</button></header>
    <div class="bar overview-counts"><span>На связи <b>{{ data?.agents_reporting ?? '…' }}/{{ data?.agents_total ?? '…' }}</b></span><span>Сервисов <b>{{ data?.services ?? '…' }}</b></span><router-link to="/problems" title="Открытые непрочитанные инциденты; прочитанные остаются в истории">Инцидентов {{ data?.unread_incidents ?? '…' }}</router-link><router-link to="/operations">Требуют внимания {{ data?.operations_attention ?? '…' }}</router-link></div>
    <div class="bar overview-tools"><div class="row"><input v-model="query" type="search" placeholder="Машина, сервис или HTTP-код" aria-label="Поиск в обзоре"/><label><input v-model="problemsOnly" type="checkbox"/> Только проблемы</label><router-link to="/machines">Выбрать машины для обзора</router-link></div><div class="row" role="group" aria-label="Вид обзора"><button :aria-pressed="layout==='rows'" @click="chooseLayout('rows')">Строки</button><button :aria-pressed="layout==='cards'" @click="chooseLayout('cards')">Карточки</button></div></div>
    <p v-if="error" role="alert" class="panel err">{{ error }}. Показанные ранее данные могут быть устаревшими.</p>
    <div v-if="loading" class="skeleton" aria-label="Загрузка машин"/>
    <p v-else-if="data && !data.agents_total" class="panel">Машин пока нет. <router-link to="/add">Подключить первую</router-link></p>
    <p v-else-if="data && !cards.length" class="panel">Нет выбранных машин, соответствующих фильтрам. <router-link to="/machines">Отметьте «Показывать в обзоре» во вкладке «Машины»</router-link>.</p>
    <div :class="layout==='rows' ? 'host-rows' : 'cards host-cards'">
      <article v-for="c in cards" :key="c.id" class="card host-card" :class="{ 'host-line':layout==='rows', 'has-problem': c.has_problem, 'measurements-stale':!fresh(c) }">
        <div class="host-ident">
          <router-link class="card-main" :to="`/machines/${encodeURIComponent(c.id)}`"><h3 class="truncate" :title="c.name">{{ c.name }}</h3><small class="muted">{{ c.os }} / {{ c.arch }}</small></router-link>
          <span class="badge"><span class="dot" :class="fresh(c) ? c.state : (c.state==='ok' ? 'stale' : c.state)"/>{{ stateLabel(c.state==='ok' && !fresh(c) ? 'stale' : c.state) }}</span>
          <small v-if="!fresh(c)" class="data-warning">Последние значения, не текущее состояние</small>
        </div>
        <dl class="metric-grid">
          <div :class="{ 'metric-problem': breach(c,'cpu') }"><dt>CPU</dt><dd>{{ number(c.cpu, '%', 1) }}</dd></div>
          <div :class="{ 'metric-problem': breach(c,'ram') }"><dt>RAM</dt><dd>{{ c.ram_total > 0 ? number(c.ram_used / c.ram_total * 100, '%', 1) : 'Нет данных' }}</dd><small>{{ bytes(c.ram_used) }} / {{ bytes(c.ram_total) }}</small></div>
          <div :class="{ 'metric-problem': breach(c,'disk') }"><dt>DISK <small class="disk-mount" :title="worstDisk(c.disks)?.mount">{{ worstDisk(c.disks)?.mount }}</small></dt><dd>{{ number(worstDisk(c.disks)?.used_percent, '%', 1) }}</dd><small>{{ bytes(worstDisk(c.disks)?.used_bytes) }} / {{ bytes(worstDisk(c.disks)?.total_bytes) }}</small><small v-if="c.disks?.length > 1">Самый заполненный из {{ c.disks.length }}</small></div>
          <div><dt>Средний ping</dt><dd>{{ number(c.ping?.mean_ms, ' мс', 1) }}</dd><small>Потери {{ number(c.ping?.loss_percent, '%') }} · {{ c.ping?.window_seconds || 60 }} с</small></div>
        </dl>
        <div class="host-service-cell"><router-link :to="`/machines/${encodeURIComponent(c.id)}#services`" class="service-heading">Сервисы · {{ c.services?.length || 0 }}</router-link><ServiceList :services="c.services || []" :limit="layout==='rows' ? 2 : 4" :compact="layout==='rows'" :force-stale="!fresh(c)"/><router-link v-if="c.services?.length > (layout==='rows'?2:4)" :to="`/machines/${encodeURIComponent(c.id)}#services`">Все сервисы →</router-link></div>
        <div class="host-line-actions"><button :disabled="pending.includes(c.id)" :aria-label="c.pinned ? 'Убрать из обзора' : 'Показывать в обзоре'" :title="c.pinned ? 'Убрать из обзора' : 'Показывать в обзоре'" @click="pin(c)"><span aria-hidden="true">{{ pending.includes(c.id) ? '…' : c.pinned ? '★' : '☆' }}</span></button><router-link :to="`/machines/${encodeURIComponent(c.id)}`" :aria-label="`Открыть ${c.name}`" title="Графики и параметры">↗</router-link></div>
      </article>
    </div>
    <p class="muted"><small>{{ cards.length }} машин показано · {{ updated ? `Данные получены ${updated.toLocaleTimeString()}` : 'Ожидаем данные сервера' }}. Ошибки сервисов показаны первыми.</small></p>
  </div>
</template>
