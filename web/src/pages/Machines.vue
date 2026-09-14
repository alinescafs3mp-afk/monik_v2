<script setup lang="ts">
import { computed, ref } from 'vue';
import { useRoute } from 'vue-router';
import { usePolling } from '../composables/usePolling';
import { get, submitOp } from '../api';
import { stateLabel } from '../format';
const emit=defineEmits<{toast:[string,string?]}>();
const route=useRoute();
type Machine={id:string;display_name:string;os:string;arch:string;state:string;reason:string;worker_version:string;managed_ready:boolean;last_live_at?:string;pinned:boolean;archived:boolean};
const rows=ref<Machine[]>([]), pending=ref<string[]>([]), feedback=ref<Record<string,string>>({});
const {loading,error,refresh}=usePolling(async()=>{
  const d=await get<{agents:Machine[]}>('/api/v1/agents');
  // A background read must not erase a mutation's committed value with an older response.
  rows.value=(d.agents || []).map(row => pending.value.includes(row.id) ? rows.value.find(old=>old.id===row.id) || row : row);
});
const filtered=computed(()=>{const q=String(route.query.q || '').toLocaleLowerCase();return rows.value.filter(r=>!q || [r.display_name,r.id,r.os,r.arch].join(' ').toLocaleLowerCase().includes(q));});
async function pin(row:Machine,event:Event){
  const input=event.target as HTMLInputElement, wanted=input.checked;
  input.checked=row.pinned; // Only a durable server result changes the checkmark.
  if(pending.value.includes(row.id))return;
  pending.value.push(row.id); feedback.value[row.id]='Сохраняем…';
  try {
    const result=await submitOp('agent.pin',{agent_id:row.id,pinned:wanted});
    const current=rows.value.find(r=>r.id===row.id); if(current)current.pinned=wanted;
    feedback.value[row.id]=wanted?'Показ в обзоре включён':'Убрано из обзора; мониторинг продолжается';
    emit('toast',feedback.value[row.id],`/operations/${result.op?.operation_id}`);
    window.dispatchEvent(new Event('monik:refresh'));
  }catch(e){feedback.value[row.id]=(e as {message?:string}).message || 'Не удалось сохранить';}
  finally{pending.value=pending.value.filter(id=>id!==row.id);}
}
</script>
<template>
  <section>
    <p class="panel">Отметьте <strong>«Показывать в обзоре»</strong> у нужных машин. Выбор сохраняется на сервере и действует во всех браузерах. Снятие галочки не останавливает мониторинг.</p>
    <p v-if="error" class="panel err" role="alert">{{ error }} <button @click="refresh">Повторить чтение</button></p>
    <p v-if="loading" role="status">Загружаем машины…</p>
    <p v-else-if="!rows.length && !error" class="panel">Нет машин. <router-link to="/add">Добавить машину</router-link></p>
    <p v-else-if="!filtered.length && !error" class="panel">Нет машин, соответствующих поиску.</p>
    <div v-if="rows.length" class="table-wrap">
      <table class="machines-table"><thead><tr><th scope="col">Показывать в обзоре</th><th scope="col">Имя</th><th scope="col">ОС</th><th scope="col">Состояние</th><th scope="col">Версия</th><th scope="col">Управляемый</th><th scope="col">Последний live</th></tr></thead>
        <tbody><tr v-for="r in filtered" :key="r.id"><td class="overview-selection"><input type="checkbox" :checked="r.pinned" :disabled="pending.includes(r.id)||r.archived" :aria-label="`Показывать в обзоре: ${r.display_name}`" @change="pin(r,$event)"/><small v-if="feedback[r.id]" role="status">{{ feedback[r.id] }}</small><small v-if="r.archived">Машина в архиве</small></td><td><router-link :to="'/machines/'+encodeURIComponent(r.id)">{{ r.display_name }}</router-link></td><td>{{ r.os }}/{{ r.arch }}</td><td><span class="dot" :class="r.state"/> {{ stateLabel(r.state) }} <small class="muted">{{ r.reason }}</small></td><td>{{ r.worker_version }}</td><td>{{ r.managed_ready?'да':'нет' }}</td><td>{{ r.last_live_at?new Date(r.last_live_at).toLocaleString():'Нет данных' }}</td></tr></tbody>
      </table>
    </div>
  </section>
</template>
