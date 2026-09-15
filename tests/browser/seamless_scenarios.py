"""V16 real Vue/DOM regression: passive telemetry must not own navigation.

Only the disposable loopback fixture is used. The multi-column layout case
replicates fixture service DTOs in a read-only response; it is NOT fleet evidence.
"""
import json
import time
from playwright.sync_api import expect


def exercise_seamless_updates(context, base, output, results):
    page = context.new_page()
    faults = []
    page.on("pageerror", lambda error: faults.append(str(error)))
    try:
        page.set_viewport_size({"width": 1440, "height": 800})
        page.goto(base + "/machines/audit-host?service=audit-api#check-editor", wait_until="domcontentloaded")
        editor = page.locator("#check-editor")
        field = editor.get_by_label("Путь и query", exact=True)
        expect(field).to_be_visible()
        field.fill("/draft-v16?keep=this")
        page.evaluate("window.scrollTo(0, 0)")
        # The input keeps focus even when scrolled out. Four real polling cycles
        # must not call scrollIntoView/focus or replace the draft/DOM element.
        page.evaluate("""() => {
          window.__draftNode = document.activeElement;
          window.__initialScroll = window.scrollY;
          window.__passiveMoves = 0;
          window.addEventListener('scroll', () => {
            if (Math.abs(window.scrollY-window.__initialScroll)>3) window.__passiveMoves++;
          });
        }""")
        inventory_reads = []
        page.on("response", lambda response: inventory_reads.append(response.url)
                if response.url.endswith("/api/v1/agents/audit-host") else None)
        deadline = time.monotonic() + 30
        while len(inventory_reads) < 4 and time.monotonic() < deadline:
            page.wait_for_timeout(250)
        assert len(inventory_reads) >= 4, "four real passive inventory reads did not finish"
        assert page.evaluate("window.__passiveMoves") == 0, "passive refresh moved viewport"
        assert page.evaluate("document.activeElement === window.__draftNode"), "passive refresh stole focus"
        expect(field).to_have_value("/draft-v16?keep=this")
        assert not page.locator(".skeleton").count()
        page.screenshot(path=str(output / "v16-stable-editor.png"), full_page=True)
        results.append("V16 four passive inventory refreshes preserve scroll, exact focused node and unsaved request")
    finally:
        page.close()

    page = context.new_page()
    try:
        page.set_viewport_size({"width": 1440, "height": 900})
        page.goto(base + "/", wait_until="domcontentloaded")
        refresh = page.get_by_role("button", name="Обновить", exact=True)
        expect(refresh).to_be_visible()
        page.evaluate("""() => {
          const button = [...document.querySelectorAll('button')].find(e=>e.textContent.trim()==='Обновить');
          window.__refreshButton = button;
          window.__refreshChanges=[];
          window.__refreshObserver = new MutationObserver(() => {
            if(button.disabled || button.textContent.trim()!=='Обновить')
              window.__refreshChanges.push(button.textContent);
          });
          window.__refreshObserver.observe(button,{subtree:true,childList:true,characterData:true,attributes:true});
        }""")
        page.wait_for_timeout(12000)
        assert page.evaluate("window.__refreshChanges.length") == 0, "background refresh blinked the manual button"
        page.evaluate("window.__refreshObserver.disconnect()")
        refresh.click()
        expect(refresh).to_be_enabled()
        results.append("V16 passive overview updates leave the manual refresh button unchanged")
    finally:
        page.close()

    page = context.new_page()
    try:
        def expanded_overview(route):
            response = route.fetch()
            data = response.json()
            cards = data.get("cards", [])
            for card in cards:
                seed = next((s for s in card.get("services", []) if s.get("state") in ("ok", "responds")), None)
                if seed:
                    card["services"] = [dict(seed, id=f"layout-v16-{i}", display_name=f"Service {i:02d}") for i in range(18)]
            route.fulfill(response=response, body=json.dumps(data), content_type="application/json")
        page.route("**/api/v1/overview", expanded_overview)
        page.set_viewport_size({"width": 1440, "height": 900})
        page.goto(base + "/", wait_until="domcontentloaded")
        expect(page.get_by_role("button", name="Строки", exact=True)).to_be_visible()
        page.get_by_role("button", name="Строки", exact=True).click()
        listing = page.locator(".host-line .service-columns").first
        expect(listing.locator(".service-row").first).to_be_visible()
        page.wait_for_timeout(500)  # allow ResizeObserver + paint, not a data acceptance wait
        rects = listing.locator(".service-row").evaluate_all("els=>els.map(e=>{let b=e.getBoundingClientRect();return {x:Math.round(b.x),y:Math.round(b.y),r:b.right,b:b.bottom}})")
        groups = {}
        for r in rects:
            groups.setdefault(r['x'], []).append(r)
        assert len(groups) >= 2, (groups, rects)
        assert all(len(g)==3 for g in groups.values()), groups
        assert page.evaluate("document.documentElement.scrollWidth<=innerWidth+1")
        page.screenshot(path=str(output / "v16-columns-desktop.png"), full_page=True)
        for width, height, mode in [(390,844,'auto'),(960,540,'tv'),(1920,1080,'tv')]:
            page.get_by_label("Режим экрана", exact=True).select_option(mode)
            page.set_viewport_size({"width":width,"height":height})
            page.wait_for_timeout(500)
            assert page.evaluate("document.documentElement.scrollWidth<=innerWidth+1"), f"horizontal overflow at {width}"
            page.screenshot(path=str(output / f"v16-layout-{mode}-{width}.png"), full_page=True)
        page.get_by_label("Режим экрана", exact=True).select_option("auto")
        results.append("V16 read-only replicated service DTOs fill columns of three without viewport overflow at desktop/mobile/TV sizes")
    finally:
        page.close()
    assert not faults, faults
