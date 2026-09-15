<script setup lang="ts">
import { columnLabels } from '../overviewColumns';
import type { OverviewColumns } from '../composables/useOverviewColumns';
defineProps<{ layout: OverviewColumns; keyboard?: boolean }>();
</script>
<template>
  <div v-if="layout.enabled" class="column-dividers" :class="{'is-resizing':layout.resizing}">
    <span v-for="(left,i) in layout.positions" :key="i" class="column-divider" role="separator"
      :tabindex="keyboard ? 0 : -1" aria-orientation="vertical" :aria-label="`Ширина ${columnLabels[i]}`"
      :aria-valuemin="Math.round(layout.minimum[i])" :aria-valuemax="Math.round(layout.widths![i]+layout.widths![i+1]-layout.minimum[i+1])"
      :aria-valuenow="Math.round(layout.widths![i])" :aria-valuetext="`${Math.round(layout.widths![i])} пикселей`"
      :title="`${columnLabels[i]}: тяните границу или используйте ← →. Escape отменяет перетаскивание.`"
      :style="{left:`${left}px`}" @pointerdown="layout.start($event,i)" @pointermove="layout.move"
      @pointerup="layout.finish($event)" @pointercancel="layout.finish($event,true)" @lostpointercapture="layout.finish($event,true)"
      @keydown="layout.key($event,i)" @click.stop.prevent />
  </div>
</template>
