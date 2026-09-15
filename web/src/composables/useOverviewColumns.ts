import { computed, onMounted, onUnmounted, reactive, ref, watch, type Ref } from 'vue';
import { columnDefaults, columnMinima, decodeColumns, fitColumns, moveColumn, type ColumnMode } from '../overviewColumns';

/** Shared by every row on this surface. No server/configuration mutation. */
export function useOverviewColumns(root: Ref<HTMLElement | null>, mode: () => ColumnMode, active: () => boolean = () => true) {
  const preferred = ref<number[]>(columnDefaults(mode()));
  const width = ref(0), viewport = ref(0), font = ref(16), notice = ref(''), resizing = ref(false);
  let observer: ResizeObserver | undefined, frame = 0;
  let drag: { pointer: number; start: number; widths: number[]; saved: number[]; index: number; element: HTMLElement } | null = null;
  const storageKey = () => `monik:overview-columns:v1:${mode()}`;
  const inset = computed(() => (mode() === 'tv' ? .6 : .75) * font.value);
  const gap = computed(() => .6 * font.value);
  const actions = computed(() => mode() === 'tv' ? 0 : 2 * font.value);
  const minimum = computed(() => columnMinima(mode()).map(n => n * font.value));
  const widths = computed(() => fitColumns(preferred.value.map(n => n * font.value), minimum.value,
    width.value - 2 * inset.value - 2 - actions.value - gap.value * (mode() === 'tv' ? 5 : 6)));
  const enabled = computed(() => active() && viewport.value > (mode() === 'tv' ? 600 : 700) && widths.value !== null);
  const style = computed(() => enabled.value ? {
    '--overview-columns': widths.value!.map(n => `${n}px`).join(' ') + (actions.value ? ` ${actions.value}px` : ''),
    '--overview-inset': `${inset.value}px`, '--overview-gap': `${gap.value}px`,
  } : {});
  const positions = computed(() => enabled.value ? widths.value!.slice(0, 5).map((_, i) =>
    inset.value + widths.value!.slice(0, i + 1).reduce((a, b) => a + b, 0) + gap.value * (i + .5)) : []);
  function read() {
    try { preferred.value = decodeColumns(localStorage.getItem(storageKey()), mode()); }
    catch { preferred.value = columnDefaults(mode()); notice.value = 'Ширина действует в этой вкладке: браузер не разрешил чтение настроек.'; }
  }
  function save() {
    try { localStorage.setItem(storageKey(), JSON.stringify({ version: 1, widths: preferred.value })); notice.value = ''; }
    catch { notice.value = 'Ширина изменена, но не сохранена: хранилище браузера недоступно.'; }
  }
  function end(cancel = false) {
    const session = drag; if (!session) return;
    drag = null;
    if (cancel) preferred.value = session.saved; else save();
    resizing.value = false;
    if (session.element.hasPointerCapture?.(session.pointer)) session.element.releasePointerCapture(session.pointer);
  }
  function measure() {
    if (frame) return;
    frame = requestAnimationFrame(() => {
      frame = 0;
      const w = root.value?.getBoundingClientRect().width || 0;
      const f = parseFloat(getComputedStyle(document.documentElement).fontSize) || 16;
      if (drag && (Math.abs(w - width.value) > .5 || font.value !== f)) end(true);
      width.value = w; viewport.value = window.innerWidth; font.value = f;
    });
  }
  function start(event: PointerEvent, index: number) {
    if (!enabled.value || drag || !event.isPrimary || event.button !== 0) return;
    const element = event.currentTarget as HTMLElement;
    event.preventDefault(); event.stopPropagation();
    element.focus({ preventScroll: true });
    try { element.setPointerCapture(event.pointerId); }
    catch { notice.value = 'Перетаскивание недоступно. Используйте стрелки влево/вправо на разделителе.'; return; }
    drag = { pointer: event.pointerId, start: event.clientX, widths: [...widths.value!], saved: [...preferred.value], index, element };
    resizing.value = true;
  }
  function move(event: PointerEvent) {
    if (!drag || event.pointerId !== drag.pointer) return;
    event.preventDefault();
    preferred.value = moveColumn(drag.widths, minimum.value, drag.index, event.clientX - drag.start).slice(0, 5).map(n => n / font.value);
  }
  function finish(event: PointerEvent, cancel = false) { if (drag?.pointer === event.pointerId) end(cancel); }
  function reset() { end(true); preferred.value = columnDefaults(mode()); save(); }
  function key(event: KeyboardEvent, index: number) {
    if (event.key === 'Escape') { event.preventDefault(); end(true); return; }
    if (!enabled.value || drag) return;
    const w = widths.value!;
    const delta = event.key === 'ArrowLeft' ? -(event.shiftKey ? 32 : 8) : event.key === 'ArrowRight' ? (event.shiftKey ? 32 : 8) :
      event.key === 'Home' ? minimum.value[index] - w[index] : event.key === 'End' ? w[index + 1] - minimum.value[index + 1] : null;
    if (delta === null) return;
    event.preventDefault();
    preferred.value = moveColumn(w, minimum.value, index, delta).slice(0, 5).map(n => n / font.value); save();
  }
  const cancel = () => end(true);
  // A captured row can be removed by a remote pin/filter change. Global end
  // events also clean up in that case; normal captured events remain primary.
  const release = (event: PointerEvent) => finish(event, drag?.element.isConnected === false);
  const abortPointer = (event: PointerEvent) => finish(event, true);
  const abortKey = (event: KeyboardEvent) => { if (drag && event.key === 'Escape') { event.preventDefault(); cancel(); } };
  const hidden = () => { if (document.hidden) cancel(); };
  watch([mode, active], () => { end(true); read(); measure(); }, { flush: 'sync' });
  watch(root, (el, old) => { if (old) observer?.unobserve(old); if (el) observer?.observe(el); measure(); });
  onMounted(() => {
    read(); measure(); window.addEventListener('resize', measure); window.addEventListener('blur', cancel);
    document.addEventListener('visibilitychange', hidden);
    window.addEventListener('pointerup', release); window.addEventListener('pointercancel', abortPointer); window.addEventListener('keydown', abortKey);
    if (typeof ResizeObserver !== 'undefined') {
      observer = new ResizeObserver(measure);
      observer.observe(document.documentElement); if (root.value) observer.observe(root.value);
    }
  });
  onUnmounted(() => { end(true); observer?.disconnect(); cancelAnimationFrame(frame); window.removeEventListener('resize', measure); window.removeEventListener('blur', cancel); document.removeEventListener('visibilitychange', hidden); window.removeEventListener('pointerup', release); window.removeEventListener('pointercancel', abortPointer); window.removeEventListener('keydown', abortKey); });
  return reactive({ widths, minimum, enabled, style, positions, resizing, notice, start, move, finish, key, reset });
}
export type OverviewColumns = ReturnType<typeof useOverviewColumns>;
