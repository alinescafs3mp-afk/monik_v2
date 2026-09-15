"""V18 real browser controls against disposable loopback server; no live SSH."""
from playwright.sync_api import expect
from tv_profile_scenarios import settled

def exercise_agent_console_and_remote(context, base, output, results):
    page=context.new_page()
    try:
        page.set_viewport_size({'width':960,'height':540})
        page.goto(base+'/?display=tv',wait_until='domcontentloaded')
        button=page.get_by_role('button',name='Ширина колонок · пульт',exact=True)
        expect(button).to_be_visible()
        expect(page.get_by_label('Плотность ТВ',exact=True)).to_be_enabled()
        page.get_by_role('button',name='Сбросить ширину',exact=True).click()
        settled(page)
        button.click()
        pad=page.get_by_role('group',name='Настройка ширины с пульта',exact=True)
        expect(pad).to_be_focused()
        handle=page.locator('.tv-board .overview-column-header').get_by_role('separator',name='Ширина Машина',exact=True)
        before=int(handle.get_attribute('aria-valuenow'))
        vmin=int(handle.get_attribute('aria-valuemin'))
        vmax=int(handle.get_attribute('aria-valuemax'))
        # Adjacent tracks: Machine grows only if CPU still has slack after TV fit.
        grow=before+16<=vmax
        if grow:
            pad.press('ArrowRight');pad.press('ArrowRight')
            changed=before+16
        else:
            assert before-16>=vmin,(before,vmin,vmax)
            pad.press('ArrowLeft');pad.press('ArrowLeft')
            changed=before-16
        expect(handle).to_have_attribute('aria-valuenow',str(changed))
        pad.press('Enter')
        settled(page)
        page.reload(wait_until='domcontentloaded')
        expect(handle).to_have_attribute('aria-valuenow',str(changed))
        button.click();pad.press('ArrowLeft' if grow else 'ArrowRight');pad.press('Escape')
        expect(handle).to_have_attribute('aria-valuenow',str(changed))
        # Native buttons also work for remotes that synthesize clicks only.
        button.click();minus=pad.get_by_role('button',name='− Уже',exact=True);minus.focus();minus.press('Enter')
        expect(handle).to_have_attribute('aria-valuenow',str(changed-8))
        pad.get_by_role('button',name='Применить',exact=True).click()
        settled(page)
        link=page.locator('.tv-machine-actions').first.get_by_role('link',name='Консоль',exact=False)
        expect(link).to_be_visible()
        rect=link.bounding_box();assert rect and rect['x']>=0 and rect['x']+rect['width']<=961
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.screenshot(path=str(output/'v18-tv-remote.png'),full_page=True)
        link.click()
        expect(page.get_by_role('button',name='Через агент',exact=True)).to_have_attribute('aria-pressed','true')
        expect(page.locator('.console-page')).to_contain_text('console-enable')
        assert page.get_by_label('Пароль SSH',exact=True).count()==0
        page.get_by_role('button',name='Прямое SSH',exact=True).click()
        expect(page.locator('.console-page')).to_contain_text('Настроить SSH')
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.set_viewport_size({'width':390,'height':844})
        assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
        page.screenshot(path=str(output/'v18-console-phone.png'),full_page=True)
        results.append('V18 remote OK/arrows and native +/- buttons resize, persist and cancel; TV console action stays reachable')
        results.append('V18 agent/SSH transport selection is explicit; absent channel never opens a shell or requests SSH credentials')
        page.goto(base+'/?display=auto',wait_until='domcontentloaded')
    finally:
        page.close()
