#!/usr/bin/env python3
"""Actual public local CLI cycle; native-run owner only, fixed synthetic profile."""
import json
import os
import platform
import subprocess
import time

BASE = '/opt/monik-spark-native-control-canary-20261014'
SOCKET = '/run/monik-spark/control.sock'
ENV = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C', 'LC_ALL': 'C'}
DEADLINE = 0


def invoke(args, timeout=15):
    remaining = DEADLINE-time.monotonic()
    if remaining <= 0:
        raise RuntimeError('whole native CLI budget exhausted')
    result = subprocess.run(args, env=ENV, capture_output=True, timeout=min(timeout, remaining))
    if len(result.stdout) > 2**20:
        raise RuntimeError('local CLI output budget exceeded')
    try:
        body = json.loads(result.stdout)
    except ValueError:
        raise RuntimeError('local CLI did not emit bounded JSON') from None
    if result.returncode:
        raise RuntimeError('local CLI failed with exit '+str(result.returncode))
    return body


def action(kind, operation):
    receipt = invoke([BASE+'/monik-spark-agent', 'action', '--profile', 'synthetic-ready', '--action', kind, '--operation', operation, '--confirm-disruption', '--socket', SOCKET])
    if receipt.get('status') not in ('succeeded', 'running', 'accepted'):
        raise RuntimeError('local CLI action did not confirm an approved transition')
    return receipt


def state():
    snapshot = invoke([BASE+'/monik-spark-agent', 'status', '--socket', SOCKET])
    return next(s for s in snapshot['model_states'] if s['profile_id'] == 'synthetic-ready')


def wait(expected, readiness=None):
    end = min(DEADLINE, time.monotonic()+20)
    while time.monotonic() < end:
        current = state()
        if current['supervisor_state'] == expected and (readiness is None or current['readiness'] == readiness):
            return current
        time.sleep(.25)
    raise RuntimeError('native CLI state observation timeout')


def main():
    global DEADLINE
    started = time.monotonic()
    DEADLINE = started+75
    result = {'scope': 'actual local root CLI; synthetic-ready only; no Qwen/model/root-launcher proof', 'success': False, 'receipts': []}
    approved = False
    prefix = 'native-cli-canary-'+str(time.time_ns())
    try:
        if os.geteuid() != 0 or platform.machine() not in ('aarch64', 'arm64'):
            raise RuntimeError('native Linux ARM64 root required')
        verification = invoke([BASE+'/spark-control-canary', 'verify'])
        if not verification.get('success'):
            raise RuntimeError('exact isolated approved broker required')
        approved = True
        result['receipts'].append(action('stop', prefix+'-baseline-stop'))
        wait('stopped')
        result['receipts'].append(action('start', prefix+'-start'))
        before = wait('running', 'ready')
        replay = action('start', prefix+'-start')
        result['receipts'].append(replay)
        if state()['generation'] != before['generation']:
            raise RuntimeError('local CLI replay changed generation')
        result['receipts'].append(action('restart', prefix+'-restart'))
        after = wait('running', 'ready')
        if after['generation'] <= before['generation']:
            raise RuntimeError('local restart did not replace native generation')
        result['states'] = {'before_restart': before, 'after_restart': after}
        result['success'] = True
    except Exception as error:
        result['error'] = str(error)
    finally:
        if approved:
            try:
                DEADLINE = time.monotonic()+25
                result['cleanup_stop_receipt'] = action('stop', prefix+'-cleanup-stop')
                result['cleanup_state'] = wait('stopped', 'not_applicable')
                result['cleanup_confirmed'] = True
            except Exception as error:
                result.update(success=False, cleanup_confirmed=False, cleanup_error=str(error))
        result['duration_ms'] = int((time.monotonic()-started)*1000)
        print(json.dumps(result, indent=2))
        if not result['success']:
            raise SystemExit(1)


if __name__ == '__main__':
    main()
