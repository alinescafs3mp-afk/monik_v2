<script setup lang="ts">
import { computed, onMounted, onUnmounted, ref, watch } from "vue";
import { useRoute } from "vue-router";
import { get, setStream } from "../api";
import { readPreference, savePreference } from "../presentation";
import DisplayControls from "./DisplayControls.vue";
import {useDisplay} from "../composables/useDisplay";
const {mode,notice:displayNotice}=useDisplay();
import {useTVProfile} from '../composables/useTVProfile';
const tv=useTVProfile();
import NavIcon from "./NavIcon.vue";

const props=defineProps<{ me: Record<string, unknown> | null; stream: string }>();
defineEmits<{ logout: [] }>();

watch(()=>props.me,me=>{if(me?.controller_id&&me?.username)tv.start(String(me.controller_id),String(me.username),me.role==='owner');else tv.stop();},{immediate:true});

const route = useRoute();
const collapsed = ref(readPreference('monik:sidebar-collapsed', 'false') === 'true');
const mobileOpen = ref(false);
const media = window.matchMedia('(max-width: 860px)');
const narrow = ref(media.matches);
const isMobile = computed(()=>narrow.value || mode.value==='tv');
function resizeMenu() { narrow.value = media.matches; mobileOpen.value = false; }
const preferenceNotice = ref('');
function toggleSidebar() {
  if (isMobile.value) { mobileOpen.value = !mobileOpen.value; return; }
  collapsed.value = !collapsed.value;
  preferenceNotice.value = savePreference('monik:sidebar-collapsed', String(collapsed.value)) ? '' : 'Выбор действует до закрытия страницы: хранилище браузера недоступно.';
}
function escapeMenu(event: KeyboardEvent) { if (event.key === 'Escape' && mobileOpen.value) { mobileOpen.value = false; document.getElementById('sidebar-toggle')?.focus(); } }
watch(() => route.fullPath, () => { mobileOpen.value = false; });
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
      { to: "/", label: "Обзор", icon: "overview" },
      { to: "/machines", label: "Машины", icon: "machine" },
      { to: "/services", label: "Сервисы", icon: "service" },
      { to: "/problems", label: "Проблемы и история", icon: "problem" },
    ],
  },
  {
    title: "Управление",
    items: [
      { to: "/agents", label: "Агенты", icon: "agent" },
      { to: "/operations", label: "Операции", icon: "operation" },
      { to: "/updates", label: "Обновления", icon: "update" },
    ],
  },
  {
    title: "Конфигурация",
    items: [{ to: "/settings", label: "Настройки и диагностика", icon: "settings" }],
  },
];

const tz = computed(() => Intl.DateTimeFormat().resolvedOptions().timeZone);

let opsLoading = false, opsRefreshAgain = false, disposed = false;
async function refreshOps() {
  if(disposed)return;
  if(opsLoading){opsRefreshAgain=true;return;} opsLoading=true;
  try {
    const data = await get<{unread_attention:number;running:number}>("/api/v1/operations/summary");
    if(!disposed){attn.value=data.unread_attention;running.value=data.running;}
  } catch { if(!disposed){attn.value=-1;running.value=-1;} }
  finally { opsLoading=false; if(opsRefreshAgain&&!disposed){opsRefreshAgain=false;void refreshOps();} }

}

function connectSSE() {
  es?.close();
  es = new EventSource("/api/v1/events");
  es.onopen = () => {
    conn.value = "live";
    setStream("live");
    window.dispatchEvent(new Event("monik:display-refresh"));
  };
  es.onerror = () => {
    conn.value = "paused";
    setStream("paused");
  };
  for (const type of ["metrics","discovery","agent","incident","operation","enrollment","preference","resnapshot"]) es.addEventListener(type,()=>window.dispatchEvent(new Event("monik:refresh")));
  es.addEventListener("display",()=>window.dispatchEvent(new Event("monik:display-refresh")));
  es.addEventListener("resnapshot",()=>window.dispatchEvent(new Event("monik:display-refresh")));
  es.addEventListener("operation", () => {
    void refreshOps();
  });
}

onMounted(() => {
  window.addEventListener("monik:refresh", refreshOps);
  if(media.addEventListener)media.addEventListener("change", resizeMenu);else media.addListener(resizeMenu);
  window.addEventListener("keydown", escapeMenu);
  void refreshOps();
  connectSSE();
  poll = window.setInterval(() => {
    void refreshOps();
  }, 8000);
});
onUnmounted(() => {
  tv.stop();
  disposed=true;window.removeEventListener("monik:refresh", refreshOps);
  if(media.removeEventListener)media.removeEventListener("change", resizeMenu);else media.removeListener(resizeMenu);
  window.removeEventListener("keydown", escapeMenu);
  es?.close();
  if (poll) window.clearInterval(poll);
});
</script>

<template>
  <div class="shell" :class="{'sidebar-collapsed':collapsed,'mobile-menu-open':mobileOpen,'tv-mode':mode==='tv'}">
    <a href="#main-content" class="skip-link">К содержимому</a>
    <nav id="main-nav" class="nav" aria-label="Основная навигация">
      <h1><span aria-hidden="true">M</span><span class="nav-label">onik</span></h1>
      <p class="muted nav-label">{{ tz }}</p>
      <div v-for="g in groups" :key="g.title" class="nav-group">
        <p class="muted nav-label">{{ g.title }}</p>
        <router-link v-for="i in g.items" :key="i.to" :to="i.to" :title="i.label" :aria-label="i.label"><NavIcon :name="i.icon"/><span class="nav-label">{{ i.label }}</span></router-link>
      </div>
      <router-link to="/add" title="Добавить машину" aria-label="Добавить машину"><NavIcon name="add"/><span class="nav-label">Добавить машину</span></router-link>
    </nav>
    <div class="shell-content">
      <header class="bar app-topbar">
        <button id="sidebar-toggle" type="button" class="sidebar-toggle" :aria-expanded="isMobile ? mobileOpen : !collapsed" aria-controls="main-nav" :aria-label="(isMobile ? !mobileOpen : collapsed) ? 'Развернуть меню' : 'Свернуть меню'" title="Развернуть или свернуть меню" @click="toggleSidebar"><NavIcon name="menu"/><span class="sr">Меню</span></button>
        <strong class="page-title">{{ (route.meta.title as string) || "Monik" }}</strong>
        <router-link class="header-operations" :class="{'err':attn>0}" :to="attn>0?'/operations?filter=attention':'/operations'" title="Выполняются / непрочитанные с проблемами">Операции {{ running<0?'?':running }}/{{ attn < 0 ? '?' : attn }}</router-link>
        <form class="global-search" @submit.prevent="$router.push({ path:'/machines', query:{ q } })"><input v-model="q" type="search" placeholder="Поиск машины" aria-label="Глобальный поиск"/></form>
        <DisplayControls/><span class="header-user muted">{{ (me && (me.username as string)) || "" }}</span>
        <button type="button" @click="$emit('logout')">Выйти</button>
      </header>
      <p v-if="conn === 'paused'" class="connection-warning" role="status">Обновления приостановлены: восстанавливаем соединение с сервером. Данные могут быть устаревшими.</p>
      <p v-if="preferenceNotice" class="muted preference-notice" role="status">{{ preferenceNotice }}</p>
      <p v-if="displayNotice" role="status" class="preference-notice">{{displayNotice}}</p>
      <main id="main-content" class="main" tabindex="-1"><slot /></main>
    </div>
  </div>
</template>
