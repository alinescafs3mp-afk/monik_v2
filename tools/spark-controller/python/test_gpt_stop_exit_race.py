"""GPT cancelled-exit positives/negative controls at unchanged real host seams."""
import copy
from pathlib import Path
import unittest
from unittest.mock import patch
import test_qwen_stop_exit_race as old

core, legacy = old.core, old.legacy


class GptStopExitRace(unittest.TestCase):
    def case(self):
        previous = getattr(self, 'current', None)
        if previous is not None: previous.doCleanups()
        c = old.QwenStopExitRace('test_exact_original_error_digest')
        c.setUp(); self.current = c; self.addCleanup(c.doCleanups)
        f = c.f
        f.row = legacy.approved_gpt_declarations()[4]
        r = f.row['runtime']; mid = legacy.GPT
        f.image['Id'] = r['image_id']
        legacy.IMAGE_CONFIG_PINS[r['image_id']] = legacy.digest(f.defaults)
        f.container.update(Image=r['image_id'], Name='/' + r['container_name'])
        f.container['Config'].update(Cmd=legacy.command_argv(f.row, old.fixtures.old.KEY),
            Env=['BASE=immutable'] + [k+'='+v for k,v in r['env'].items()],
            Labels={'spark.v4.model':mid, 'spark.v4.run':f.run['run_id'],
                    'spark.v4.generation':'1', 'spark.v4.attempt':f.run['attempt']})
        f.container['HostConfig']['AutoRemove'] = False
        f.container['Mounts'] = [{'Source':m['source'], 'Destination':m['target'],
                                 'RW':not m['readonly'], 'Type':'bind'} for m in r['mounts']]
        f.run.update(model_id=mid, legacy_declaration_sha256=legacy.declaration_digest(f.container))
        f.run['owner']['unit'] = core.unit_name(mid, f.run['run_id'])
        f.unit['ControlGroup'] = '/system.slice/' + f.run['owner']['unit']
        c.ctl.rows[mid] = f.row
        def stopped_container():
            # GPT has AutoRemove=false; its stopped full CID remains as evidence.
            f.present = f.named = True
            f.container['State'].update(Running=False, Pid=0)
        c.after_absence = stopped_container
        c.save()
        actual_path = Path
        def routed_path(*parts):
            p = actual_path(*parts)
            return c.proc/p.relative_to('/proc') if p==actual_path('/proc') or actual_path('/proc') in p.parents else p
        q = patch.object(legacy, 'Path', side_effect=routed_path); q.start(); c.addCleanup(q.stop)
        return c

    def test_actual_cancelled_exit_fresh_full_proof_no_effect_replay(self):
        c = self.case()
        self.assertEqual('STOPPED', c.ctl.stop(legacy.GPT)['state'])
        d = c.ctl.store.read(); r = d['runs'][legacy.GPT]
        self.assertIsNone(d['reservation'])
        self.assertEqual('exact_cancelled_gpt_engine_exited', r['stop_observation_recovery']['classification'])
        self.assertEqual('Legacy engine PID birth replaced', r['stop_observation_recovery']['original_error'])
        self.assertEqual(2, c.proofs)
        self.assertEqual(1, c.terminates)
        self.assertEqual(1, len(c.effects))
        self.assertEqual(0, c.forces)
        self.assertTrue(c.f.present)

    def test_exit_before_termination_observation_adds_no_stop_effect(self):
        c = self.case(); c.early = True
        self.assertEqual('STOPPED', c.ctl.stop(legacy.GPT)['state'])
        self.assertEqual([], c.effects)
        self.assertEqual(1, c.proofs)
        self.assertEqual(0, c.forces)

    def zombie(self, c, birth='202', state='Z'):
        c.stage = 'race'; c.f.unit.update(ActiveState='deactivating', MainPID='10001')
        p = c.proc/'10002'; p.mkdir(exist_ok=True)
        (p/'stat').write_text('10002 (owned engine) '+' '.join([state]+['0']*18+[birth]))

    def test_matching_zombie_is_active_not_ready_never_released(self):
        c = self.case(); self.zombie(c)
        run = {**c.f.run, 'state':'STOPPING', 'cancelled':True}
        d = c.ctl.store.read(); d['runs'][legacy.GPT] = run; c.ctl.store.write(d)
        c.f.adapter.api_ready = lambda *_: (_ for _ in ()).throw(AssertionError('cannot call readiness'))
        observed = c.f.adapter.observe(c.f.row, run)
        self.assertTrue(observed['active']); self.assertTrue(observed['engine_active'])
        self.assertFalse(c.f.adapter.resources_released(c.f.row, run))
        snap = c.ctl.status(legacy.GPT)
        self.assertEqual('STOPPING', snap['state']); self.assertFalse(snap['api_ready'])
        self.assertEqual([], c.effects)

    def test_reused_tick_live_process_and_ready_uncancelled_identity_refuse(self):
        for change in ['reused_tick', 'live', 'ready', 'uncancelled', 'foreign_model', 'active_unit']:
            with self.subTest(change=change):
                c = self.case(); self.zombie(c)
                run = {**c.f.run, 'state':'STOPPING', 'cancelled':True}
                if change == 'reused_tick': self.zombie(c, birth='999')
                if change == 'live': self.zombie(c, state='S')
                if change == 'ready': run['state'] = 'READY'
                if change == 'uncancelled': run['cancelled'] = False
                if change == 'foreign_model': run['model_id'] = legacy.QWEN
                if change == 'active_unit': c.f.unit['ActiveState'] = 'active'
                with self.assertRaises(core.ObservationError) as caught:
                    c.f.adapter.observe(c.f.row, run)
                self.assertFalse(hasattr(caught.exception, 'gpt_stopping_run'))
                self.assertEqual([], c.effects)

    def test_full_identity_and_pid_reuse_drift_cannot_release(self):
        mutations = [lambda c:setattr(c,'engine_birth','999'),
                     lambda c:c.f.container['State'].update(Pid=10003),
                     lambda c:c.f.container.update(Created='foreign'),
                     lambda c:c.f.container.update(Image='sha256:'+'f'*64),
                     lambda c:c.f.container.update(Name='/foreign'),
                     lambda c:c.f.container['Config'].update(Cmd=['foreign']),
                     lambda c:c.f.unit.update(InvocationID='f'*32)]
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                c = self.case(); c.after_stop = lambda:mutation(c)
                with self.assertRaises(core.ObservationError): c.ctl.stop(legacy.GPT)
                self.assertEqual('STOPPING', c.ctl.store.read()['runs'][legacy.GPT]['state'])
                self.assertEqual(1, c.terminates); self.assertEqual(0, c.forces)

    def test_full_cgroup_gpu_or_still_active_unit_keeps_reservation(self):
        for held in ['deactivating', 'cgroup', 'gpu']:
            with self.subTest(held=held):
                c = self.case()
                if held == 'deactivating': c.after_stop=lambda:c.f.unit.update(ActiveState='deactivating')
                if held == 'cgroup': c.f.adapter.owned_cgroups_released=lambda *_:False
                if held == 'gpu':
                    c.f.adapter.gpu_pids=lambda:[30003]
                    c.f.adapter.process_cgroups=lambda *_:['/system.slice/docker-'+old.fixtures.old.CID+'.scope']
                expected = ('Legacy supervisor invocation/PID birth replaced' if held == 'deactivating'
                            else 'fresh exact GPT release proof still active')
                with self.assertRaisesRegex(core.ObservationError, expected):
                    c.ctl.stop(legacy.GPT)
                self.assertEqual(legacy.GPT, c.ctl.store.read()['reservation'])
                self.assertEqual(1, c.terminates); self.assertEqual(0, c.forces)

    def test_missing_or_cross_model_tag_and_durable_cancel_drift_refuse(self):
        c = self.case(); expected={**c.f.run, 'state':'STOPPING', 'cancelled':True}
        for attr in [None, 'qwen_stopping_run']:
            e=core.ObservationError('Legacy engine PID birth replaced')
            if attr: setattr(e, attr, copy.deepcopy(expected))
            with self.assertRaises(core.ObservationError): c.ctl._finish_gpt_stop_exit(c.f.row, expected, e)
        self.assertEqual(0, c.proofs)
        c = self.case(); original=c.after_absence
        def drift():
            original();d=c.ctl.store.read();d['runs'][legacy.GPT]['cancelled']=False;c.ctl.store.write(d)
        c.after_absence=drift
        with self.assertRaisesRegex(core.Refusal, 'identity/cancellation changed'):
            c.ctl.stop(legacy.GPT)
        self.assertEqual(1, c.proofs);self.assertEqual(0, c.forces)


if __name__ == '__main__':
    unittest.main()
