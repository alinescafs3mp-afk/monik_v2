<script setup lang="ts">
import { computed, onMounted, ref, watch } from "vue";
import { useRoute, useRouter } from "vue-router";
import { get, post, setCsrf, setStream, streamState } from "./api";
import AppShell from "./components/AppShell.vue";

const route = useRoute();
const router = useRouter();
const ready = ref(false);
const me = ref<Record<string, unknown> | null>(null);
const setupRequired = ref(false);
const toast = ref("");
const toastLink = ref("");

onMounted(async () => {
  try {
    const st = await get<{ setup_required: boolean }>("/api/v1/setup/status");
    setupRequired.value = st.setup_required;
    if (st.setup_required && route.path !== "/setup") {
      await router.replace("/setup");
      ready.value = true;
      return;
    }
  } catch {
    /* server still booting */
  }
  if (!route.meta.public) {
    try {
      const u = await get<Record<string, unknown>>(" /api/v1/me".trim());
      me.value = u;
      if (typeof u.csrf === "string") setCsrf(u.csrf);
    } catch {
      await router.replace("/login");
    }
  }
  ready.value = true;
});

watch(
  () => route.path,
  async () => {
    if (route.meta.public) return;
    try {
      const u = await get<Record<string, unknown>>("/api/v1/me");
      me.value = u;
      if (typeof u.csrf === "string") setCsrf(u.csrf);
    } catch {
      await router.replace("/login");
    }
  }
);

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
  <div v-if="!ready" class="main"><div class="skeleton" /></div>
  <router-view v-else-if="publicPage" @toast="onToast" />
  <AppShell v-else :me="me" :stream="streamState()" @logout="logout">
    <router-view @toast="onToast" />
  </AppShell>
  <div v-if="toast" class="toast" role="status">
    {{ toast }}
    <router-link v-if="toastLink" :to="toastLink">Открыть операцию</router-link>
  </div>
</template>
