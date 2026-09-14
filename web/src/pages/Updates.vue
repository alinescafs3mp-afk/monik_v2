<script setup lang="ts">
import { computed, ref } from "vue";
import { get, submitOp } from "../api";
import { usePolling } from "../composables/usePolling";
import { agentUpdateReason, importNotice, releaseReason, selectedUpdateTargets } from "../releases";
import RolloutProgress from "../components/RolloutProgress.vue";
import {canaryPreview,rolloutSubmitNotice,type Rollout} from "../rollouts";
const rollouts=ref<Rollout[]>([]),batchSize=ref(2),observeSeconds=ref(30),review=ref<{release:any;targets:string[];batch:number;observe:number}|null>(null);
const canaries=computed(()=>canaryPreview(review.value?.targets||[],agents.value));
const emit = defineEmits<{ toast: [string, string?] }>();
const rows=ref<any[]>([]), agents=ref<any[]>([]), selected=ref<string[]>([]);
const bundle=ref(''), enroll=ref(false), pending=ref(''), actionError=ref(''),serverTime=ref('');
const eligible=computed(()=>selectedUpdateTargets(selected.value,agents.value));
const {loading,error,refresh}=usePolling(async()=>{
 const [rel,ag,waves]=await Promise.all([get<any>('/api/v1/releases'),get<any>('/api/v1/agents'),get<any>('/api/v1/rollouts')]);
 rollouts.value=waves.rollouts||[];
 rows.value=rel.releases||[];agents.value=ag.agents||[];serverTime.value=rel.server_time;
});
async function importBundle(){
 pending.value='import';actionError.value='';
 try{const r=await submitOp('update.import',{bundle_path:bundle.value,enroll_root:enroll.value});emit('toast',importNotice(r.op),`/operations/${r.op?.operation_id}`);await refresh();}
 catch(e){actionError.value=(e as any)?.message||'Не удалось подтвердить импорт. Проверьте исходную операцию.';}
 finally{pending.value='';}
}
function prepare(r:any){
 actionError.value='';const why=releaseReason(r,serverTime.value);if(why){actionError.value=why;return;}
 if(!Number.isInteger(batchSize.value)||batchSize.value<1||batchSize.value>10||!Number.isInteger(observeSeconds.value)||observeSeconds.value<15||observeSeconds.value>300){actionError.value='Размер партии: 1–10; наблюдение: 15–300 секунд.';return;}
 review.value={release:r,targets:[...eligible.value],batch:batchSize.value,observe:observeSeconds.value};
}
async function rollout(){
 if(!review.value)return;
 const plan=review.value,r=plan.release;
 const why=releaseReason(r,serverTime.value);if(why){actionError.value=why;return;}
 const targets=[...plan.targets];if(!targets.length){actionError.value='Выберите совместимые управляемые машины.';return;}
 pending.value=r.id;actionError.value='';
 try{const result=await submitOp('update.rollout',{release_id:r.id,batch_size:plan.batch,observe_seconds:plan.observe},targets);emit('toast',rolloutSubmitNotice(result.op),`/operations/${result.op?.operation_id}`);review.value=null;await refresh();}
 catch(e){actionError.value=(e as any)?.message||'Не удалось подтвердить отправку. Проверьте исходную операцию.';}
 finally{pending.value='';}
}
</script>
<template>
 <section class="panel">
  <h2>Обновления агентов</h2>
  <p>Каждый релиз хранит собственные подписанные метаданные и файлы. Импорт нового комплекта не меняет уже выбранную версию.</p>
  <p class="muted">Сначала по одной пробной машине для каждой ОС/архитектуры, затем ограниченные партии. Ошибка останавливает дальнейшую выдачу. Обновление самого service-host и его независимое восстановление пока не завершены.</p>
  <label>Путь к комплекту на контроллере <input v-model="bundle" autocomplete="off" :disabled="!!pending"/></label>
  <label><input type="checkbox" v-model="enroll" :disabled="!!pending"/> Зачислить корневой ключ TUF root из проверенного владельцем комплекта (только первый импорт)</label>
  <button :disabled="!bundle.trim()||!!pending" @click="importBundle">{{pending==='import'?'Проверяем и публикуем…':'Импортировать комплект'}}</button>
  <p v-if="actionError" role="alert">{{actionError}} <router-link to="/operations">Открыть операции</router-link></p>
  <p v-if="pending" role="status">{{pending==='import'?'Проверка подписей, файлов и запись каталога':'Подготовка задания для выбранных машин'}}. Принятый запрос ещё не означает завершение установки.</p>
 </section>
 <section class="panel">
  <h3>Цели раскатки</h3>
  <div class="update-policy"><label>Машин в обычной партии <input v-model.number="batchSize" type="number" min="1" max="10" step="1" :disabled="!!pending"/></label><label>Наблюдение после подтверждения, секунд <input v-model.number="observeSeconds" type="number" min="15" max="300" step="1" :disabled="!!pending"/></label></div>
  <p class="muted">Наблюдаем новый процесс, его хеш и свежую связь. Это не проверка исправности всех приложений машины. Срок плана: 24 часа; выданного задания: до 10 минут.</p>
  <p>Выбрано {{eligible.length}} совместимых машин. Статус окончательно проверяется сервером перед отправкой.</p>
  <label v-for="ag in agents" :key="ag.id" class="update-target"><input type="checkbox" :value="ag.id" v-model="selected" :disabled="!!pending||!!agentUpdateReason(ag)"/>
   <span>{{ag.display_name || ag.hostname || ag.id}} · {{ag.os}}/{{ag.arch}}<small v-if="agentUpdateReason(ag)" class="muted">{{agentUpdateReason(ag)}}</small></span>
  </label>
 </section>
 <section v-if="review" class="panel" aria-label="Подтверждение раскатки">
  <h3>Проверьте план перед запуском</h3>
  <p>Версия {{review.release.version}} · {{review.targets.length}} машин. Пробные машины идут последовательно, затем партии по {{review.batch}}. Наблюдение: {{review.observe}} с.</p>
  <p v-for="a in canaries" :key="a.id">Пробная: {{a.display_name||a.hostname||a.id}} · {{a.os}}/{{a.arch}}</p>
  <button :disabled="!!pending||!review.targets.length" @click="rollout">Подтвердить запуск по партиям</button> <button :disabled="!!pending" @click="review=null">Отмена</button>
 </section>
 <section class="panel"><h3>Раскатки</h3><p v-if="!rollouts.length">Раскаток нового формата пока нет.</p><p class="muted">Последние 30 планов. Все операции доступны в общем журнале. Закрытие страницы не отменяет работу.</p></section>
 <RolloutProgress v-for="r in rollouts" :key="r.operation_id" :rollout="r" @changed="refresh"/>
 <section class="panel">
  <h3>Каталог</h3>
  <p v-if="loading">Загрузка…</p><p v-if="error" role="alert">{{error}} <button @click="refresh">Повторить</button></p>
  <p v-else-if="!rows.length&&!loading">Каталог пуст. Импортируйте подписанный комплект.</p>
  <article v-for="r in rows" :key="r.id" class="release-entry">
   <div><strong>{{r.version}}</strong> · {{r.immutable?'Неизменяемый релиз':'Старый формат'}}<small class="muted">{{r.imported_at}}</small></div>
   <code>{{r.digest}}</code>
   <small v-if="r.metadata_expires_at">Метаданные действуют до {{r.metadata_expires_at}}</small>
   <p v-if="releaseReason(r,serverTime)" role="status">{{releaseReason(r,serverTime)}}</p>
   <button :disabled="!eligible.length||!!pending||!!releaseReason(r,serverTime)" @click="prepare(r)">{{pending===r.id?'Подготовка…':'Раскатить выбранным'}}</button>
  </article>
 </section>
</template>
<style scoped>
.update-policy{display:flex;gap:1rem;flex-wrap:wrap}.update-policy input{max-width:8rem}
.update-target{display:flex;align-items:flex-start;gap:.6rem;margin:.6rem 0;overflow-wrap:anywhere}
.update-target small,.release-entry small{display:block}
.release-entry{display:grid;gap:.5rem;padding:1rem 0;border-bottom:1px solid var(--border,#3a414b);min-width:0}
.release-entry code{overflow-wrap:anywhere;white-space:normal}.release-entry button{justify-self:start;max-width:100%}
</style>
