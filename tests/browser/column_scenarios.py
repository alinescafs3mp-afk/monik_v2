"""V17 actual pointer/keyboard acceptance against the existing local fixture.

Synthetic cards are only a layout fixture. No agent actions or deployments.
"""
import json
from playwright.sync_api import expect
from tv_profile_scenarios import settled


def exercise_columns(context, base, output, results):
    page = context.new_page()
    faults, mutations = [], []
    page.on('pageerror', lambda error: faults.append(str(error)))
    page.on('request', lambda req: mutations.append(req.url) if req.method not in ('GET', 'HEAD') else None)
    try:
        def overview(route):
            response = route.fetch()
            data = response.json()
            for card in data.get('cards', []):
                if card.get('services'):
                    seed = card['services'][0]
                    card['services'] = [dict(seed, id=f'cols-{i}', display_name=f'Service {i:02d}') for i in range(24)]
            route.fulfill(response=response, body=json.dumps(data), content_type='application/json')
        page.route('**/api/v1/overview', overview)
        page.set_viewport_size({'width': 1680, 'height': 1050})
        page.goto(base + '/?display=auto', wait_until='domcontentloaded')
        page.get_by_role('button', name='Строки', exact=True).click()
        page.get_by_role('button', name='Сбросить ширину', exact=True).click()
        header = page.locator('.overview-column-header')
        expect(header).to_be_visible()
        expect(header.get_by_role('separator')).to_have_count(5)
        handle = header.get_by_role('separator', name='Ширина CPU', exact=True)
        original = int(handle.get_attribute('aria-valuenow'))
        box = handle.bounding_box()
        assert box
        x, y = box['x'] + box['width']/2, box['y'] + box['height']/2
        page.mouse.move(x, y)
        page.mouse.down()
        page.mouse.move(x + 24, y, steps=8)
        page.mouse.up()
        expect(handle).to_have_attribute('aria-valuenow', str(original+24))
        # Header and every row share the same actual grid track positions.
        positions = page.locator('.host-line .metric-grid').evaluate_all(
            'els=>els.map(e=>[...e.children].map(c=>c.getBoundingClientRect().left))')
        assert positions
        # Absolute handles must use the row padding edge, not an inset grid area.
        pair = page.locator('.host-line .metric-grid').first.evaluate(
            'e=>{const a=e.children[0].getBoundingClientRect(),b=e.children[1].getBoundingClientRect();return (a.right+b.left)/2}')
        actual = page.locator('.host-line').first.get_by_role('separator',name='Ширина CPU',exact=True).bounding_box()
        assert actual and abs(actual['x']+actual['width']/2-pair)<1, (actual,pair)
        head = header.locator(':scope > span').evaluate_all('els=>els.map(e=>e.getBoundingClientRect().left)')
        assert all(abs(a-b)<1 for a,b in zip(head[1:5], positions[0])), (head,positions)
        for row in positions[1:]:
            assert all(abs(a-b)<1 for a,b in zip(positions[0], row)), positions
        page.reload(wait_until='domcontentloaded')
        handle = page.locator('.overview-column-header').get_by_role('separator', name='Ширина CPU', exact=True)
        expect(handle).to_have_attribute('aria-valuenow', str(original+24))
        handle.focus()
        page.keyboard.press('ArrowLeft')
        expect(handle).to_have_attribute('aria-valuenow', str(original+16))
        # Escape aborts the gesture, including unsaved preference changes.
        box = handle.bounding_box()
        x, y = box['x'] + box['width']/2, box['y'] + box['height']/2
        page.mouse.move(x,y); page.mouse.down(); page.mouse.move(x+10,y)
        page.keyboard.press('Escape'); page.mouse.up()
        expect(handle).to_have_attribute('aria-valuenow',str(original+16))
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.screenshot(path=str(output/'v17-columns-desktop.png'),full_page=True)
        results.append('V17 pointer drag, keyboard, Escape, persisted width and cross-row alignment')
        # Widen service space: last splitter changes Ping/Services only.
        end = page.locator('.overview-column-header').get_by_role('separator', name='Ширина Ping', exact=True)
        end.focus(); page.keyboard.press('Home')
        page.wait_for_timeout(300)
        listing=page.locator('.host-line .service-columns').first
        boxes=listing.locator('.service-row').evaluate_all('els=>els.map(e=>{const r=e.getBoundingClientRect();return {x:Math.round(r.x),right:r.right}})')
        if boxes:
            groups={}
            for box in boxes: groups[box['x']]=groups.get(box['x'],0)+1
            assert all(n==3 for n in groups.values()),groups
            assert max(b['right'] for b in boxes)<=listing.bounding_box()['x']+listing.bounding_box()['width']+1
        # Mode-specific preferences do not overwrite desktop widths.
        page.get_by_label('Режим экрана',exact=True).select_option('tv')
        expect(page.get_by_label('Плотность ТВ',exact=True)).to_be_enabled()
        page.get_by_label('Плотность ТВ',exact=True).select_option('10')
        settled(page)
        page.set_viewport_size({'width':960,'height':540})
        expect(page.locator('.tv-board .overview-column-header')).to_be_visible()
        page.get_by_role('button',name='Сбросить ширину',exact=True).click()
        tv_handle=page.locator('.tv-board .overview-column-header').get_by_role('separator',name='Ширина RAM',exact=True)
        settled(page)
        tv_handle.focus(); page.keyboard.press('ArrowRight')
        settled(page)
        tv_head=page.locator('.tv-board .overview-column-header > span').evaluate_all('els=>els.map(e=>e.getBoundingClientRect().left)')
        tv_row=page.locator('.tv-machine').first.locator(':scope > .tv-identity, :scope > .tv-metric, :scope > .tv-services').evaluate_all('els=>els.map(e=>e.getBoundingClientRect().left)')
        assert len(tv_row)==6 and all(abs(a-b)<1 for a,b in zip(tv_head,tv_row)), (tv_head,tv_row)
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.wait_for_timeout(300)
        footer=page.locator('.tv-pager').bounding_box()
        assert footer and footer['y']+footer['height']<=540+1, footer
        page.screenshot(path=str(output/'v17-columns-tv.png'),full_page=True)
        page.get_by_label('Режим экрана',exact=True).select_option('auto')
        page.set_viewport_size({'width':1680,'height':1050})
        expect(page.locator('.overview-column-header').get_by_role('separator',name='Ширина CPU',exact=True)).to_have_attribute('aria-valuenow',str(original+16))
        page.set_viewport_size({'width':390,'height':844})
        expect(page.locator('.overview-column-header')).to_have_count(0)
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.screenshot(path=str(output/'v17-columns-phone.png'),full_page=True)
        page.set_viewport_size({'width':1680,'height':1050})
        expect(page.locator('.overview-column-header').get_by_role('separator',name='Ширина CPU',exact=True)).to_have_attribute('aria-valuenow',str(original+16))
        page.get_by_role('button',name='Сбросить ширину',exact=True).click()
        expect(page.locator('.overview-column-header').get_by_role('separator',name='Ширина CPU',exact=True)).to_have_attribute('aria-valuenow',str(original))
        results.append('V17 service columns of three adapt; TV and desktop profiles separate; phone reflows; reset restores defaults')
        assert not faults, faults
        assert all(url.endswith('/api/v1/display/tv') for url in mutations), mutations
    finally:
        page.close()
