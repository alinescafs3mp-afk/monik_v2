"""A failed lazy module must produce feedback, not an HTML/200 or blind retry.

Requires the real compiled UI. The intentionally broken request is isolated in
its own page, so expected module errors are not confused with unrelated errors.
"""
from playwright.sync_api import expect


def exercise_navigation_failure(context, base, output, results):
    missing = context.request.get(base + "/assets/does-not-exist-v12.js")
    assert missing.status == 404
    assert "no-store" in missing.headers.get("cache-control", "")
    page = context.new_page()
    mutations = []
    page.on("request", lambda request: mutations.append(request.url)
            if request.method not in ("GET", "HEAD") else None)
    try:
        page.route("**/assets/Settings-*.js", lambda route: route.abort("connectionreset"))
        page.goto(base + "/", wait_until="domcontentloaded")
        expect(page.get_by_role("heading", name="Состояние машин", exact=True)).to_be_visible()
        page.get_by_role("link", name="Настройки и диагностика", exact=True).click()
        alert = page.locator(".navigation-failure")
        expect(alert).to_be_visible()
        expect(alert).to_contain_text("Не удалось")
        expect(alert.get_by_role("button", name="Обновить страницу", exact=True)).to_be_visible()
        assert page.url == base + "/", "failed navigation must retain the current view"
        assert not mutations, "reading a failed route must not repeat any mutation"
        page.screenshot(path=str(output / "navigation-error.png"), full_page=True)
        alert.get_by_role("button", name="Закрыть сообщение", exact=True).click()
        expect(alert).to_have_count(0)
        results.append("missing modules return 404/no-store and route failure is visible without auto reload or mutation")
    finally:
        page.close()
