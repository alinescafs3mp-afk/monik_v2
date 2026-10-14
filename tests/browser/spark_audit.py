"""Real compiled Vue + Go API; explicitly synthetic, loopback-only Spark fixture."""
import argparse
import json
import os
from pathlib import Path
import socket
import subprocess
import tempfile
import time

from playwright.sync_api import expect, sync_playwright


def run(binary, output):
    output.mkdir(parents=True, exist_ok=True)
    checks, errors = [], []
    with tempfile.TemporaryDirectory(prefix="monik-spark-browser-") as state:
        with socket.socket() as sock:
            sock.bind(("127.0.0.1", 0))
            port = sock.getsockname()[1]
        with (output / "fixture.log").open("w") as log:
            proc = subprocess.Popen([str(binary.resolve()), "--data-dir", state,
                                     "--port", str(port), "--with-spark"], stdout=log, stderr=log)
            try:
                access = Path(state) / "fixture-access.json"
                until = time.monotonic() + 45
                while not access.exists():
                    if proc.poll() is not None:
                        raise RuntimeError("synthetic controller exited")
                    if time.monotonic() > until:
                        raise TimeoutError("fixture access not ready")
                    time.sleep(.05)
                auth = json.loads(access.read_text())
                with sync_playwright() as pw:
                    options = {"headless": True, "args": ["--no-sandbox", "--disable-dev-shm-usage"]}
                    if os.environ.get("MONIK_TEST_CHROMIUM"):
                        options["executable_path"] = os.environ["MONIK_TEST_CHROMIUM"]
                    browser = pw.chromium.launch(**options)
                    try:
                        for label, viewport in [("desktop", {"width": 1440, "height": 1000}),
                                                ("mobile", {"width": 390, "height": 844}),
                                                ("tv", {"width": 1920, "height": 1080})]:
                            # TLS relaxation is confined to this freshly generated test CA.
                            ctx = browser.new_context(ignore_https_errors=True, viewport=viewport)
                            page = ctx.new_page()
                            page.on("pageerror", lambda e: errors.append(str(e)))
                            mutations = []
                            page.on("request", lambda r: mutations.append(r.url) if r.method not in ("GET", "HEAD") else None)
                            page.goto(auth["url"] + "/login")
                            page.locator("#username").fill(auth["username"])
                            page.locator("#login-password").fill(auth["password"])
                            page.get_by_role("button", name="Войти", exact=True).click()
                            expect(page.get_by_role("heading", name="Состояние машин", exact=True)).to_be_visible()
                            mutations.clear()
                            # Supplement the real fixture/API response with native-shaped
                            # disk counters: their long identifiers exposed mobile overflow.
                            def disk_counters(route):
                                response = route.fetch()
                                data = response.json()
                                stamp = data["spark"]["metrics"][0]["observation"]["observed_at"]
                                for counter in ["read", "write"]:
                                    data["spark"]["metrics"].append({
                                        "metric_id": f"disk.nvme0n1.{counter}_bytes_per_second",
                                        "observation": {"value": 11458.41, "unit": "bytes/second",
                                            "source": "fixture:synthetic-disk-counter", "observed_at": stamp,
                                            "quality": "ok"}})
                                route.fulfill(response=response, json=data)
                            page.route("**/api/v1/sparks/spark-fixture", disk_counters)
                            page.goto(auth["url"] + "/sparks")
                            expect(page.get_by_role("heading", name="Спарки", exact=True)).to_be_visible()
                            card = page.locator(".spark-card")
                            expect(card).to_have_count(1)
                            expect(card).to_contain_text("UMA")
                            expect(card).to_contain_text("0 / 1")
                            page.screenshot(path=str(output / (label + "-sparks.png")), full_page=True)
                            page.goto(auth["url"] + "/sparks/spark-fixture#models")
                            expect(page.get_by_role("heading", name="Модели и профили", exact=True)).to_be_visible()
                            expect(page.get_by_role("button", name="Старт", exact=True)).to_be_disabled()
                            expect(page.get_by_role("button", name="Стоп", exact=True)).to_be_disabled()
                            expect(page.get_by_role("button", name="Рестарт", exact=True)).to_be_disabled()
                            expect(page.locator(".spark-detail")).to_contain_text("32768")
                            for tab in ["Хранилище", "Диагностика", "Обзор", "Модели"]:
                                page.get_by_role("button", name=tab, exact=True).click()
                                expect(page.get_by_role("button", name=tab, exact=True)).to_have_attribute("aria-pressed", "true")
                                if tab == "Хранилище":
                                    expect(page.locator(".spark-detail dt").filter(has_text="disk.nvme0n1.read_bytes_per_second")).to_be_visible()
                                suffix = {"Хранилище": "storage", "Диагностика": "diagnostics", "Обзор": "overview", "Модели": "models"}[tab]
                                page.screenshot(path=str(output / f"{label}-{suffix}.png"), full_page=True)
                                assert page.evaluate("document.documentElement.scrollWidth <= window.innerWidth + 1"), f"{label}/{tab}: viewport overflow"
                            assert not mutations, "read-only views emitted a mutation"
                            checks.append(label + ": actual bundle/API, Spark identity/UMA, four tabs with long disk counters fit viewport, disabled unapproved actions, no mutations")
                            ctx.close()
                        assert not errors, errors
                    finally:
                        browser.close()
            except Exception as error:
                errors.append(str(error))
                raise
            finally:
                proc.terminate()
                try:
                    proc.wait(timeout=10)
                except subprocess.TimeoutExpired:
                    proc.kill(); proc.wait()
                (output / "results.json").write_text(json.dumps({"checks": checks, "errors": errors,
                    "scope": "synthetic loopback UI, not real engine or native supervisor acceptance"}, ensure_ascii=False, indent=2))
    print("Spark browser:", len(checks), "viewport journeys passed")


if __name__ == "__main__":
    parser = argparse.ArgumentParser()
    parser.add_argument("--fixture", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    run(args.fixture, args.output)
