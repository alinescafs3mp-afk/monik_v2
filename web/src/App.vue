<script setup lang="ts">
import { computed, onMounted, onUnmounted, onErrorCaptured, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { get, post, setCsrf, setStream, streamState } from "./api";
import { isAuthenticationFailure } from "./refreshQueue";
import RecentAuth from "./components/RecentAuth.vue";
import AppShell from "./components/AppShell.vue";

const route = useRoute();
const router = useRouter();
const ready = ref(false);
const me = ref<Record<string, unknown> | null>(null);
const setupRequired = ref(false);
const toast = ref("");
const toastLink = ref("");
const actionError = ref(""), errorOperation = ref("");
function handleError(e: any) { const err=e.detail || e; actionError.value=err?.message || "Ошибка действия"; errorOperation.value=err?.operation_id || ""; }
onMounted(()=>window.addEventListener("monik:error",handleError));
onUnmounted(()=>window.removeEventListener("monik:error",handleError));
onErrorCaptured((e)=>{handleError(e);return false;});

const authError = ref("");
const authLoading = ref(false);
let authGeneration = 0;
async function refreshSession() {
  const ticket = ++authGeneration;
  authLoading.value = true;
  try {
    const u = await get<Record<string, unknown>>("/api/v1/me");
    if (ticket !== authGeneration || route.meta.public) return;
    me.value = u; authError.value = "";
    if (typeof u.csrf === "string") setCsrf(u.csrf);
  } catch (e) {
    if (ticket !== authGeneration || route.meta.public) return;
    if (isAuthenticationFailure(e)) {
      me.value = null; setCsrf(""); authError.value = "";
      await router.replace("/login");
    } else {
      authError.value = (e as Error).message || "Не удалось проверить соединение с сервером. Сессия не сбрасывалась.";
      setStream("unknown");
    }
  } finally { if (ticket === authGeneration) authLoading.value = false; }
}
async function initialize() {
  authError.value = "";
  try {
    const st = await get<{ setup_required: boolean }>("/api/v1/setup/status");
    setupRequired.value = st.setup_required;
    if (st.setup_required) { await router.replace("/setup"); ready.value = true; return; }
    if (!route.meta.public) await refreshSession();
  } catch (e) {
    authError.value = (e as Error).message || "Сервер недоступен. Повторите чтение.";
  } finally { ready.value = true; }
}
onMounted(initialize);
watch(() => route.path, async () => {
  if (route.meta.public) { ++authGeneration; authLoading.value = false; authError.value = ""; return; }
  await refreshSession();
});

const publicPage = computed(() => !!route.meta.public);

async function logout() {
  await post("/api/v1/logout", {});
  me.value = null;
  setCsrf("");
  await router.replace("/login");
}

function onToast(msg: string, href = "") {
  toast.value = msg;
  toastLink.value = href;
  window.setTimeout(() => {
    if (toast.value === msg) toast.value = "";
  }, 6000);
}

defineExpose({ onToast });
</script>

<template>
  <RecentAuth v-if="ready && !publicPage"/>
  <div v-if="!ready" class="main"><div class="skeleton" /></div>
  <router-view v-else-if="publicPage" @toast="onToast" />
  <AppShell v-else :me="me" :stream="streamState()" @logout="logout">
    <div v-if="actionError" class="global-error" role="alert">{{ actionError }} <router-link :to="errorOperation?'/operations/'+errorOperation:'/operations'">Центр операций</router-link> <button @click="actionError=''">Закрыть сообщение</button></div>
    <div v-if="authError" class="panel err" role="alert">{{ authError }} <p>Ошибка связи не означает выход из аккаунта. Данные не обновляются.</p><button :disabled="authLoading" @click="initialize">{{ authLoading ? 'Проверяем…' : 'Повторить соединение' }}</button></div>
    <router-view v-else @toast="onToast" />
  </AppShell>
  <div v-if="toast" class="toast" role="status">
    {{ toast }}
    <router-link v-if="toastLink" :to="toastLink">Открыть операцию</router-link>
  </div>
</template>
