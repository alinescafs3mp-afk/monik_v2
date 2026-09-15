<script setup lang="ts">
import { computed, onUnmounted, ref } from "vue";
import {inventoryRows,inventoryNote} from "../inventory";
import ServiceNameEditor from "./ServiceNameEditor.vue";
import ServiceMonitorToggle from "./ServiceMonitorToggle.vue";
import ServiceOverviewToggle from "./ServiceOverviewToggle.vue";
import {serviceFresh} from "../display";
import { stateLabel } from "../format";
import {serviceHint} from "../checkDraft";
import { orderedServices } from "../presentation";
const emit = defineEmits<{ configure: [string] }>();
const props = defineProps<{ services: any[]; limit?: number; compact?: boolean; forceStale?: boolean; configureInPlace?: boolean; locked?: boolean }>();
const showInactive=ref(false);
const activeServices=computed(()=>inventoryRows(props.services||[],showInactive.value));
const inactiveCount=computed(()=>(props.services||[]).filter(s=>s.inventory_archived).length);
const now=ref(Date.now()); const tick=window.setInterval(()=>now.value=Date.now(),2000); onUnmounted(()=>clearInterval(tick));
function stale(s:any){return !!s.fresh && (!!props.forceStale || !serviceFresh(s,now.value));}
const visible = computed(() => { const sorted = orderedServices(activeServices.value); return props.limit ? sorted.slice(0, props.limit) : sorted; });
</script>
<template>
  <div class="service-list" :class="{'service-list-compact': compact}">
    <label v-if="!compact && inactiveCount" class="inventory-choice"><input v-model="showInactive" type="checkbox"/> Показывать исчезнувшие ({{inactiveCount}})</label>
    <p v-if="!activeServices.length" class="muted">Актуальные сервисы не обнаружены. История и настройки исчезнувших сохранены.</p>
    <div v-for="s in visible" :key="s.id" class="service-row">
      <div class="row"><span class="dot" :class="stale(s) ? 'stale' : s.state"/><strong class="truncate" :title="s.url">{{ s.display_name || s.url }}</strong><span v-if="!compact" class="muted">{{ stateLabel(stale(s) ? 'stale' : s.state) }}</span></div>
      <p class="service-outcome" :title="s.summary" :class="{ err: !stale(s) && ['app_fail','http_error','transport_fail'].includes(s.state) }">{{ s.summary }} <small v-if="s.maintenance_active"> · Обслуживание</small><span v-if="stale(s) && s.fresh"> · устарело</span></p>
      <small v-if="!compact" class="inventory-note muted">{{inventoryNote(s)}}</small><p v-if="!compact && serviceHint(s)" class="muted">{{serviceHint(s)}}</p><template v-if="!compact"><ServiceNameEditor v-if="!locked" :id="String(s.id)" :name="String(s.display_name||s.url)" :expected-name="String(s.display_name??'')"/><div class="service-choices"><ServiceMonitorToggle :service="s" :locked="locked"/><ServiceOverviewToggle :service="s"/></div><button v-if="configureInPlace" type="button" @click="emit('configure',String(s.id))">Настроить запрос</button><router-link v-else :to="{path:`/machines/${encodeURIComponent(s.agent_id)}`,query:{service:s.id},hash:'#check-editor'}">Настроить запрос</router-link></template>
      <small v-if="!compact" class="muted">{{ s.observation?.vantage || 'agent/local' }}<template v-if="s.observation?.observed_at"> · {{ new Date(s.observation.observed_at).toLocaleString() }}</template></small>
      <details v-if="!compact && s.observation?.feedback" class="response-feedback"><summary>{{ s.state==='pending' ? 'Предыдущий ответ сервиса' : 'Что ответил сервис' }}</summary><p v-if="s.state==='pending'" class="muted">Ожидаем результат актуальной конфигурации. Ревизия показанного измерения: {{ s.observation.config_revision ?? 'неизвестна' }}.</p><dl><dt>Метод</dt><dd>{{ s.observation.feedback.method }}</dd><dt>HTTP</dt><dd>{{ s.observation.http_status }} {{ s.observation.feedback.status_text }}</dd><dt>Тип ответа</dt><dd>{{ s.observation.feedback.content_type || 'Не указан' }}</dd><dt>Тело ответа</dt><dd>{{ s.observation.feedback.body_state }} · прочитано {{ s.observation.feedback.sampled_bytes || 0 }} байт</dd></dl><p v-if="s.observation.feedback.health">{{ s.observation.feedback.health }} <small class="muted">(сервис сообщил; не независимая проверка)</small></p><p class="muted">Полное тело, заголовки авторизации и произвольные поля не сохраняются.</p></details>
    </div>
    <p v-if="limit && activeServices.length > limit" class="muted service-more">Ещё {{ activeServices.length - limit }}</p>
  </div>
</template>
