<script setup lang="ts">
import { columnLabels } from '../overviewColumns';
import type { OverviewColumns } from '../composables/useOverviewColumns';
import {nextTick,ref,watch} from 'vue';
const props=defineProps<{layout:OverviewColumns}>();
const entry=ref<HTMLButtonElement|null>(null),pad=ref<HTMLElement|null>(null);
watch(()=>props.layout.remoteIndex,async(value,old)=>{await nextTick();if(value>=0&&old<0)pad.value?.focus({preventScroll:true});else if(value<0&&old>=0)entry.value?.focus({preventScroll:true});});
</script>
<template><div class="remote-column-controls">
 <button ref="entry" v-if="layout.remoteIndex<0" type="button" :disabled="!layout.enabled" @click="layout.remoteBegin()">Ширина колонок · пульт</button>
 <div v-else ref="pad" tabindex="0" class="remote-column-pad" role="group" aria-label="Настройка ширины с пульта" @keydown="layout.remoteKey" @contextmenu.prevent>
  <b role="status">{{columnLabels[layout.remoteIndex]}}: {{Math.round(layout.widths?.[layout.remoteIndex]||0)}} px</b>
  <span>← → ширина · ↑ ↓ граница · OK сохранить · Назад отменить</span>
  <div class="row"><button type="button" @click="layout.remoteSelect(-1)">Граница ↑</button><button type="button" @click="layout.remoteSelect(1)">Граница ↓</button><button type="button" @click="layout.remoteStep(-8)">− Уже</button><button type="button" @click="layout.remoteStep(8)">+ Шире</button><button type="button" @click="layout.remoteEnd()">Применить</button><button type="button" @click="layout.remoteEnd(true)">Отмена</button></div>
 </div>
</div></template>
<style scoped>
.remote-column-controls{min-width:0;max-width:100%}.remote-column-pad{display:flex;flex-direction:column;gap:.4rem;border:2px solid currentColor;border-radius:6px;padding:.5rem;max-width:100%;overflow-wrap:anywhere}.remote-column-pad .row{flex-wrap:wrap}.remote-column-pad button{min-height:32px;min-width:52px}.remote-column-pad:focus{outline:3px solid var(--accent,#78b9ff);outline-offset:2px}
</style>
