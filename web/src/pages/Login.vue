<script setup lang="ts">
import { ref } from "vue";
import { useRouter, useRoute } from "vue-router";
import { post, setCsrf } from "../api";

const router = useRouter(), route = useRoute();
const username = ref("owner");
const password = ref("");
const err = ref("");
const pending = ref(false);

async function submit() {
  err.value = "";
  pending.value = true;
  try {
    const r = await post<{ csrf: string }>("/api/v1/login", { username: username.value, password: password.value });
    setCsrf(r.csrf);
    await router.replace("/");
  } catch (e) {
    err.value = (e as { message?: string }).message || "Ошибка входа";
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <main class="main" style="max-width: 28rem; margin: 8vh auto">
    <h1>Вход в Monik</h1>
    <p v-if="route.query.changed==='1'" role="status">Пароль изменён, браузерные сессии завершены. Войдите с новым паролем.</p>
    <form class="panel" @submit.prevent="submit">
      <label>Имя пользователя<br /><input v-model="username" autocomplete="username" required /></label>
      <p>
        <label>Пароль<br /><input v-model="password" type="password" autocomplete="current-password" required /></label>
      </p>
      <p v-if="err" class="err" role="alert">{{ err }}</p>
      <button class="primary" :disabled="pending">{{ pending ? "Вход…" : "Войти" }}</button>
    </form>
  </main>
</template>
