#!/usr/bin/env python3
"""Private offline checks of the copied, modified V4 implementation.

This module imports the candidate Controller and StateStore. It does not emulate
their transaction/dedup code. All systemd/Docker/API observations and effects are
injected at the existing SystemAdapter seam; state files and flock remain real.
"""
from __future__ import annotations

import copy
import importlib.util
import json
import os
from pathlib import Path
import sys
import tempfile
import threading
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
CANDIDATE = Path(__file__).resolve().parent
SOURCE_ROOT = CANDIDATE.parent / 'copied-source/opt/spark/model-collection-v4'
spec = importlib.util.spec_from_file_location('candidate_modelctl', CANDIDATE / 'modelctl.py')
modelctl = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = modelctl
spec.loader.exec_module(modelctl)
supervisor_spec = importlib.util.spec_from_file_location('candidate_supervisor', CANDIDATE / 'supervisor.py')
supervisor = importlib.util.module_from_spec(supervisor_spec)
sys.modules[supervisor_spec.name] = supervisor
saved_import_path = sys.path[:]
try:
    with patch.dict(sys.modules, {'modelctl': modelctl}):
        supervisor_spec.loader.exec_module(supervisor)
finally:
    # The production supervisor's fixed import path is not an offline override.
    sys.path[:] = saved_import_path
bridge_spec = importlib.util.spec_from_file_location('candidate_monik_bridge', CANDIDATE / 'monik_bridge.py')
bridge = importlib.util.module_from_spec(bridge_spec)
sys.modules[bridge_spec.name] = bridge
bridge_spec.loader.exec_module(bridge)


class FakeAdapter:
    """Exact-run fake at the production adapter boundary, with counted effects."""

    def __init__(self):
        self.mutex = threading.Lock()
        self.live = {}
        self.handoffs = []
        self.terminations = []
        self.forces = []
        self.preflights = []
        self.api_observed = threading.Event()
        self.handoff_observed = threading.Event()
        self.allow_ready = threading.Event()
        self.allow_ready.set()
        self.fail_preflight = False
        self.fail_observation = False
        self.handoff_error = None
        self.artifacts = []

    def idle(self, row):
        # Unrecorded ownership is covered independently on SystemAdapter.
        return None

    def preflight(self, row):
        self.preflights.append(row['id'])
        if self.fail_preflight:
            raise modelctl.Refusal('Foreign baseline active; no effects')

    def verify_bundle(self, row):
        witnesses = []
        for path in self.artifacts:
            st = path.stat(follow_symlinks=False)
            witnesses.append((path, (st.st_dev, st.st_ino, st.st_size,
                                      st.st_mtime_ns, st.st_ctime_ns)))
        return witnesses

    def recheck_bundle(self, witnesses):
        for path, witness in witnesses:
            st = path.stat(follow_symlinks=False)
            current = (st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns)
            if current != tuple(witness):
                raise modelctl.Refusal('Offline exact artifact witness changed')

    def handoff(self, row, run):
        with self.mutex:
            self.handoffs.append(copy.deepcopy(run))
            self.live[run['run_id']] = True
        self.handoff_observed.set()
        if self.handoff_error:
            raise self.handoff_error

    def observe(self, row, run):
        if self.fail_observation:
            raise modelctl.ObservationError('Exact owner replaced')
        with self.mutex:
            active = self.live.get(run['run_id'], False)
        return {'active': active, 'engine_active': active,
                'supervisor_active': active, 'engine_pid': 0}

    def api_ready(self, row):
        self.api_observed.set()
        return self.allow_ready.is_set(), 'Exact fake API identity verified'

    def terminate(self, row, run):
        # Fake effect also checks the exact ownership boundary, like production.
        self.observe(row, run)
        with self.mutex:
            self.terminations.append(copy.deepcopy(run))
            self.live[run['run_id']] = False

    def force(self, row, run):
        self.observe(row, run)
        with self.mutex:
            self.forces.append(copy.deepcopy(run))
            self.live[run['run_id']] = False

    def resources_released(self, row, run):
        return not self.observe(row, run)['active']


class CandidateFixture(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory(prefix='spark-v4-candidate-')
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.logs = self.root / 'logs'
        self.logs.mkdir()
        self.manifest = json.loads((SOURCE_ROOT / 'config/controller-manifest.json').read_text())
        for row in self.manifest['models']:
            (self.logs / row['id']).mkdir()
            row['startup_timeout_seconds'] = 3
            row['stop_timeout_seconds'] = .1
        self.mid, self.other = modelctl.IDS[:2]
        self.adapter = FakeAdapter()
        self.bundle_file = self.root / 'offline-artifact.bin'
        self.bundle_file.write_bytes(b'offline exact artifact fixture')
        self.adapter.artifacts = [self.bundle_file]
        self.store = modelctl.StateStore(self.root, protected=False)
        self.ctl = self.new_controller()
        # Any accidental escape from the injected adapter fails before an effect.
        self.command_patch = patch.object(modelctl.Command, '__call__',
                                          side_effect=AssertionError('real command forbidden offline'))
        self.command_patch.start()
        self.addCleanup(self.command_patch.stop)
        self.workers = []
        self.addCleanup(self.join_workers)

    def new_controller(self):
        return modelctl.Controller(self.manifest, self.adapter,
                                   store=modelctl.StateStore(self.root, protected=False),
                                   log_root=self.logs, protected=False)

    def current_authority(self):
        return copy.deepcopy(self.store.read()['bridge_authority'])

    def binding(self, **changes):
        data = self.store.read()
        binding = {**data['bridge_approval'], **data['bridge_authority']}
        binding.update(changes)
        return binding

    def join_workers(self):
        self.adapter.allow_ready.set()
        for thread in self.workers:
            thread.join(4)
            self.assertFalse(thread.is_alive(), 'offline worker must be cleaned up')

    def run_worker(self, fn):
        result = {}

        def run():
            try:
                result['value'] = fn()
            except BaseException as exc:
                result['error'] = exc

        thread = threading.Thread(target=run, daemon=True)
        self.workers.append(thread)
        thread.start()
        return thread, result

    def require_event(self, event):
        self.assertTrue(event.wait(3), 'deterministic fake boundary was not reached')

    def join(self, thread, result):
        thread.join(4)
        self.assertFalse(thread.is_alive(), 'offline operation hung')
        return result


class ControllerRegressionTests(CandidateFixture):
    def test_actual_start_reserves_before_handoff_and_stop_releases_exact_run(self):
        saved_at_handoff = []
        original_handoff = self.adapter.handoff

        def handoff(row, run):
            saved_at_handoff.append(self.store.read())
            original_handoff(row, run)

        self.adapter.handoff = handoff
        start = self.ctl.start(self.mid)
        self.assertEqual('READY', start['state'])
        self.assertEqual(1, len(self.adapter.handoffs))
        reservation = saved_at_handoff[0]
        self.assertEqual(self.mid, reservation['reservation'])
        self.assertEqual('STARTING', reservation['runs'][self.mid]['state'])
        self.assertEqual(start['run_id'], reservation['runs'][self.mid]['run_id'])
        stopped = self.ctl.stop(self.mid)
        self.assertEqual('STOPPED', stopped['state'])
        self.assertEqual(start['run_id'], self.adapter.terminations[0]['run_id'])
        self.assertEqual(1, len(self.adapter.terminations))
        self.assertEqual([], self.adapter.forces)
        data = self.store.read()
        self.assertIsNone(data['reservation'])
        self.assertTrue(data['runs'][self.mid]['cancelled'])
        self.assertEqual('STOPPED', data['runs'][self.mid]['state'])

    def test_collection_slot_competing_models_has_one_real_handoff(self):
        self.adapter.allow_ready.clear()
        thread, result = self.run_worker(lambda: self.ctl.start(self.mid))
        self.require_event(self.adapter.api_observed)
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().start(self.other)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertNotIn(self.other, self.store.read()['counters'])
        self.adapter.allow_ready.set()
        self.join(thread, result)
        self.assertNotIn('error', result)
        self.assertEqual('READY', result['value']['state'])

    def test_stop_during_readiness_wait_prevents_late_ready_publication(self):
        self.adapter.allow_ready.clear()
        thread, result = self.run_worker(lambda: self.ctl.start(self.mid))
        self.require_event(self.adapter.api_observed)
        run_id = self.store.read()['runs'][self.mid]['run_id']
        stopped = self.new_controller().stop(self.mid)
        self.assertEqual('STOPPED', stopped['state'])
        self.adapter.allow_ready.set()
        self.join(thread, result)
        self.assertIsInstance(result.get('error'), modelctl.Refusal)
        final = self.store.read()['runs'][self.mid]
        self.assertEqual(run_id, final['run_id'])
        self.assertEqual('STOPPED', final['state'])
        self.assertTrue(final['cancelled'])
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual(1, len(self.adapter.terminations))

    def test_old_waiter_and_fail_cannot_modify_replacement_generation(self):
        first = self.ctl.start(self.mid)
        self.ctl.stop(self.mid)
        second = self.new_controller().start(self.mid)
        self.assertGreater(second['generation'], first['generation'])
        before = self.store.path.read_bytes()
        with self.assertRaises(modelctl.Refusal):
            self.ctl.wait_ready(self.mid, first['run_id'], .1)
        self.ctl._fail(self.mid, first['run_id'], 'late failure from old generation')
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual('READY', self.store.read()['runs'][self.mid]['state'])

    def test_status_has_no_state_or_log_writes_and_unknown_owner_is_not_stopped(self):
        self.ctl.start(self.mid)
        before = {p.relative_to(self.root): p.read_bytes()
                  for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual('READY', self.ctl.status(self.mid)['state'])
        self.adapter.fail_observation = True
        snap = self.ctl.status(self.mid)
        self.assertEqual('UNKNOWN', snap['state'])
        after = {p.relative_to(self.root): p.read_bytes()
                 for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(before, after)

    def test_preflight_refusal_and_unknown_identity_have_no_effect(self):
        self.adapter.fail_preflight = True
        with self.assertRaises(modelctl.Refusal):
            self.ctl.start(self.mid)
        self.assertEqual([], self.adapter.handoffs)
        self.assertFalse(self.store.path.exists())
        self.adapter.fail_preflight = False
        self.ctl.start(self.mid)
        self.adapter.fail_observation = True
        before = self.store.path.read_bytes()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.stop(self.mid)
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual([], self.adapter.terminations)
        self.assertEqual([], self.adapter.forces)


class StateAndSourceGuardTests(CandidateFixture):
    def test_state_symlink_and_nonregular_data_refused(self):
        target = self.root / 'other-state.json'
        target.write_text(json.dumps(self.store.read()))
        self.store.path.symlink_to(target)
        with self.assertRaises(modelctl.ObservationError):
            self.store.read()
        self.store.path.unlink()
        os.mkfifo(self.store.path)
        with self.assertRaises(modelctl.ObservationError):
            self.store.read()

    def test_lock_is_real_cross_instance_and_never_unlinked(self):
        inside = threading.Event()
        release = threading.Event()

        def hold_lock():
            with self.store.lock():
                inside.set()
                if not release.wait(3):
                    raise AssertionError('test failed to release flock')

        thread, result = self.run_worker(hold_lock)
        try:
            self.require_event(inside)
            with self.assertRaises(modelctl.Refusal):
                with modelctl.StateStore(self.root, protected=False).lock(timeout=.03):
                    self.fail('different StateStore incorrectly entered occupied flock')
            inode = self.store.lock_path.stat().st_ino
        finally:
            release.set()
        self.join(thread, result)
        self.assertNotIn('error', result)
        self.assertEqual(inode, self.store.lock_path.stat().st_ino)
        with self.store.lock():
            self.assertTrue(self.store.lock_path.exists())

    def test_privileged_paths_and_fixed_cli_reject_configurable_bypass(self):
        for bad in ('relative/state', '/tmp/../var/lib/state'):
            with self.subTest(path=bad), self.assertRaises(modelctl.Refusal):
                modelctl.protect(bad)
        for argv in (
            ['start', self.mid, '--json'],
            ['stop', self.mid, '--manifest=/tmp/manifest.json'],
            ['start', '../../foreign'],
            ['restart', self.mid],
            ['status', self.mid, '--profile=unsafe'],
        ):
            with self.subTest(argv=argv), self.assertRaises(modelctl.Refusal):
                modelctl.parse_cli(argv)
        self.assertEqual(('status', self.mid, True),
                         modelctl.parse_cli(['status', self.mid, '--json']))
        for run_id in ('', '../x', 'A' * 32, '0' * 31):
            with self.subTest(run_id=run_id), self.assertRaises(modelctl.Refusal):
                modelctl.unit_name(self.mid, run_id)

    def test_actual_systemadapter_owner_replacement_refuses_before_signal(self):
        effects = []

        def command(argv, timeout=.65):
            if argv[1] == 'show':
                if argv[-1] == '--property=FragmentPath,DropInPaths,NeedDaemonReload':
                    return ('FragmentPath=/etc/systemd/system/' + modelctl.unit_name(self.mid)
                            + '\nDropInPaths=\nNeedDaemonReload=no\n')
                return ('LoadState=loaded\nActiveState=active\nSubState=running\n'
                        'InvocationID=replaced-invocation\nMainPID=4321\nControlGroup=/fake\n')
            effects.append(argv)
            raise AssertionError('must reject replacement before any command effect')

        adapter = modelctl.SystemAdapter(self.manifest, command=command)
        run = {'run_id': 'a' * 32, 'generation': 1, 'attempt': 'b' * 32,
               'owner': {'invocation_id': 'original', 'pid': 4321, 'birth': '123'},
               'container_id': None}
        for operation in (adapter.terminate, adapter.request_terminate, adapter.force):
            with self.subTest(operation=operation.__name__), self.assertRaises(modelctl.ObservationError):
                operation(self.ctl.row(self.mid), run)
        self.assertEqual([], effects)

    def test_actual_bounded_stop_uses_exact_nonblocking_supervisor_only(self):
        effects = []
        invocation = 'a' * 32
        pid = os.getpid()

        def command(argv, timeout=.65):
            if argv[1] == 'show':
                if argv[-1] == '--property=FragmentPath,DropInPaths,NeedDaemonReload':
                    return ('FragmentPath=/etc/systemd/system/' + modelctl.unit_name(self.mid)
                            + '\nDropInPaths=\nNeedDaemonReload=no\n')
                return ('LoadState=loaded\nActiveState=active\nSubState=running\n'
                        'InvocationID=' + invocation + '\nMainPID=' + str(pid) + '\nControlGroup=/fake\n')
            effects.append((argv, timeout))
            return ''

        adapter = modelctl.SystemAdapter(self.manifest, command=command)
        run = {'run_id': 'b' * 32, 'generation': 1, 'attempt': 'c' * 32,
               'owner': {'invocation_id': invocation, 'pid': pid, 'birth': modelctl.proc_birth(pid)},
               'container_id': None}
        adapter.request_terminate(self.ctl.row(self.mid), run)
        self.assertEqual([(['/usr/bin/systemctl', 'stop', '--no-block',
                           modelctl.unit_name(self.mid, run['run_id'])], 2.0)], effects)


class TypedBridgeTests(CandidateFixture):
    def setUp(self):
        super().setUp()
        self.now = 1800000000.0
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('offline-test-session')
        for mid in (self.mid, self.other):
            self.ctl.prepare_bundle(mid)

    def new_controller(self):
        ctl = super().new_controller()
        ctl.wall_time = lambda: self.now
        return ctl

    def request(self, action='start', mid=None, operation_id='test-operation', **changes):
        mid = mid or self.mid
        snap = self.ctl.bridge_snapshot(mid)
        request = {
            'schema_version': 1,
            'operation_id': operation_id,
            'profile_id': mid,
            'action': action,
            'profile_revision': snap['profile_revision'],
            'expected_generation': snap['generation'],
            'expected_instance': snap['instance_identity'],
            'control_epoch': snap['control_epoch'],
            'agent_session': snap['agent_session'],
            'boot_id': snap['boot_id'],
            'issued_at_ms': int(self.now * 1000),
            'deadline_ms': int(self.now * 1000) + 10000,
        }
        request.update(changes)
        return request

    def test_response_loss_replay_after_controller_recreation_never_repeats_effect(self):
        request = self.request()
        self.ctl.submit_typed(request)  # Pretend the response was lost in transport.
        first_state = self.store.read()
        self.assertEqual(1, len(self.adapter.handoffs))
        run_id = first_state['runs'][self.mid]['run_id']
        self.now += 20  # Original request deadline is now in the past.
        receipt = self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual(request['operation_id'], receipt['operation_id'])
        self.assertIn(receipt['phase'], {'dispatched', 'completed', 'unresolved'})
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual(run_id, self.store.read()['runs'][self.mid]['run_id'])
        self.assertEqual(1, self.store.read()['counters'][self.mid])

    def test_concurrent_same_request_uses_durable_dedup_and_one_handoff(self):
        request = self.request()
        ready = threading.Barrier(3)

        def submit(ctl):
            ready.wait(3)
            return ctl.submit_typed(copy.deepcopy(request))

        one, result_one = self.run_worker(lambda: submit(self.ctl))
        two, result_two = self.run_worker(lambda: submit(self.new_controller()))
        ready.wait(3)
        self.join(one, result_one)
        self.join(two, result_two)
        self.assertNotIn('error', result_one)
        self.assertNotIn('error', result_two)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual(1, self.store.read()['counters'][self.mid])
        self.assertEqual(request['operation_id'], result_one['value']['operation_id'])
        self.assertEqual(request['operation_id'], result_two['value']['operation_id'])

    def test_native_start_pending_blocks_external_other_model_without_handoff(self):
        request = self.request(mid=self.other)
        self.adapter.allow_ready.clear()
        native, result = self.run_worker(lambda: self.ctl.start(self.mid))
        self.require_event(self.adapter.api_observed)
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().submit_typed(request)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertNotIn(self.other, self.store.read()['counters'])
        self.adapter.allow_ready.set()
        self.join(native, result)
        self.assertNotIn('error', result)

    def test_external_start_pending_blocks_native_other_model_without_handoff(self):
        self.adapter.allow_ready.clear()
        receipt = self.ctl.submit_typed(self.request())
        self.assertIn(receipt['phase'], {'dispatched', 'completed'})
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().start(self.other)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertNotIn(self.other, self.store.read()['counters'])

    def test_fresh_start_for_already_ready_model_is_noop_preserving_generation(self):
        current = self.ctl.start(self.mid)
        before = self.store.read()['runs'][self.mid]
        receipt = self.ctl.submit_typed(self.request(operation_id='already-ready'))
        self.assertEqual('completed', receipt['phase'])
        after = self.store.read()['runs'][self.mid]
        self.assertEqual(current['run_id'], after['run_id'])
        self.assertEqual(before['generation'], after['generation'])
        self.assertEqual('READY', after['state'])
        self.assertEqual(1, len(self.adapter.handoffs))

    def test_typed_stop_while_native_start_waits_cancels_late_ready_waiter(self):
        self.adapter.allow_ready.clear()
        native, result = self.run_worker(lambda: self.ctl.start(self.mid))
        self.require_event(self.adapter.api_observed)
        pending_run = self.store.read()['runs'][self.mid]
        request = self.request(action='stop', operation_id='cancel-start')
        self.assertEqual(pending_run['generation'], request['expected_generation'])
        receipt = self.new_controller().submit_typed(request)
        self.assertIn(receipt['phase'], {'dispatched', 'completed'})
        self.adapter.allow_ready.set()
        self.join(native, result)
        self.assertIsInstance(result.get('error'), modelctl.Refusal)
        run = self.store.read()['runs'][self.mid]
        self.assertEqual(pending_run['run_id'], run['run_id'])
        self.assertTrue(run['cancelled'])
        self.assertIn(run['state'], {'STOPPING', 'STOPPED'})
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual(1, len(self.adapter.terminations))
        self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual(1, len(self.adapter.terminations))

    def test_expired_and_stale_generation_requests_have_zero_effects(self):
        rejected = (
            {'issued_at_ms': int(self.now * 1000) - 20000,
             'deadline_ms': int(self.now * 1000) - 10000},
            {'expected_generation': 1},
            {'expected_instance': 'f' * 64},
            {'control_epoch': 'stale-epoch'},
            {'agent_session': 'stale-session'},
            {'boot_id': 'stale-boot'},
            {'profile_revision': '0' * 64},
        )
        for index, changes in enumerate(rejected):
            request = self.request(operation_id='reject-' + str(index), **changes)
            with self.subTest(changes=changes), self.assertRaises(modelctl.Refusal):
                self.ctl.submit_typed(request)
            self.assertEqual([], self.adapter.handoffs)
            self.assertEqual([], self.adapter.terminations)
            self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_old_generation_stop_cannot_cancel_replacement(self):
        self.ctl.start(self.mid)
        stale_stop = self.request(action='stop', operation_id='old-generation-stop')
        self.ctl.stop(self.mid)
        replacement = self.ctl.start(self.mid)
        before = copy.deepcopy(self.store.read()['runs'][self.mid])
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(stale_stop)
        self.assertEqual(before, self.store.read()['runs'][self.mid])
        self.assertEqual(replacement['run_id'], self.store.read()['runs'][self.mid]['run_id'])
        self.assertEqual(1, len(self.adapter.terminations))

    def test_reused_operation_id_changed_digest_refused_without_second_effect(self):
        request = self.request()
        self.ctl.submit_typed(request)
        changed = copy.deepcopy(request)
        changed['deadline_ms'] += 1
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().submit_typed(changed)
        changed = copy.deepcopy(request)
        changed['action'] = 'stop'
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().submit_typed(changed)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual([], self.adapter.terminations)
        self.assertFalse(self.store.read()['runs'][self.mid]['cancelled'])

    def test_deadline_expiring_during_admission_prevents_start_handoff(self):
        request = self.request()
        original = self.adapter.preflight

        def advance_clock(row):
            original(row)
            self.now += 20

        self.adapter.preflight = advance_clock
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(request)
        self.assertEqual([], self.adapter.handoffs)
        self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_changed_sealed_artifact_refuses_start_before_handoff(self):
        request = self.request()
        self.bundle_file.write_bytes(b'changed offline artifact fixture')
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(request)
        self.assertEqual([], self.adapter.handoffs)
        self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_ambiguous_handoff_after_effect_replay_is_unresolved_without_retry(self):
        request = self.request()
        self.adapter.handoff_error = RuntimeError('effect accepted; response lost')
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(request)
        self.assertEqual(1, len(self.adapter.handoffs))
        first_run = self.store.read()['runs'][self.mid]
        self.assertEqual(self.mid, self.store.read()['reservation'])
        receipt = self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual(first_run['run_id'], self.store.read()['runs'][self.mid]['run_id'])

    def test_crash_after_durable_intent_before_effect_replay_never_launches(self):
        class SimulatedProcessDeath(BaseException):
            pass

        request = self.request()
        original_write = self.ctl.store.write

        def die_after_attempt_intent(data):
            original_write(data)
            operation = data.get('bridge_operations', {}).get(request['operation_id'])
            if operation and operation['receipt']['phase'] == 'effect_attempted':
                raise SimulatedProcessDeath('durable intent exists; adapter has not been called')

        with patch.object(self.ctl.store, 'write', side_effect=die_after_attempt_intent):
            with self.assertRaises(SimulatedProcessDeath):
                self.ctl.submit_typed(request)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual(1, self.store.read()['counters'][self.mid])
        receipt = self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual(1, self.store.read()['counters'][self.mid])

    def test_ambiguous_stop_after_effect_replay_is_unresolved_without_retry(self):
        self.ctl.start(self.mid)
        request = self.request(action='stop')
        original = self.adapter.terminate

        def lose_response(row, run):
            original(row, run)
            raise RuntimeError('stop accepted; response lost')

        self.adapter.terminate = lose_response
        with self.assertRaises((modelctl.Refusal, RuntimeError)):
            self.ctl.submit_typed(request)
        self.assertEqual(1, len(self.adapter.terminations))
        receipt = self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual(1, len(self.adapter.terminations))
        self.assertTrue(self.store.read()['runs'][self.mid]['cancelled'])

    def test_backward_wall_jump_does_not_extend_monotonic_dispatch_lease(self):
        monotonic_now = [100.0]
        self.ctl.monotonic_time = lambda: monotonic_now[0]
        request = self.request()
        original = self.adapter.preflight

        def shift_clocks(row):
            original(row)
            self.now -= 20
            monotonic_now[0] += 20

        self.adapter.preflight = shift_clocks
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(request)
        self.assertEqual([], self.adapter.handoffs)
        self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_receipt_lookup_of_unknown_operation_never_dispatches(self):
        request = self.request(operation_id='unknown-lookup')
        with self.assertRaises(modelctl.Refusal):
            self.ctl.reconcile_typed(request)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)
        self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_stop_receipt_reconciles_exact_release_without_repeated_effect(self):
        self.ctl.start(self.mid)
        request = self.request(action='stop')
        dispatched = self.ctl.submit_typed(request)
        self.assertEqual('dispatched', dispatched['phase'])
        self.assertEqual(1, len(self.adapter.terminations))
        completed = self.new_controller().reconcile_typed(request)
        self.assertEqual('completed', completed['phase'])
        self.assertEqual(dispatched['run_id'], completed['run_id'])
        self.assertEqual(dispatched['generation'], completed['generation'])
        self.assertEqual('STOPPED', self.store.read()['runs'][self.mid]['state'])
        self.assertIsNone(self.store.read()['reservation'])
        self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual(1, len(self.adapter.terminations))

    def test_reconcile_replacement_never_rebinds_or_adopts_original_receipt(self):
        request = self.request()
        original = self.ctl.submit_typed(request)
        self.ctl.stop(self.mid)
        replacement = self.ctl.start(self.mid)
        self.assertNotEqual(original['run_id'], replacement['run_id'])
        # The later generation has captured an exact supervisor. The original
        # request may not claim this evidence as its own successful effect.
        with self.store.lock():
            data = self.store.read()
            data['runs'][self.mid]['owner'] = {
                'unit': modelctl.unit_name(self.mid, replacement['run_id']),
                'invocation_id': 'replacement-only', 'pid': os.getpid(),
                'birth': modelctl.proc_birth(os.getpid())}
            self.store.write(data)
        for _ in range(2):
            receipt = self.new_controller().reconcile_typed(copy.deepcopy(request))
            self.assertEqual('unresolved', receipt['phase'])
            self.assertEqual(original['run_id'], receipt['run_id'])
            self.assertEqual(original['generation'], receipt['generation'])
        self.assertEqual(2, len(self.adapter.handoffs))
        self.assertEqual(1, len(self.adapter.terminations))
        self.assertEqual(replacement['run_id'], self.store.read()['runs'][self.mid]['run_id'])

    def test_restarted_session_binding_recovers_old_executed_receipt_immutably(self):
        request = self.request()
        original = self.ctl.submit_typed(request)
        before = self.store.read()
        operation = copy.deepcopy(before['bridge_operations'][request['operation_id']])
        restarted = self.new_controller()
        authority = restarted.bind_current_agent(self.binding(
            agent_session='restarted-worker-session', control_epoch='restarted-broker-epoch'))
        after_binding = self.store.read()
        self.assertEqual(before['runs'], after_binding['runs'])
        self.assertEqual(before['counters'], after_binding['counters'])
        self.assertEqual(before['reservation'], after_binding['reservation'])
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual([], self.adapter.terminations)
        with self.assertRaises(modelctl.Refusal):
            restarted.submit_typed(copy.deepcopy(request))
        recovered = restarted.reconcile_typed(copy.deepcopy(request), authority)
        self.assertEqual(original['operation_id'], recovered['operation_id'])
        self.assertEqual(original['run_id'], recovered['run_id'])
        self.assertEqual(original['generation'], recovered['generation'])
        after_lookup = self.store.read()['bridge_operations'][request['operation_id']]
        self.assertEqual(operation['digest'], after_lookup['digest'])
        self.assertEqual(operation['request'], after_lookup['request'])
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual([], self.adapter.terminations)

    def test_session_binding_wrong_identity_or_no_local_approval_has_zero_effects(self):
        for changes in ({'agent_id': 'foreign-agent'}, {'controller_id': 'foreign-controller'},
                        {'boot_id': 'f' * 32}):
            before = self.store.path.read_bytes()
            with self.subTest(changes=changes), self.assertRaises(modelctl.Refusal):
                self.ctl.bind_current_agent(self.binding(**changes))
            self.assertEqual(before, self.store.path.read_bytes())
        with self.store.lock():
            data = self.store.read()
            binding = self.binding(agent_session='never-auto-approved')
            del data['bridge_approval']
            self.store.write(data)
        before = self.store.path.read_bytes()
        with self.assertRaises(modelctl.Refusal):
            self.new_controller().bind_current_agent(binding)
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)
        self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_binding_revokes_unexecuted_prepared_intent_without_runtime_effect(self):
        class SimulatedProcessDeath(BaseException):
            pass

        request = self.request()
        original_write = self.ctl.store.write

        def die_after_prepare(data):
            original_write(data)
            operation = data.get('bridge_operations', {}).get(request['operation_id'])
            if operation and operation['receipt']['phase'] == 'prepared':
                raise SimulatedProcessDeath('prepared reservation exists; no adapter effect')

        with patch.object(self.ctl.store, 'write', side_effect=die_after_prepare):
            with self.assertRaises(SimulatedProcessDeath):
                self.ctl.submit_typed(request)
        before = self.store.read()
        original_operation = copy.deepcopy(before['bridge_operations'][request['operation_id']])
        authority = self.new_controller().bind_current_agent(self.binding(
            agent_session='fresh-worker', control_epoch='fresh-epoch'))
        after = self.store.read()
        operation = after['bridge_operations'][request['operation_id']]
        self.assertTrue(operation['execution_forbidden'])
        self.assertEqual('unresolved', operation['receipt']['phase'])
        self.assertEqual(original_operation['digest'], operation['digest'])
        self.assertEqual(original_operation['request'], operation['request'])
        self.assertEqual(original_operation['receipt']['run_id'], operation['receipt']['run_id'])
        self.assertEqual(before['runs'], after['runs'])
        self.assertEqual(before['reservation'], after['reservation'])
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)
        # Even apparently active external evidence cannot promote an intent whose
        # own effect never ran. The receipt remains attached to the original run.
        run_id = operation['receipt']['run_id']
        self.adapter.live[run_id] = True
        with self.store.lock():
            data = self.store.read()
            data['runs'][self.mid]['owner'] = {'invocation_id': 'out-of-band',
                'pid': os.getpid(), 'birth': modelctl.proc_birth(os.getpid())}
            self.store.write(data)
        receipt = self.ctl.reconcile_typed(request, authority)
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual(run_id, receipt['run_id'])
        self.assertEqual([], self.adapter.handoffs)

    def test_receipt_recovery_rejects_stale_current_authority(self):
        request = self.request()
        self.ctl.submit_typed(request)
        stale = self.current_authority()
        self.ctl.bind_current_agent(self.binding(agent_session='new-authenticated-session'))
        with self.assertRaises(modelctl.Refusal):
            self.ctl.reconcile_typed(request, stale)
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual([], self.adapter.terminations)

    def test_second_preflight_argv_drift_refuses_before_counter_or_reservation(self):
        request = self.request()
        original = self.adapter.preflight
        calls = []
        before = self.store.path.read_bytes()

        def change_argv_on_final_preflight(row):
            original(row)
            calls.append(row['id'])
            if len(calls) == 2:
                row['runtime']['argv'].append('--changed-after-typed-validation')

        self.adapter.preflight = change_argv_on_final_preflight
        with self.assertRaises(modelctl.Refusal):
            self.ctl.submit_typed(request)
        self.assertEqual(2, len(calls))
        self.assertNotEqual(request['profile_revision'], self.ctl.profile_revision(self.mid))
        self.assertEqual(before, self.store.path.read_bytes())
        data = self.store.read()
        self.assertNotIn(self.mid, data['counters'])
        self.assertNotIn(self.mid, data['runs'])
        self.assertIsNone(data['reservation'])
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)

    def assert_argv_drift_at_durable_boundary(self, boundary):
        request = self.request(operation_id='drift-' + boundary)
        original_write, original_log = self.ctl.store.write, self.ctl.log
        mutation_count = []

        def mutate_once():
            if not mutation_count:
                self.ctl.row(self.mid)['runtime']['argv'].append('--unapproved-boundary-drift')
                mutation_count.append(boundary)

        def write_then_change(data):
            original_write(data)
            operation = data.get('bridge_operations', {}).get(request['operation_id'])
            if operation and operation['receipt']['phase'] == boundary:
                mutate_once()

        def log_then_change(mid, message):
            original_log(mid, message)
            if boundary == 'start-log' and message.startswith('START run='):
                mutate_once()

        with patch.object(self.ctl.store, 'write', side_effect=write_then_change), \
                patch.object(self.ctl, 'log', side_effect=log_then_change):
            with self.assertRaises(modelctl.Refusal):
                self.ctl.submit_typed(request)
        self.assertEqual([boundary], mutation_count)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)
        data = self.store.read()
        run = data['runs'][self.mid]
        self.assertEqual(request['profile_revision'], run['approved_profile_revision'])
        self.assertNotEqual(run['approved_profile_revision'], self.ctl.profile_revision(self.mid))
        self.assertEqual(1, data['counters'][self.mid])
        self.assertIsNone(data['reservation'])
        self.assertEqual('FAILED', run['state'])
        operation = copy.deepcopy(data['bridge_operations'][request['operation_id']])
        receipt = self.new_controller().submit_typed(copy.deepcopy(request))
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual(run['run_id'], receipt['run_id'])
        self.assertEqual(run['generation'], receipt['generation'])
        replayed = self.store.read()['bridge_operations'][request['operation_id']]
        self.assertEqual(operation['digest'], replayed['digest'])
        self.assertEqual(request, replayed['request'])
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)

    def test_argv_drift_after_prepared_fsync_refuses_handoff_with_original_approval(self):
        self.assert_argv_drift_at_durable_boundary('prepared')

    def test_argv_drift_after_effect_intent_fsync_refuses_handoff_with_original_approval(self):
        self.assert_argv_drift_at_durable_boundary('effect_attempted')

    def test_argv_drift_after_start_log_refuses_handoff_with_original_approval(self):
        self.assert_argv_drift_at_durable_boundary('start-log')


class SupervisorStageTests(CandidateFixture):
    def setUp(self):
        super().setUp()
        self.now = 1800000000.0
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('offline-test-session')
        self.ctl.prepare_bundle(self.mid)
        # Reserve and hand off using the actual native start implementation, without
        # invoking its independent readiness waiter in this supervisor-stage test.
        self.ctl._start(self.mid, wait=False)
        self.run = self.store.read()['runs'][self.mid]
        self.invocation = 'd' * 32
        self.cid = 'e' * 64
        self.created = '2026-10-14T18:00:00.000000Z'
        self.docker_commands = []
        self.running = False
        self.adapter.unit = lambda mid, run_id: {
            'InvocationID': self.invocation, 'MainPID': str(os.getpid())}
        self.adapter.inspect_container = lambda cid: {
            'Created': self.created,
            'State': {'Running': self.running, 'Pid': os.getpid() if self.running else 0}}

    def stop_request(self):
        return TypedBridgeTests.request(self, action='stop', operation_id='stop-during-create')

    def docker_command(self, argv, timeout=.65):
        self.docker_commands.append(copy.deepcopy(argv))
        if argv[:2] == ['/usr/bin/docker', 'create']:
            return self.cid
        if argv[:2] == ['/usr/bin/docker', 'start']:
            saved = self.store.read()['runs'][self.mid]
            self.assertEqual(self.cid, saved['container_id'])
            self.assertEqual(self.created, saved['container_created'])
            self.assertTrue(saved['container_create_attempted'])
            self.running = True
            return self.cid
        raise AssertionError('unexpected supervisor command: ' + repr(argv))

    def test_actual_supervisor_persists_exact_cid_before_engine_start(self):
        worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                       self.invocation, command=self.docker_command)
        launched = worker.launch()
        self.assertEqual(['create', 'start'], [argv[1] for argv in self.docker_commands])
        self.assertEqual(self.cid, launched['container_id'])
        self.assertEqual(os.getpid(), launched['engine_pid'])
        self.assertEqual(modelctl.proc_birth(os.getpid()), launched['engine_birth'])

    def test_actual_supervisor_create_releases_lock_and_stop_prevents_engine_start(self):
        creating = threading.Event()
        finish_create = threading.Event()

        def blocked_create(argv, timeout=.65):
            if argv[:2] == ['/usr/bin/docker', 'create']:
                creating.set()
                if not finish_create.wait(3):
                    raise AssertionError('test did not release inert container creation')
            return self.docker_command(argv, timeout)

        worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                       self.invocation, command=blocked_create)
        thread, result = self.run_worker(worker.launch)
        try:
            self.require_event(creating)
            # Different file descriptor and actual flock prove Docker create is
            # outside the collection transaction, before a stop is dispatched.
            with modelctl.StateStore(self.root, protected=False).lock(timeout=.05):
                pending = self.store.read()['runs'][self.mid]
                self.assertTrue(pending['container_create_attempted'])
                self.assertIsNone(pending['container_id'])
            stop_receipt = self.ctl.submit_typed(self.stop_request())
            self.assertEqual('dispatched', stop_receipt['phase'])
            self.assertTrue(self.store.read()['runs'][self.mid]['cancelled'])
        finally:
            finish_create.set()
        self.join(thread, result)
        self.assertNotIn('error', result)
        self.assertEqual(['create'], [argv[1] for argv in self.docker_commands])
        final = self.store.read()['runs'][self.mid]
        self.assertEqual(self.cid, final['container_id'])
        self.assertEqual(self.created, final['container_created'])
        self.assertTrue(final['cancelled'])
        self.assertEqual('STOPPING', final['state'])
        self.assertEqual(1, len(self.adapter.terminations))
        self.assertFalse(self.running)

    def test_changed_manifest_before_supervisor_create_has_no_container_effect(self):
        frozen = self.run['approved_profile_revision']
        self.ctl.row(self.mid)['context'] += 1
        worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                       self.invocation, command=self.docker_command)
        with self.assertRaises(modelctl.Refusal):
            worker.launch()
        final = self.store.read()['runs'][self.mid]
        self.assertEqual(frozen, final['approved_profile_revision'])
        self.assertIsNone(final['owner'])
        self.assertIsNone(final['container_id'])
        self.assertEqual([], self.docker_commands)
        self.assertFalse(self.running)

    def test_changed_manifest_during_create_prevents_engine_start(self):
        frozen = self.run['approved_profile_revision']

        def mutate_profile_after_create(argv, timeout=.65):
            value = self.docker_command(argv, timeout)
            if argv[:2] == ['/usr/bin/docker', 'create']:
                self.ctl.row(self.mid)['context'] += 1
            return value

        worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                       self.invocation, command=mutate_profile_after_create)
        with self.assertRaises(modelctl.Refusal):
            worker.launch()
        final = self.store.read()['runs'][self.mid]
        self.assertEqual(frozen, final['approved_profile_revision'])
        self.assertEqual(self.cid, final['container_id'])
        self.assertEqual(['create'], [argv[1] for argv in self.docker_commands])
        self.assertFalse(self.running)

    def test_exact_instance_unit_dropin_fragment_or_reload_drift_refuses_worker(self):
        expected = str(Path('/etc/systemd/system') / modelctl.unit_name(self.mid))
        initial = self.store.path.read_bytes()
        policies = (
            {'FragmentPath': expected, 'DropInPaths': '/etc/systemd/system/instance.service.d/override.conf',
             'NeedDaemonReload': 'no'},
            {'FragmentPath': '/tmp/foreign.service', 'DropInPaths': '', 'NeedDaemonReload': 'no'},
            {'FragmentPath': expected, 'DropInPaths': '', 'NeedDaemonReload': 'yes'},
        )
        for policy in policies:
            observed_commands = []

            def command(argv, timeout=.65):
                observed_commands.append(argv)
                self.assertEqual(['/usr/bin/systemctl', 'show',
                                  modelctl.unit_name(self.mid, self.run['run_id']),
                                  '--property=FragmentPath,DropInPaths,NeedDaemonReload'], argv)
                return '\n'.join(k + '=' + v for k, v in policy.items())

            exact = modelctl.SystemAdapter(self.manifest, command=command)
            self.adapter.validate_run_definition = exact.validate_run_definition
            worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                           self.invocation, command=self.docker_command)
            with self.subTest(policy=policy), self.assertRaises(modelctl.Refusal):
                worker.launch()
            self.assertEqual(1, len(observed_commands))
            self.assertEqual(initial, self.store.path.read_bytes())
            self.assertEqual([], self.docker_commands)
        self.assertFalse(self.running)

    def test_approved_exact_unit_definition_allows_both_worker_launch_stages(self):
        expected = str(Path('/etc/systemd/system') / modelctl.unit_name(self.mid))
        observed = []

        def command(argv, timeout=.65):
            observed.append(argv)
            self.assertEqual(['/usr/bin/systemctl', 'show',
                              modelctl.unit_name(self.mid, self.run['run_id']),
                              '--property=FragmentPath,DropInPaths,NeedDaemonReload'], argv)
            return 'FragmentPath=' + expected + '\nDropInPaths=\nNeedDaemonReload=no\n'

        exact = modelctl.SystemAdapter(self.manifest, command=command)
        self.adapter.validate_run_definition = exact.validate_run_definition
        worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                       self.invocation, command=self.docker_command)
        worker.launch()
        self.assertEqual(2, len(observed))
        self.assertEqual(['create', 'start'], [argv[1] for argv in self.docker_commands])
        self.assertTrue(self.running)

    def test_revoked_prepared_operation_cannot_launch_actual_supervisor(self):
        class SimulatedProcessDeath(BaseException):
            pass

        self.ctl.stop(self.mid)
        self.adapter.handoffs.clear()
        self.adapter.terminations.clear()
        request = TypedBridgeTests.request(self, operation_id='prepared-no-dispatch')
        original_write = self.ctl.store.write

        def die_after_prepare(data):
            original_write(data)
            operation = data.get('bridge_operations', {}).get(request['operation_id'])
            if operation and operation['receipt']['phase'] == 'prepared':
                raise SimulatedProcessDeath('no systemd effect dispatched')

        with patch.object(self.ctl.store, 'write', side_effect=die_after_prepare):
            with self.assertRaises(SimulatedProcessDeath):
                self.ctl.submit_typed(request)
        pending = self.store.read()['runs'][self.mid]
        self.assertEqual(request['operation_id'], pending['bridge_operation_id'])
        self.ctl.bind_current_agent(self.binding(agent_session='replacement-worker-session'))
        before_worker = self.store.path.read_bytes()
        worker = supervisor.Supervisor(self.ctl, self.mid, pending['run_id'],
                                       self.invocation, command=self.docker_command)
        with self.assertRaises(modelctl.Refusal):
            worker.launch()
        self.assertEqual(before_worker, self.store.path.read_bytes())
        self.assertEqual([], self.docker_commands)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)
        self.assertFalse(self.running)

    def test_actual_protected_source_pin_change_before_create_or_before_start(self):
        # Exercise the production source hashing path using temporary backing files
        # for its fixed root paths. Ownership checks and path opening are mapped
        # explicitly; Controller.profile_revision and worker guards remain real.
        sources = (modelctl.MANIFEST, modelctl.PROJECT / 'bin/modelctl.py',
                   modelctl.PROJECT / 'bin/supervisor.py', modelctl.PROJECT / 'bin/monik_bridge.py',
                   Path('/usr/local/sbin/spark-modelctl-v4'),
                   Path('/etc/systemd/system') / modelctl.unit_name(self.mid))
        mapping = {}
        for index, source in enumerate(sources):
            backing = self.root / ('protected-source-' + str(index))
            backing.write_bytes(b'approved source ' + str(index).encode())
            mapping[source] = backing
        source_to_change = mapping[modelctl.PROJECT / 'bin/supervisor.py']
        expected_fragment = str(Path('/etc/systemd/system') / modelctl.unit_name(self.mid))
        self.adapter.command = lambda argv, timeout=.65: (
            'FragmentPath=' + expected_fragment + '\nDropInPaths=\nNeedDaemonReload=no\n')
        original_open = os.open

        def mapped_open(path, flags, *args, **kwargs):
            return original_open(mapping.get(Path(path), path), flags, *args, **kwargs)

        with patch.object(modelctl, 'protect', side_effect=lambda path, **_: None), \
                patch.object(modelctl.os, 'open', side_effect=mapped_open):
            self.ctl.protected = True
            # Freeze the production definition against the mapped approved sources.
            with self.store.lock():
                data = self.store.read()
                frozen = self.ctl.profile_revision(self.mid)
                data['runs'][self.mid]['approved_profile_revision'] = frozen
                self.store.write(data)
            worker = supervisor.Supervisor(self.ctl, self.mid, self.run['run_id'],
                                           self.invocation, command=self.docker_command)
            worker.engine_log.protected = False
            source_to_change.write_bytes(b'changed supervisor source before create')
            with self.assertRaises(modelctl.Refusal):
                worker.launch()
            self.assertEqual([], self.docker_commands)
            source_to_change.write_bytes(b'approved source 2')
            self.assertEqual(frozen, self.ctl.profile_revision(self.mid))

            def change_code_after_create(argv, timeout=.65):
                value = self.docker_command(argv, timeout)
                if argv[:2] == ['/usr/bin/docker', 'create']:
                    source_to_change.write_bytes(b'changed supervisor source after create')
                return value

            worker.command = change_code_after_create
            with self.assertRaises(modelctl.Refusal):
                worker.launch()
            self.assertEqual(['create'], [argv[1] for argv in self.docker_commands])
            self.assertFalse(self.running)
            self.assertEqual(frozen, self.store.read()['runs'][self.mid]['approved_profile_revision'])


class TransportGuardTests(CandidateFixture):
    def setUp(self):
        super().setUp()
        self.now = 1800000000.0
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('offline-test-session')
        self.ctl.prepare_bundle(self.mid)

    def request(self, **changes):
        return TypedBridgeTests.request(self, **changes)

    @staticmethod
    def decode(method, **fields):
        return bridge.decode_envelope(json.dumps(
            {'schema_version': 1, 'method': method, **fields}).encode())

    def test_duplicate_unknown_keys_and_nonfinite_json_refused(self):
        rejected = (
            b'{"schema_version":1,"schema_version":1,"method":"observe_many","profile_ids":[]}',
            b'{"schema_version":1,"method":"submit","request":{"operation_id":"a","operation_id":"b"}}',
            b'{"schema_version":1,"method":"submit","request":{"value":NaN}}',
            b'{"schema_version":1,"method":"submit","request":{"value":Infinity}}',
            b'{"schema_version":1,"method":"submit","request":{"value":-Infinity}}',
            json.dumps({'schema_version': 1, 'method': 'observe_many',
                        'profile_ids': [self.mid], 'manifest': '/tmp/foreign.json'}).encode(),
            json.dumps({'schema_version': 1, 'method': 'submit', 'request': {},
                        'command': '/usr/bin/systemctl restart foreign.service'}).encode(),
        )
        for raw in rejected:
            with self.subTest(raw=raw[:160]), self.assertRaises(ValueError):
                bridge.decode_envelope(raw)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)

    def test_input_bound_schema_and_fixed_observation_selectors_refused(self):
        oversized = json.dumps({'schema_version': 1, 'method': 'submit',
                                'request': {'text': 'x' * bridge.MAX_INPUT}}).encode()
        with self.assertRaises(ValueError):
            bridge.decode_envelope(oversized)
        for profiles in ([], [self.mid, self.mid], ['/tmp/model'], [self.mid] * 6,
                         [None], [{'path': '/tmp/manifest.json'}]):
            with self.subTest(profiles=profiles), self.assertRaises(ValueError):
                self.decode('observe_many', profile_ids=profiles)
        for version in (True, 0, '1'):
            raw = json.dumps({'schema_version': version, 'method': 'observe_many',
                              'profile_ids': [self.mid]}).encode()
            with self.subTest(version=version), self.assertRaises(ValueError):
                bridge.decode_envelope(raw)
        for method in ('start', 'enable_bridge', 'prepare_bundle', 'restart', 'shell'):
            with self.subTest(method=method), self.assertRaises(ValueError):
                self.decode(method, request={})

    def test_nested_path_or_argv_injection_reaches_actual_controller_and_is_refused(self):
        for extra in (
            {'manifest': {'path': '/tmp/foreign.json'}},
            {'runtime': {'argv': ['/bin/sh', '-c', 'touch /tmp/effect']}},
            {'unit': {'name': 'foreign.service'}},
            {'container': {'id': 'f' * 64}},
            {'environment': {'LD_PRELOAD': '/tmp/library.so'}},
        ):
            request = self.request()
            request.update(extra)
            body = self.decode('submit', request=request)
            with self.subTest(extra=extra), self.assertRaises(modelctl.Refusal):
                bridge.dispatch(self.ctl, body)
            self.assertEqual([], self.adapter.handoffs)
            self.assertEqual([], self.adapter.terminations)
            self.assertNotIn(self.mid, self.store.read()['counters'])

    def test_fixed_observation_dispatch_is_readonly_without_runtime_import_or_effect(self):
        before = {p.relative_to(self.root): p.read_bytes()
                  for p in self.root.rglob('*') if p.is_file()}
        body = self.decode('observe_many', profile_ids=[self.mid, self.other])
        with patch.object(bridge.importlib.util, 'spec_from_file_location',
                          side_effect=AssertionError('transport decoding must not import runtime sources')):
            observed = bridge.dispatch(self.ctl, body)
        self.assertEqual(1, observed['schema_version'])
        self.assertEqual([self.mid, self.other], [m['model_id'] for m in observed['models']])
        self.assertEqual(['STOPPED', 'STOPPED'], [m['state'] for m in observed['models']])
        after = {p.relative_to(self.root): p.read_bytes()
                 for p in self.root.rglob('*') if p.is_file()}
        self.assertEqual(before, after)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)

    def test_submit_and_receipt_dispatch_use_actual_controller_without_replay(self):
        request = self.request()
        submitted = bridge.dispatch(self.ctl, self.decode('submit', request=request))
        self.assertEqual(1, submitted['schema_version'])
        self.assertEqual('dispatched', submitted['receipt']['phase'])
        self.assertEqual(1, len(self.adapter.handoffs))
        retrieved = bridge.dispatch(self.ctl, self.decode(
            'receipt', request=request, authority=self.current_authority()))
        self.assertEqual(request['operation_id'], retrieved['receipt']['operation_id'])
        self.assertEqual(submitted['receipt']['run_id'], retrieved['receipt']['run_id'])
        self.assertEqual(1, len(self.adapter.handoffs))
        duplicate = bridge.dispatch(self.ctl, self.decode('submit', request=request))
        self.assertEqual(request['operation_id'], duplicate['receipt']['operation_id'])
        self.assertEqual(1, len(self.adapter.handoffs))

    def test_receipt_requires_current_authority_and_binding_envelope_has_fixed_fields(self):
        request = self.request()
        for fields in ({'request': request},
                       {'request': request, 'authority': {'agent_session': 'only-one-key'}},
                       {'request': request, 'authority': {**self.current_authority(),
                                                         'path': '/tmp/authority'}}):
            with self.subTest(fields=fields), self.assertRaises(ValueError):
                self.decode('receipt', **fields)
        for binding in ({}, {**self.binding(), 'manifest': '/tmp/manifest.json'},
                        {**self.binding(), 'agent_id': {'path': '/tmp/agent'}}):
            with self.subTest(binding=binding), self.assertRaises(ValueError):
                self.decode('bind_current_agent', binding=binding)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)

    def test_binding_transport_updates_metadata_and_recovers_original_old_session_receipt(self):
        request = self.request()
        dispatched = bridge.dispatch(self.ctl, self.decode('submit', request=request))['receipt']
        before = self.store.read()
        bound = bridge.dispatch(self.new_controller(), self.decode('bind_current_agent',
            binding=self.binding(agent_session='transport-restarted-session',
                                 control_epoch='transport-restarted-epoch')))
        self.assertEqual(1, bound['schema_version'])
        authority = bound['authority']
        self.assertEqual('transport-restarted-session', authority['agent_session'])
        self.assertEqual({'control_epoch', 'agent_session', 'boot_id'}, set(authority))
        self.assertEqual(before['runs'], self.store.read()['runs'])
        recovered = bridge.dispatch(self.new_controller(), self.decode(
            'receipt', request=request, authority=authority))['receipt']
        self.assertEqual(dispatched['run_id'], recovered['run_id'])
        self.assertEqual(dispatched['generation'], recovered['generation'])
        stored = self.store.read()['bridge_operations'][request['operation_id']]
        self.assertEqual(before['bridge_operations'][request['operation_id']]['digest'], stored['digest'])
        self.assertEqual(request, stored['request'])
        self.assertEqual(1, len(self.adapter.handoffs))
        self.assertEqual([], self.adapter.terminations)


if __name__ == '__main__':
    unittest.main(verbosity=2)
