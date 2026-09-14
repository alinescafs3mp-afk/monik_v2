<script setup lang="ts">
import {nextTick,ref} from 'vue';
import {submitOp} from '../api';
const props=defineProps<{id:string;name:string}>();
const emit=defineEmits<{saved:[]}>();
const editing=ref(false),busy=ref(false),value=ref(''),message=ref(''),error=ref(''),base=ref(''),input=ref<HTMLInputElement|null>(null);
async function open(){value.value=props.name;base.value=props.name;editing.value=true;error.value='';await nextTick();input.value?.focus();input.value?.select();}
async function save(){if(busy.value)return;busy.value=true;error.value='';message.value='Сохраняем имя…';try{
 const r=await submitOp('agent.rename',{agent_id:props.id,display_name:value.value.trim(),expected_name:base.value});
 if(r.op?.status!=='completed')throw new Error('Имя не подтверждено сервером. Откройте результат операции.');
 editing.value=false;message.value='Имя сохранено';emit('saved');window.dispatchEvent(new Event('monik:refresh'));
 }catch(e){error.value=(e as Error).message;message.value='';}finally{busy.value=false;}}
</script>
<template><div class="inline-rename">
 <button v-if="!editing" type="button" :aria-label="`Переименовать: ${name}`" @click="open">Переименовать</button>
 <form v-else @submit.prevent="save"><label :for="`name-${id}`">Новое имя машины</label><input ref="input" :id="`name-${id}`" v-model="value" maxlength="255" required :disabled="busy" autocomplete="off"/>
 <button type="submit" :disabled="busy||!value.trim()">{{busy?'Сохраняем…':'Сохранить имя'}}</button><button type="button" :disabled="busy" @click="editing=false;error='';message=''">Отмена</button></form>
 <small v-if="message" role="status">{{message}}</small><small v-if="error" class="err" role="alert">{{error}}</small>
</div></template>
