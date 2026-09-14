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
