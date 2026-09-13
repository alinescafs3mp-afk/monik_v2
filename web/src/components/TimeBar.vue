<script setup lang="ts">
const props = defineProps<{ mode: "live" | "history"; hours: number }>();
const emit = defineEmits<{ "update:mode": [v: "live" | "history"]; "update:hours": [n: number] }>();
const hours = [1, 2, 3, 6, 12, 24];
</script>

<template>
  <div class="timebar row" role="toolbar" aria-label="Интервал истории">
    <button type="button" :aria-pressed="props.mode === 'live'" @click="emit('update:mode', 'live')">LIVE</button>
    <button type="button" :aria-pressed="props.mode === 'history'" @click="emit('update:mode', 'history')">HISTORY</button>
    <button
      v-for="h in hours"
      :key="h"
      type="button"
      :aria-pressed="props.hours === h"
      @click="emit('update:hours', h)"
    >
      {{ h }}ч
    </button>
  </div>
</template>
