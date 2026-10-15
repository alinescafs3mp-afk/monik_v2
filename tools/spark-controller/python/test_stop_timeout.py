"""Private offline regression: real Controller, StateStore, force and Command.

No command runs. Adapter observations are injected; subprocess.run is replaced
with a daemon-response simulation at the existing command boundary.
"""
import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location('stop_candidate', Path(__file__).with_name('modelctl.py'))
core = importlib.util.module_from_spec(spec)
sys.modules[spec.name] = core
spec.loader.exec_module(core)


class ExactAdapter(core.SystemAdapter):
    def __init__(self, manifest):
        super().__init__(manifest)
        self.active = True
        self.replaced = False
        self.terminate_calls = 0
        self.force_calls = 0

    def observe(self, row, run):
        if self.replaced:
            raise core.ObservationError('Exact owner replaced')
        return {'active': self.active, 'engine_active': self.active,
                'supervisor_active': False, 'engine_pid': 123 if self.active else 0}

    def terminate(self, row, run):
        self.terminate_calls += 1
        self.observe(row, run)

    def force(self, row, run):
        self.force_calls += 1
        return super().force(row, run)

    def resources_released(self, row, run):
        return not self.observe(row, run)['active']


class StopTimeoutTests(unittest.TestCase):
    def setUp(self):
        tmp = tempfile.TemporaryDirectory(prefix='native-stop-proof-')
        self.addCleanup(tmp.cleanup)
        self.root = Path(tmp.name)
        self.mid = core.IDS[0]
        self.cid = 'c' * 64
        (self.root / 'logs' / self.mid).mkdir(parents=True)
        self.row = {'id': self.mid, 'prepared': True,
                    'stop_timeout_seconds': 180, 'context': 1024,
                    'endpoint': 'http://127.0.0.1:1/v1'}
        self.manifest = {'models': [self.row]}
        self.adapter = ExactAdapter(self.manifest)
        self.store = core.StateStore(self.root, protected=False)
        self.ctl = core.Controller(self.manifest, self.adapter, store=self.store,
                                   log_root=self.root / 'logs', protected=False)
        run = {'model_id': self.mid, 'run_id': 'a' * 32, 'generation': 1,
               'attempt': 'b' * 32, 'created_at': 0, 'state': 'READY',
               'cancelled': False, 'owner': None, 'container_id': self.cid}
        data = self.store.read()
        data.update(reservation=self.mid, runs={self.mid: run})
        self.store.write(data)
        self.commands = []
        self.tick = -181

        def monotonic():
            self.tick += 181
            return self.tick

        p = patch.object(core.time, 'monotonic', side_effect=monotonic)
        p.start(); self.addCleanup(p.stop)
        p = patch.object(core.time, 'sleep', return_value=None)
        p.start(); self.addCleanup(p.stop)

    def daemon(self, outcome):
        def run(argv, **kwargs):
            self.commands.append(copy.deepcopy(argv))
            self.assertEqual(['/usr/bin/docker', 'kill', '--signal=KILL', self.cid], argv)
            self.assertEqual(2.0, kwargs['timeout'])
            if outcome == 'stopped_timeout':
                self.adapter.active = False
            if outcome == 'replaced_timeout':
                self.adapter.replaced = True
            if outcome != 'rc_failure':
                raise subprocess.TimeoutExpired(argv, kwargs['timeout'])
            return subprocess.CompletedProcess(argv, 1, b'', b'private error')
        return patch.object(core.subprocess, 'run', side_effect=run)

    def assert_retained(self):
        data = self.store.read()
        self.assertEqual(self.mid, data['reservation'])
        self.assertEqual('STOPPING', data['runs'][self.mid]['state'])

    def test_daemon_stopped_before_client_timeout_reconciles_without_replay(self):
        with self.daemon('stopped_timeout'):
            result = self.ctl.stop(self.mid)
        self.assertEqual('STOPPED', result['state'])
        self.assertFalse(result['process_active'])
        self.assertIsNone(self.store.read()['reservation'])
        self.assertEqual(1, len(self.commands))
        self.assertEqual(1, self.adapter.force_calls)

    def test_timeout_with_still_active_engine_retains_reservation_and_never_retries(self):
        with self.daemon('active_timeout'):
            with self.assertRaisesRegex(core.Refusal, 'STOP_UNCONFIRMED'):
                self.ctl.stop(self.mid)
        self.assert_retained()
        self.assertEqual(1, len(self.commands))
        self.assertEqual(1, self.adapter.force_calls)

    def test_identity_change_after_ambiguous_kill_refuses_release_and_replay(self):
        with self.daemon('replaced_timeout'):
            with self.assertRaisesRegex(core.ObservationError, 'Exact owner replaced'):
                self.ctl.stop(self.mid)
        self.assert_retained()
        self.assertEqual(1, len(self.commands))

    def test_nontimeout_command_failure_remains_immediate_failure(self):
        with self.daemon('rc_failure'):
            with self.assertRaisesRegex(core.ObservationError, 'Command failed rc=1'):
                self.ctl.stop(self.mid)
        self.assert_retained()
        self.assertEqual(1, len(self.commands))

    def test_already_inactive_stop_reconciles_with_no_command_or_force(self):
        self.adapter.active = False
        data = self.store.read()
        data['runs'][self.mid].update(state='STOPPING', cancelled=True)
        self.store.write(data)
        with patch.object(core.subprocess, 'run', side_effect=AssertionError('No effect allowed')):
            result = self.ctl.stop(self.mid)
        self.assertEqual('STOPPED', result['state'])
        self.assertIsNone(self.store.read()['reservation'])
        self.assertEqual(0, self.adapter.force_calls)

    def test_status_remains_read_only_and_cannot_clear_stale_reservation(self):
        self.adapter.active = False
        data = self.store.read()
        data['runs'][self.mid].update(state='STOPPING', cancelled=True)
        self.store.write(data)
        before = self.store.path.read_bytes()
        result = self.ctl.status(self.mid)
        self.assertEqual('STOPPING', result['state'])
        self.assertFalse(result['process_active'])
        self.assertEqual(before, self.store.path.read_bytes())

    def test_real_legacy_terminate_inactive_has_no_effect(self):
        legacy = core.legacy_support()
        row = legacy.definitions()[0]
        run = {'run_id': 'a' * 32, 'owner': {'pid': 123}}
        with patch.object(legacy, 'effective_unit', return_value={'control_safe': True}), \
             patch.object(legacy, 'observe', return_value={
                 'supervisor_active': False, 'engine_active': False}), \
             patch.object(self.adapter, 'command', side_effect=AssertionError('No effect allowed')):
            legacy.request_terminate(self.adapter, row, run)

    def test_real_legacy_orphan_refuses_without_replaying_kill(self):
        legacy = core.legacy_support()
        row = legacy.definitions()[0]
        run = {'run_id': 'a' * 32, 'owner': {'pid': 123}}
        with patch.object(legacy, 'effective_unit', return_value={'control_safe': True}), \
             patch.object(legacy, 'observe', return_value={
                 'supervisor_active': False, 'engine_active': True}), \
             patch.object(self.adapter, 'command', side_effect=AssertionError('No effect allowed')):
            with self.assertRaisesRegex(core.Refusal, 'Legacy orphan requires exact local recovery'):
                legacy.request_terminate(self.adapter, row, run)


if __name__ == '__main__':
    unittest.main(verbosity=2)
