import { onMounted, onUnmounted, ref } from "vue";

/** One in-flight load per view. SSE is a hint; the bounded polling path also
 * refreshes measurements when the event stream is unavailable. */
export function usePolling(load: () => Promise<void>, enabled: () => boolean = () => true) {
  const loading = ref(true), refreshing = ref(false), error = ref(""), updated = ref<Date | null>(null);
  let disposed = false, timer = 0, lastStart = 0;
  async function refresh() {
    if (disposed || refreshing.value) return;
    refreshing.value = true; lastStart = Date.now();
    try { await load(); if (!disposed) { error.value = ""; updated.value = new Date(); } }
    catch (e) { if (!disposed) error.value = (e as Error)?.message || "Не удалось получить данные"; }
    finally { if (!disposed) { loading.value = false; refreshing.value = false; } }
  }
  function hint() { if (enabled() && Date.now() - lastStart > 1500) void refresh(); }
  onMounted(() => { void refresh(); timer = window.setInterval(() => { if (enabled() && !document.hidden) void refresh(); }, 5000); window.addEventListener("monik:refresh", hint); document.addEventListener("visibilitychange", hint); });
  onUnmounted(() => { disposed = true; window.clearInterval(timer); window.removeEventListener("monik:refresh", hint); document.removeEventListener("visibilitychange", hint); });
  return { loading, refreshing, error, updated, refresh };
}
