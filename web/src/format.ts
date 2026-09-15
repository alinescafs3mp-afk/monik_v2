export const finite = (n: unknown): n is number => typeof n === "number" && Number.isFinite(n);
export function number(n: unknown, unit = "", precision = 0): string {
  return finite(n) ? `${n.toLocaleString("ru-RU", { maximumFractionDigits: precision })}${unit}` : "Нет данных";
}
export function bytes(n: unknown): string {
  if (!finite(n) || n < 0) return "Нет данных";
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let i = 0; while (n >= 1024 && i < units.length - 1) { n /= 1024; i++; }
  return number(n, ` ${units[i]}`, i > 0 ? 1 : 0);
}
const states: Record<string, string> = {
  inactive: "Больше не обнаружен", unmonitored: "Не проверяется", pending: "Ожидает подтверждения", ok: "На связи", unknown: "Неизвестно", stale: "Данные устарели", unreachable: "Нет связи с агентом",
  revoked: "Доступ отозван", archived: "В архиве", responds: "HTTP отвечает", paused: "Пауза",
  app_fail: "Ошибка приложения", http_error: "Ошибка HTTP", transport_fail: "Нет соединения",
  queued: "В очереди", waiting_offline: "Ожидает агента", accepted: "Получено агентом", running: "Выполняется",
  succeeded: "Подтверждено", completed: "Завершено", completed_with_errors: "Завершено с ошибками",
  attention_required: "Требует внимания", awaiting_confirmation: "Ожидает подтверждения", failed: "Ошибка",
  unsupported: "Не поддерживается", rejected: "Отклонено", expired: "Срок истёк", cancelled: "Отменено",
  cancelled_before_execution: "Отменено до отправки", unknown_result: "Результат неизвестен", rolled_back: "Выполнен откат",
};
export const stateLabel = (s: unknown) => states[String(s)] || String(s || "Неизвестно");
export function localDateValue(d: Date): string {
  const shifted = new Date(d.getTime() - d.getTimezoneOffset() * 60000);
  return shifted.toISOString().slice(0, 19);
}
