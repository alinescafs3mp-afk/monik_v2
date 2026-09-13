<script setup lang="ts">
import { computed, ref } from "vue";
import { finite, number } from "../format";
const props = defineProps<{ points: Array<{ t: string; v: number | null; min?: number; max?: number }>; label: string; unit?: string; step?: number }>();
const emit = defineEmits<{ select: [string] }>();
const index = ref(0);
const points = computed(() => props.points.filter(p => Number.isFinite(Date.parse(p.t))));
const values = computed(() => points.value.flatMap(p => [p.v,p.min,p.max]).filter(finite));
const extent = computed(() => { const lo = Math.min(...values.value), hi = Math.max(...values.value); return { lo, hi, span: hi-lo || 1 }; });
const xmin = computed(() => Date.parse(points.value[0]?.t || ''));
const xmax = computed(() => Date.parse(points.value.at(-1)?.t || ''));
function x(t: string) { return 8 + (Date.parse(t)-xmin.value)/Math.max(xmax.value-xmin.value, 1)*484; }
function y(v: number) { return 110-(v-extent.value.lo)/extent.value.span*94; }
const path = computed(() => { let out='', previous=0, connected=false; for (const p of points.value) { const t=Date.parse(p.t); if (!finite(p.v)) { connected=false; continue; } const gap=t-previous>(props.step || 5)*3000; out+=`${connected&&!gap?'L':'M'}${x(p.t)},${y(p.v)} `; connected=true; previous=t; } return out; });
const selected = computed(() => points.value[Math.min(index.value,points.value.length-1)]);
function move(event: MouseEvent) { const rect=(event.currentTarget as SVGElement).getBoundingClientRect(); const at=xmin.value+(event.clientX-rect.left)/rect.width*(xmax.value-xmin.value); let best=0; points.value.forEach((p,i) => {if(Math.abs(Date.parse(p.t)-at)<Math.abs(Date.parse(points.value[best].t)-at))best=i;}); index.value=best; }
function key(e: KeyboardEvent) { if (e.key==='ArrowLeft'||e.key==='ArrowRight') { e.preventDefault(); index.value=Math.max(0,Math.min(points.value.length-1,index.value+(e.key==='ArrowRight'?1:-1))); } if(e.key==='Enter'&&selected.value)emit('select',selected.value.t); }
</script>
<template>
  <figure class="metric-chart">
    <figcaption><strong>{{ label }}</strong> <small v-if="values.length" class="muted">Мин {{ number(extent.lo, unit, 1) }} · макс {{ number(extent.hi, unit, 1) }}</small></figcaption>
    <p v-if="!values.length" class="muted chart-empty">Нет измерений в выбранном интервале</p>
    <svg v-else class="chart" viewBox="0 0 500 126" role="img" tabindex="0" :aria-label="`${label}. Стрелки выбирают точку, Enter открывает срез состояния.`" @mousemove="move" @keydown="key" @click="selected && emit('select',selected.t)">
      <line x1="8" y1="110" x2="492" y2="110" class="chart-axis"/>
      <template v-for="p in points" :key="p.t"><line v-if="finite(p.min)&&finite(p.max)" :x1="x(p.t)" :x2="x(p.t)" :y1="y(p.min!)" :y2="y(p.max!)" class="chart-range"/></template>
      <path :d="path" fill="none" stroke="currentColor" stroke-width="1.8"/>
      <circle v-for="p in points.filter(p=>finite(p.v))" :key="p.t" :cx="x(p.t)" :cy="y(p.v!)" r="1.2" fill="currentColor"/>
      <line v-if="selected" :x1="x(selected.t)" :x2="x(selected.t)" y1="5" y2="115" stroke="currentColor" stroke-dasharray="3 3"/>
    </svg>
    <div v-if="points.length" class="bar chart-times"><small>{{ new Date(points[0].t).toLocaleString() }}</small><small>{{ new Date(points.at(-1)!.t).toLocaleString() }}</small></div>
    <p v-if="selected" class="muted">{{ new Date(selected.t).toLocaleString() }} · {{ number(selected.v, unit, 2) }}<template v-if="finite(selected.min) && selected.min!==selected.max"> · диапазон {{ number(selected.min, unit, 2) }}…{{ number(selected.max, unit, 2) }}</template></p>
  </figure>
</template>
