<script setup lang="ts">
import {nextTick,onMounted,onUnmounted,ref,watch} from 'vue';
import {useRoute} from 'vue-router';
import {post,setReauthHandler} from '../api';
const dialog=ref<HTMLDialogElement|null>(null),password=ref(''),error=ref(''),busy=ref(false),action=ref(''),shown=ref(false);
let finish:((approved:boolean)=>void)|null=null, generation=0, returnFocus:HTMLElement|null=null;
const route=useRoute();
function close(approved=false){generation++;password.value='';busy.value=false;dialog.value?.close();shown.value=false;const resolve=finish;finish=null;resolve?.(approved);if(returnFocus?.isConnected)returnFocus.focus();returnFocus=null;}
async function ask(name:string){
 if(finish)return false;action.value=name;error.value='';password.value='';returnFocus=document.activeElement as HTMLElement|null;
 const result=new Promise<boolean>(resolve=>{finish=resolve;});shown.value=true;await nextTick();
 if(!dialog.value||typeof dialog.value.showModal!=='function'){close(false);return result;}
 dialog.value.showModal();dialog.value.querySelector<HTMLInputElement>('input')?.focus();return result;
}
async function verify(){if(busy.value)return;busy.value=true;error.value='';const ticket=generation;
 try{await post('/api/v1/reauth',{password:password.value});if(ticket===generation)close(true);}
 catch(e){if(ticket===generation)error.value=(e as Error).message;}
 finally{if(ticket===generation){password.value='';busy.value=false;}}
}
onMounted(()=>setReauthHandler(ask));onUnmounted(()=>{setReauthHandler(null);close(false);});watch(()=>route.fullPath,()=>close(false));
</script>
<template><dialog v-if="shown" ref="dialog" class="reauth-dialog" aria-labelledby="reauth-title" aria-describedby="reauth-description" @cancel.prevent="close(false)">
 <h2 id="reauth-title">Подтвердите действие</h2><p id="reauth-description">Для <code>{{action}}</code> требуется недавнее подтверждение личности. После проверки продолжится исходный запрос, новая операция не создаётся.</p>
 <form @submit.prevent="verify"><label>Текущий пароль <input v-model="password" type="password" autocomplete="current-password" maxlength="1024" required :disabled="busy"/></label><p v-if="error" role="alert" class="err">{{error}}</p><div class="row"><button type="submit" :disabled="busy">{{busy?'Проверяем…':'Подтвердить и продолжить'}}</button><button type="button" @click="close(false)">Отмена</button></div></form>
</dialog></template>
