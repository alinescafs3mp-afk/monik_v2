<script setup lang="ts">
import {ref} from 'vue';
import {markOperationsRead, type ApiError} from '../api';
import {readTarget, type OperationRecord} from '../operations';
const props=defineProps<{operation:OperationRecord}>();
const emit=defineEmits<{changed:[]}>();
const pending=ref(false),message=ref(''),error=ref('');
async function toggle(){
 if(pending.value)return;
 pending.value=true;error.value='';message.value='';
 // Capture intent and generation before awaiting any network result.
 const value=!props.operation.read;
 try{await markOperationsRead([readTarget(props.operation)],value);message.value=value?'Отмечено прочитанным. Результат выполнения не изменён.':'Снова требует прочтения.';}
 catch(e){const err=e as ApiError;error.value=err.status===0||err.status>=500?'Не удалось подтвердить сохранение. Отметка могла сохраниться; перечитайте состояние.':err.message||'Не удалось сохранить отметку.';}
 finally{pending.value=false;emit('changed');}
}
</script>
<template>
 <div class="operation-read-control">
  <button type="button" :disabled="pending||!operation.attention_revision" :aria-busy="pending" :aria-label="`${operation.read?'Считать непрочитанной':'Отметить прочитанной'}: ${operation.action}`" @click="toggle">{{pending?'Сохраняем…':operation.read?'Считать непрочитанной':'Прочитано'}}</button>
  <span v-if="operation.read" class="read-badge" :title="operation.read_at?`${operation.read_by||''} · ${new Date(operation.read_at).toLocaleString()}`:''">✓ Прочитано</span>
  <span role="status" class="sr">{{message}}</span>
  <small v-if="error" class="err" role="alert">{{error}} <button type="button" @click="emit('changed')">Перечитать</button></small>
 </div>
</template>
