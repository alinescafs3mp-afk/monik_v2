import { onMounted, onUnmounted, ref } from "vue";
import { createPollingController } from "../refreshQueue";

/** SSE is only a hint. Background refresh preserves all visible interaction state. */
export function usePolling(load: () => Promise<void>, enabled: () => boolean = () => true) {
  const loading=ref(true), refreshing=ref(false), busy=ref(false), error=ref(""), updated=ref<Date|null>(null);
  let timer=0, lastStart=0;
  const controller=createPollingController(async()=>{lastStart=performance.now();await load();},s=>{
    loading.value=s.loading;refreshing.value=s.refreshing;busy.value=s.busy;error.value=s.error;updated.value=s.updated;
  });
  const refresh=controller.refresh;
  function hint(){if(!busy.value&&enabled()&&!document.hidden&&performance.now()-lastStart>1500)void controller.background();}
  onMounted(()=>{void controller.background();timer=window.setInterval(()=>{if(enabled()&&!document.hidden)void controller.background();},5000);window.addEventListener("monik:refresh",hint);document.addEventListener("visibilitychange",hint);});
  onUnmounted(()=>{controller.dispose();window.clearInterval(timer);window.removeEventListener("monik:refresh",hint);document.removeEventListener("visibilitychange",hint);});
  return {loading,refreshing,busy,error,updated,refresh};
}
