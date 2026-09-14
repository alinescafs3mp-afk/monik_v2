"""Real controller/UI scheduling with synthetic offline agents, never executing a payload."""
import json
import sqlite3
from pathlib import Path
from datetime import datetime, timezone
from playwright.sync_api import expect


def exercise_rollouts(page, context, base, directory, access, output, results):
    now = datetime.now(timezone.utc).strftime('%Y-%m-%dT%H:%M:%S.000000000Z')
    # Disposable fixture database ONLY. These agents have no credentials/client processes.
    with sqlite3.connect(Path(directory) / 'monik.db', timeout=5) as db:
        for agent_id in ['rollout-a', 'rollout-b', 'rollout-c']:
            db.execute('''INSERT INTO agents(id,hostname,display_name,os,arch,credential_hash,desired_config,managed_ready,capabilities,last_live_at,session_id,created_at)
                VALUES(?,?,?,'linux','amd64','unusable-test-verifier','{}',1,?,?,?,?)''',
                (agent_id, agent_id, agent_id, json.dumps({'immutable_release_v1': {'status': 'supported'}}), now, 'synthetic-original', now))
    page.reload(wait_until='domcontentloaded')
    page.get_by_label('Путь к комплекту на контроллере', exact=True).fill(access['release_bundle'])
    page.get_by_role('checkbox', name='Зачислить корневой ключ TUF root', exact=False).check()
    page.get_by_role('button', name='Импортировать комплект', exact=True).click()
    release = page.locator('.release-entry').filter(has_text='browser-canary-fixture')
    expect(release).to_be_visible()
    for name in ['rollout-a', 'rollout-b', 'rollout-c']:
        page.locator('.update-target').filter(has_text=name).get_by_role('checkbox').check()
    page.get_by_label('Машин в обычной партии', exact=True).fill('2')
    page.get_by_label('Наблюдение после подтверждения, секунд', exact=True).fill('15')
    release.get_by_role('button', name='Раскатить выбранным', exact=True).click()
    plan = page.get_by_role('region', name='Подтверждение раскатки', exact=True)
    expect(plan).to_contain_text('Пробная: rollout-a')
    plan.get_by_role('button', name='Подтвердить запуск по партиям', exact=True).click()
    row = page.locator('.rollout-progress').first
    expect(row).to_contain_text('ждут своей очереди 2')
    row.get_by_role('button', name='Пауза раскатки', exact=True).click()
    expect(row).to_contain_text('На паузе')
    page.reload(wait_until='domcontentloaded')
    expect(row).to_contain_text('На паузе')
    row.get_by_role('button', name='Продолжить раскатку', exact=True).click()
    expect(row).to_contain_text('Продолжается исходная раскатка')
    row.get_by_role('button', name='Показать цели', exact=True).click()
    expect(row.locator('tbody tr')).to_have_count(3)
    page.screenshot(path=str(output / 'rollout-waves-desktop.png'), full_page=True)
    page.set_viewport_size({'width': 390, 'height': 844})
    assert page.evaluate('document.documentElement.scrollWidth<=window.innerWidth+1')
    page.screenshot(path=str(output / 'rollout-waves-mobile.png'), full_page=True)
    page.set_viewport_size({'width': 1440, 'height': 1000})
    row.get_by_role('link', name='Открыть операцию', exact=True).click()
    expect(page.locator('.rollout-progress')).to_be_visible()
    page.get_by_role('button', name='Отменить ещё не отправленные цели', exact=True).click()
    expect(page.locator('.rollout-progress')).to_contain_text('отменено 3')
    states=context.request.get(base + '/api/v1/rollouts').json()['rollouts']
    assert len(states)==1 and all(m['status']=='cancelled_before_execution' for m in states[0]['members'])
    results.append('signed fixture release -> frozen canary preview -> durable pause/reload/resume -> cancel held work; no synthetic update success claimed')
