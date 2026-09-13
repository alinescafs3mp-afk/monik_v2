# Monik v2

Самостоятельный монитор хостов и локальных HTTP/HTTPS-сервисов: один сервер на Go со встроенным Vue 3 UI и SQLite, нативные агенты Linux и Windows.

Адрес по умолчанию: `https://46.120.103.61:8777`. Сохранённый адрес всегда важнее значения по умолчанию.

## Сборка

```bash
make ui && make linux && make windows
```

Агент для раздачи:

- Linux: `dist/linux-amd64/monik-agent` и `dist/linux-amd64/monik-service-host`
- Windows: `dist/windows-amd64/monik-agent.exe` и `dist/windows-amd64/monik-service-host.exe`

## Сервер

```bash
./dist/linux-amd64/monik-server setup --non-interactive \
  --listen 0.0.0.0:8777 \
  --advertised-url https://<LAN-IP>:8777
./dist/linux-amd64/monik-server run --data-dir ~/.local/share/monik-server
```

Пароль администратора: `$data-dir/admin-bootstrap.txt` (0600). UI — HTTPS, порт 8777. Первичная веб-настройка только с loopback.

## Агент

В UI: **Добавить машину** → код на 10 минут → `enrollment.yaml`.

```bash
./monik-agent setup --profile enrollment.yaml
# неуправляемый режим:
./monik-agent run --config ~/.local/share/monik-agent/agent.json
# управляемый:
sudo ./monik-agent service install --config /var/lib/monik-agent/agent.json
```

TLS агента проверяет CA контроллера. Не отключайте проверку сертификата.

## Обновления

`monik-release` подписывает артефакты TUF. Ключи `root` и `targets` только офлайн. Сервер зеркалирует уже подписанное.

## Что не входит

Telegram, удалённая перезагрузка хоста, произвольные команды, установка посторонних пакетов, HA, маркетплейс плагинов.
