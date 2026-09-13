package actions

// These release requirements remain mandatory, but the current implementation
// does not safely execute them. Keep an auditable fail-closed boundary until each
// action has native/integration evidence; do not silently mark a stub successful.
func UnavailableReason(action string) string {
	switch action {
	case "update.import", "update.rollout", "update.rollback", "update.resume":
		return "Подписанное обновление ещё не готово: доверенный TUF root, безопасная активация и подтверждённый откат требуют реализации. Это блокер релиза."
	case "rebind.prepare", "rebind.arm", "rebind.activate", "rebind.retire":
		return "Безопасный ребинд ещё не готов: проверка кандидата, поколения, подтверждение и возврат не реализованы. Текущий адрес не изменён."
	case "secret.replace":
		return "Передача секретов пока заблокирована: требуется исключить открытый текст из журналов операций и реализовать защищённую доставку."
	case "agent.restart":
		return "Перезапуск ещё не связан с нативным supervisor и проверкой новой сессии. Команда не отправлена."
	case "rule.save", "maintenance.set", "history.export", "operation.retry_selected", "credential.rotate", "trust.stage", "trust.retire", "check.trial":
		return "Этот обработчик пока не реализован. Изменение не выполнено."
	}
	return ""
}
