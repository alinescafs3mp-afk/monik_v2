<script setup lang="ts">
import {nextTick,onMounted,onUnmounted,ref} from 'vue';
const props=withDefaults(defineProps<{modelValue:string;id:string;name?:string;label?:string;autocomplete?:string;disabled?:boolean;required?:boolean;maxlength?:number}>(),{label:'Пароль',autocomplete:'current-password',required:true,maxlength:1024});
const emit=defineEmits<{ 'update:modelValue':[string] }>();
const shown=ref(false),input=ref<HTMLInputElement|null>(null);
async function toggle(){
 const start=input.value?.selectionStart??props.modelValue.length,end=input.value?.selectionEnd??start;
 shown.value=!shown.value;await nextTick();input.value?.focus();input.value?.setSelectionRange(start,end);
}
function hide(){if(document.hidden)shown.value=false;}
onMounted(()=>document.addEventListener('visibilitychange',hide));onUnmounted(()=>document.removeEventListener('visibilitychange',hide));
defineExpose({conceal:()=>{shown.value=false;}});
</script>
<template><div class="password-field">
 <label :for="id">{{label}}</label>
 <div class="password-control"><input ref="input" :id="id" :name="name||id" :value="modelValue" @input="emit('update:modelValue',($event.target as HTMLInputElement).value)" :type="shown?'text':'password'" :autocomplete="autocomplete" :disabled="disabled" :required="required" :maxlength="maxlength" autocapitalize="off" :spellcheck="false"/>
 <button type="button" :disabled="disabled" :aria-controls="id" :aria-pressed="shown" :aria-label="shown?'Скрыть пароль':'Показать пароль'" :title="shown?'Скрыть пароль':'Показать пароль'" @click="toggle">
 <svg viewBox="0 0 24 24" width="22" height="22" fill="none" stroke="currentColor" stroke-width="1.7" aria-hidden="true"><path d="M2 12s3.5-7 10-7 10 7 10 7-3.5 7-10 7S2 12 2 12Z"/><circle cx="12" cy="12" r="3"/><path v-if="shown" d="M3 3l18 18"/></svg></button></div>
</div></template>
