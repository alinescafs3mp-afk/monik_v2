"""Native Qwen ExecStop after exact supervisor exit; no real host commands.

Uses real legacy observe/release, native entrypoint and process-release parsing.
Only systemd/Docker observations and the temporary /proc root are injected.
"""
import copy
import importlib.util
from pathlib import Path
import sys
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import test_final5 as fixtures
import test_bridge_candidate as base

core = fixtures.core
legacy = fixtures.legacy


class DeactivatingOwnerTests(fixtures.Fixture):
    def setUp(self):
        super().setUp()
        f = self.f
        f.run.update(state='STOPPING', cancelled=True, engine_birth='202')
        f.run['owner']['birth'] = '101'
        f.unit.update(ActiveState='deactivating', SubState='stop', MainPID='0')
        self.proc = self.root / 'proc'
        self.proc.mkdir()
        real_path = core.Path

        def routed_path(*parts):
            path = real_path(*parts)
            if path == real_path('/proc') or real_path('/proc') in path.parents:
                return self.proc / path.relative_to('/proc')
            return path

        p = patch.object(core, 'Path', side_effect=routed_path)
        p.start(); self.addCleanup(p.stop)
        p = patch.object(core, 'proc_birth', side_effect=lambda pid: {10001: '101', 10002: '202'}.get(pid))
        p.start(); self.addCleanup(p.stop)
        self.save()

    def observe(self):
        return self.f.adapter.observe(self.f.row, self.f.run)

    def assert_no_effects(self):
        effects = {'stop', 'start', 'kill', 'rm', 'create', 'restart'}
        self.assertFalse([argv for argv in self.f.commands if len(argv) > 1 and argv[1] in effects])

    def write_owner_stat(self, birth='101', malformed=False):
        parent = self.proc / '10001'
        parent.mkdir(exist_ok=True)
        path = parent / 'stat'
        tail = ['S'] + ['0'] * 18 + [birth]
        path.write_text('malformed' if malformed else '10001 (exact owner) ' + ' '.join(tail))
        return path

    def test_exact_cancelled_deactivating_owner_absence_preserves_active_container(self):
        self.assertEqual({'active': True, 'engine_active': True,
                          'supervisor_active': True, 'engine_pid': 10002}, self.observe())
        self.assertFalse(self.f.adapter.resources_released(self.f.row, self.f.run))
        self.assert_no_effects()

    def test_deactivating_without_engine_still_cannot_release_reservation(self):
        self.f.present = False
        self.f.named = False
        self.assertEqual({'active': True, 'engine_active': False,
                          'supervisor_active': True, 'engine_pid': 0}, self.observe())
        self.assertFalse(self.f.adapter.resources_released(self.f.row, self.f.run))
        self.assertEqual(legacy.QWEN, self.ctl.store.read()['reservation'])
        self.assert_no_effects()

    def test_real_live_old_owner_and_pid_reuse_are_refused(self):
        for birth in ('101', '999'):
            with self.subTest(birth=birth):
                self.write_owner_stat(birth)
                with self.assertRaises(core.ObservationError):
                    self.observe()
        self.assert_no_effects()

    def test_real_unreadable_and_malformed_old_process_evidence_are_refused(self):
        path = self.write_owner_stat(malformed=True)
        with self.assertRaises(core.ObservationError):
            self.observe()
        real_read = Path.read_text

        def unreadable(current, *args, **kwargs):
            if current == path:
                raise PermissionError('injected unreadable old supervisor stat')
            return real_read(current, *args, **kwargs)

        with patch.object(Path, 'read_text', unreadable), self.assertRaises(core.ObservationError):
            self.observe()
        self.assert_no_effects()

    def test_missing_stat_with_still_present_proc_directory_is_not_release(self):
        (self.proc / '10001').mkdir()
        with self.assertRaises(core.ObservationError):
            self.observe()
        self.assert_no_effects()

    def test_missing_or_malformed_owner_identity_is_refused(self):
        original = copy.deepcopy(self.f.run['owner'])
        cases = [('birth', None), ('birth', ''), ('birth', 'unknown'),
                 ('pid', None), ('pid', 0), ('pid', '10001'),
                 ('unit', 'other.service'), ('invocation_id', ''),
                 ('invocation_id', 'x' * 32), ('invocation_id', 'f' * 32)]
        for key, value in cases:
            with self.subTest(field=key, value=value):
                self.f.run['owner'] = copy.deepcopy(original)
                self.f.run['owner'][key] = value
                with self.assertRaises((core.ObservationError, KeyError)):
                    self.observe()
        self.f.run['owner'] = original
        self.assert_no_effects()

    def test_same_run_cancellation_and_model_are_required(self):
        for changes in ({'state': 'READY'}, {'state': 'STARTING'}, {'state': 'FAILED'},
                        {'cancelled': False}, {'cancelled': 1}, {'model_id': legacy.GPT}):
            with self.subTest(changes=changes):
                original = copy.deepcopy(self.f.run)
                self.f.run.update(changes)
                with self.assertRaises(core.ObservationError):
                    self.observe()
                self.f.run = original
        self.assert_no_effects()

    def test_other_active_unit_states_or_nonzero_unknown_mainpid_are_refused(self):
        original = copy.deepcopy(self.f.unit)
        changes = [{'ActiveState': state} for state in ('active', 'activating', 'reloading')]
        changes += [{'MainPID': pid} for pid in (None, '', '10001', '10003')]
        changes += [{'InvocationID': invocation} for invocation in ('', 'f' * 32)]
        changes += [{'LoadState': 'not-found'}]
        for change in changes:
            with self.subTest(change=change):
                observed = {**original, **change}
                with patch.object(self.f.adapter, 'unit', return_value=observed), \
                     patch.object(core, 'proc_birth', side_effect=lambda pid: '202' if pid == 10002 else None), \
                     self.assertRaises(core.ObservationError):
                    self.observe()
        missing = copy.deepcopy(original)
        missing.pop('MainPID')
        with patch.object(self.f.adapter, 'unit', return_value=missing), self.assertRaises(core.ObservationError):
            self.observe()
        self.assert_no_effects()

    def test_existing_deactivating_live_owner_contract_is_unchanged(self):
        self.f.unit['MainPID'] = '10001'
        with patch.object(self.f.adapter, 'process_released', side_effect=AssertionError('Exception not needed')):
            self.assertTrue(self.observe()['active'])
        self.assert_no_effects()

    def test_gpt_cannot_use_qwen_owner_absence_exception(self):
        self.f.row = legacy.definitions()[1]
        self.f.run['model_id'] = legacy.GPT
        self.f.run['owner']['unit'] = core.unit_name(legacy.GPT, self.f.run['run_id'])
        with self.assertRaises(core.ObservationError):
            self.observe()
        self.assert_no_effects()

    def test_full_captured_container_identity_is_required(self):
        original = copy.deepcopy(self.f.run)
        for key in ('container_id', 'container_created', 'legacy_declaration_sha256',
                    'engine_pid', 'engine_birth'):
            with self.subTest(missing=key):
                self.f.run = copy.deepcopy(original)
                self.f.run[key] = None
                with self.assertRaises(core.ObservationError):
                    self.observe()
        for key, value in [('container_id', 'invalid'), ('legacy_declaration_sha256', 'invalid'),
                           ('engine_pid', '10002'), ('engine_birth', 'unknown')]:
            with self.subTest(invalid=key):
                self.f.run = copy.deepcopy(original)
                self.f.run[key] = value
                with self.assertRaises(core.ObservationError):
                    self.observe()
        self.f.run = original
        self.assert_no_effects()

    def test_existing_created_image_declaration_and_engine_birth_checks_still_apply(self):
        original = copy.deepcopy(self.f.container)
        mutations = [lambda c: c.update(Created='replacement'),
                     lambda c: c.update(Image='sha256:' + 'f' * 64),
                     lambda c: c['Config'].update(Cmd=['replacement']),
                     lambda c: c['State'].update(Pid=10003)]
        for mutate in mutations:
            with self.subTest(mutation=mutate):
                self.f.container = copy.deepcopy(original)
                mutate(self.f.container)
                with self.assertRaises(core.ObservationError):
                    self.observe()
        self.f.container = original
        with patch.object(core, 'proc_birth', return_value='999'), self.assertRaises(core.ObservationError):
            self.observe()
        self.assert_no_effects()

    def load_entry(self):
        spec = importlib.util.spec_from_file_location('deactivating_qwen_entry', base.CANDIDATE / 'legacy_entry.py')
        entry = importlib.util.module_from_spec(spec)
        saved = sys.path[:]
        try:
            with patch.dict(sys.modules, {'modelctl': core, 'supervisor': base.supervisor}):
                spec.loader.exec_module(entry)
        finally:
            sys.path[:] = saved
        return entry

    def invoke_entry(self, entry):
        with patch.object(entry.os, 'geteuid', return_value=0), \
             patch.object(entry.sys, 'flags', SimpleNamespace(isolated=True)), \
             patch.object(entry.sys, 'argv', ['legacy_entry.py', 'qwen-exact-stop']), \
             patch.object(entry, 'protect', return_value=None), \
             patch.object(entry, 'load_production_manifest', return_value={'models': legacy.definitions()}), \
             patch.object(entry, 'SystemAdapter', return_value=self.f.adapter), \
             patch.object(entry, 'Controller', return_value=self.ctl):
            return entry.main()

    def test_real_native_execstop_issues_one_exact_cid_stop_after_owner_exit(self):
        entry = self.load_entry()
        real_command = self.f.command
        stops = []

        def command(argv, timeout=.65):
            if argv[:2] == ['/usr/bin/docker', 'stop']:
                stops.append((argv, timeout))
                self.f.present = False
                self.f.named = False
                return fixtures.old.CID
            return real_command(argv, timeout)

        with patch.object(self.f.adapter, 'command', side_effect=command):
            self.assertEqual(0, self.invoke_entry(entry))
        self.assertEqual([(['/usr/bin/docker', 'stop', '--time', '90', fixtures.old.CID], 95)], stops)
        data = self.ctl.store.read()
        self.assertEqual('STOPPING', data['runs'][legacy.QWEN]['state'])
        self.assertEqual(legacy.QWEN, data['reservation'])
        self.assertFalse(self.f.adapter.resources_released(self.f.row, data['runs'][legacy.QWEN]))

    def test_native_execstop_rechecks_invocation_before_exact_cid_effect(self):
        entry = self.load_entry()
        real_unit = self.f.adapter.unit
        reads = []

        def unit(*args):
            observed = real_unit(*args)
            reads.append(observed)
            if len(reads) > 1:
                observed['InvocationID'] = 'f' * 32
            return observed

        with patch.object(self.f.adapter, 'unit', side_effect=unit), self.assertRaises(core.ObservationError):
            self.invoke_entry(entry)
        self.assert_no_effects()


if __name__ == '__main__':
    unittest.main(verbosity=2)
