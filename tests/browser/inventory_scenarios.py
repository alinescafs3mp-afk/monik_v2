"""Disposable local fixture only: inventory presentation and non-dialing profile preflight."""
import sqlite3
from pathlib import Path
from datetime import datetime, timezone
from playwright.sync_api import expect


def exercise_inventory_profiles(page, context, base, directory, output, results):
    now = datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.000000000Z')
    # These fixture-only rows have no periodic job or credential.
    with sqlite3.connect(Path(directory) / 'monik.db', timeout=5) as db:
        for ident, pinned in [('v14-vanished', 0), ('v14-selected-absent', 1)]:
            db.execute('''INSERT INTO services(id,agent_id,display_name,url,dial_target,source,pinned,first_seen_at,last_discovered_at)
                          VALUES(?,?,?,?,?,'listener+http',?,?,?)''',
                       (ident, 'audit-host', ident, 'http://127.0.0.1:29144', '127.0.0.1:29144', pinned, now, now))
            db.execute('''INSERT INTO service_presence(service_id,state,last_seen_at,missing_since,absent_snapshots)
                          VALUES(?,'missing',?,?,2)''', (ident, now, now))
    current=context.request.get(base+'/api/v1/services').json()['services']
    assert 'v14-vanished' not in [s['id'] for s in current]
    assert 'v14-selected-absent' in [s['id'] for s in current]
    page.goto(base+'/services',wait_until='domcontentloaded')
    group=page.locator('.service-group').filter(has=page.locator('.service-group-header').filter(has_text='Audit host'))
    group.locator('.service-group-header').click()
    expect(group.get_by_role('button',name='Переименовать: v14-vanished',exact=True)).to_have_count(0)
    expect(group.get_by_role('button',name='Переименовать: v14-selected-absent',exact=True)).to_be_visible()
    page.get_by_role('checkbox',name='Показывать исчезнувшие',exact=False).check()
    expect(group.get_by_role('button',name='Переименовать: v14-vanished',exact=True)).to_be_visible()
    expect(group).to_contain_text('Порт не найден в повторных полных обходах')
    page.screenshot(path=str(output/'v14-inactive-inventory.png'),full_page=True)
    results.append('inactive discoveries hidden by default and available explicitly; selected missing endpoint remains visible')
    page.goto(base+'/machines/audit-host#services',wait_until='domcontentloaded')
    page.get_by_role('button',name='Сервисы',exact=True).click()
    page.locator('.service-list').get_by_role('checkbox',name='Показывать исчезнувшие',exact=False).check()
    expect(page.locator('.service-list')).to_contain_text('v14-vanished')
    page.goto(base+'/add',wait_until='domcontentloaded')
    address=page.get_by_label('Доступный агенту HTTPS-адрес',exact=True)
    expect(address).to_have_value('https://46.150.103.61:8777')
    page.get_by_role('button',name='Проверить сертификат профиля',exact=True).click()
    expect(page.get_by_role('alert').filter(has_text='Сертификат контроллера не подходит')).to_be_visible()
    page.get_by_role('button',name='Адрес из настроек сервера',exact=True).click()
    expect(address).to_have_value(base)
    page.get_by_role('button',name='Проверить сертификат профиля',exact=True).click()
    expect(page.get_by_role('status').filter(has_text='Сертификат подходит адресу')).to_be_visible()
    with page.expect_download() as download_info:
        page.get_by_role('button',name='Скачать профиль автообнаружения',exact=True).click()
    import json
    download=download_info.value
    profile=json.loads(Path(download.path()).read_text())
    assert profile['controller_url']==base and profile['auto_discover'] is True
    assert 'PRIVATE KEY' not in profile['ca_cert_pem']
    assert 'credential' not in profile and 'enrollment_code' not in profile
    page.set_viewport_size({'width':390,'height':844})
    assert page.evaluate('document.documentElement.scrollWidth<=innerWidth+1')
    page.screenshot(path=str(output/'v14-profile-mobile.png'),full_page=True)
    page.set_viewport_size({'width':1440,'height':1000})
    results.append('public profile default is independent of saved LAN address; TLS mismatch blocks download; selected matching local profile contains public CA only; no external probe')
