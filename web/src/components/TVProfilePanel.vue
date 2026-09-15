<script setup lang="ts">
import {useTVProfile} from '../composables/useTVProfile';
const tv=useTVProfile();
function publish(){void tv.sync.update({});}
function adopt(){if(['editing','conflict','unknown','error'].includes(tv.state.phase)&&!window.confirm('Заменить местный черновик общим ТВ-профилем?'))return;void tv.sync.adopt();}
</script>
<template><div class="tv-profile-panel" :data-sync-state="tv.state.phase" :data-saving="String(tv.state.inFlight)" :data-revision="tv.state.profile?.revision" :class="{'err':['conflict','unknown','error'].includes(tv.state.phase)}">
 <span role="status" aria-live="polite">{{tv.state.message}}</span>
 <small v-if="tv.state.phase==='synced'">Колонки, плотность и автолистание общие для всех ТВ-экранов.</small>
 <button v-if="tv.state.profile?.revision===0" type="button" :disabled="!tv.sync.canChange()" @click="publish">Опубликовать текущую ТВ-раскладку</button>
 <button v-if="['conflict','unknown','error'].includes(tv.state.phase)" type="button" :disabled="tv.state.inFlight" @click="adopt">Загрузить общий профиль</button>
 <small v-if="tv.state.profile?.revision&&tv.state.profile.updated_by" :title="tv.state.profile.updated_at||''">Изменил: {{tv.state.profile.updated_by}}</small>
</div></template>
<style scoped>.tv-profile-panel{display:flex;flex-wrap:wrap;align-items:center;gap:.4rem .8rem;margin:.4rem 0;min-width:0;max-width:100%;overflow-wrap:anywhere}.tv-profile-panel>*,.tv-profile-panel button{min-width:0;max-width:100%;overflow-wrap:anywhere}.tv-profile-panel small{opacity:.8}</style>
