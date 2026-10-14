#!/usr/bin/env python3
"""Real API canary. The parent executes this after native staging and review."""
import argparse
import copy
import datetime
import hashlib
import http.cookiejar
import json
import os
import pathlib
import re
import ssl
import stat
import threading
import time
import urllib.error
import urllib.request
import uuid

BASE_URL = 'https://127.0.0.1:18786'
AGENT_ID = 'c7966ecd-e521-4e18-9142-dff2bc9d218e'
ACCESS = pathlib.Path('/home/jericho/.cache/sol-monik-spark-v2-20261014/native-readonly-controller/fixture-access.json')
CA = ACCESS.parent/'tls/ca.crt'
SECRETS = ['MONIK_SYNTHETIC_PASSWORD_CANARY', 'MONIK_SYNTHETIC_BEARER_CANARY', 'MONIK_SYNTHETIC_LITERAL_REDACTION_20261014', 'hf_SYNTHETICCANARYTOKEN123456', 'sk-SYNTHETICCANARYTOKEN123456']
PROFILES = {'synthetic-ready', 'synthetic-starting', 'synthetic-preflight'}
TERMINAL = {'succeeded', 'rejected', 'failed', 'rolled_back', 'unsupported', 'expired', 'cancelled_before_execution'}


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, *args, **kwargs):
        raise RuntimeError('controller redirect refused')


class API:
    def __init__(self, credential):
        self.csrf = ''
        self.jar = http.cookiejar.CookieJar()
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}), urllib.request.HTTPSHandler(context=ssl.create_default_context(cafile=str(CA))), urllib.request.HTTPCookieProcessor(self.jar), NoRedirect())
        status, body = self.request('POST', '/api/v1/login', {'username': credential['username'], 'password': credential['password']})
        check(status == 200 and body.get('role') == 'owner', 'isolated owner login failed')
        self.csrf = body['csrf']
        self.logged_out = False

    def request(self, method, path, body=None):
        check(path.startswith('/api/v1/'), 'fixed API prefix required')
        data = None if body is None else json.dumps(body, separators=(',', ':')).encode()
        req = urllib.request.Request(BASE_URL+path, data=data, method=method, headers={'Content-Type': 'application/json', 'X-CSRF-Token': self.csrf, 'Origin': BASE_URL})
        try:
            with self.opener.open(req, timeout=5) as response:
                raw = response.read(2**20+1)
                check(len(raw) <= 2**20, 'API response byte budget exceeded')
                return response.status, json.loads(raw)
        except urllib.error.HTTPError as error:
            raw = error.read(2**16)
            try:
                body = json.loads(raw)
            except ValueError:
                body = {'error': 'non-json response'}
            return error.code, body

    def logout(self):
        if self.logged_out:
            return
        code, body = self.request('POST', '/api/v1/logout', {})
        check(code == 200 and body.get('ok'), 'owned API session logout failed')
        self.logged_out = True


def check(value, message):
    if not value:
        raise RuntimeError(message)


def safe_error(body):
    value = body.get('error', {})
    if isinstance(value, dict):
        return value.get('code', 'unknown')
    return str(value)[:80]


def shutdown_witness(detail, state, profile, target, now=None):
    """Couple terminal stop proof to fresh native state and exact authority."""
    try:
        evidence = target['evidence']
        control = detail['spark']['control_state']
        public = next(p for p in detail['spark']['inventory']['profiles'] if p['profile_id'] == profile)
        state_time = datetime.datetime.fromisoformat(state['observed_at'].replace('Z', '+00:00'))
        proof_time = datetime.datetime.fromisoformat(evidence['observed_at'].replace('Z', '+00:00'))
        now = now or datetime.datetime.now(datetime.timezone.utc)
        supervisor = state['supervisor_state']
        identity = evidence['instance_identity']
        generation = state['generation']
        typed_state = supervisor == 'stopped' and state.get('reason', '') == '' and identity == 'stopped'
        if supervisor == 'failed':
            typed_state = state.get('reason') == 'failed_service_shutdown_verified' and evidence.get('reason') == 'failed_service_shutdown_verified' and isinstance(identity, str) and re.fullmatch(r'sha256:[0-9a-f]{64}', identity) is not None
        return (target.get('agent_id') == AGENT_ID and target.get('status') == 'succeeded' and target.get('stage') == 'model_stopped'
                and state['profile_id'] == profile and state['quality'] == 'ok' and state['readiness'] == 'not_applicable' and typed_state
                and evidence.get('shutdown_verified') is True and evidence.get('readiness') == 'not_applicable' and evidence.get('supervisor_state') == supervisor
                and type(generation) is int and generation > 0 and type(evidence.get('generation')) is int and evidence['generation'] == generation
                and evidence.get('profile_id') == profile and evidence.get('profile_revision') == public['profile_revision']
                and control.get('enabled') is True and evidence.get('control_epoch') == control['epoch'] and evidence.get('agent_session') == detail['agent_session']
                and datetime.timedelta(seconds=-5) <= now-state_time <= datetime.timedelta(seconds=30) and proof_time <= state_time <= now+datetime.timedelta(seconds=5))
    except (KeyError, TypeError, ValueError, StopIteration):
        return False


class Run:
    def __init__(self, credential, limit, journal):
        self.dispatch_journal = journal
        self.api = API(credential)
        self.credential = credential
        self.deadline = time.monotonic()+limit
        self.checks = []
        self.streams = []
        self.sessions = [self.api]
        self.operations = []
        self.touched = set()
        self.start_dispatches = {}
        self.shutdowns = {}
        self.prefix = 'synthetic-control-'+uuid.uuid4().hex

    def budget(self):
        check(time.monotonic() < self.deadline, 'whole canary time budget exhausted')

    def detail(self):
        self.budget()
        code, detail = self.api.request('GET', '/api/v1/sparks/'+AGENT_ID)
        check(code == 200 and detail.get('agent_id') == AGENT_ID and detail.get('online'), 'isolated Spark online detail unavailable')
        check(detail['spark']['control_state']['enabled'], 'isolated local control not enabled')
        inventory = detail['spark']['inventory']
        check(inventory.get('complete'), 'canary inventory incomplete')
        found = {p['profile_id'] for p in inventory['profiles']}
        check(found == PROFILES, 'canary policy contains unexpected/missing profiles')
        for p in inventory['profiles']:
            check(p['engine'] == 'custom' and p['configured_model_ref'] == 'synthetic-test-only-not-Qwen-no-model' and p['supervisor'] == 'systemd-system', 'synthetic/nonroot scope projection changed')
        return detail

    def binding(self, profile, detail=None, logs=False):
        check(profile in PROFILES, 'only fixed canary profiles permitted')
        detail = detail or self.detail()
        public = next(p for p in detail['spark']['inventory']['profiles'] if p['profile_id'] == profile)
        state = next(s for s in detail['spark']['model_states'] if s['profile_id'] == profile)
        check(state['quality'] == 'ok', 'fresh native state required')
        result = {'profile_id': profile, 'profile_revision': public['profile_revision'], 'expected_generation': state['generation'], 'control_epoch': detail['spark']['control_state']['epoch'], 'expected_agent_session': detail['agent_session']}
        if not logs:
            result['confirm_disruption'] = True
        return result

    def poll(self, function, condition, label, seconds=35):
        end = min(self.deadline, time.monotonic()+seconds)
        last = None
        while time.monotonic() < end:
            last = function()
            if condition(last):
                return last
            time.sleep(.4)
        raise RuntimeError('timed out: '+label)

    def state(self, profile, detail=None):
        detail = detail or self.detail()
        return next(s for s in detail['spark']['model_states'] if s['profile_id'] == profile)

    def wait_state(self, profile, state, readiness=None):
        return self.poll(lambda: self.state(profile), lambda s: s['supervisor_state'] == state and (readiness is None or s['readiness'] == readiness), f'{profile} {state}/{readiness}')

    def wait_shutdown(self, profile, operation):
        check(len(operation['targets']) == 1, 'single exact native stop target required')
        target = operation['targets'][0]

        def observe():
            detail = self.detail()
            state = self.state(profile, detail)
            return detail, state

        _, state = self.poll(observe, lambda observed: shutdown_witness(observed[0], observed[1], profile, target), profile+' exact verified shutdown')
        self.shutdowns[profile] = target
        return state

    def operation(self, action, profile, label, binding=None, key=None):
        self.budget()
        params = binding or self.binding(profile)
        check(params['profile_id'] in PROFILES, 'operation target scope invalid')
        body = {'action': 'model.'+action, 'client_request_key': key or self.prefix+'-'+label, 'target_ids': [AGENT_ID], 'target_mode': 'selected', 'params': params}
        # A committed dispatch with a lost HTTP response still belongs to cleanup.
        self.touched.add(profile)
        self.dispatch_journal.write(json.dumps(body, separators=(',', ':'))+'\n')
        self.dispatch_journal.flush()
        os.fsync(self.dispatch_journal.fileno())
        if action in ('start', 'restart'):
            self.start_dispatches[body['client_request_key']] = time.monotonic()
        code, op = self.api.request('POST', '/api/v1/operations', body)
        if code not in (200, 202):
            self.start_dispatches.pop(body['client_request_key'], None)
        check(code in (200, 202) and op.get('operation_id'), 'canary operation submission failed: '+safe_error(op))
        self.operations.append(op['operation_id'])
        return op, body

    def wait_operation(self, operation_id, succeeded=True):
        def get():
            code, op = self.api.request('GET', '/api/v1/operations/'+operation_id)
            check(code == 200, 'operation evidence unavailable')
            return op
        op = self.poll(get, lambda o: all(t['status'] in TERMINAL for t in o['targets']), 'terminal operation '+operation_id)
        if succeeded:
            check(all(t['status'] == 'succeeded' for t in op['targets']), 'operation did not succeed: '+','.join(t['status'] for t in op['targets']))
        return op

    def record(self, name, **evidence):
        self.checks.append({'name': name, 'status': 'PASS', **evidence})

    def hold_stopped(self, profile, name, seconds=25):
        start = time.monotonic()
        check(start+seconds < self.deadline, 'full no-resurrection observation does not fit remaining run budget')
        while time.monotonic() < start+seconds:
            detail = self.detail()
            state = self.state(profile, detail)
            check(shutdown_witness(detail, state, profile, self.shutdowns.get(profile, {})), 'cancelled starting service resurrected or exact shutdown proof changed')
            time.sleep(min(1, max(0, start+seconds-time.monotonic())))
        self.record(name, required_seconds=seconds, actual_seconds=time.monotonic()-start)

    def get_operation(self, ident):
        code, op = self.api.request('GET', '/api/v1/operations/'+ident)
        check(code == 200, 'operation evidence unavailable')
        return op

    def stream(self, api, profile):
        params = self.binding(profile, logs=True)
        code, body = api.request('POST', f'/api/v1/sparks/{AGENT_ID}/logs', params)
        check(code in (200, 201) and body.get('stream_id'), 'bound log stream create failed')
        stream = Stream(api, body['stream_id'])
        stream.start()
        self.streams.append(stream)
        return stream, params

    def run(self):
        self.detail()
        # Resolve an owned clean state with a real typed stop. No other unit can be addressed.
        op, _ = self.operation('stop', 'synthetic-ready', 'baseline-stop')
        self.wait_shutdown('synthetic-ready', self.wait_operation(op['operation_id']))
        before = self.binding('synthetic-ready')
        errors = {'expected_generation': 'stale_generation_or_state', 'profile_revision': 'profile_revision_changed', 'control_epoch': 'stale_or_disabled_control', 'expected_agent_session': 'agent_offline_or_session_changed'}
        for field, value in [('expected_generation', before['expected_generation']+1), ('profile_revision', 'sha256:'+'0'*64), ('control_epoch', 'stale-canary-epoch'), ('expected_agent_session', 'stale-canary-session')]:
            params = dict(before, **{field: value})
            req = {'action': 'model.start', 'client_request_key': self.prefix+'-denied-'+field, 'target_ids': [AGENT_ID], 'params': params}
            code, body = self.api.request('POST', '/api/v1/operations', req)
            check(code == 409 and safe_error(body) == errors[field] and not body.get('operation_id'), 'stale binding rejection did not prove the expected fence')
            self.record('controller_denies_'+field, http_status=code, error_code=safe_error(body))
        injected = dict(before, command='/usr/bin/true')
        code, body = self.api.request('POST', '/api/v1/operations', {'action': 'model.start', 'client_request_key': self.prefix+'-unknown-command', 'target_ids': [AGENT_ID], 'params': injected})
        check(code == 400 and safe_error(body) == 'bad_params' and not body.get('operation_id'), 'untyped remote command not rejected')
        self.record('controller_denies_untyped_command', http_status=code)
        after_denials = self.binding('synthetic-ready')
        check(before == after_denials, 'state changed during denied requests')
        start, payload = self.operation('start', 'synthetic-ready', 'ready-start')
        done = self.wait_operation(start['operation_id'])
        ready = self.wait_state('synthetic-ready', 'running', 'ready')
        check(ready['generation'] > before['expected_generation'], 'start did not create a fresh native generation')
        self.record('real_start_and_readiness', operation_id=start['operation_id'], target=done['targets'][0], state=ready)
        # Same stale original payload must return its original durable operation; no second start.
        code, replay = self.api.request('POST', '/api/v1/operations', payload)
        check(code == 200 and replay.get('operation_id') == start['operation_id'], 'idempotent replay did not return original operation')
        conflicting = copy.deepcopy(payload)
        conflicting['params']['expected_generation'] += 1
        code, conflict = self.api.request('POST', '/api/v1/operations', conflicting)
        check(code == 409 and safe_error(conflict) == 'idempotency_conflict', 'conflicting replay not denied')
        self.record('controller_replay_and_conflict', operation_id=start['operation_id'], http_status=code, generation=self.state('synthetic-ready')['generation'])
        stream, old_log_binding = self.stream(self.api, 'synthetic-ready')
        self.poll(lambda: stream.snapshot(), lambda s: s['redacted_marker'] and s['redactions'] >= 5 and s['ansi_removed'], 'real journal redaction', seconds=20)
        redaction = stream.snapshot()
        check(not redaction['leak_detected'] and redaction['redactions'] >= 5 and redaction['ansi_removed'], 'redaction output incomplete/unsafe')
        self.record('real_journal_secret_and_ansi_redaction', **redaction)
        restart, _ = self.operation('restart', 'synthetic-ready', 'ready-restart')
        done = self.wait_operation(restart['operation_id'])
        current = self.wait_state('synthetic-ready', 'running', 'ready')
        check(current['generation'] > ready['generation'], 'restart did not replace native generation')
        # The agent lease/batch path combines authority and source invalidation;
        # generation change plus the stale binding denial still prove this cause.
        self.poll(lambda: stream.snapshot(), lambda s: s['closed_reason'] in ('source_changed_or_expired', 'source_closed', 'source_changed', 'authority_or_source_changed'), 'log source generation revocation', seconds=15)
        code, body = self.api.request('POST', f'/api/v1/sparks/{AGENT_ID}/logs', old_log_binding)
        check(code == 409, 'stale log binding survived restart')
        self.record('real_staged_restart_and_log_generation_revocation', operation_id=restart['operation_id'], target=done['targets'][0], state=current, log_closed=stream.snapshot()['closed_reason'], stale_log_http_status=code)
        # Fresh independent session revoked via its own logout; no owner account changes.
        second = API(self.credential)
        self.sessions.append(second)
        revoked, _ = self.stream(second, 'synthetic-ready')
        self.poll(lambda: revoked.snapshot(), lambda s: s['event_count'] > 0, 'second owner log stream begins', seconds=15)
        second.logout()
        self.poll(lambda: revoked.snapshot(), lambda s: s['closed_reason'] in ('authority_revoked', 'authority_or_source_changed'), 'owner session log revocation', seconds=10)
        code, body = second.request('GET', f'/api/v1/sparks/{AGENT_ID}/logs/{revoked.ident}/events')
        check(code == 401 and safe_error(body) == 'unauthenticated', 'logged-out session retained log stream access')
        self.record('session_logout_revokes_live_logs', closed_reason=revoked.snapshot()['closed_reason'], logged_out_stream_http_status=code, logged_out_stream_error_code=safe_error(body))
        op, _ = self.operation('stop', 'synthetic-ready', 'ready-final-stop')
        self.wait_shutdown('synthetic-ready', self.wait_operation(op['operation_id']))
        self.record('real_stop', operation_id=op['operation_id'])
        # The 20-second native nonroot ExecStartPre gives a real starting window.
        initial, _ = self.operation('stop', 'synthetic-starting', 'slow-baseline-stop')
        self.wait_shutdown('synthetic-starting', self.wait_operation(initial['operation_id']))
        initial_start, _ = self.operation('start', 'synthetic-starting', 'slow-start')
        initial_starting = self.wait_state('synthetic-starting', 'starting')
        self.wait_operation(initial_start['operation_id'])
        initial_running = self.wait_state('synthetic-starting', 'running', 'ready')
        self.record('slow_start_reaches_running_readiness', operation_id=initial_start['operation_id'], starting_state=initial_starting, ready_state=initial_running)
        starting, _ = self.operation('restart', 'synthetic-starting', 'slow-restart')
        observed = self.poll(lambda: self.state('synthetic-starting'), lambda s: s['supervisor_state'] == 'starting' and s['generation'] > initial_running['generation'], 'real linked restart start is starting')
        stop, _ = self.operation('stop', 'synthetic-starting', 'slow-stop')
        stop_done = self.wait_operation(stop['operation_id'])
        self.wait_shutdown('synthetic-starting', stop_done)
        start_done = self.wait_operation(starting['operation_id'], succeeded=False)
        check(start_done['targets'][0]['status'] == 'failed' and start_done['targets'][0]['stage'] == 'restart_start_superseded', 'restart workflow was not explicitly superseded after stop')
        self.record('real_restart_starting_stop', restart_operation_id=starting['operation_id'], stop_operation_id=stop['operation_id'], starting_state=observed, restart_target=start_done['targets'][0], stop_target=stop_done['targets'][0])
        # Remain stopped across a full original pre-start delay plus report interval.
        self.hold_stopped('synthetic-starting', 'restart_starting_stop_does_not_resurrect')
        # Also retain the independent plain start -> distinct stop case. Its
        # final source must prove cancellation of the exact observed start.
        standalone, standalone_payload = self.operation('start', 'synthetic-starting', 'plain-start')
        plain_starting = self.wait_state('synthetic-starting', 'starting')
        plain_stop, _ = self.operation('stop', 'synthetic-starting', 'plain-stop')
        plain_stop_done = self.wait_operation(plain_stop['operation_id'])
        plain_stopped = self.wait_shutdown('synthetic-starting', plain_stop_done)
        original = self.wait_operation(standalone['operation_id'], succeeded=False)
        check(original['targets'][0]['status'] == 'failed' and original['targets'][0]['stage'] == 'start_cancelled_after_stop', 'exact observed standalone start was not terminally cancelled by verified stop')
        self.record('standalone_start_distinct_stop_physical_proof', start_operation_id=standalone['operation_id'], stop_operation_id=plain_stop['operation_id'], starting_state=plain_starting, stopped_state=plain_stopped, start_target=original['targets'][0], stop_target=plain_stop_done['targets'][0], terminal_cancellation_proved=True)
        code, replay = self.api.request('POST', '/api/v1/operations', standalone_payload)
        check(code in (200, 202) and replay.get('operation_id') == standalone['operation_id'], 'plain start replay created a new operation')
        self.hold_stopped('synthetic-starting', 'standalone_start_replay_does_not_resurrect')
        # Fresh authority must recover availability; a persistent unresolved
        # reservation is an explicit product FAIL, never hidden as a harness PASS.
        fresh_params = self.binding('synthetic-starting')
        fresh_payload = {'action': 'model.start', 'client_request_key': self.prefix+'-fresh-after-plain-stop', 'target_ids': [AGENT_ID], 'target_mode': 'selected', 'params': fresh_params}
        self.touched.add('synthetic-starting')
        self.dispatch_journal.write(json.dumps(fresh_payload, separators=(',', ':'))+'\n')
        self.dispatch_journal.flush()
        os.fsync(self.dispatch_journal.fileno())
        self.start_dispatches[fresh_payload['client_request_key']] = time.monotonic()
        code, fresh = self.api.request('POST', '/api/v1/operations', fresh_payload)
        if code not in (200, 202):
            self.start_dispatches.pop(fresh_payload['client_request_key'], None)
            self.checks.append({'name': 'fresh_authority_reallows_start_after_plain_stop', 'status': 'FAIL', 'http_status': code, 'error_code': safe_error(fresh), 'original_start_target': original['targets'][0]})
            raise RuntimeError('fresh start after verified stop blocked: '+safe_error(fresh))
        check(fresh.get('operation_id'), 'fresh start response lacked durable operation ID')
        self.operations.append(fresh['operation_id'])
        self.wait_state('synthetic-starting', 'starting')
        fresh_stop, _ = self.operation('stop', 'synthetic-starting', 'fresh-after-stop-clean')
        self.wait_shutdown('synthetic-starting', self.wait_operation(fresh_stop['operation_id']))
        self.hold_stopped('synthetic-starting', 'fresh_start_stop_does_not_resurrect')
        self.record('fresh_authority_reallows_start_after_plain_stop', operation_id=fresh['operation_id'], stop_operation_id=fresh_stop['operation_id'])

    def cleanup(self):
        errors = []
        for stream in self.streams:
            try:
                if not stream.api.logged_out:
                    code, _ = stream.api.request('DELETE', f'/api/v1/sparks/{AGENT_ID}/logs/{stream.ident}')
                    check(code in (200, 404), 'owned log delete failed')
            except Exception as error:
                errors.append(str(error))
        for stream in self.streams:
            stream.thread.join(timeout=6)
            if stream.thread.is_alive():
                errors.append('owned SSE reader still active')
            final = stream.snapshot()
            if final['leak_detected'] or final.get('reader_error'):
                errors.append('owned SSE final evidence reports leak/reader error')
        # Fresh authority required; any failure is reported as unconfirmed cleanup.
        cleanup_deadline = time.monotonic()+50
        for profile in sorted(self.touched):
            try:
                self.deadline = cleanup_deadline
                op, _ = self.operation('stop', profile, 'cleanup-'+profile, key=self.prefix+'-cleanup-'+profile)
                self.wait_shutdown(profile, self.wait_operation(op['operation_id']))
            except Exception as error:
                errors.append('owned stop '+profile+': '+str(error))
        # A lost dispatch response may conceal a queued start. Observe stopped
        # through its maximum 30s lease + 5s request budget + 5s report margin.
        watch_until = max(self.start_dispatches.values(), default=0)+40
        if watch_until > cleanup_deadline:
            errors.append('pending-start lease observation exceeds cleanup budget')
        while time.monotonic() < min(watch_until, cleanup_deadline):
            try:
                for profile in sorted(self.touched):
                    detail = self.detail()
                    state = self.state(profile, detail)
                    if not shutdown_witness(detail, state, profile, self.shutdowns.get(profile, {})):
                        errors.append('owned profile changed during pending-start cleanup: '+profile)
                        op, _ = self.operation('stop', profile, 'late-cleanup-'+profile, key=self.prefix+'-late-cleanup-'+profile)
                        self.wait_shutdown(profile, self.wait_operation(op['operation_id']))
            except Exception as error:
                errors.append('pending-start cleanup observation: '+str(error))
                break
            time.sleep(.5)
        for session in self.sessions:
            try:
                session.logout()
            except Exception as error:
                errors.append(str(error))
        return {'confirmed': not errors, 'errors': errors, 'streams_final': [s.snapshot() for s in self.streams], 'pending_start_lease_window_observed': time.monotonic() >= watch_until, 'scope': 'only touched fixed canary profiles, owned streams and sessions; broker/files cleanup is native owner responsibility'}


class Stream:
    def __init__(self, api, ident):
        self.api = api
        self.ident = ident
        self.lock = threading.Lock()
        self.data = {'event_count': 0, 'log_count': 0, 'redactions': 0, 'redacted_marker': False, 'ansi_removed': False, 'leak_detected': False, 'closed_reason': '', 'bounded_record_sha256': []}
        self.thread = threading.Thread(target=self.read, daemon=True)

    def start(self):
        self.thread.start()

    def snapshot(self):
        with self.lock:
            return copy.deepcopy(self.data)

    def read(self):
        event = ''
        end = time.monotonic()+45
        req = urllib.request.Request(BASE_URL+f'/api/v1/sparks/{AGENT_ID}/logs/{self.ident}/events', headers={'Accept': 'text/event-stream'})
        try:
            with self.api.opener.open(req, timeout=5) as response:
                check(response.headers.get_content_type() == 'text/event-stream', 'SSE content type changed')
                while time.monotonic() < end:
                    raw = response.readline(2**16+1)
                    check(len(raw) <= 2**16, 'SSE line byte budget exceeded')
                    if not raw:
                        break
                    line = raw.decode('utf-8').strip()
                    if line.startswith('event: '):
                        event = line[7:]
                    elif line.startswith('data: '):
                        body = json.loads(line[6:])
                        with self.lock:
                            self.data['event_count'] += 1
                            if event == 'closed':
                                self.data['closed_reason'] = body.get('reason', 'unknown')
                            if event == 'log':
                                text = body['text']
                                # Never preserve raw leaked credentials, even synthetic ones.
                                self.data['leak_detected'] |= any(secret in text for secret in SECRETS)
                                self.data['log_count'] += 1
                                self.data['redactions'] += text.count('[redacted]')
                                self.data['redacted_marker'] |= 'MONIK_SYNTHETIC_LOG_CANARY' in text
                                self.data['ansi_removed'] |= 'MONIK_SYNTHETIC_ANSI_CANARY' in text and '\x1b' not in text
                                if len(self.data['bounded_record_sha256']) < 32:
                                    self.data['bounded_record_sha256'].append(hashlib.sha256(text.encode()).hexdigest())
        except Exception as error:
            with self.lock:
                self.data['reader_error'] = type(error).__name__


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument('--output', required=True, type=pathlib.Path)
    parser.add_argument('--execute', action='store_true', help='parent-owned real synthetic controller mutations')
    parser.add_argument('--time-budget', type=int, default=240)
    args = parser.parse_args()
    check(args.execute, 'explicit --execute required')
    check(120 <= args.time_budget <= 300, 'whole-run budget must be 120..300 seconds')
    info = ACCESS.lstat()
    check(stat.S_ISREG(info.st_mode) and not info.st_mode & 0o077 and info.st_nlink == 1 and info.st_size <= 4096, 'private regular fixture credentials required')
    credential = json.loads(ACCESS.read_text())
    check(credential['url'] == BASE_URL, 'credential fixture URL differs from exact controller')
    args.output.parent.mkdir(parents=True, exist_ok=True)
    # Reserve both evidence artifacts before any login or controller mutation.
    result_fd = os.open(args.output, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o600)
    journal_path = args.output.with_suffix('.dispatch.jsonl')
    journal_fd = os.open(journal_path, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o600)
    journal = os.fdopen(journal_fd, 'w')
    started = time.monotonic()
    result = {'started_at': datetime.datetime.now(datetime.timezone.utc).isoformat(), 'agent_id': AGENT_ID, 'controller_url': BASE_URL, 'synthetic': True, 'model_loaded': False, 'native_preflight_cancellation': 'separate native helper evidence required', 'checks': []}
    run = None
    try:
        run = Run(credential, args.time_budget, journal)
        run.run()
        result['success'] = True
    except Exception as error:
        result.update(success=False, error=str(error))
    finally:
        if run:
            result['checks'] = run.checks
            result['operation_ids'] = list(dict.fromkeys(run.operations))
            result['cleanup'] = run.cleanup()
            result['success'] = result.get('success', False) and result['cleanup']['confirmed']
        result['duration_ms'] = int((time.monotonic()-started)*1000)
        journal.close()
        with os.fdopen(result_fd, 'w') as output:
            json.dump(result, output, indent=2)
            output.write('\n')
        print(json.dumps({'success': result.get('success'), 'checks': len(result['checks']), 'duration_ms': result['duration_ms'], 'result_path': str(args.output)}))
        if not result.get('success'):
            raise SystemExit(1)


if __name__ == '__main__':
    main()
