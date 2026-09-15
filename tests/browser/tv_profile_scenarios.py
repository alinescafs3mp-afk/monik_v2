"""V19: two independently authenticated browsers, real profile API/SSE.

No production hosts, no agent control, no terminal credentials. Only layout is
changed. HTTP mutation waits replace former localStorage-immediate assumptions.
"""
from playwright.sync_api import expect
import re


def settled(page):
    expect(page.locator('.tv-profile-panel')).to_have_attribute('data-sync-state', 'synced')
    expect(page.locator('.tv-profile-panel')).to_have_attribute('data-saving', 'false')
    expect(page.get_by_label('Плотность ТВ', exact=True)).to_be_enabled()


def exercise_tv_profile(browser, context, base, credentials, output, results):
    remote = browser.new_context(ignore_https_errors=True, viewport={'width': 960, 'height': 540})
    errors = []
    try:
        login = remote.request.post(base+'/api/v1/login', data={'username': credentials['username'], 'password': credentials['password']})
        assert login.ok, 'independent TV login failed'
        pc, tv = context.new_page(), remote.new_page()
        for page in (pc, tv):
            page.on('pageerror', lambda e: errors.append(str(e)))
            page.set_viewport_size({'width': 960, 'height': 540})
            page.goto(base+'/?display=tv', wait_until='domcontentloaded')
            expect(page.get_by_label('Плотность ТВ', exact=True)).to_be_enabled()
        pc.get_by_role('button', name='Сбросить ширину', exact=True).click()
        settled(pc)
        pc.get_by_label('Плотность ТВ', exact=True).select_option('12')
        settled(pc)
        expect(tv.get_by_label('Плотность ТВ', exact=True)).to_have_value('12', timeout=10000)
        handle = pc.locator('.tv-board .overview-column-header').get_by_role('separator', name='Ширина RAM', exact=True)
        before = int(handle.get_attribute('aria-valuenow'))
        handle.focus();handle.press('ArrowRight')
        settled(pc)
        expect(handle).to_have_attribute('aria-valuenow', str(before+8))
        follower = tv.locator('.tv-board .overview-column-header').get_by_role('separator', name='Ширина RAM', exact=True)
        expect(follower).to_have_attribute('aria-valuenow', str(before+8), timeout=10000)
        pc.get_by_role('checkbox', name='Листать каждые 20 с', exact=True).check()
        settled(pc)
        expect(tv.get_by_role('checkbox', name='Листать каждые 20 с', exact=True)).to_be_checked(timeout=10000)
        tv.reload(wait_until='domcontentloaded')
        expect(follower).to_have_attribute('aria-valuenow', str(before+8))
        expect(tv.get_by_label('Плотность ТВ', exact=True)).to_have_value('12')
        results.append('V19 independent sessions sync column geometry, density and autoplay live and after reload')
        # Lose the POST response AFTER actual server execution. A GET must recover
        # the matching request ID; there must be no blind replay.
        writes=[]
        def lose(route):
            if route.request.method == 'POST':
                writes.append(route.request.post_data)
                response=route.fetch();assert response.ok
                route.abort()
            else:
                route.continue_()
        pc.route('**/api/v1/display/tv', lose)
        pc.get_by_label('Плотность ТВ', exact=True).select_option('14')
        settled(pc)
        assert len(writes)==1, 'lost acknowledgement caused replay'
        expect(tv.get_by_label('Плотность ТВ', exact=True)).to_have_value('14', timeout=10000)
        pc.unroute('**/api/v1/display/tv', lose)
        results.append('V19 lost profile acknowledgement is reconciled by identity with exactly one POST')
        # No SSE route available: bounded fallback reads still update the TV.
        tv.route('**/api/v1/events', lambda r: r.abort())
        tv.reload(wait_until='domcontentloaded');settled(tv)
        pc.get_by_label('Плотность ТВ', exact=True).select_option('10');settled(pc)
        expect(tv.get_by_label('Плотность ТВ', exact=True)).to_have_value('10', timeout=11000)
        # A TV already being edited cannot silently publish its stale snapshot.
        tv.get_by_role('button',name='Ширина колонок · пульт',exact=True).click()
        pad=tv.get_by_role('group',name='Настройка ширины с пульта',exact=True)
        pad.press('ArrowRight')
        pc.get_by_label('Плотность ТВ', exact=True).select_option('12');settled(pc)
        expect(tv.locator('.tv-profile-panel')).to_have_attribute('data-revision', str(context.request.get(base+'/api/v1/display/tv').json()['profile']['revision']), timeout=11000)
        pad.get_by_role('button',name='Применить',exact=True).click()
        expect(tv.locator('.tv-profile-panel')).to_have_attribute('data-sync-state','conflict')
        tv.once('dialog', lambda d:d.accept())
        tv.get_by_role('button',name='Загрузить общий профиль',exact=True).click();settled(tv)
        expect(tv.get_by_label('Плотность ТВ',exact=True)).to_have_value('12')
        results.append('V19 fallback works without SSE; stale remote edits preserve draft and require explicit conflict resolution')
        # Display selection is local; only the TV layout is shared.
        pc.get_by_label('Режим экрана',exact=True).select_option('auto')
        expect(tv.get_by_label('Режим экрана',exact=True)).to_have_value('tv')
        tv.set_viewport_size({'width':390,'height':844})
        expect(tv.locator('.tv-profile-panel')).to_be_visible()
        assert tv.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        tv.screenshot(path=str(output/'v19-tv-profile-phone.png'),full_page=True)
        tv.set_viewport_size({'width':960,'height':540})
        tv.screenshot(path=str(output/'v19-tv-profile-shared.png'),full_page=True)
        results.append('V19 local display-mode choice and narrow-screen adaptation do not republish the shared layout')
        assert not errors,errors
        pc.close();tv.close()
    finally:
        remote.close()
