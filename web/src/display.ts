export type DisplayMode = 'auto' | 'compact' | 'tv';
export function displayMode(value: unknown): DisplayMode { return value === 'tv' || value === 'compact' ? value : 'auto'; }
export function tvDensity(value: unknown): string { return ['10','12','14'].includes(String(value)) ? String(value) : '10'; }
export function boardCapacity(height: number, top: number, rowHeight: number): number {
 if (![height,top,rowHeight].every(Number.isFinite) || rowHeight <= 0) return 1;
 return Math.max(1, Math.min(30, Math.floor((height - top - 56) / rowHeight)));
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
 const interval = Number(service?.observation?.interval_seconds) || 5;
 return Number.isFinite(at) && at <= now+5000 && now-at <= Math.max(15,3*interval)*1000;
}
