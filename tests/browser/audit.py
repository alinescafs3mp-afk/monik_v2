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
                    options = {"headless": True}
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
                    card = page.locator("article.host-card").filter(has=page.get_by_role("heading", name="Audit host", exact=True))
                    expect(card).to_have_count(1)
                    for text in ["CPU", "RAM", "DISK", "Средний ping", "HTTP 200", "HTTP 401", "HTTP 503"]:
                        expect(card).to_contain_text(text)
                    expect(card).to_contain_text("95%")
                    expect(card).to_contain_text("23,4 мс")
                    page.screenshot(path=str(output / "overview-desktop.png"), full_page=True)
                    results.append("authenticated overview with CPU/RAM/DISK/ping and distinct HTTP outcomes")

                    card.get_by_role("button", name="Закрепить", exact=True).click()
                    expect(card.get_by_role("button", name="Открепить", exact=True)).to_be_visible()
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
                    card.get_by_role("button", name="Открепить", exact=True).click()
                    expect(card.get_by_role("button", name="Закрепить", exact=True)).to_be_visible()
                    assert len(writes) == 1, "lost response duplicated the mutation"
                    page.unroute("**/api/v1/operations", lose_response)
                    assert page.evaluate("JSON.parse(sessionStorage.getItem('monik:pending-operations:v1') || '[]').length") == 0
                    results.append("pin persisted; lost mutation response reconciled without duplicate POST")

                    card.locator("a.card-main").click()
                    expect(page.get_by_role("heading", name="Audit host", exact=True)).to_be_visible()
                    expect(page.locator("svg.chart").first).to_be_visible()
                    assert page.locator("svg.chart").count() >= 6
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

                    page.get_by_role("button", name="LIVE", exact=True).click()
                    page.get_by_role("button", name="Собрать сейчас", exact=True).click()
                    expect(page.locator(".toast")).to_contain_text("Ждём результат агента")
                    page.locator(".toast").get_by_role("link").click()
                    expect(page.get_by_role("heading", name="agent.collect_now", exact=True)).to_be_visible()
                    expect(page.locator("main")).to_contain_text("В очереди")
                    page.screenshot(path=str(output / "operation-pending.png"), full_page=True)
                    results.append("accepted operation stays pending without synthetic worker completion")

                    page.goto(base + "/updates", wait_until="domcontentloaded")
                    expect(page.locator("main")).to_contain_text("заблокированы")
                    assert page.get_by_role("button", name="Обновить всех").count() == 0
                    results.append("unimplemented update gate is visible and cannot claim release readiness")

                    page.set_viewport_size({"width": 390, "height": 844})
                    page.goto(base + "/", wait_until="domcontentloaded")
                    expect(page.get_by_role("heading", name="Audit host", exact=True)).to_be_visible()
                    assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth + 1"), "mobile horizontal overflow"
                    page.screenshot(path=str(output / "overview-mobile.png"), full_page=True)
                    results.append("mobile overview has no horizontal overflow")
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
