<script setup lang="ts">
import {onMounted,ref} from 'vue';
import {useRouter,useRoute} from 'vue-router';
import {get,post,setCsrf} from '../api';
import PasswordInput from '../components/PasswordInput.vue';
const router=useRouter(),route=useRoute();
const username=ref('owner'),password=ref(''),remember=ref(false),err=ref(''),pending=ref(false),checking=ref(true);
const passwordField=ref<InstanceType<typeof PasswordInput>|null>(null);
onMounted(async()=>{
 try{const r=await get<{csrf:string}>('/api/v1/me');setCsrf(r.csrf);await router.replace('/');}
 catch{/* A failed check never blocks an explicit sign-in. */}finally{checking.value=false;}
});
async function submit(){
 if(pending.value)return;err.value='';pending.value=true;passwordField.value?.conceal();
 try{
  const r=await post<{csrf:string}>('/api/v1/login',{username:username.value,password:password.value,remember:remember.value});
  setCsrf(r.csrf);password.value='';await router.replace('/');
 }catch(e){err.value=(e as Error).message||'Ошибка входа';}finally{pending.value=false;}
}
</script>
<template><main class="main login-page">
 <h1>Вход в Monik</h1>
 <p v-if="route.query.changed==='1'" role="status">Пароль изменён, браузерные сессии завершены. Войдите с новым паролем.</p>
 <p v-if="checking" role="status" class="muted">Проверяем сохранённый вход…</p>
 <form class="panel login-form" method="post" @submit.prevent="submit">
 <label for="username">Имя пользователя</label><input id="username" name="username" v-model="username" autocomplete="username" autocapitalize="off" :spellcheck="false" maxlength="255" required :disabled="pending"/>
 <PasswordInput ref="passwordField" id="login-password" name="password" v-model="password" :disabled="pending"/>
 <label class="remember-choice"><input v-model="remember" name="remember" type="checkbox" :disabled="pending"/> Запомнить вход на 30 дней</label>
 <small class="muted">Только на своём устройстве. Сохраняется сессия, не пароль. Пароль можно сохранить штатным менеджером браузера.</small>
 <p v-if="err" class="err" role="alert">{{err}}</p><button class="primary" type="submit" :disabled="pending">{{pending?'Вход…':'Войти'}}</button>
 </form></main></template>
