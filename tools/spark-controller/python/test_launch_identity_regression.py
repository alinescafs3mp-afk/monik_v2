"""Offline launch identity controls; no host commands or model effects."""
import copy
import os
from pathlib import Path
import tempfile
import unittest
from unittest.mock import MagicMock, patch

import test_legacy_shared as shared

core, legacy, supervisor = shared.core, shared.legacy, shared.base.supervisor


class DeclarationControls(unittest.TestCase):
    def setUp(self):
        self.fixture = shared.LegacyAdapter()
        self.fixture.setUp()
        self.addCleanup(self.fixture.doCleanups)
        self.row = self.fixture.row
        self.container = self.fixture.container
        self.container['HostConfig'].update(copy.deepcopy(legacy.RESOURCE_DEFAULTS))
        self.container['HostConfig'].setdefault('OomKillDisable', False)

    def qualify(self):
        return legacy.check_declaration(self.row, self.container, shared.KEY,
                                        self.fixture.adapter)

    def test_root_observed_null_image_user_and_empty_container_user(self):
        self.fixture.defaults['User'] = None
        with patch.dict(legacy.IMAGE_CONFIG_PINS, {
            self.row['runtime']['image_id']: legacy.digest(self.fixture.defaults)
        }):
            self.assertTrue(self.qualify())
            self.container['Config']['User'] = '1000'
            with self.assertRaises(core.ObservationError):
                self.qualify()

    def test_exact_label_disable_default_and_negative_security_options(self):
        self.assertTrue(self.qualify())
        self.container['HostConfig']['SecurityOpt'] = ['label=disable']
        self.assertTrue(self.qualify())
        for options in [[], ['seccomp=unconfined'], ['label=disable', 'seccomp=unconfined'],
                        ['label=disable', 'label=disable'], 'label=disable']:
            with self.subTest(options=options):
                self.container['HostConfig']['SecurityOpt'] = options
                with self.assertRaises(core.ObservationError):
                    self.qualify()

    def test_entrypoint_workdir_user_image_and_resource_drift_still_fail(self):
        for section, field, value in [
            ('Config', 'Entrypoint', ['/bin/sh']),
            ('Config', 'WorkingDir', '/changed'),
            ('Config', 'User', '1000'),
            ('HostConfig', 'Memory', 1024),
            ('HostConfig', 'Runtime', 'unqualified'),
            ('HostConfig', 'CapAdd', ['SYS_ADMIN']),
        ]:
            before = copy.deepcopy(self.container)
            with self.subTest(field=field):
                self.container[section][field] = value
                with self.assertRaises(core.ObservationError):
                    self.qualify()
            self.container.clear()
            self.container.update(before)

    def test_mount_order_does_not_change_new_capture_or_observation(self):
        frozen = self.qualify()
        self.fixture.run['legacy_declaration_sha256'] = frozen
        self.container['Mounts'].reverse()
        self.assertEqual(frozen, self.qualify())
        self.assertTrue(self.fixture.adapter.observe(
            self.row, self.fixture.run)['engine_active'])

    def test_oom_false_to_null_start_transition_preserves_capture(self):
        self.container['HostConfig']['OomKillDisable'] = False
        frozen = self.qualify()
        self.fixture.run['legacy_declaration_sha256'] = frozen
        self.container['HostConfig']['OomKillDisable'] = None
        self.assertEqual(frozen, self.qualify())
        self.assertTrue(self.fixture.adapter.observe(
            self.row, self.fixture.run)['engine_active'])

    def test_oom_true_missing_and_wrong_types_are_not_qualified(self):
        self.container['HostConfig']['OomKillDisable'] = False
        frozen = self.qualify()
        for value in [True, 0, '', 'false']:
            self.container['HostConfig']['OomKillDisable'] = value
            with self.subTest(value=value):
                with self.assertRaises(core.ObservationError):
                    self.qualify()
                self.assertFalse(legacy.declaration_matches(self.container, frozen))
        del self.container['HostConfig']['OomKillDisable']
        with self.assertRaises(core.ObservationError):
            self.qualify()
        self.assertFalse(legacy.declaration_matches(self.container, frozen))

    def test_historical_null_oom_captures_remain_exact_and_unmodified(self):
        self.container['HostConfig']['OomKillDisable'] = None
        payload = {k: copy.deepcopy(self.container.get(k)) for k in
                   ('Name', 'Image', 'Config', 'HostConfig', 'Mounts')}
        historical_raw = legacy.digest(payload)
        payload['Mounts'] = sorted(payload['Mounts'], key=lambda m: shared.json.dumps(
            m, sort_keys=True, separators=(',', ':'), allow_nan=False))
        historical_canonical = legacy.digest(payload)
        for frozen in [historical_raw, historical_canonical]:
            self.fixture.run['legacy_declaration_sha256'] = frozen
            before = copy.deepcopy(self.fixture.run)
            with self.subTest(frozen=frozen):
                self.assertTrue(self.fixture.adapter.observe(
                    self.row, self.fixture.run)['engine_active'])
                self.assertEqual(before, self.fixture.run)

    def test_historical_unsorted_false_oom_capture_survives_null_only(self):
        self.container['HostConfig']['OomKillDisable'] = False
        frozen = legacy.digest({k: self.container.get(k) for k in
                                ('Name', 'Image', 'Config', 'HostConfig', 'Mounts')})
        self.assertNotEqual(frozen, self.qualify())
        self.fixture.run['legacy_declaration_sha256'] = frozen
        before_run = copy.deepcopy(self.fixture.run)
        self.container['HostConfig']['OomKillDisable'] = None
        self.assertTrue(self.fixture.adapter.observe(
            self.row, self.fixture.run)['engine_active'])
        self.assertEqual(before_run, self.fixture.run)
        before_container = copy.deepcopy(self.container)
        for section, field, value in [('HostConfig', 'Memory', 1),
                                      ('Config', 'User', '1000')]:
            with self.subTest(field=field):
                self.container[section][field] = value
                with self.assertRaises(core.ObservationError):
                    self.fixture.adapter.observe(self.row, self.fixture.run)
            self.container.clear()
            self.container.update(copy.deepcopy(before_container))
        self.container['Mounts'][0]['Source'] = '/changed'
        with self.assertRaises(core.ObservationError):
            self.fixture.adapter.observe(self.row, self.fixture.run)

    def test_changed_or_duplicate_mounts_remain_refused(self):
        frozen = self.qualify()
        original = copy.deepcopy(self.container['Mounts'])
        for field, value in [('Source', '/changed'), ('Destination', '/changed'),
                             ('RW', True), ('Type', 'volume'),
                             ('Propagation', 'shared')]:
            self.container['Mounts'] = copy.deepcopy(original)
            self.container['Mounts'][0][field] = value
            with self.subTest(field=field):
                self.assertFalse(legacy.declaration_matches(self.container, frozen))
        self.container['Mounts'] = copy.deepcopy(original) + [copy.deepcopy(original[0])]
        self.assertFalse(legacy.declaration_matches(self.container, frozen))
        with self.assertRaises(core.ObservationError):
            self.qualify()

    def test_exact_historical_digest_still_matches_without_mutating_run(self):
        historical = legacy.digest({k: self.container.get(k) for k in
                                    ('Name', 'Image', 'Config', 'HostConfig', 'Mounts')})
        self.fixture.run['legacy_declaration_sha256'] = historical
        before = copy.deepcopy(self.fixture.run)
        self.assertTrue(self.fixture.adapter.observe(
            self.row, self.fixture.run)['engine_active'])
        self.assertEqual(before, self.fixture.run)
        self.container['Mounts'][0]['Source'] = '/changed'
        self.assertFalse(legacy.declaration_matches(self.container, historical))

    def test_repeated_adoption_preserves_historical_digest(self):
        historical = legacy.digest({k: self.container.get(k) for k in
                                    ('Name', 'Image', 'Config', 'HostConfig', 'Mounts')})
        self.assertNotEqual(historical, self.qualify())
        with tempfile.TemporaryDirectory() as directory:
            ctl = core.Controller({'models': legacy.definitions()}, self.fixture.adapter,
                                 store=core.StateStore(directory, False),
                                 log_root=directory, protected=False)
            argv = legacy.original_qwen_owner_argv(self.row, shared.KEY)
            with patch.object(legacy, 'qualify', return_value=(
                    {'control_safe': False}, [], shared.KEY)), \
                    patch.object(self.fixture.adapter, 'process_cgroups', return_value=[
                        '/system.slice/docker-' + shared.CID + '.scope']), \
                    patch.object(legacy.os, 'readlink', return_value='/usr/bin/docker'), \
                    patch.object(Path, 'read_bytes', return_value=(
                        b'\0'.join(x.encode() for x in argv) + b'\0')):
                adopted = ctl.adopt_legacy(legacy.QWEN)
                data = ctl.store.read()
                data['runs'][legacy.QWEN]['legacy_declaration_sha256'] = historical
                ctl.store.write(data)
                self.assertEqual(adopted, ctl.adopt_legacy(legacy.QWEN))
                self.assertEqual(historical, ctl.store.read()['runs'][legacy.QWEN][
                    'legacy_declaration_sha256'])


class LaunchIdentityControls(shared.base.CandidateFixture):
    # Reuse helpers without inheriting tests that assume the original fake
    # observation adapter. These controls intentionally use real observation.
    docker_command = shared.base.SupervisorStageTests.docker_command

    def setUp(self):
        super().setUp()
        self.manifest['models'] += legacy.definitions()
        for row in legacy.definitions():
            (self.logs / row['id']).mkdir()
        self.mid, self.other = legacy.QWEN, legacy.GPT
        self.ctl = self.new_controller()
        self.now = 1800000000.0
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('offline-test-session')
        self.ctl.prepare_bundle(self.mid)
        self.ctl._start(self.mid, wait=False)
        self.run = self.store.read()['runs'][self.mid]
        self.invocation = 'd' * 32
        self.cid = 'e' * 64
        self.created = '2026-10-14T18:00:00.000000Z'
        self.docker_commands = []
        self.running = False
        self.unit = {'LoadState': 'loaded', 'ActiveState': 'active',
                     'InvocationID': self.invocation, 'MainPID': str(os.getpid()),
                     'ControlGroup': '/system.slice/spark-sglang.service'}
        self.adapter.unit = lambda mid, run_id: copy.deepcopy(self.unit)
        self.container = {
            'Id': self.cid, 'Created': self.created,
            'Image': self.ctl.row(self.mid)['runtime']['image_id'],
            'Config': {'Labels': {
                'spark.v4.model': self.mid, 'spark.v4.run': self.run['run_id'],
                'spark.v4.generation': str(self.run['generation']),
                'spark.v4.attempt': self.run['attempt']}},
            'HostConfig': {'OomKillDisable': False},
            'State': {'Running': False, 'Pid': 0, 'StartedAt': '0001-01-01T00:00:00Z'}}

        def inspect(cid):
            c = copy.deepcopy(self.container)
            if self.running:
                c['State'].update(Running=True, Pid=os.getpid())
                c['HostConfig']['OomKillDisable'] = None
            return c

        self.adapter.inspect_container = inspect
        self.adapter.observe = lambda row, run: legacy.observe(self.adapter, row, run)
        for name, value in [('read_secret', shared.KEY),
                            ('container_ids', [self.cid]),
                            ('check_declaration', legacy.declaration_digest(self.container))]:
            p = patch.object(legacy, name, return_value=value)
            p.start()
            self.addCleanup(p.stop)

    def worker(self):
        return supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                     self.invocation, command=self.docker_command)

    def test_successful_creation_snapshots_are_observable_at_every_write(self):
        original = self.ctl.store.write
        published = []

        def checked_write(data):
            original(data)
            run = self.store.read()['runs'][self.mid]
            if run.get('container_id'):
                published.append(run)
                self.adapter.observe(self.ctl.row(self.mid), run)

        with patch.object(self.ctl.store, 'write', side_effect=checked_write):
            self.worker().launch()
        self.assertTrue(published)
        self.assertTrue(all(r['container_created'] == self.created for r in published))
        self.assertEqual(['create', 'start'], [a[1] for a in self.docker_commands])

    def test_declaration_failure_retains_cid_created_and_never_starts(self):
        with patch.object(legacy, 'check_declaration',
                          side_effect=core.ObservationError('declaration rejected')):
            with self.assertRaisesRegex(core.ObservationError, 'declaration rejected'):
                self.worker().launch()
        saved = self.store.read()['runs'][self.mid]
        self.assertEqual(self.cid, saved['container_id'])
        self.assertEqual(self.created, saved['container_created'])
        self.assertFalse(saved.get('legacy_declaration_sha256'))
        self.assertFalse(self.adapter.observe(self.ctl.row(self.mid), saved)['engine_active'])
        self.assertEqual(['create'], [a[1] for a in self.docker_commands])

    def test_inspect_failure_preserves_unknown_create_and_never_starts(self):
        with patch.object(self.adapter, 'inspect_container',
                          side_effect=core.ObservationError('inspect failed')):
            with self.assertRaisesRegex(core.ObservationError, 'inspect failed'):
                self.worker().launch()
        saved = self.store.read()['runs'][self.mid]
        self.assertEqual(self.cid, saved['container_id'])
        self.assertFalse(saved.get('container_created'))
        with self.assertRaises(core.ObservationError):
            self.adapter.observe(self.ctl.row(self.mid), saved)
        self.assertEqual(['create'], [a[1] for a in self.docker_commands])

    def test_captured_created_image_and_declaration_replacements_still_fail(self):
        self.worker().launch()
        saved = self.store.read()['runs'][self.mid]
        for field, value in [('Created', 'replaced'), ('Image', 'sha256:' + 'f' * 64)]:
            before = self.container[field]
            with self.subTest(field=field):
                self.container[field] = value
                with self.assertRaises(core.ObservationError):
                    self.adapter.observe(self.ctl.row(self.mid), saved)
            self.container[field] = before
        self.container['Config']['Labels']['spark.v4.run'] = 'changed'
        with self.assertRaises(core.ObservationError):
            self.adapter.observe(self.ctl.row(self.mid), saved)


class QwenPassiveWorkerControls(unittest.TestCase):
    class Reply:
        def __init__(self, body, status=200):
            self.body, self.status = body, status

        def __enter__(self):
            return self

        def __exit__(self, *args):
            pass

        def read(self, size):
            return self.body[:size]

    def setUp(self):
        self.row = legacy.definitions()[0]
        self.adapter = core.SystemAdapter({})
        self.adapter.http = MagicMock()

    def probe(self, info=None, model_names=None, raw=None, status=200):
        info = shared.qwen_worker_info_fixture() if info is None else info
        names = [self.row['runtime']['served_model_alias']] if model_names is None else model_names
        body = shared.json.dumps(info).encode() if raw is None else raw
        self.adapter.http.open.side_effect = [self.Reply(body, status), self.Reply(
            shared.json.dumps({'data': [{'id': name} for name in names]}).encode())]
        with patch.object(legacy, 'read_secret', return_value=shared.KEY):
            return self.adapter.api_ready(self.row)

    def test_initialized_worker_and_model_identity_use_only_bounded_passive_gets(self):
        info = shared.qwen_worker_info_fixture()
        info['api_key'] = shared.KEY
        info['extra_metadata'] = 'x' * 32612
        self.assertTrue(self.probe(info)[0])
        requests = [call.args[0] for call in self.adapter.http.open.call_args_list]
        self.assertEqual(['/server_info', '/v1/models'], [core.urllib.parse.urlparse(
            request.full_url).path for request in requests])
        self.assertTrue(all(request.get_method() == 'GET' for request in requests))
        self.assertTrue(all(request.get_header('Authorization') == 'Bearer ' + shared.KEY
                            for request in requests))
        self.assertTrue(all(call.kwargs['timeout'] == .45
                            for call in self.adapter.http.open.call_args_list))

    def test_identity_worker_population_and_schema_mismatches_are_rejected(self):
        changes = [
            (('served_model_name',), 'foreign'), (('context_length',), True),
            (('context_length',), 131072), (('quantization',), 'other'),
            (('version',), 'other'), (('tp_size',), True), (('dp_size',), 2),
            (('internal_states',), []), (('internal_states',), [{}, {}]),
            (('internal_states',), [None]),
            (('internal_states', 0, 'served_model_name'), 'foreign'),
            (('internal_states', 0, 'model_path'), '/foreign'),
            (('internal_states', 0, 'tp_size'), True),
            (('internal_states', 0, 'dp_size'), 2),
            (('internal_states', 0, 'startup_time'), {}),
            (('internal_states', 0, 'memory_usage'), {}),
        ]
        for path, value in changes:
            info = shared.qwen_worker_info_fixture()
            info['api_key'] = shared.KEY
            target = info
            for component in path[:-1]:
                target = target[component]
            target[path[-1]] = value
            self.adapter.http.open.reset_mock()
            with self.subTest(path=path, value=value), self.assertRaises(core.ObservationError) as error:
                self.probe(info)
            self.assertNotIn(shared.KEY, str(error.exception))
            self.assertEqual(1, self.adapter.http.open.call_count)
        for raw in [b'{}', b'[]', b'<html>ok</html>', b'{"api_key":"' + shared.KEY.encode() + b'"}']:
            with self.subTest(raw_size=len(raw)), self.assertRaises(core.ObservationError):
                self.probe(raw=raw)

    def test_initialization_requires_positive_finite_nonboolean_timings_and_capacity(self):
        for timing in ['load_weight', 'kv_cache_allocation', 'scheduler_e2e', 'tokenizer_e2e']:
            for value in [0, -1, True, '1', None, float('nan'), float('inf')]:
                info = shared.qwen_worker_info_fixture()
                startup = info['startup_time'] if timing == 'tokenizer_e2e' else info['internal_states'][0]['startup_time']
                startup[timing] = value
                with self.subTest(timing=timing, value=value), self.assertRaises(core.ObservationError):
                    self.probe(info)
        for value in [0, -1, True, 1.0, '1', None, float('nan')]:
            info = shared.qwen_worker_info_fixture()
            info['internal_states'][0]['memory_usage']['token_capacity'] = value
            with self.subTest(capacity=value), self.assertRaises(core.ObservationError):
                self.probe(info)
        info = shared.qwen_worker_info_fixture()
        info['internal_states'][0]['memory_usage']['token_capacity'] = 1
        info['internal_states'][0]['startup_time'] = {
            'load_weight': .001, 'kv_cache_allocation': .002, 'scheduler_e2e': .003}
        info['startup_time']['tokenizer_e2e'] = .004
        self.assertTrue(self.probe(info)[0])

    def test_body_limit_nonfinite_json_and_foreign_model_alias_remain_strict(self):
        with self.assertRaisesRegex(core.ObservationError, 'body exceeds bound'):
            self.probe(raw=b'x' * 65537)
        info = shared.qwen_worker_info_fixture()
        info['ignored_metadata'] = float('nan')
        with self.assertRaises(core.ObservationError):
            self.probe(info)
        for names in [[], ['foreign'], [self.row['runtime']['served_model_alias'], 'foreign']]:
            with self.subTest(names=names), self.assertRaises(core.ObservationError):
                self.probe(model_names=names)

    def test_loading_status_is_not_ready_and_does_not_probe_models(self):
        self.assertFalse(self.probe(status=503)[0])
        self.assertEqual(1, self.adapter.http.open.call_count)
        self.adapter.http.open.reset_mock()
        self.adapter.http.open.side_effect = core.urllib.error.HTTPError(
            'http://127.0.0.1:30000/server_info', 503, 'loading', {}, None)
        with patch.object(legacy, 'read_secret', return_value=shared.KEY):
            self.assertFalse(self.adapter.api_ready(self.row)[0])
        self.assertEqual(1, self.adapter.http.open.call_count)

    def test_passive_worker_probe_keeps_original_absolute_deadline(self):
        def blocked_open(request, timeout):
            core.time.sleep(1)
            raise AssertionError('absolute deadline was not enforced')

        self.adapter.http.open.side_effect = blocked_open
        started = core.time.monotonic()
        with patch.object(legacy, 'read_secret', return_value=shared.KEY), \
                self.assertRaisesRegex(core.ObservationError, 'API identity .*timed out'):
            self.adapter.api_ready(self.row)
        self.assertLess(core.time.monotonic() - started, .9)
        self.assertEqual(1, self.adapter.http.open.call_count)


class ReadinessWaiterControls(shared.base.CandidateFixture):
    def setUp(self):
        super().setUp()
        self.ctl.enable_bridge('offline-readiness-session')
        self.ctl.prepare_bundle(self.mid)
        self.ctl._start(self.mid, wait=False)
        self.run_id = self.store.read()['runs'][self.mid]['run_id']
        self.clock = 0.0
        for name, replacement in [('monotonic', lambda: self.clock),
                                  ('sleep', self.advance_clock)]:
            p = patch.object(core.time, name, side_effect=replacement)
            p.start()
            self.addCleanup(p.stop)

    def advance_clock(self, seconds):
        self.clock += seconds

    def test_exact_api_timeouts_stay_unknown_then_publish_only_verified_ready(self):
        for reason in ['API identity probe timed out',
                       'API identity absolute request deadline expired']:
            with self.subTest(reason=reason):
                data = self.store.read()
                data['runs'][self.mid]['state'] = 'STARTING'
                self.store.write(data)
                with patch.object(self.adapter, 'api_ready', side_effect=core.ObservationError(reason)):
                    self.assertEqual('UNKNOWN', self.ctl.status(self.mid)['state'])
                    self.assertEqual('STARTING', self.store.read()['runs'][self.mid]['state'])
                with patch.object(self.adapter, 'api_ready', side_effect=[
                        core.ObservationError(reason), (True, 'verified'), (True, 'verified')]) as probe:
                    self.assertEqual('READY', self.ctl.wait_ready(self.mid, self.run_id, 1)['state'])
                    self.assertEqual(3, probe.call_count)
                self.assertEqual('READY', self.store.read()['runs'][self.mid]['state'])
                self.assertEqual(1, len(self.adapter.handoffs))
                self.assertFalse(self.adapter.terminations or self.adapter.forces)

    def test_persistent_api_timeout_expires_original_deadline(self):
        with patch.object(self.adapter, 'api_ready', side_effect=core.ObservationError(
                'API identity probe timed out')) as probe:
            with self.assertRaisesRegex(core.Refusal, 'Readiness timeout'):
                self.ctl.wait_ready(self.mid, self.run_id, .3)
            self.assertEqual(2, probe.call_count)
        self.assertAlmostEqual(.3, self.clock)
        self.assertEqual('FAILED', self.store.read()['runs'][self.mid]['state'])
        self.assertEqual(1, len(self.adapter.handoffs))

    def test_identity_docker_parse_and_other_unknowns_fail_immediately(self):
        reasons = ['API identity differs', 'Legacy effective declaration replaced',
                   'API identity response invalid', 'Foreign resource owner',
                   'API identity probe timed out: extra evidence']
        for reason in reasons:
            with self.subTest(reason=reason):
                data = self.store.read()
                data['runs'][self.mid]['state'] = 'STARTING'
                self.store.write(data)
                before = self.clock
                with patch.object(self.adapter, 'api_ready', side_effect=core.ObservationError(reason)):
                    with self.assertRaisesRegex(core.Refusal, 'Start failed'):
                        self.ctl.wait_ready(self.mid, self.run_id, 1)
                self.assertEqual(before, self.clock)
                self.assertEqual('FAILED', self.store.read()['runs'][self.mid]['state'])

    def test_cancellation_or_replacement_during_timeout_retry_cannot_publish_ready(self):
        for change in ['cancel', 'replace']:
            with self.subTest(change=change):
                data = self.store.read()
                run = data['runs'][self.mid]
                run.update(run_id=self.run_id, cancelled=False, state='STARTING')
                self.store.write(data)

                def change_at_retry(seconds):
                    self.advance_clock(seconds)
                    data = self.store.read()
                    if change == 'cancel':
                        data['runs'][self.mid].update(cancelled=True, state='STOPPING')
                    else:
                        data['runs'][self.mid]['run_id'] = 'f' * 32
                    self.store.write(data)

                with patch.object(core.time, 'sleep', side_effect=change_at_retry), \
                        patch.object(self.adapter, 'api_ready', side_effect=core.ObservationError(
                            'API identity probe timed out')) as probe:
                    with self.assertRaisesRegex(core.Refusal, 'cancelled/replaced'):
                        self.ctl.wait_ready(self.mid, self.run_id, 1)
                    self.assertEqual(1, probe.call_count)
                self.assertNotEqual('READY', self.store.read()['runs'][self.mid]['state'])
                self.assertEqual(1, len(self.adapter.handoffs))


if __name__ == '__main__':
    unittest.main()
