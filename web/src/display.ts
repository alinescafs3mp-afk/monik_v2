export type DisplayMode = 'auto' | 'compact' | 'tv';
export function displayMode(value: unknown): DisplayMode { return value === 'tv' || value === 'compact' ? value : 'auto'; }
export function tvDensity(value: unknown): string { return ['10','12','14'].includes(String(value)) ? String(value) : '10'; }
export function boardCapacity(height: number, top: number, rowHeight: number, footerHeight: number = 56): number {
 if (![height,top,rowHeight,footerHeight].every(Number.isFinite) || rowHeight <= 0 || footerHeight < 0) return 1;
 return Math.max(1, Math.min(30, Math.floor((height - top - footerHeight) / rowHeight)));
}
export function pageSlice<T>(rows: T[], page: number, size: number): {rows:T[]; page:number; pages:number} {
 const safeSize = Math.max(1, Math.min(100, Number.isFinite(size) ? Math.floor(size) : 1));
 const pages = Math.max(1, Math.ceil(rows.length / safeSize));
 const safePage = Math.max(0, Math.min(pages-1, Number.isFinite(page) ? Math.floor(page) : 0));
 return {rows:rows.slice(safePage*safeSize,(safePage+1)*safeSize),page:safePage,pages};
}
export function serviceFresh(service: any, now: number = Date.now()): boolean {
 if (!service?.fresh) return false;
 const at = Date.parse(service?.observation?.observed_at || '');
 const raw = Number(service?.observation?.interval_seconds);
 const interval = Number.isFinite(raw)&&raw>=5&&raw<=3600?raw:5;
 const provided = Number(service?.fresh_for_seconds);
 const seconds = Number.isFinite(provided)&&provided>=10&&provided<=10800?provided:Math.max(20,3*interval);
 return Number.isFinite(at) && at <= now+5000 && now-at <= seconds*1000;
}

export function serviceState(s:any,now:number):string { return s?.fresh&&!serviceFresh(s,now)?'stale':String(s?.state||'unknown'); }
// Binary evidence lamp for active checks. Paused/inventory-only is not a failure.
export function serviceTone(s:any,now:number):string {
 const state=serviceState(s,now);
 if(['paused','unmonitored','inactive'].includes(state))return 'idle';
 return ['ok','responds'].includes(state)&&s?.fresh?'ok':'crit';
}
export function serviceOutcome(s:any,now:number):string {return s?.fresh&&!serviceFresh(s,now)?'Нет свежих данных':String(s?.summary||'Нет измерений');}
