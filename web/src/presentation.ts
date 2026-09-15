/** Display policy shared by the dense overview and expanded service list. */
export function worstDisk(disks: any[] = []) {
  return disks.filter(d => typeof d?.used_percent === 'number' && Number.isFinite(d.used_percent))
    .reduce((best, disk) => !best || disk.used_percent > best.used_percent ? disk : best, undefined as any);
}
const severity: Record<string, number> = { app_fail: 0, http_error: 0, transport_fail: 0, unknown: 1, stale: 1, paused: 3 };
export function orderedServices(services: any[] = []) {
  return [...services].sort((a, b) => (severity[a.state] ?? 2) - (severity[b.state] ?? 2)
    || Number(!!b.pinned) - Number(!!a.pinned) || String(a.display_name || a.url || a.id).localeCompare(String(b.display_name || b.url || b.id)));
}
export function readPreference(key: string, fallback: string): string {
  try { return localStorage.getItem(key) ?? fallback; } catch { return fallback; }
}
export function savePreference(key: string, value: string): boolean {
  try { localStorage.setItem(key, value); return true; } catch { return false; }
}
export function matchesMachine(machine: any, query: string): boolean {
  const text = [machine.name, machine.hostname, machine.id, machine.os, ...(machine.services || []).flatMap((s: any) => [s.display_name, s.url, s.summary])].join(' ').toLocaleLowerCase();
  return query.trim().toLocaleLowerCase().split(/\s+/).every(term => text.includes(term));
}

/** Actual problems in the displayed scope. Acknowledgement never changes health. */
export function overviewPriority(machine: any, metricsFresh: boolean): number {
  if (machine?.state === 'revoked' || machine?.state === 'archived') return 0;
  const failed = ['app_fail','http_error','transport_fail','critical','warning'];
  const metricProblem = metricsFresh && (machine?.breaches || []).length > 0;
  const selectedProblem = (machine?.services || []).some((s:any) => s.pinned !== false && s.state !== 'paused' && failed.includes(s.state));
  if (metricProblem || selectedProblem) return 2;
  if (['unreachable','stale'].includes(machine?.state) || (!metricsFresh && machine?.state === 'ok')) return 1;
  return 0;
}
export function orderOverview<T extends {name?:string;id?:string}>(machines:T[], isFresh:(m:T)=>boolean):T[] {
  return [...machines].sort((a,b)=>overviewPriority(b,isFresh(b))-overviewPriority(a,isFresh(a))
    || String(a.name||a.id).localeCompare(String(b.name||b.id)) || String(a.id).localeCompare(String(b.id)));
}

/** Three items down each bounded column, never a hidden unbounded table. */
export function serviceColumnCapacity(width:number,columnWidth=200):number {
 if(!Number.isFinite(width)||width<=0)return 3;
 return Math.max(1,Math.min(12,Math.floor((width+12)/(Math.max(100,columnWidth)+12))))*3;
}
