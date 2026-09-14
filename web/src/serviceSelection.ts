// A presentation pin never changes scheduling. Request edits preserve all fields.
export function monitoringCheck(check: Record<string, any>, enabled: boolean): Record<string, any> {
 if (!check?.id || !check?.service_id) throw new Error('У сервиса ещё нет шаблона проверки. Настройте запрос.');
 return {...check, paused:!enabled, ignored:enabled?false:!!check.ignored};
}
export function monitoringLabel(s: any): string {
 if (!s?.has_check) return 'Не настроено';
 if (!s.monitoring_applied) return s.monitoring_enabled?'Включение ожидает агента':'Отключение ожидает агента';
 if (s.globally_paused) return 'Общая пауза машины';
 return s.monitoring_enabled?'Проверки включены':'Проверки выключены';
}
