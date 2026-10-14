"""Offline physical shutdown controls using the actual Controller/SystemAdapter.

Only command output and /proc/cgroup paths are injected; ownership parsing,
resources_released, state stores, locks, witnesses and receipt logic are real.
"""
import copy
import json
from pathlib import Path
import unittest
from unittest.mock import patch

import test_bridge_candidate as regression
from test_bridge_candidate import CandidateFixture, modelctl


class FakeSystemAdapter(modelctl.SystemAdapter):
    def __init__(self, manifest, row, run):
        self.row, self.run = row, copy.deepcopy(run)
        self.commands = []
        self.gpu = []
        self.orphans = []
        self.unit_data = {'LoadState': 'loaded', 'ActiveState': 'inactive',
                          'SubState': 'dead', 'InvocationID': run['owner']['invocation_id'],
                          'MainPID': '0', 'ControlGroup': ''}
        self.container = {
            'Id': run['container_id'], 'Created': run['container_created'],
            'Image': row['runtime']['image_id'],
            'Config': {'Labels': {'spark.v4.model': row['id'],
                                 'spark.v4.run': run['run_id'],
                                 'spark.v4.generation': str(run['generation']),
                                 'spark.v4.attempt': run['attempt']}},
            'HostConfig': {'CgroupParent': ''},
            'State': {'Running': False, 'Pid': 0, 'StartedAt': '2026-10-14T12:00:00Z'},
        }
        super().__init__(manifest, command=self.fake_command)

    def fake_command(self, argv, timeout=.65):
        self.commands.append(argv)
        if argv[:2] == ['/usr/bin/systemctl', 'show']:
            if argv[-1] == '--property=FragmentPath,DropInPaths,NeedDaemonReload':
                return ('FragmentPath=/etc/systemd/system/' + modelctl.unit_name(self.row['id'])
                        + '\nDropInPaths=\nNeedDaemonReload=no\n')
            return ''.join(k + '=' + str(v) + '\n' for k, v in self.unit_data.items())
        if argv[:2] == ['/usr/bin/systemctl', 'list-units']:
            return ''
        if argv[:2] == ['/usr/bin/docker', 'inspect']:
            return json.dumps([self.container])
        if argv[:2] == ['/usr/bin/docker', 'ps']:
            return '\n'.join(self.orphans)
        if argv[0] == '/usr/bin/nvidia-smi':
            return '\n'.join(str(x) for x in self.gpu)
        raise AssertionError('Effect forbidden in physical offline controls: ' + repr(argv))


class ShutdownProofTests(CandidateFixture):
    def setUp(self):
        super().setUp()
        self.now = 1800000000.0
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('shutdown-test-session')
        self.ctl.prepare_bundle(self.mid)
        self.proc = self.root / 'proc'
        self.cgroups = self.root / 'cgroup'
        self.proc.mkdir()
        self.cgroups.mkdir()
        (self.cgroups / 'cgroup.controllers').write_text('cpu memory pids\n')
        real_path = modelctl.Path

        def routed_path(*parts):
            path = real_path(*parts)
            if path == real_path('/proc/sys/kernel/random/boot_id'):
                return path
            if path == real_path('/proc') or real_path('/proc') in path.parents:
                return self.proc / path.relative_to('/proc')
            if path == real_path('/sys/fs/cgroup') or real_path('/sys/fs/cgroup') in path.parents:
                return self.cgroups / path.relative_to('/sys/fs/cgroup')
            return path

        self.path_patch = patch.object(modelctl, 'Path', side_effect=routed_path)
        self.path_patch.start()
        self.addCleanup(self.path_patch.stop)

    request = regression.TypedBridgeTests.request

    def seed(self, state='STOPPED'):
        data = self.store.read()
        run = {'model_id': self.mid, 'run_id': 'a' * 32, 'generation': 1,
               'attempt': 'b' * 32, 'created_at': self.now - 100, 'state': state,
               'cancelled': False, 'owner': {
                   'unit': modelctl.unit_name(self.mid, 'a' * 32),
                   'invocation_id': 'c' * 32, 'pid': 4201, 'birth': '101'},
               'container_id': 'd' * 64, 'container_created': '2026-10-14T12:00:00Z',
               'container_create_attempted': True, 'engine_pid': 4202,
               'engine_birth': '102', 'message': 'Engine crashed' if state == 'FAILED' else 'Stopped'}
        data['runs'][self.mid] = run
        data['counters'][self.mid] = 1
        data['reservation'] = None
        self.store.write(data)
        self.system = FakeSystemAdapter(self.manifest, self.ctl.row(self.mid), run)
        self.ctl.adapter = self.system
        return run

    def proc_live(self, pid, birth, group='/foreign.scope'):
        target = self.proc / str(pid)
        target.mkdir(exist_ok=True)
        fields = ['S'] + ['0'] * 18 + [birth]
        (target / 'stat').write_text(str(pid) + ' (fake engine) ' + ' '.join(fields))
        (target / 'cgroup').write_text('0::' + group + '\n')

    def cgroup(self, group, populated):
        target = self.cgroups / group.lstrip('/')
        target.mkdir(parents=True, exist_ok=True)
        (target / 'cgroup.events').write_text('populated ' + str(populated) + '\nfrozen 0\n')

    def bytes_snapshot(self):
        return {p.relative_to(self.root): p.read_bytes()
                for p in self.root.rglob('*') if p.is_file()}

    def test_actual_system_adapter_positive_exact_snapshot_is_read_only(self):
        run = self.seed()
        self.cgroup('/system.slice/docker-' + run['container_id'] + '.scope', 0)
        self.cgroup('/system.slice/' + run['owner']['unit'], 0)
        before = self.bytes_snapshot()
        snap = self.ctl.bridge_snapshot(self.mid)
        self.assertTrue(snap['shutdown_verified'])
        proof = snap['shutdown_witness']
        self.assertEqual('exact_run_resources_released', proof['kind'])
        self.assertEqual(run['attempt'], proof['attempt'])
        self.assertEqual(snap['instance_identity'], proof['instance_identity'])
        for field in ('run_id', 'generation', 'owner', 'container_id', 'container_created', 'engine_pid', 'engine_birth'):
            self.assertEqual(run[field], proof[field])
        self.assertEqual(before, self.bytes_snapshot())

    def test_inactive_failed_label_kept_with_real_release_proof(self):
        self.seed('FAILED')
        before = self.store.path.read_bytes()
        snap = self.ctl.bridge_snapshot(self.mid)
        self.assertEqual('FAILED', snap['state'])
        self.assertTrue(snap['shutdown_verified'])
        self.assertEqual(before, self.store.path.read_bytes())

    def test_failed_after_cancellation_preserves_supervisor_failure_label(self):
        self.seed('FAILED')
        data = self.store.read()
        data['runs'][self.mid].update(cancelled=True, message='Supervisor failure after cancellation')
        self.store.write(data)
        before = self.store.path.read_bytes()
        self.assertEqual('FAILED', self.ctl.status(self.mid)['state'])
        snap = self.ctl.bridge_snapshot(self.mid)
        self.assertEqual('FAILED', snap['state'])
        self.assertEqual('Supervisor failure after cancellation', snap['message'])
        self.assertTrue(snap['shutdown_verified'])
        self.assertEqual(before, self.store.path.read_bytes())
        receipt = self.ctl.submit_typed(self.request(action='stop'))
        self.assertEqual('FAILED', receipt['result']['state'])
        self.assertTrue(receipt['result']['shutdown_verified'])

    def test_failed_with_residual_cgroup_stays_failed_through_pending_stop(self):
        run = self.seed('FAILED')
        group = '/system.slice/' + run['owner']['unit']
        self.cgroup(group, 1)
        request = self.request(action='stop')
        receipt = self.ctl.submit_typed(request)
        self.assertEqual('dispatched', receipt['phase'])
        self.assertEqual('FAILED', self.ctl.bridge_snapshot(self.mid)['state'])
        self.assertNotIn('shutdown_verified', self.ctl.bridge_snapshot(self.mid))
        # Actual supervisor cancellation can replace the intermediate label.
        data = self.store.read()
        data['runs'][self.mid].update(state='STOPPING', message='Supervisor cancellation')
        self.store.write(data)
        self.cgroup(group, 0)
        receipt = self.ctl.reconcile_typed(request)
        self.assertEqual('completed', receipt['phase'])
        self.assertEqual('FAILED', receipt['result']['state'])
        self.assertEqual('Engine crashed', receipt['result']['message'])
        self.assertTrue(receipt['result']['shutdown_verified'])
        self.assertEqual('FAILED', self.store.read()['runs'][self.mid]['state'])

    def test_stopped_typed_noop_requires_resources_and_preserves_failed(self):
        run = self.seed('FAILED')
        request = self.request(action='stop')
        receipt = self.ctl.submit_typed(request)
        self.assertEqual('completed', receipt['phase'])
        self.assertEqual('FAILED', receipt['result']['state'])
        self.assertTrue(receipt['result']['shutdown_verified'])
        self.assertEqual(run['attempt'], receipt['attempt'])
        self.assertEqual('FAILED', self.store.read()['runs'][self.mid]['state'])
        self.assertFalse(self.store.read()['runs'][self.mid]['cancelled'])
        before = self.store.path.read_bytes()
        self.system.container['Created'] = 'replaced-after-original-proof'
        self.assertEqual(receipt, self.ctl.submit_typed(request))
        self.assertEqual(receipt, self.ctl.reconcile_typed(request))
        self.assertEqual(before, self.store.path.read_bytes())

    def test_stop_noop_unknown_resource_cannot_record_completed(self):
        self.seed()
        request = self.request(action='stop')
        self.system.gpu = ['N/A']
        before = self.store.path.read_bytes()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.submit_typed(request)
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertNotIn(request['operation_id'], self.store.read().get('bridge_operations', {}))

    def test_container_identity_negatives_cannot_certify_release(self):
        cases = (
            lambda c: c.update(Id='e' * 64),
            lambda c: c.update(Created='replaced-created'),
            lambda c: c.update(Image='sha256:foreign-image'),
            lambda c: c['Config']['Labels'].update({'spark.v4.attempt': 'e' * 32}),
            lambda c: c['Config']['Labels'].update({'spark.v4.run': 'e' * 32}),
            lambda c: c['State'].update(Pid=9999),
            lambda c: c['HostConfig'].update(CgroupParent='foreign.slice'),
            lambda c: c['State'].update(Running=None),
        )
        for change in cases:
            with self.subTest(change=change):
                self.seed()
                change(self.system.container)
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_incomplete_stopped_container_evidence_cannot_certify(self):
        cases = (
            lambda state: state.pop('Pid'),
            lambda state: state.update(Pid=None),
            lambda state: state.update(Pid='0'),
            lambda state: state.pop('StartedAt'),
            lambda state: state.update(StartedAt=None),
            lambda state: state.update(StartedAt=''),
        )
        for change in cases:
            with self.subTest(change=change):
                self.seed()
                data = self.store.read()
                data['runs'][self.mid].update(engine_pid=None, engine_birth=None)
                self.store.write(data)
                change(self.system.container['State'])
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_created_never_started_container_positive_requires_explicit_zero(self):
        self.seed()
        data = self.store.read()
        data['runs'][self.mid].update(engine_pid=None, engine_birth=None)
        self.store.write(data)
        self.system.container['State'].update(Pid=0, Running=False, StartedAt='0001-01-01T00:00:00Z')
        snap = self.ctl.bridge_snapshot(self.mid)
        self.assertTrue(snap['shutdown_verified'])
        self.assertIsNone(snap['shutdown_witness']['engine_pid'])
        self.assertIsNone(snap['shutdown_witness']['engine_birth'])

    def test_inactive_unit_identity_negatives(self):
        for change in ({'InvocationID': 'replaced'}, {'MainPID': '9999'},
                       {'ControlGroup': '/system.slice/foreign.service'},
                       {'ActiveState': 'unclassified'}):
            with self.subTest(change=change):
                self.seed()
                self.system.unit_data.update(change)
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_owned_pid_live_and_reused_birth_cannot_certify(self):
        run = self.seed()
        for pid_key, birth_key in (('engine_pid', 'engine_birth'),):
            self.proc_live(run[pid_key], run[birth_key])
            snap = self.ctl.bridge_snapshot(self.mid)
            self.assertNotIn('shutdown_verified', snap)
            (self.proc / str(run[pid_key]) / 'stat').write_text('4202 (fake) ' + ' '.join(['S'] + ['0'] * 18 + ['reused']))
            with self.assertRaises(modelctl.ObservationError):
                self.ctl.bridge_snapshot(self.mid)

    def test_owned_supervisor_birth_reused_refuses(self):
        run = self.seed()
        self.proc_live(run['owner']['pid'], 'reused')
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def test_exact_supervisor_cgroup_survivors_when_unit_group_cleared(self):
        run = self.seed()
        self.cgroup('/system.slice/' + run['owner']['unit'], 1)
        self.assertNotIn('shutdown_verified', self.ctl.bridge_snapshot(self.mid))

    def test_exact_container_cgroup_survivors_without_gpu(self):
        run = self.seed()
        self.cgroup('/custom.slice/docker-' + run['container_id'] + '.scope', 1)
        self.assertEqual([], self.system.gpu)
        self.assertNotIn('shutdown_verified', self.ctl.bridge_snapshot(self.mid))

    def test_exact_gpu_child_cgroup_veto(self):
        run = self.seed()
        self.system.gpu = [4300]
        self.proc_live(4300, '300', '/system.slice/docker-' + run['container_id'] + '.scope/child')
        self.assertNotIn('shutdown_verified', self.ctl.bridge_snapshot(self.mid))

    def test_other_profile_gpu_does_not_veto_harmless_inactive_stop(self):
        self.seed()
        other_group = '/system.slice/docker-' + 'e' * 64 + '.scope'
        self.system.gpu = [4300]
        self.proc_live(4300, '300', other_group)
        self.cgroup(other_group, 1)
        data = self.store.read()
        data['reservation'] = self.other
        self.store.write(data)
        request = self.request(action='stop')
        receipt = self.ctl.submit_typed(request)
        self.assertEqual('completed', receipt['phase'])
        self.assertTrue(receipt['result']['shutdown_verified'])
        self.assertEqual(self.other, self.store.read()['reservation'])

    def test_resource_unknown_and_namespace_bound_fail_closed(self):
        run = self.seed()
        group = '/system.slice/' + run['owner']['unit']
        self.cgroup(group, 0)
        (self.cgroups / group.lstrip('/') / 'cgroup.events').write_text('populated unknown\n')
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)
        (self.cgroups / group.lstrip('/') / 'cgroup.events').write_text('populated 0\n')
        (self.cgroups / 'cgroup.controllers').unlink()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)
        (self.cgroups / 'cgroup.controllers').write_text('cpu\n')
        path = self.cgroups
        for _ in range(34):
            path = path / 'deep'
            path.mkdir()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def test_unknown_gpu_process_ownership_fails_closed(self):
        self.seed()
        self.system.gpu = [4300]
        self.proc_live(4300, '300')
        (self.proc / '4300' / 'cgroup').write_text('not-a-cgroup-line\n')
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def test_existing_owned_namespace_missing_evidence_is_unknown(self):
        run = self.seed()
        self.proc_live(run['engine_pid'], run['engine_birth'])
        (self.proc / str(run['engine_pid']) / 'stat').unlink()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)
        (self.proc / str(run['engine_pid']) / 'cgroup').unlink()
        (self.proc / str(run['engine_pid'])).rmdir()
        self.cgroup('/docker/' + run['container_id'], 0)
        (self.cgroups / 'docker' / run['container_id'] / 'cgroup.events').unlink()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)
        self.cgroup('/docker/' + run['container_id'], 0)
        self.system.gpu = [4300]
        self.proc_live(4300, '300')
        (self.proc / '4300' / 'cgroup').unlink()
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def test_incomplete_supervisor_observation_is_unknown(self):
        for key in ('MainPID', 'InvocationID', 'ControlGroup'):
            with self.subTest(key=key):
                self.seed()
                del self.system.unit_data[key]
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_missing_launch_identity_never_becomes_resource_certificate(self):
        for change in ({'container_id': None}, {'container_created': None}, {'owner': None},
                       {'engine_pid': None, 'engine_birth': None},
                       {'engine_birth': None}):
            with self.subTest(change=change):
                self.seed()
                data = self.store.read()
                data['runs'][self.mid].update(change)
                self.store.write(data)
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_old_uncancelled_starting_never_certifies_future_launch_as_stopped(self):
        self.seed('STARTING')
        data = self.store.read()
        data['runs'][self.mid].update(owner=None, container_id=None, container_created=None,
                                     engine_pid=None, engine_birth=None,
                                     container_create_attempted=False,
                                     created_at=modelctl.time.time() - 100)
        self.store.write(data)
        snap = self.ctl.bridge_snapshot(self.mid)
        self.assertEqual('FAILED', snap['state'])
        self.assertFalse(snap['process_active'])
        self.assertNotIn('shutdown_verified', snap)
        self.assertNotIn('shutdown_witness', snap)

    def test_state_and_identity_changes_during_release_are_rejected(self):
        changes = (
            lambda d: d['runs'][self.mid].update(state='READY'),
            lambda d: d['runs'][self.mid].update(attempt='f' * 32),
            lambda d: d['runs'][self.mid].update(container_id='f' * 64),
            lambda d: d['runs'][self.mid]['owner'].update(birth='reused'),
            lambda d: d['counters'].update({self.mid: 2}),
            lambda d: d.update(reservation=self.other),
            lambda d: d['bridge_authority'].update(agent_session='replaced-session'),
        )
        for mutate in changes:
            with self.subTest(mutate=mutate):
                self.seed()
                released = self.system.resources_released

                def changed(row, run):
                    result = released(row, run)
                    data = self.store.read()
                    mutate(data)
                    self.store.write(data)
                    return result

                self.system.resources_released = changed
                with self.assertRaises(modelctl.ObservationError):
                    self.ctl.bridge_snapshot(self.mid)

    def test_no_owned_run_is_separate_proof_not_historical_certificate(self):
        run = self.seed()
        data = self.store.read()
        del data['runs'][self.mid]
        data['reservation'] = self.other
        self.store.write(data)
        self.system.gpu = [4300]
        self.proc_live(4300, '300')
        snap = self.ctl.bridge_snapshot(self.mid)
        proof = snap['shutdown_witness']
        self.assertTrue(snap['shutdown_verified'])
        self.assertEqual('no_owned_run', proof['kind'])
        self.assertEqual(1, proof['generation'])
        self.assertEqual('', proof['instance_identity'])
        for key in ('run_id', 'attempt', 'owner', 'container_id', 'container_created', 'engine_pid', 'engine_birth'):
            self.assertIsNone(proof[key])
        self.assertFalse(any(argv[0] == '/usr/bin/nvidia-smi' for argv in self.system.commands))
        receipt = self.ctl.submit_typed(self.request(action='stop'))
        self.assertIsNone(receipt['run_id'])
        self.assertEqual(proof, receipt['result']['shutdown_witness'])
        self.assertEqual(self.other, self.store.read()['reservation'])

    def test_no_run_reserved_or_orphan_container_cannot_certify_idle(self):
        self.seed()
        data = self.store.read()
        del data['runs'][self.mid]
        data['reservation'] = self.mid
        self.store.write(data)
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)
        data['reservation'] = None
        self.store.write(data)
        self.system.orphans = ['f' * 64]
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def pending_stop(self):
        self.ctl.adapter = self.adapter
        start = self.ctl.start(self.mid)
        request = self.request(action='stop')
        self.ctl.submit_typed(request)
        return request, self.store.read()['runs'][self.mid]

    def test_completed_reconciliation_proof_binds_same_original_attempt(self):
        request, run = self.pending_stop()
        receipt = self.ctl.reconcile_typed(request)
        self.assertEqual('completed', receipt['phase'])
        proof = receipt['result']['shutdown_witness']
        self.assertTrue(receipt['result']['shutdown_verified'])
        self.assertEqual(run['run_id'], proof['run_id'])
        self.assertEqual(run['attempt'], proof['attempt'])
        original = copy.deepcopy(self.store.read()['bridge_operations'][request['operation_id']])
        data = self.store.read()
        data['runs'][self.mid].update(attempt='f' * 32, generation=2)
        self.store.write(data)
        self.assertEqual(receipt, self.ctl.reconcile_typed(request))
        self.assertEqual(original, self.store.read()['bridge_operations'][request['operation_id']])

    def test_pending_original_attempt_or_physical_identity_replacement_unresolved(self):
        for change in ({'attempt': 'f' * 32}, {'owner': {'pid': 9999}},
                       {'run_id': 'f' * 32}, {'generation': 2}):
            with self.subTest(change=change):
                self.store.path.unlink(missing_ok=True)
                self.ctl.enable_bridge('shutdown-test-session')
                self.ctl.prepare_bundle(self.mid)
                request, run = self.pending_stop()
                data = self.store.read()
                # Initial fake owner is None; replacement attempt/run/gen is bound.
                if 'owner' in change:
                    original = data['bridge_operations'][request['operation_id']]
                    original['bound_instance']['owner'] = {'pid': 4201}
                data['runs'][self.mid].update(change)
                self.store.write(data)
                receipt = self.ctl.reconcile_typed(request)
                self.assertEqual('unresolved', receipt['phase'])
                self.assertEqual('original_runtime_replaced_or_missing', receipt['result']['reason'])
                self.assertEqual(run['run_id'], receipt['run_id'])
                self.assertEqual(run['attempt'], receipt['attempt'])
                self.assertNotIn('shutdown_verified', receipt['result'])

    def test_pending_stop_known_container_replacement_never_adopts_new_cid(self):
        run = self.seed('STOPPING')
        # Construct a genuine effect-attempted original receipt without effects.
        request = self.request(action='stop')
        digest = self.ctl._validate_typed(request)
        with self.store.lock():
            data = self.store.read()
            self.ctl._record_typed(data, request, digest, 'effect_attempted', data['runs'][self.mid])
            self.store.write(data)
        data = self.store.read()
        data['runs'][self.mid]['container_id'] = 'f' * 64
        self.system.container['Id'] = 'f' * 64
        self.store.write(data)
        receipt = self.ctl.reconcile_typed(request)
        self.assertEqual('unresolved', receipt['phase'])
        self.assertEqual('original_runtime_replaced_or_missing', receipt['result']['reason'])
        self.assertEqual(run['container_id'], self.store.read()['bridge_operations'][request['operation_id']]['bound_instance']['container_id'])

    def test_actual_system_adapter_stop_reconciliation_positive(self):
        run = self.seed('STOPPING')
        data = self.store.read()
        data['reservation'] = self.mid
        self.store.write(data)
        request = self.request(action='stop')
        digest = self.ctl._validate_typed(request)
        with self.store.lock():
            data = self.store.read()
            self.ctl._record_typed(data, request, digest, 'effect_attempted', data['runs'][self.mid])
            self.store.write(data)
        receipt = self.ctl.reconcile_typed(request)
        self.assertEqual('completed', receipt['phase'])
        self.assertEqual('STOPPED', receipt['result']['state'])
        self.assertEqual(run['attempt'], receipt['attempt'])
        self.assertTrue(receipt['result']['shutdown_verified'])
        self.assertEqual(run['container_id'], receipt['result']['shutdown_witness']['container_id'])
        self.assertIsNone(self.store.read()['reservation'])

    def test_physical_unit_changes_during_resource_probe_fail_closed(self):
        self.seed()
        command = self.system.command

        def changed(argv, timeout=.65):
            result = command(argv, timeout)
            if argv[0] == '/usr/bin/nvidia-smi':
                self.system.unit_data['InvocationID'] = 'replaced-during-observation'
            return result

        self.system.command = changed
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.bridge_snapshot(self.mid)

    def test_state_changes_inside_stop_proof_cannot_record_receipt(self):
        self.seed()
        request = self.request(action='stop')
        released = self.system.resources_released

        def changed(row, run):
            result = released(row, run)
            data = self.store.read()
            data['runs'][self.mid]['attempt'] = 'f' * 32
            self.store.write(data)
            return result

        self.system.resources_released = changed
        with self.assertRaises(modelctl.ObservationError):
            self.ctl.submit_typed(request)
        self.assertNotIn(request['operation_id'], self.store.read().get('bridge_operations', {}))


if __name__ == '__main__':
    unittest.main()
