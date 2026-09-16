/** Device-local TV presentation only. Never sent to the controller or agents. */
export const tvScreenKey = 'monik:tv-passive:v1';
export function tvPassiveChoice(query: string|null, saved: string|null): boolean {
  return query === '1' || (query !== '0' && saved === '1');
}
export function canHideTVTools(phase: string, inFlight: boolean): boolean {
  return !inFlight && !['editing', 'saving', 'conflict', 'unknown'].includes(phase);
}
export function tvPassiveWarning(phase: string): string {
  if (phase === 'local') return 'Общий ТВ-профиль ещё не создан. Настройте его на компьютере.';
  if (phase === 'conflict' || phase === 'unknown') return 'ТВ-профиль требует проверки. Откройте панель ТВ.';
  if (phase === 'error') return 'ТВ-профиль не синхронизируется. Показаны последние настройки.';
  return '';
}
export function canAdvanceTV(p: {autoplay:boolean;passive:boolean;resizing:boolean;hidden:boolean;hovered:boolean;focused:boolean;pages:number}): boolean {
  return p.autoplay && !p.resizing && !p.hidden && !p.focused && p.pages > 1 && (p.passive || !p.hovered);
}
