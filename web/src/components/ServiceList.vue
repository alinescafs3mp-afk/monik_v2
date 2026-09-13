<script setup lang="ts">
import { stateLabel } from "../format";
defineProps<{ services: any[]; limit?: number }>();
</script>
<template>
  <div class="service-list">
    <p v-if="!services?.length" class="muted">Веб-сервисы пока не обнаружены.</p>
    <div v-for="s in (limit ? services.slice(0, limit) : services)" :key="s.id" class="service-row">
      <div class="row"><span class="dot" :class="s.state"/><strong class="truncate" :title="s.url">{{ s.display_name || s.url }}</strong><span class="muted">{{ stateLabel(s.state) }}</span></div>
      <p class="service-outcome" :class="{ err: ['app_fail','http_error','transport_fail'].includes(s.state) }">{{ s.summary }}</p>
      <small class="muted">{{ s.observation?.vantage || 'agent/local' }}<template v-if="s.observation?.observed_at"> · {{ new Date(s.observation.observed_at).toLocaleString() }}</template></small>
    </div>
    <p v-if="limit && services?.length > limit" class="muted">Ещё {{ services.length - limit }}: полный список в карточке машины.</p>
  </div>
</template>
