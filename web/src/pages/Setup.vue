<script setup lang="ts">
import { onMounted, ref } from "vue";
import { useRouter } from "vue-router";
import { get, post } from "../api";

const router = useRouter();
const username = ref("owner");
const password = ref("");
const advertised = ref("https://46.120.103.61:8777");
const listen = ref("0.0.0.0:8777");
const err = ref("");
const pending = ref(false);
const info = ref("");

onMounted(async () => {
  try {
    const st = await get<{ advertised_url: string; default_url: string; listen: string; setup_required: boolean }>(
      "/api/v1/setup/status"
    );
    advertised.value = st.advertised_url || st.default_url;
    listen.value = st.listen;
    if (!st.setup_required) await router.replace("/login");
  } catch (e) {
    err.value = (e as { message?: string }).message || "Не удалось получить статус";
  }
});

async function submit() {
  err.value = "";
  pending.value = true;
  try {
    await post("/api/v1/setup", {
      username: username.value,
      password: password.value,
      advertised_url: advertised.value,
      listen: listen.value,
    });
    info.value = "Настройка завершена. Войдите с созданной учётной записью.";
    await router.replace("/login");
  } catch (e) {
    err.value = (e as { message?: string }).message || "Настройка недоступна с этого адреса (только loopback)";
  } finally {
    pending.value = false;
  }
}
</script>

<template>
  <main class="main" style="max-width: 36rem; margin: 6vh auto">
    <h1>Первичная настройка</h1>
    <p class="muted">Создание владельца доступно только с loopback. Пароль не короче 10 символов.</p>
    <form class="panel" @submit.prevent="submit">
      <p><label>Администратор<br /><input v-model="username" required /></label></p>
      <p><label>Пароль<br /><input v-model="password" type="password" minlength="10" required /></label></p>
      <p><label>Рекламируемый URL агентов<br /><input v-model="advertised" required /></label></p>
      <p><label>Listen<br /><input v-model="listen" required /></label></p>
      <p v-if="err" class="err" role="alert">{{ err }}</p>
      <p v-if="info" class="ok">{{ info }}</p>
      <button class="primary" :disabled="pending">{{ pending ? "Сохранение…" : "Завершить настройку" }}</button>
    </form>
  </main>
</template>
