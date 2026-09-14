"""Real operations API/UI checks on the disposable loopback fixture only."""
import json
import re
import secrets
from playwright.sync_api import expect


def exercise_operation_reads(page, context, base, output, results):
    assert base.startswith("https://127.0.0.1:")
    me = context.request.get(base + "/api/v1/me").json()
    headers = {"Content-Type": "application/json", "X-CSRF-Token": me["csrf"]}

    def failed_operation():
        response = context.request.post(base + "/api/v1/operations", data=json.dumps({
            "action": "agent.pin", "client_request_key": secrets.token_hex(20),
            "params": {"agent_id": "nonexistent-audit9-target", "pinned": True}
        }), headers=headers)
        assert response.ok, response.text()
        op = response.json()
        assert op["status"] == "completed_with_errors", op
        return op

    def saved(op):
        response = context.request.get(base + "/api/v1/operations/" + op["operation_id"])
        assert response.ok
        return response.json()

    first, second = failed_operation(), failed_operation()
    total = context.request.get(base + "/api/v1/operations/summary").json()["total"]
    page.goto(base + "/operations", wait_until="domcontentloaded")
    row = page.locator('tr[data-operation-id="' + first["operation_id"] + '"]')
    expect(row).to_have_class(re.compile("operation-needs-attention"))
    row.get_by_role("button", name="Отметить прочитанной: agent.pin", exact=True).click()
    expect(row).to_have_class(re.compile("operation-read"))
    expect(row).not_to_have_class(re.compile("operation-needs-attention"))
    assert saved(first)["status"] == "completed_with_errors"
    page.reload(wait_until="domcontentloaded")
    expect(row).to_have_class(re.compile("operation-read"))
    page.screenshot(path=str(output / "operation-read-desktop.png"), full_page=True)
    results.append("operation read persists after reload, removes red notice and preserves failed execution")

    row.get_by_role("button", name="Считать непрочитанной: agent.pin", exact=True).click()
    expect(row).to_have_class(re.compile("operation-needs-attention"))
    for op in (first, second):
        page.get_by_role("checkbox", name="Выбрать операцию " + op["operation_id"], exact=True).check()
    page.get_by_role("button", name="Прочитать выбранные", exact=True).click()
    expect(page.get_by_role("button", name="Прочитать выбранные", exact=True)).to_be_disabled()
    for op in (first, second):
        expect(page.locator('tr[data-operation-id="' + op["operation_id"] + '"]')).to_have_class(re.compile("operation-read"))
        assert saved(op)["read"] is True
    assert context.request.get(base + "/api/v1/operations/summary").json()["total"] == total
    page.get_by_label("Прочтение операций", exact=True).select_option("read")
    page.get_by_label("Поиск операций", exact=True).fill(first["operation_id"])
    page.get_by_role("button", name="Найти", exact=True).click()
    expect(page.locator(".operation-table tbody tr")).to_have_count(1)
    row.get_by_role("link", name="agent.pin", exact=True).click()
    expect(page.get_by_role("heading", name="agent.pin", exact=True)).to_be_visible()
    expect(page.locator("main .read-badge")).to_be_visible()
    results.append("explicit bulk read/unread, search and detail agree; read creates no extra operation or command")

    # Persist the same write, then lose only its HTTP response. The widget must
    # read actual state, not create a job, retry blindly or pretend execution ran.
    writes = []
    def lose_read_response(route):
        if route.request.method == "POST":
            writes.append(route.request.post_data)
            assert route.fetch().ok
            route.abort("connectionreset")
        else:
            route.continue_()
    page.route("**/api/v1/operations/read", lose_read_response)
    page.get_by_role("button", name="Считать непрочитанной: agent.pin", exact=True).click()
    expect(page.locator(".operation-read-control [role=alert]")).to_be_visible()
    expect(page.get_by_role("button", name="Отметить прочитанной: agent.pin", exact=True)).to_be_visible()
    page.unroute("**/api/v1/operations/read", lose_read_response)
    assert len(writes) == 1
    assert saved(first)["read"] is False
    results.append("lost acknowledgement response is reconciled by reread without duplicate writes")

    page.goto(base + "/operations", wait_until="domcontentloaded")
    page.set_viewport_size({"width": 390, "height": 844})
    expect(page.locator(".operation-table")).to_be_visible()
    assert page.evaluate("document.documentElement.scrollWidth<=window.innerWidth+1")
    page.screenshot(path=str(output / "operations-mobile.png"), full_page=True)
    page.set_viewport_size({"width": 1440, "height": 1000})
    results.append("operation filters and bulk/read controls fit narrow screen with internal table scroll")
