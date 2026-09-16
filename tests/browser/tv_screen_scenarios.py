"""V21 device-local clean screen with live shared layout and square TV actions.

Two independent logins, loopback fixture only. This is presentation, not a
viewer credential: no permissions are granted or removed by hiding controls.
"""
from playwright.sync_api import expect
from tv_profile_scenarios import settled
import json
import time


def wait_eval(page, expression, arg=None, timeout=11000):
    """Poll via CDP evaluate. wait_for_function string predicates use page eval() and
    are blocked by the production script-src 'self' CSP; do not add unsafe-eval."""
    deadline = time.monotonic() + timeout / 1000
    while True:
        value = page.evaluate(expression, arg) if arg is not None else page.evaluate(expression)
        if value:
            return
        if time.monotonic() >= deadline:
            raise TimeoutError(expression)
        page.wait_for_timeout(50)


def exercise_tv_screen(browser, context, base, credentials, output, results):
    remote = browser.new_context(ignore_https_errors=True, viewport={'width': 960, 'height': 540})
    pc = context.new_page()
    tv = remote.new_page()
    errors, writes = [], []
    for page in (pc, tv):
        page.on('pageerror', lambda e: errors.append(str(e)))
    tv.on('request', lambda r: writes.append(r.url) if r.method == 'POST' and r.url.endswith('/api/v1/display/tv') else None)
    try:
        response = remote.request.post(base+'/api/v1/login', data={'username': credentials['username'], 'password': credentials['password']})
        assert response.ok
        pc.set_viewport_size({'width': 960, 'height': 540})
        pc.goto(base+'/?display=tv&tvclean=0', wait_until='domcontentloaded')
        expect(pc.get_by_label('Плотность ТВ', exact=True)).to_be_enabled()
        pc.get_by_label('Плотность ТВ', exact=True).select_option('10')
        settled(pc)
        tv.goto(base+'/?display=tv&tvclean=1', wait_until='domcontentloaded')
        expect(tv.get_by_role('button', name='Показать панель ТВ', exact=True)).to_be_visible()
        expect(tv.locator('.tv-machine').first).to_be_visible()
        expect(tv.locator('.tv-profile-panel, .tv-pager, .tv-board .column-dividers')).to_have_count(0)
        expect(tv.get_by_label('Плотность ТВ', exact=True)).to_have_count(0)
        assert tv.locator('.tv-board').evaluate('e=>e.lastElementChild.matches(".tv-machine")')
        expect(pc.locator('.tv-profile-panel')).to_be_visible()
        before = context.request.get(base+'/api/v1/display/tv').json()['profile']['revision']
        # Explicit local switch, no controller write and no hiding on the PC.
        tv.get_by_role('button', name='Показать панель ТВ', exact=True).click()
        expect(tv.locator('.tv-profile-panel')).to_be_visible()
        tv.get_by_role('button', name='Скрыть панель ТВ', exact=True).click()
        expect(tv.locator('.tv-pager')).to_have_count(0)
        assert context.request.get(base+'/api/v1/display/tv').json()['profile']['revision'] == before
        assert writes == []
        tv.reload(wait_until='domcontentloaded')
        expect(tv.get_by_role('button', name='Показать панель ТВ', exact=True)).to_be_visible()
        expect(tv.locator('.tv-profile-panel')).to_have_count(0)
        results.append('V21 clean TV is device-local, survives reload, removes all below-row controls and never saves the shared profile')
        # Real shared geometry/density still arrives in the passive screen.
        pc.get_by_label('Плотность ТВ', exact=True).select_option('12')
        settled(pc)
        wait_eval(tv, '() => getComputedStyle(document.documentElement).fontSize === "12px"')
        handle = pc.locator('.tv-board .overview-column-header').get_by_role('separator', name='Ширина RAM', exact=True)
        start = int(handle.get_attribute('aria-valuenow'))
        end = int(handle.get_attribute('aria-valuemax'))
        handle.focus();handle.press('ArrowRight' if end-start >= 8 else 'ArrowLeft')
        settled(pc)
        expected = pc.locator('.tv-machine .tv-metric').nth(1).evaluate('e=>e.getBoundingClientRect().width')
        wait_eval(tv, '(width)=>Math.abs(document.querySelectorAll(".tv-machine .tv-metric")[1].getBoundingClientRect().width-width)<1', expected)
        expect(tv.locator('.tv-profile-panel')).to_have_count(0)
        assert writes == []
        results.append('V21 passive TV applies real cross-device density and column updates without exposing controls or republishing')
        # Icons must remain small, square, named and keyboard focusable at every TV density.
        for density in ['10', '12', '14']:
            pc.get_by_label('Плотность ТВ', exact=True).select_option(density);settled(pc)
            wait_eval(tv, '(v)=>getComputedStyle(document.documentElement).fontSize===v', density+'px')
            links = tv.locator('.tv-machine-actions .tv-icon-action')
            expect(links.first).to_be_visible()
            boxes = links.evaluate_all('els=>els.map(e=>{const r=e.getBoundingClientRect();return {w:r.width,h:r.height,left:r.left,right:r.right,label:e.getAttribute("aria-label"),title:e.title}})')
            assert boxes and len(boxes) % 2 == 0
            for box in boxes:
                assert 29.5 <= box['w'] <= 30.5 and abs(box['w']-box['h']) < .5, box
                assert box['left'] >= 0 and box['right'] <= 961 and box['label'] and box['title'], box
            links.first.focus();expect(links.first).to_be_focused()
            assert links.first.evaluate('e=>getComputedStyle(e).outlineStyle') != 'none'
        tv.screenshot(path=str(output/'v21-tv-clean-960.png'), full_page=True)
        results.append('V21 console/details targets remain 30px squares with accessible names and visible focus at TV density 10/12/14')
        # No SSE: polling still synchronizes the clean screen; errors stay above the rows.
        tv.route('**/api/v1/events', lambda r:r.abort())
        tv.reload(wait_until='domcontentloaded')
        pc.get_by_label('Плотность ТВ', exact=True).select_option('10');settled(pc)
        wait_eval(tv, '() => getComputedStyle(document.documentElement).fontSize === "10px"')
        tv.route('**/api/v1/display/tv', lambda r:r.fulfill(status=503, content_type='application/json', body='{"error":"fixture","message":"profile read unavailable"}'))
        expect(tv.locator('.tv-sync-warning')).to_be_visible(timeout=11000)
        expect(tv.locator('.tv-profile-panel')).to_have_count(0)
        tv.unroute('**/api/v1/display/tv');tv.unroute('**/api/v1/events')
        tv.reload(wait_until='domcontentloaded')
        expect(tv.locator('.tv-sync-warning')).to_have_count(0, timeout=11000)
        assert writes == []
        results.append('V21 clean screen retains polling fallback and visible top-level synchronization failure without reopening its footer')
        # Enough layout-only fixture rows to prove navigation remains reachable above them.
        def many(route):
            response=route.fetch();data=response.json()
            seed=next(c for c in data['cards'] if c.get('pinned'))
            data['cards']=[dict(seed,id=f'v21-layout-{i}',name=f'Screen fixture {i}',services=[]) for i in range(16)]
            route.fulfill(response=response, body=json.dumps(data), content_type='application/json')
        tv.route('**/api/v1/overview',many)
        tv.reload(wait_until='domcontentloaded')
        expect(tv.locator('.tv-page-strip')).to_be_visible()
        assert tv.locator('.tv-board').evaluate('e=>e.firstElementChild.matches(".tv-page-strip") && e.lastElementChild.matches(".tv-machine")')
        forward=tv.get_by_role('button',name='Следующая страница машин',exact=True)
        expect(forward).to_be_enabled();forward.click()
        expect(tv.locator('.tv-page-strip')).to_contain_text('2 /')
        assert writes == []
        for width,height in [(390,844),(960,540),(1920,1080)]:
            tv.set_viewport_size({'width':width,'height':height})
            wait_eval(tv, '() => document.documentElement.scrollWidth<=innerWidth+1', timeout=5000)
            expect(tv.locator('.tv-pager,.tv-profile-panel')).to_have_count(0)
        results.append('V21 clean multi-page controls live above rows; 390/960/1920px viewports retain bounded layout')
        assert errors == [],errors
        # Query ?display=tv persists into shared origin storage. Restore auto so
        # later same-context pages still see the ordinary overview heading.
        pc.get_by_label('Режим экрана', exact=True).select_option('auto')
    finally:
        try:
            pc.evaluate("() => { localStorage.setItem('monik:display','auto'); localStorage.setItem('monik:tv-passive:v1','0'); }")
        except Exception:
            pass
        pc.close();remote.close()
