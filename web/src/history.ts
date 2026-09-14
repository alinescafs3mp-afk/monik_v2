export function historyRange(hours: number, end: string | Date) {
  if (![1, 2, 3, 6, 12, 24].includes(hours)) throw new Error('Неподдерживаемый интервал');
  const to = new Date(end);
  if (!Number.isFinite(to.getTime())) throw new Error('Укажите корректные дату и время');
  return { from: new Date(to.getTime() - hours * 3600000).toISOString(), to: to.toISOString() };
}
export function incidentStateLabel(value: string) {
  return ({ pending:'Ожидает подтверждения', confirmed:'Подтверждён', resolved:'Восстановлен', interrupted:'Наблюдение прервано', policy_changed:'Правило изменено (не восстановление)' } as Record<string,string>)[value] || value;
}
export function exportDownload(operation: any): string | null {
  const target = operation?.targets?.find((t:any) => t.agent_id === 'server' && t.status === 'succeeded');
  if (operation?.action !== 'history.export' || !target) return null;
  const expected = '/api/v1/exports/' + encodeURIComponent(String(operation.operation_id));
  return target.evidence?.download_url === expected ? expected : null;
}
