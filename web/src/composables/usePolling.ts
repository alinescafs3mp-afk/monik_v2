import { onMounted, onUnmounted, ref } from "vue";
import { createRefreshQueue } from "../refreshQueue";

/** SSE is a hint, never the only reconciliation path. */
export function usePolling(load: () => Promise<void>, enabled: () => boolean = () => true) {
  const loading = ref(true), refreshing = ref(false), error = ref(""), updated = ref<Date | null>(null);
  let disposed = false, timer = 0, lastStart = 0;
  const queue = createRefreshQueue(async () => {
    if (disposed) return;
    refreshing.value = true; lastStart = Date.now();
    try { await load(); if (!disposed) { error.value = ""; updated.value = new Date(); } }
    catch (e) { if (!disposed) error.value = (e as Error)?.message || "Не удалось получить данные"; }
    finally { if (!disposed) { loading.value = false; refreshing.value = false; } }
  });
  const refresh = queue.refresh;
  function hint() { if (!refreshing.value && enabled() && !document.hidden && Date.now() - lastStart > 1500) void refresh(); }
  onMounted(() => { void refresh(); timer = window.setInterval(() => { if (!refreshing.value && enabled() && !document.hidden) void refresh(); }, 5000); window.addEventListener("monik:refresh", hint); document.addEventListener("visibilitychange", hint); });
  onUnmounted(() => { disposed = true; queue.dispose(); window.clearInterval(timer); window.removeEventListener("monik:refresh", hint); document.removeEventListener("visibilitychange", hint); });
  return { loading, refreshing, error, updated, refresh };
}
