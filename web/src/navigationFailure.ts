// Navigation failures do not authorize a reload or repetition of a mutation.
export function navigationFailureMessage(error: unknown): string {
  const text = error instanceof Error ? error.message : String(error ?? "");
  if (/dynamically imported|module script|loading chunk|importing a module|module load/i.test(text)) {
    return "Не удалось загрузить раздел. Возможно, сервер обновлён или потеряна связь. Сохраните нужный текст из черновиков и обновите страницу.";
  }
  return "Не удалось открыть раздел. Текущие действия не повторялись. Проверьте соединение; перед обновлением страницы сохраните несохранённые данные.";
}
