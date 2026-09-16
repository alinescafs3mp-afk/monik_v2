<script setup lang="ts">
import {onMounted,onUnmounted,ref} from 'vue';
import {useDisplay} from '../composables/useDisplay';
const {mode,density,choose,size,tvPassive,setTVPassive}=useDisplay();
import {canHideTVTools,tvPassiveWarning} from '../tvScreen';
import {useTVProfile} from '../composables/useTVProfile';
const tv=useTVProfile();
const fullscreen=ref(!!document.fullscreenElement),error=ref('');
function update(){fullscreen.value=!!document.fullscreenElement;}
onMounted(()=>document.addEventListener('fullscreenchange',update));onUnmounted(()=>document.removeEventListener('fullscreenchange',update));
async function toggleFull(){error.value='';try{
 if(document.fullscreenElement)await document.exitFullscreen();
 else if(document.documentElement.requestFullscreen)await document.documentElement.requestFullscreen();
 else error.value='Браузер не поддерживает полноэкранный режим. ТВ-раскладка работает и без него.';
}catch{error.value='Браузер не разрешил полноэкранный режим. Компактная раскладка сохранена.';}}
</script>
<template><div class="display-controls">
 <label><span class="sr">Режим экрана</span><select aria-label="Режим экрана" :value="mode" @change="choose(($event.target as HTMLSelectElement).value)"><option value="auto">Обычный экран</option><option value="compact">Компактный</option><option value="tv">Телевизор</option></select></label>
 <label v-if="mode==='tv'&&!tvPassive"><span class="sr">Плотность ТВ</span><select :disabled="!tv.sync.canChange()||tv.state.phase==='editing'" aria-label="Плотность ТВ" :value="density" @change="size(($event.target as HTMLSelectElement).value)"><option value="14">Крупнее</option><option value="12">Плотно</option><option value="10">Максимум строк · 70%</option></select></label>
 <button v-if="mode==='tv'" type="button" class="tv-tools-toggle" :aria-pressed="tvPassive" :disabled="!tvPassive&&!canHideTVTools(tv.state.phase,tv.state.inFlight)" :title="!tvPassive&&!canHideTVTools(tv.state.phase,tv.state.inFlight)?'Завершите настройку или разрешите конфликт перед скрытием панели':'Только этот браузер; общий ТВ-профиль не меняется'" @click="setTVPassive(!tvPassive)">{{tvPassive?'Показать панель ТВ':'Скрыть панель ТВ'}}</button>
 <span v-if="mode==='tv'&&tvPassive&&tvPassiveWarning(tv.state.phase)" role="status" class="tv-sync-warning">{{tvPassiveWarning(tv.state.phase)}}</span>
 <button v-if="mode==='tv'" type="button" @click="toggleFull" :aria-pressed="fullscreen">{{fullscreen?'Выйти из полного экрана':'Полный экран'}}</button>
 <span v-if="error" role="status" class="muted display-error">{{error}}</span>
</div></template>
