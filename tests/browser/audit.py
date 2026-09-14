"""Real compiled UI + real Go API, synthetic telemetry, loopback only.

Run after npm run build and go build -o /tmp/monik-audit-server
./tests/fixtures/audit-server. No production credentials or network targets.
"""
import argparse
import json
import os
from pathlib import Path
import socket
import ssl
import subprocess
import tempfile
import time
import urllib.request
from playwright.sync_api import sync_playwright, expect


def run(binary: Path, output: Path) -> None:
    output.mkdir(parents=True, exist_ok=True)
    results, errors = [], []
    with tempfile.TemporaryDirectory(prefix="monik-browser-") as directory:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        with (output / "fixture.log").open("w") as log:
            process = subprocess.Popen([str(binary.resolve()), "--data-dir", directory, "--port", str(port)], stdout=log, stderr=log)
            browser = None
            try:
                access_file = Path(directory) / "fixture-access.json"
                deadline = time.monotonic() + 45
                base = f"https://127.0.0.1:{port}"
                while True:
                    if process.poll() is not None:
                        raise RuntimeError("fixture exited before readiness")
                    try:
                        if access_file.exists():
                            with urllib.request.urlopen(base + "/health", context=ssl._create_unverified_context(), timeout=1) as response:
                                assert response.status == 200
                            break
                    except (OSError, urllib.error.URLError):
                        pass
                    if time.monotonic() > deadline:
                        raise TimeoutError("fixture did not become ready")
                    time.sleep(0.1)
                access = json.loads(access_file.read_text())
                assert access["url"] == base
                with sync_playwright() as playwright:
                    options = {"headless": True, "args": ["--no-sandbox", "--disable-dev-shm-usage"]}
                    if os.environ.get("MONIK_TEST_CHROMIUM"):
                        options["executable_path"] = os.environ["MONIK_TEST_CHROMIUM"]
                    browser = playwright.chromium.launch(**options)
                    context = browser.new_context(ignore_https_errors=True, viewport={"width": 1440, "height": 1000}, timezone_id="UTC")
                    page = context.new_page()
                    page.on("pageerror", lambda error: errors.append(str(error)))
                    def local_only(route):
                        if route.request.url.startswith(base + "/"):
                            route.continue_()
                        else:
                            errors.append("unexpected external request: " + route.request.url.split("?")[0])
                            route.abort()
                    page.route("**/*", local_only)
                    page.goto(base + "/login", wait_until="domcontentloaded")
                    page.locator('input[autocomplete="username"]').fill(access["username"])
                    page.locator('input[type="password"]').fill(access["password"])
                    page.get_by_role("button", name="Войти", exact=True).click()
                    expect(page.get_by_role("heading", name="Состояние машин", exact=True)).to_be_visible()
                    expect(page.locator(".page-title")).to_have_text("Обзор")
                    expect(page.locator("header.app-topbar")).to_contain_text("Операции")
                    expect(page.get_by_label("Глобальный поиск")).to_be_visible()
                    expect(page.get_by_role("button", name="Выйти", exact=True)).to_be_visible()
                    expect(page.locator("header.app-topbar")).not_to_contain_text("Живые обновления")
                    expect(page.locator("main")).not_to_contain_text("Весь парк:")
                    expect(page.locator("article.host-card")).to_have_count(0)
                    page.goto(base + "/machines", wait_until="domcontentloaded")
                    checkbox = page.get_by_role("checkbox", name="Показывать в обзоре: Audit host", exact=True)
                    expect(checkbox).not_to_be_checked()
                    checkbox.click()
                    expect(checkbox).to_be_checked()
                    page.goto(base + "/", wait_until="domcontentloaded")
                    card = page.locator("article.host-card").filter(has=page.get_by_role("heading", name="Audit host", exact=True))
                    expect(card).to_have_count(1)
                    expect(page.get_by_role("heading", name="Offline fixture", exact=True)).to_have_count(0)
                    assert "host-line" in (card.get_attribute("class") or ""), "default overview is not rows"
                    for text in ["CPU", "RAM", "DISK", "Средний ping", "HTTP 503"]:
                        expect(card).to_contain_text(text)
                    expect(card).to_contain_text("95%")
                    expect(card).to_contain_text("23,4 мс")
                    page.get_by_role("button", name="Карточки", exact=True).click()
                    for text in ["HTTP 200", "HTTP 401", "HTTP 503"]:
                        expect(card).to_contain_text(text)
                    page.get_by_role("button", name="Строки", exact=True).click()
                    page.get_by_role("button", name="Свернуть меню", exact=True).click()
                    expect(page.locator(".shell")).to_have_class("shell sidebar-collapsed")
                    page.reload(wait_until="domcontentloaded")
                    expect(page.locator(".shell")).to_have_class("shell sidebar-collapsed")
                    expect(card).to_have_count(1)
                    page.screenshot(path=str(output / "overview-desktop.png"), full_page=True)
                    results.append("overview rows contain only selected machines; layout and sidebar persist across reload")
                    page.goto(base + "/machines", wait_until="domcontentloaded")
                    checkbox = page.get_by_role("checkbox", name="Показывать в обзоре: Audit host", exact=True)
                    expect(checkbox).to_be_checked()
                    writes = []
                    def lose_response(route):
                        if route.request.method == "POST":
                            writes.append(route.request.post_data)
                            result = route.fetch()
                            assert result.ok
                            route.abort("connectionreset")
                        else:
                            route.continue_()
                    page.route("**/api/v1/operations", lose_response)
                    checkbox.click()
                    expect(checkbox).not_to_be_checked()
                    assert len(writes) == 1, "lost response duplicated the mutation"
                    page.unroute("**/api/v1/operations", lose_response)
                    assert page.evaluate("JSON.parse(sessionStorage.getItem('monik:pending-operations:v1') || '[]').length") == 0
                    checkbox.click()
                    expect(checkbox).to_be_checked()
                    page.goto(base + "/", wait_until="domcontentloaded")
                    card = page.locator("article.host-card").filter(has=page.get_by_role("heading", name="Audit host", exact=True))
                    expect(card).to_have_count(1)
                    results.append("server-side overview checkbox persists; lost response reconciled with one POST")

                    card.locator("a.card-main").click()
                    expect(page.get_by_role("heading", name="Audit host", exact=True)).to_be_visible()
                    expect(page.locator("svg.chart").first).to_be_visible()
                    # Inventory is published before history; wait for disk/temperature charts.
                    expect(page.locator("svg.chart").nth(5)).to_be_visible()
                    assert page.locator("svg.chart").count() >= 6
                    expect(page.locator("svg.chart").first.locator(".y-tick text")).to_have_count(5)
                    expect(page.locator("svg.chart").first.locator(".x-tick")).to_have_count(5)
                    expect(page.locator("svg.chart").first).to_contain_text("25")
                    expect(page.locator("svg.chart").first.locator(".chart-unit")).to_have_text("%")
                    results.append("chart axis values, timestamps and units are visible without hover")
                    expect(page.locator("figure").filter(has=page.locator("strong", has_text="CPU")).first).to_contain_text("99%")
                    for hours in [1, 2, 3, 6, 12, 24]:
                        expect(page.get_by_role("button", name=f"{hours}ч", exact=True)).to_be_visible()
                    page.get_by_role("button", name="6ч", exact=True).click()
                    page.get_by_role("button", name="HISTORY", exact=True).click()
                    date = page.locator('input[type="datetime-local"]')
                    before = date.input_value()
                    page.wait_for_timeout(5500)
                    assert date.input_value() == before, "live data moved the fixed history window"
                    page.screenshot(path=str(output / "machine-history.png"), full_page=True)
                    results.append("machine graphs, extrema, six range presets and fixed history")
                    page.get_by_role("button", name="Экспорт интервала в JSON", exact=True).click()
                    download_link = page.get_by_role("link", name="Скачать экспорт", exact=True)
                    expect(download_link).to_be_visible()
                    export_path = download_link.get_attribute("href")
                    assert export_path.startswith("/api/v1/exports/")
                    exported = context.request.get(base + export_path)
                    assert exported.ok
                    export_data = exported.json()
                    assert export_data["entity_id"] == "audit-host"
                    assert export_data["count"] > 0 and export_data["precision"] == "raw"
                    assert "cpu_percent" in export_data["samples"][0]["payload"]
                    results.append("history export button returns authenticated raw telemetry for the displayed interval")

                    page.get_by_role("button", name="LIVE", exact=True).click()
                    page.get_by_role("button", name="Собрать сейчас", exact=True).click()
                    expect(page.locator(".toast")).to_contain_text("Ждём результат агента")
                    page.locator(".toast").get_by_role("link").click()
                    expect(page.get_by_role("heading", name="agent.collect_now", exact=True)).to_be_visible()
                    expect(page.locator("main")).to_contain_text("В очереди")
                    page.screenshot(path=str(output / "operation-pending.png"), full_page=True)
                    results.append("accepted operation stays pending without synthetic worker completion")
                    page.goto(base + "/problems", wait_until="domcontentloaded")
                    expect(page.get_by_role("heading", name="Проблемы и история", exact=True)).to_be_visible()
                    page.get_by_label("Состояние", exact=True).select_option("all")
                    page.get_by_role("button", name="HISTORY", exact=True).click()
                    expect(page.get_by_label("Конец интервала")).to_be_visible()
                    expect(page.get_by_role("button", name="24ч", exact=True)).to_be_visible()
                    expect(page.locator("main")).to_contain_text("Инциденты, пересекающие выбранный интервал")
                    results.append("historical incident controls expose fixed time, state filters and shared ranges")

                    page.goto(base + "/services", wait_until="domcontentloaded")
                    group = page.locator(".service-group").filter(has=page.get_by_role("button", name="Audit host", exact=False))
                    expect(group).to_have_count(1)
                    expect(group.locator(".service-group-body")).to_have_count(0)
                    group.locator(".service-group-header").click()
                    expect(group.locator(".service-group-body")).to_be_visible()
                    group.locator("tr").filter(has=page.get_by_role("cell", name="API", exact=True)).get_by_role("link", name="Настроить запрос", exact=True).click()
                    editor = page.locator(".check-editor")
                    expect(editor).to_be_visible()
                    expect(editor.locator("select").first).to_have_value("audit-api")
                    expect(editor.locator("h3")).to_be_focused()
                    assert editor.bounding_box()["y"] < 350, "configure did not scroll to editor"
                    # Repeated same-route click after hiding the editor was a silent no-op.
                    page.get_by_role("button", name="Параметры и графики", exact=True).click()
                    expect(editor).not_to_be_visible()
                    page.locator(".service-row").filter(has=page.locator("strong", has_text="API")).get_by_role("button", name="Настроить запрос", exact=True).click()
                    expect(editor).to_be_visible()
                    expect(editor.locator("h3")).to_be_focused()
                    results.append("Services groups expand by host; repeated configure click opens, selects and focuses actual editor")
                    # Editor is independent from a failing history endpoint.
                    def fail_history(route):
                        route.fulfill(status=503, content_type="application/json", body='{"message":"history fixture failure"}')
                    page.route("**/api/v1/history/series?*", fail_history)
                    page.goto(base + "/machines/audit-host?service=audit-auth#check-editor", wait_until="domcontentloaded")
                    expect(editor).to_be_visible()
                    expect(editor.locator("select").first).to_have_value("audit-auth")
                    expect(page.locator("main")).to_contain_text("history fixture failure")
                    page.unroute("**/api/v1/history/series?*", fail_history)
                    results.append("failed graph history does not hide service editor")
                    # Owner label survives background telemetry, reload and a second change.
                    page.goto(base + "/machines", wait_until="domcontentloaded")
                    page.get_by_role("button", name="Переименовать: Audit host", exact=True).click()
                    page.get_by_label("Новое имя машины", exact=True).fill("Audit renamed")
                    page.get_by_role("button", name="Сохранить имя", exact=True).click()
                    expect(page.get_by_role("link", name="Audit renamed", exact=True)).to_be_visible()
                    page.wait_for_timeout(5500)
                    page.reload(wait_until="domcontentloaded")
                    expect(page.get_by_role("link", name="Audit renamed", exact=True)).to_be_visible()
                    page.get_by_role("button", name="Переименовать: Audit renamed", exact=True).click()
                    page.get_by_label("Новое имя машины", exact=True).fill("Audit host")
                    page.get_by_role("button", name="Сохранить имя", exact=True).click()
                    expect(page.get_by_role("link", name="Audit host", exact=True)).to_be_visible()
                    results.append("rename control commits and keeps owner label through live telemetry and reload")
                    page.goto(base + "/machines/audit-host?service=audit-api#services", wait_until="domcontentloaded")
                    editor = page.locator(".check-editor")
                    expect(editor.get_by_role("heading", name="Кастомный запрос к сервису")).to_be_visible()
                    expect(editor.get_by_label("URL сервиса", exact=True)).to_have_value("http://127.0.0.1:28000")
                    editor.get_by_label("Метод", exact=True).select_option("POST")
                    editor.get_by_label("Путь и query", exact=True).fill("/rpc?mode=brief")
                    editor.get_by_label("Интервал, с", exact=True).fill("30")
                    editor.get_by_label("Таймаут, с", exact=True).fill("10")
                    editor.get_by_label("Открытое тело запроса, до 16 КиБ", exact=True).fill('{"method":"health"}')
                    editor.get_by_label("Открытые заголовки, JSON", exact=True).fill('{"Content-Type":"application/json"}')
                    editor.get_by_role("button", name="Пробный запрос", exact=True).click()
                    expect(editor.get_by_role("alert")).to_contain_text("Подтвердите")
                    editor.get_by_role("checkbox", name="Я проверил", exact=False).check()
                    with page.expect_response(lambda r: r.request.method == "POST" and r.url.endswith("/api/v1/operations")) as submitted:
                        editor.get_by_role("button", name="Пробный запрос", exact=True).click()
                    request = json.loads(submitted.value.request.post_data)
                    assert request["action"] == "check.trial"
                    assert request["params"]["path"] == "/rpc?mode=brief"
                    assert request["params"]["body"] == '{"method":"health"}'
                    assert request["params"]["interval_seconds"] == 30
                    assert request["params"]["timeout_seconds"] == 10
                    expect(editor.get_by_role("link", name="Открыть операцию", exact=True)).to_be_visible()
                    assert editor.locator(".trial-result").count() == 0, "synthetic agent must not fabricate a completed trial"
                    page.screenshot(path=str(output / "custom-request-editor.png"), full_page=True)
                    results.append("custom POST requires consent and preserves body/path/headers/interval in actual API; result stays pending without agent evidence")
                    page.on("dialog", lambda dialog: dialog.accept())
                    page.goto(base + "/updates", wait_until="domcontentloaded")
                    expect(page.locator("main")).to_contain_text("TUF root")
                    assert page.get_by_role("button", name="Обновить всех").count() == 0
                    results.append("update import is visible and fleet-wide update remains absent")

                    page.set_viewport_size({"width": 390, "height": 844})
                    page.goto(base + "/", wait_until="domcontentloaded")
                    expect(page.get_by_role("heading", name="Audit host", exact=True)).to_be_visible()
                    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth + 1"), "mobile horizontal overflow"
                    page.screenshot(path=str(output / "overview-mobile.png"), full_page=True)
                    page.get_by_role("button", name="Развернуть меню", exact=True).click()
                    expect(page.locator("#main-nav")).to_be_visible()
                    page.keyboard.press("Escape")
                    expect(page.locator("#main-nav")).not_to_be_visible()
                    page.goto(base + "/machines/audit-host", wait_until="domcontentloaded")
                    expect(page.locator("svg.chart").first.locator(".x-tick")).to_have_count(3)
                    page.screenshot(path=str(output / "machine-mobile.png"), full_page=True)
                    results.append("mobile overview fits; menu closes on Escape and charts use three readable time ticks")
                    assert not errors, errors
                    results.append("no uncaught browser errors or external requests")
                    context.close()
                    browser.close()
                    browser = None
            except Exception as error:
                errors.append(str(error))
                raise
            finally:
                process.terminate()
                try:
                    process.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    process.kill()
                    process.wait()
                (output / "results.json").write_text(json.dumps({"passed": results, "errors": errors, "fixture": "synthetic loopback; not production/native fleet acceptance"}, ensure_ascii=False, indent=2))
    print(f"Browser audit: {len(results)} checks passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    arguments = parser.parse_args()
    run(arguments.fixture, arguments.output)
