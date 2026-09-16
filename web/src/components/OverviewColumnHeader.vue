<script setup lang="ts">
import { columnLabels } from '../overviewColumns';
import type { OverviewColumns } from '../composables/useOverviewColumns';
import ColumnDividers from './ColumnDividers.vue';
// Vue treats omitted Boolean props as false; keep ordinary/TV editor handles on unless explicitly disabled.
withDefaults(defineProps<{layout:OverviewColumns; tv?:boolean; interactive?:boolean}>(), {interactive: true});
</script>
<template>
  <div v-if="layout.enabled" class="overview-column-header" :class="{'tv-columns':tv}" :style="layout.style" aria-label="Ширина колонок обзора">
    <span v-for="label in columnLabels" :key="label">{{label}}</span>
    <span v-if="tv" class="sr">Действия</span><ColumnDividers v-if="interactive" :layout="layout" keyboard/>
  </div>
</template>
