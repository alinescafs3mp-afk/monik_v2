/** Deterministic axis math. Bounds include the requested interval, not only received samples. */
export type ChartPoint = { t: string; v: number | null; min?: number; max?: number };
const valid = (n: unknown): n is number => typeof n === 'number' && Number.isFinite(n);
export function numericAxis(points: ChartPoint[], unit = '') {
  const values = points.flatMap(p => [p.v, p.min, p.max]).filter(valid);
  if (!values.length) return { low: 0, high: 1, ticks: [0, .25, .5, .75, 1], minimum: null, maximum: null };
  const minimum = Math.min(...values), maximum = Math.max(...values);
  // Percent scales should not make a 1% fluctuation look like a full-height outage.
  if (unit.trim() === '%' && minimum >= 0 && maximum <= 100)
    return { low: 0, high: 100, ticks: [0, 25, 50, 75, 100], minimum, maximum };
  let low = Math.min(0, minimum), high = Math.max(0, maximum);
  if (low === high) high = low + 1;
  const rough = (high - low) / 4, power = Math.pow(10, Math.floor(Math.log10(rough)));
  const fraction = rough / power;
  const step = (fraction <= 1 ? 1 : fraction <= 2 ? 2 : fraction <= 2.5 ? 2.5 : fraction <= 5 ? 5 : 10) * power;
  low = Math.floor(low / step) * step; high = Math.ceil(high / step) * step;
  const ticks = Array.from({length: Math.round((high-low)/step)+1}, (_,i) => Number((low + step*i).toPrecision(12)));
  return { low, high, ticks, minimum, maximum };
}
export function timeAxis(points: ChartPoint[], from?: string, to?: string, step = 5) {
  const times = points.map(p => Date.parse(p.t)).filter(Number.isFinite);
  const start = Date.parse(from || ''), end = Date.parse(to || '');
  let low = Number.isFinite(start) ? start : times.length ? Math.min(...times) : 0;
  let high = Number.isFinite(end) ? end : times.length ? Math.max(...times) : low + 1000;
  if (high <= low) high = low + Math.max(step,1)*1000;
  return { low, high };
}
export function axisNumber(value: number): string {
  if (value !== 0 && (Math.abs(value) < .001 || Math.abs(value) >= 1e6)) return value.toExponential(1);
  return value.toLocaleString('ru-RU', {maximumFractionDigits: 3});
}
export function nearestPoint(points: ChartPoint[], at: number): number {
  let best = 0;
  points.forEach((p,i) => { if (Math.abs(Date.parse(p.t)-at) < Math.abs(Date.parse(points[best].t)-at)) best = i; });
  return best;
}
