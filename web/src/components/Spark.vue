<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from 'vue';
import { finite, number } from '../format';
import { numericAxis, timeAxis, axisNumber, nearestPoint, type ChartPoint } from '../chart';
const props = defineProps<{points: ChartPoint[]; label: string; unit?: string; step?: number; from?: string; to?: string}>();
const emit = defineEmits<{select: [string]}>();
const container = ref<HTMLElement|null>(null), width = ref(560), selectedTime = ref<number|null>(null);
const height = 238, left = 66, right = 18, top = 28, bottom = 53;
let observer: ResizeObserver | undefined;
onMounted(() => {
  if (typeof ResizeObserver !== 'undefined' && container.value) {
    observer = new ResizeObserver(entries => { width.value = Math.max(240, Math.round(entries[0].contentRect.width)); });
    observer.observe(container.value);
  }
});
onUnmounted(() => observer?.disconnect());
const points = computed(() => props.points.filter(p => Number.isFinite(Date.parse(p.t))).slice().sort((a,b) => Date.parse(a.t)-Date.parse(b.t)));
const axis = computed(() => numericAxis(points.value, props.unit));
const time = computed(() => timeAxis(points.value, props.from, props.to, props.step));
const plotWidth = computed(() => width.value-left-right), plotHeight = height-top-bottom;
const timeTicks = computed(() => { const n = width.value < 400 ? 2 : 4; return Array.from({length:n+1},(_,i)=>time.value.low+(time.value.high-time.value.low)*i/n); });
const zone = Intl.DateTimeFormat().resolvedOptions().timeZone;
const crossesDay = computed(() => new Date(time.value.low).toDateString()!==new Date(time.value.high).toDateString());
function x(t: number|string) { return left + ((typeof t==='number'?t:Date.parse(t))-time.value.low)/(time.value.high-time.value.low)*plotWidth.value; }
function y(v: number) { return top+plotHeight-(v-axis.value.low)/(axis.value.high-axis.value.low)*plotHeight; }
const visiblePoints = computed(() => points.value.filter(p => Date.parse(p.t)>=time.value.low && Date.parse(p.t)<=time.value.high));
const path = computed(() => {
  let out='', previous=0, connected=false;
  for (const p of visiblePoints.value) {
    const t=Date.parse(p.t);
    if (!finite(p.v)) { connected=false; continue; }
    const gap=t-previous>(props.step || 5)*3000;
    out+=`${connected&&!gap?'L':'M'}${x(t)},${y(p.v)} `; connected=true; previous=t;
  }
  return out;
});
const selected = computed(() => selectedTime.value===null || !visiblePoints.value.length ? null : visiblePoints.value[nearestPoint(visiblePoints.value,selectedTime.value)]);
watch(time, () => { if (selectedTime.value!==null && (selectedTime.value<time.value.low || selectedTime.value>time.value.high)) selectedTime.value=null; });
function move(event: MouseEvent) {
  const rect=(event.currentTarget as SVGElement).getBoundingClientRect();
  const px=(event.clientX-rect.left)/rect.width*width.value;
  selectedTime.value=time.value.low+Math.max(0,Math.min(1,(px-left)/plotWidth.value))*(time.value.high-time.value.low);
}
function click(event: MouseEvent) { move(event); if(selected.value) emit('select',selected.value.t); }
function key(event: KeyboardEvent) {
  if (!visiblePoints.value.length) return;
  const i=selected.value ? visiblePoints.value.indexOf(selected.value) : 0;
  if (['ArrowLeft','ArrowRight','Home','End'].includes(event.key)) {
    event.preventDefault();
    const next=event.key==='Home'?0:event.key==='End'?visiblePoints.value.length-1:i+(event.key==='ArrowRight'?1:-1);
    selectedTime.value=Date.parse(visiblePoints.value[Math.max(0,Math.min(visiblePoints.value.length-1,next))].t);
  }
  if (event.key==='Enter' && selected.value) emit('select',selected.value.t);
}
function tickTime(t:number) { return new Date(t).toLocaleTimeString('ru-RU',{hour:'2-digit',minute:'2-digit',...(time.value.high-time.value.low<120000?{second:'2-digit'}:{})}); }
function tickDate(t:number) { return new Date(t).toLocaleDateString('ru-RU',{day:'2-digit',month:'2-digit'}); }
</script>
<template>
  <figure ref="container" class="metric-chart">
    <figcaption><strong>{{ label }}</strong><small v-if="axis.minimum!==null" class="muted">Мин {{ number(axis.minimum,unit,1) }} · макс {{ number(axis.maximum,unit,1) }}</small></figcaption>
    <svg class="chart" :viewBox="`0 0 ${width} ${height}`" :style="{height:height+'px'}" role="img" tabindex="0" :aria-label="`${label}, ${unit || 'значение'}. Ось X: время ${zone}; ось Y: значение. Стрелки выбирают измерение; Enter открывает срез.`" @mousemove="move" @keydown="key" @click="click">
      <title>{{ label }}: значения и время измерений, {{ zone }}</title>
      <text :x="left" y="17" class="chart-tick chart-unit">{{ unit?.trim() || 'Значение' }}</text>
      <g v-for="value in axis.ticks" :key="value" class="y-tick">
        <line :x1="left" :x2="width-right" :y1="y(value)" :y2="y(value)" class="chart-gridline"/>
        <text :x="left-9" :y="y(value)+4" text-anchor="end" class="chart-tick">{{ axisNumber(value) }}</text>
      </g>
      <g v-for="(value,i) in timeTicks" :key="value" class="x-tick">
        <line :x1="x(value)" :x2="x(value)" :y1="top" :y2="height-bottom+5" class="chart-gridline"/>
        <text :x="x(value)" :y="height-bottom+21" :text-anchor="i===0?'start':i===timeTicks.length-1?'end':'middle'" class="chart-tick">{{ tickTime(value) }}</text>
        <text v-if="crossesDay" :x="x(value)" :y="height-bottom+38" :text-anchor="i===0?'start':i===timeTicks.length-1?'end':'middle'" class="chart-tick">{{ tickDate(value) }}</text>
      </g>
      <line :x1="left" :x2="left" :y1="top" :y2="height-bottom" class="chart-axis"/>
      <line :x1="left" :x2="width-right" :y1="height-bottom" :y2="height-bottom" class="chart-axis"/>
      <template v-for="p in visiblePoints" :key="p.t"><line v-if="finite(p.min)&&finite(p.max)" :x1="x(p.t)" :x2="x(p.t)" :y1="y(p.min!)" :y2="y(p.max!)" class="chart-range"/></template>
      <path :d="path" fill="none" stroke="currentColor" stroke-width="1.8"/>
      <circle v-for="p in visiblePoints.filter(p=>finite(p.v))" :key="p.t" :cx="x(p.t)" :cy="y(p.v!)" r="1.3" fill="currentColor"/>
      <line v-if="selected" :x1="x(selected.t)" :x2="x(selected.t)" :y1="top" :y2="height-bottom" stroke="currentColor" stroke-dasharray="3 3"/>
      <text v-if="axis.minimum===null" :x="left+plotWidth/2" :y="top+plotHeight/2" text-anchor="middle" class="chart-tick">Нет измерений</text>
    </svg>
    <small class="muted chart-timezone">Время: {{ zone }} · {{ new Date(time.low).toLocaleDateString() }}<template v-if="crossesDay"> … {{ new Date(time.high).toLocaleDateString() }}</template></small>
    <p v-if="selected" class="muted chart-value">{{ new Date(selected.t).toLocaleString() }} · {{ number(selected.v,unit,2) }}<template v-if="finite(selected.min)&&selected.min!==selected.max"> · диапазон {{ number(selected.min,unit,2) }}…{{ number(selected.max,unit,2) }}</template></p>
    <p v-else class="muted chart-value">Шкалы видны постоянно. Клик по измерению открывает срез.</p>
  </figure>
</template>
