<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref } from "vue";
import { useRoute } from "vue-router";
import { get, setStream } from "../api";

defineProps<{ me: Record<string, unknown> | null; stream: string }>();
defineEmits<{ logout: [] }>();

const route = useRoute();
const q = ref("");
const attn = ref(0);
const running = ref(0);
const conn = ref("unknown");
let es: EventSource | null = null;
let poll: number | null = null;

const groups = [
  {
    title: "Мониторинг",
    items: [
      { to: "/", label: "Обзор" },
      { to: "/machines", label: "Машины" },
      { to: "/services", label: "Сервисы" },
      { to: "/problems", label: "Проблемы и история" },
    ],
  },
  {
    title: "Управление",
    items: [
      { to: "/agents", label: "Агенты" },
      { to: "/operations", label: "Операции" },
      { to: "/updates", label: "Обновления" },
    ],
  },
  {
    title: "Конфигурация",
    items: [{ to: "/settings", label: "Настройки и диагностика" }],
  },
];

const tz = computed(() => Intl.DateTimeFormat().resolvedOptions().timeZone);

async function refreshOps() {
  try {
    const data = await get<{ operations: Array<{ status: string }> }>("/api/v1/operations");
    const ops = data.operations || [];
    attn.value = ops.filter((o) => o.status === "attention_required" || o.status === "completed_with_errors").length;
    running.value = ops.filter((o) => o.status === "queued" || o.status === "running").length;
  } catch {
    /* ignore */
  }
}

function connectSSE() {
  es?.close();
  es = new EventSource("/api/v1/events");
  es.onopen = () => {
    conn.value = "live";
    setStream("live");
  };
  es.onerror = () => {
    conn.value = "paused";
    setStream("paused");
  };
  es.addEventListener("operation", () => {
    void refreshOps();
  });
}

onMounted(() => {
  void refreshOps();
  connectSSE();
  poll = window.setInterval(() => {
    if (conn.value !== "live") void refreshOps();
  }, 8000);
});
onUnmounted(() => {
  es?.close();
  if (poll) window.clearInterval(poll);
});
</script>

<template>
  <div class="shell">
    <nav class="nav" aria-label="Основная навигация">
      <h1>Monik</h1>
      <p class="muted">{{ tz }}</p>
      <p class="badge">
        <span class="dot" :class="conn === 'live' ? 'ok' : 'warn'" />
        <span v-if="conn === 'live'">Живые обновления</span>
        <span v-else>Живые обновления приостановлены</span>
      </p>
      <div v-for="g in groups" :key="g.title">
        <p class="muted">{{ g.title }}</p>
        <router-link v-for="i in g.items" :key="i.to" :to="i.to">{{ i.label }}</router-link>
      </div>
      <router-link to="/add">Добавить машину</router-link>
    </nav>
    <div>
      <header class="bar" style="padding: 0.8rem 1.2rem 0">
        <strong>{{ (route.meta.title as string) || "Monik" }}</strong>
        <input v-model="q" type="search" placeholder="Поиск хоста или сервиса" aria-label="Глобальный поиск" @keydown.enter="$router.push({ path: '/machines', query: { q } })" />
        <router-link to="/operations">Операции {{ running }}/{{ attn }}</router-link>
        <span class="muted">{{ (me && (me.username as string)) || "" }}</span>
        <button type="button" @click="$emit('logout')">Выйти</button>
      </header>
      <main class="main">
        <slot />
      </main>
    </div>
  </div>
</template>
