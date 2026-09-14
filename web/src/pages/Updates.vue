<script setup lang="ts">
import { computed, ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { agentUpdateReason, importNotice, releaseReason, selectedUpdateTargets } from "../releases";
const emit = defineEmits<{ toast: [string, string?] }>();
const rows=ref<any[]>([]), agents=ref<any[]>([]), selected=ref<string[]>([]);
const bundle=ref(''), enroll=ref(false), pending=ref(''), actionError=ref(''),serverTime=ref('');
const eligible=computed(()=>selectedUpdateTargets(selected.value,agents.value));
const {loading,error,refresh}=usePolling(async()=>{
 const [rel,ag]=await Promise.all([get<any>('/api/v1/releases'),get<any>('/api/v1/agents')]);
 rows.value=rel.releases||[];agents.value=ag.agents||[];serverTime.value=rel.server_time;
});
async function importBundle(){
 pending.value='import';actionError.value='';
 try{const r=await submitOp('update.import',{bundle_path:bundle.value,enroll_root:enroll.value});emit('toast',importNotice(r.op),`/operations/${r.op?.operation_id}`);await refresh();}
 catch(e){actionError.value=(e as any)?.message||'Не удалось подтвердить импорт. Проверьте исходную операцию.';}
 finally{pending.value='';}
}
async function rollout(r:any){
 const why=releaseReason(r,serverTime.value);if(why){actionError.value=why;return;}
 const targets=[...eligible.value];if(!targets.length){actionError.value='Выберите совместимые управляемые машины.';return;}
 pending.value=r.id;actionError.value='';
 try{const result=await submitOp('update.rollout',{release_id:r.id},targets);emit('toast','Задание сохранено. Установка подтверждается каждой машиной отдельно.',`/operations/${result.op?.operation_id}`);}
 catch(e){actionError.value=(e as any)?.message||'Не удалось подтвердить отправку. Проверьте исходную операцию.';}
 finally{pending.value='';}
}
</script>
<template>
 <section class="panel">
  <h2>Обновления агентов</h2>
  <p>Каждый релиз хранит собственные подписанные метаданные и файлы. Импорт нового комплекта не меняет уже выбранную версию.</p>
  <p class="muted">Пробные группы, автоматическая остановка партий и самообновление service-host ещё не завершены. Выбирайте небольшой проверочный набор машин.</p>
  <label>Путь к комплекту на контроллере <input v-model="bundle" autocomplete="off" :disabled="!!pending"/></label>
  <label><input type="checkbox" v-model="enroll" :disabled="!!pending"/> Зачислить корневой ключ TUF root из проверенного владельцем комплекта (только первый импорт)</label>
  <button :disabled="!bundle.trim()||!!pending" @click="importBundle">{{pending==='import'?'Проверяем и публикуем…':'Импортировать комплект'}}</button>
  <p v-if="actionError" role="alert">{{actionError}} <router-link to="/operations">Открыть операции</router-link></p>
  <p v-if="pending" role="status">{{pending==='import'?'Проверка подписей, файлов и запись каталога':'Подготовка задания для выбранных машин'}}. Принятый запрос ещё не означает завершение установки.</p>
 </section>
 <section class="panel">
  <h3>Цели раскатки</h3>
  <p>Выбрано {{eligible.length}} совместимых машин. Статус окончательно проверяется сервером перед отправкой.</p>
  <label v-for="ag in agents" :key="ag.id" class="update-target"><input type="checkbox" :value="ag.id" v-model="selected" :disabled="!!pending||!!agentUpdateReason(ag)"/>
   <span>{{ag.display_name || ag.hostname || ag.id}} · {{ag.os}}/{{ag.arch}}<small v-if="agentUpdateReason(ag)" class="muted">{{agentUpdateReason(ag)}}</small></span>
  </label>
 </section>
 <section class="panel">
  <h3>Каталог</h3>
  <p v-if="loading">Загрузка…</p><p v-if="error" role="alert">{{error}} <button @click="refresh">Повторить</button></p>
  <p v-else-if="!rows.length&&!loading">Каталог пуст. Импортируйте подписанный комплект.</p>
  <article v-for="r in rows" :key="r.id" class="release-entry">
   <div><strong>{{r.version}}</strong> · {{r.immutable?'Неизменяемый релиз':'Старый формат'}}<small class="muted">{{r.imported_at}}</small></div>
   <code>{{r.digest}}</code>
   <small v-if="r.metadata_expires_at">Метаданные действуют до {{r.metadata_expires_at}}</small>
   <p v-if="releaseReason(r,serverTime)" role="status">{{releaseReason(r,serverTime)}}</p>
   <button :disabled="!eligible.length||!!pending||!!releaseReason(r,serverTime)" @click="rollout(r)">{{pending===r.id?'Подготовка…':'Раскатить выбранным'}}</button>
  </article>
 </section>
</template>
<style scoped>
.update-target{display:flex;align-items:flex-start;gap:.6rem;margin:.6rem 0;overflow-wrap:anywhere}
.update-target small,.release-entry small{display:block}
.release-entry{display:grid;gap:.5rem;padding:1rem 0;border-bottom:1px solid var(--border,#3a414b);min-width:0}
.release-entry code{overflow-wrap:anywhere;white-space:normal}.release-entry button{justify-self:start;max-width:100%}
</style>
