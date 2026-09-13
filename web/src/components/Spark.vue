<script setup lang="ts">
import { computed } from "vue";

const props = defineProps<{ points: Array<{ t: string; v: number | null }>; label: string }>();

const path = computed(() => {
  const pts = props.points.filter((p) => p.v != null) as Array<{ t: string; v: number }>;
  if (!pts.length) return "";
  const vs = pts.map((p) => p.v);
  const min = Math.min(...vs);
  const max = Math.max(...vs);
  const span = max - min || 1;
  return pts
    .map((p, i) => {
      const x = (i / Math.max(pts.length - 1, 1)) * 300;
      const y = 80 - ((p.v - min) / span) * 70 - 5;
      return `${i === 0 ? "M" : "L"}${x.toFixed(1)},${y.toFixed(1)}`;
    })
    .join(" ");
});
const summary = computed(() => {
  const vs = props.points.map((p) => p.v).filter((v): v is number => v != null);
  if (!vs.length) return "нет точек";
  return `${props.label}: мин ${Math.min(...vs).toFixed(1)}, макс ${Math.max(...vs).toFixed(1)}, последняя ${vs[vs.length - 1].toFixed(1)}`;
});
</script>

<template>
  <figure>
    <svg class="chart" viewBox="0 0 300 80" role="img" :aria-label="summary">
      <path :d="path" fill="none" stroke="currentColor" stroke-width="1.6" />
    </svg>
    <figcaption class="muted">{{ summary }}</figcaption>
  </figure>
</template>
