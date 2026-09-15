/** Device-local column geometry. Saved values are rem, never executable CSS. */
export type ColumnMode = 'auto' | 'compact' | 'tv';
export const columnLabels = ['Машина', 'CPU', 'RAM', 'DISK', 'Ping', 'Сервисы'] as const;
export const columnDefaults = (mode: ColumnMode): number[] => mode === 'tv' ? [9.5, 3.5, 8, 6, 8] : [9.5, 4.5, 7.8, 7.8, 8];
export const columnMinima = (mode: ColumnMode): number[] => mode === 'tv' ? [6.5, 3, 5, 4.5, 5, 10] : [7, 3, 5, 5, 5, 10];
export function decodeColumns(raw: string | null, mode: ColumnMode): number[] {
  try {
    if (!raw || raw.length > 512) return columnDefaults(mode);
    const data = JSON.parse(raw);
    if (data?.version !== 1 || !Array.isArray(data.widths) || data.widths.length !== 5 ||
        data.widths.some((n: unknown) => typeof n !== 'number' || !Number.isFinite(n) || n < 2 || n > 4096)) return columnDefaults(mode);
    return data.widths.map((n: number, i: number) => Math.max(columnMinima(mode)[i], n));
  } catch { return columnDefaults(mode); }
}
export function fitColumns(preferred: number[], minimum: number[], space: number): number[] | null {
  if (preferred.length !== 5 || minimum.length !== 6 || !Number.isFinite(space) ||
      [...preferred, ...minimum].some(n => !Number.isFinite(n) || n <= 0)) return null;
  const minTotal = minimum.reduce((a, b) => a + b, 0);
  if (space < minTotal) return null;
  const first = preferred.map((n, i) => Math.max(minimum[i], n));
  const excess = first.reduce((a, b) => a + b, 0) + minimum[5] - space;
  if (excess > 0) {
    const slack = first.reduce((s, n, i) => s + n - minimum[i], 0);
    first.forEach((n, i) => { first[i] = n - excess * (n - minimum[i]) / slack; });
  }
  return [...first, space - first.reduce((a, b) => a + b, 0)];
}
/** Only the two adjacent columns change. The whole row keeps its width. */
export function moveColumn(widths: number[], minimum: number[], index: number, delta: number): number[] {
  if (widths.length !== 6 || minimum.length !== 6 || !Number.isInteger(index) || index < 0 || index > 4 ||
      !Number.isFinite(delta) || [...widths, ...minimum].some(n => !Number.isFinite(n) || n <= 0)) return [...widths];
  const actual = Math.min(widths[index + 1] - minimum[index + 1], Math.max(minimum[index] - widths[index], delta));
  return widths.map((n, i) => n + (i === index ? actual : i === index + 1 ? -actual : 0));
}
/** Keep a grabbed/focused row still, while continuing to replace its telemetry. */
export function keepVisibleOrder<T extends {id?: string}>(rows: T[], frozen: string[]): T[] {
  if (!frozen.length) return rows;
  const rank = new Map(frozen.map((id, i) => [id, i]));
  return [...rows].sort((a, b) => (rank.get(String(a.id)) ?? frozen.length) - (rank.get(String(b.id)) ?? frozen.length));
}
