<script setup lang="ts">
import {ref} from 'vue';import {useRouter} from 'vue-router';import {post,setCsrf} from '../api';
const router=useRouter(),current=ref(''),next=ref(''),repeat=ref(''),busy=ref(false),error=ref(''),unknown=ref(false);
async function save(){if(busy.value||unknown.value)return;error.value='';
 if(next.value!==repeat.value){error.value='Новые пароли не совпадают.';return;}
 if(new TextEncoder().encode(next.value).length<12){error.value='Новый пароль должен содержать не менее 12 байт.';return;}
 busy.value=true;
 try{await post('/api/v1/account/password',{current_password:current.value,new_password:next.value});setCsrf('');await router.replace({path:'/login',query:{changed:'1'}});}
 catch(e){const err=e as any;if(err.status===0||err.status>=500){unknown.value=true;error.value='Ответ не получен. Пароль мог измениться: войдите с новым паролем. Не отправляем замену повторно вслепую.';}else error.value=err.message;}
 finally{current.value='';next.value='';repeat.value='';busy.value=false;}
}
</script>
<template><section class="panel password-form"><h2>Пароль владельца</h2><p class="muted">После смены завершатся все браузерные сессии, включая текущую. Агенты продолжат работать: их ключи не меняются.</p><form @submit.prevent="save"><fieldset :disabled="busy||unknown"><legend>Сменить пароль</legend><div class="form-grid"><label>Текущий пароль <input v-model="current" type="password" autocomplete="current-password" maxlength="1024" required/></label><label>Новый пароль <input v-model="next" type="password" autocomplete="new-password" maxlength="1024" minlength="12" required/></label><label>Повтор нового пароля <input v-model="repeat" type="password" autocomplete="new-password" maxlength="1024" required/></label></div><button type="submit">{{busy?'Сохраняем и отзываем сессии…':'Изменить пароль и выйти'}}</button></fieldset></form><p v-if="error" role="alert" class="err">{{error}}</p><router-link v-if="unknown" to="/login">Войти для проверки нового пароля</router-link></section></template>
