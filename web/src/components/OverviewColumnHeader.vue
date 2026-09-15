<script setup lang="ts">
import { columnLabels } from '../overviewColumns';
import type { OverviewColumns } from '../composables/useOverviewColumns';
import ColumnDividers from './ColumnDividers.vue';
defineProps<{layout:OverviewColumns; tv?:boolean}>();
</script>
<template>
  <div v-if="layout.enabled" class="overview-column-header" :class="{'tv-columns':tv}" :style="layout.style" aria-label="Ширина колонок обзора">
    <span v-for="label in columnLabels" :key="label">{{label}}</span>
    <ColumnDividers :layout="layout" keyboard/>
  </div>
</template>
