"""Matching Qwen zombie is ACTIVE until the unchanged full release proof succeeds."""
import copy
from pathlib import Path
import unittest
from unittest.mock import patch
import test_qwen_stop_exit_race as old
core,legacy=old.core,old.legacy

class ExactZombieStop(unittest.TestCase):
    def case(self):
        previous=getattr(self,'current',None)
        if previous is not None:previous.doCleanups()
        c=old.QwenStopExitRace('test_exact_original_error_digest');c.setUp();self.current=c;self.addCleanup(c.doCleanups)
        actual_path=Path
        def path(*parts):
            p=actual_path(*parts)
            return c.proc/p.relative_to('/proc') if p==actual_path('/proc') or actual_path('/proc') in p.parents else p
        p=patch.object(legacy,'Path',side_effect=path);p.start();c.addCleanup(p.stop)
        return c
    @staticmethod
    def stat(c,state='Z',birth='202',pid=10002,raw=None):
        p=c.proc/str(pid);p.mkdir(exist_ok=True)
        (p/'stat').write_bytes(raw if raw is not None else (str(pid)+' (owned engine) '+' '.join([state]+['0']*18+[birth])).encode())
    def zombie(self,c):
        c.stage='race';c.f.unit.update(ActiveState='deactivating',MainPID='10001');self.stat(c)
    def test_actual_stop_waits_for_same_zombie_then_full_absence_no_extra_effect(self):
        c=self.case();c.after_stop=lambda:self.zombie(c);actual=c.f.adapter.resources_released;held=[]
        def proof(row,run):
            value=actual(row,run)
            if not value and not held:
                observed=c.f.adapter.observe(row,run);held.append(observed)
                self.assertTrue(observed['active']);self.assertTrue(observed['engine_active']);self.assertFalse(c.f.adapter.process_released(10002,'202'))
                self.assertEqual('STOPPING',c.ctl.store.read()['runs'][legacy.QWEN]['state'])
                (c.proc/'10002/stat').unlink();(c.proc/'10002').rmdir();c.f.present=c.f.named=False;c.stage='released';c.f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
            return value
        c.f.adapter.resources_released=proof
        result=c.ctl.stop(legacy.QWEN);self.assertEqual('STOPPED',result['state']);self.assertEqual(1,len(held));self.assertEqual(3,c.proofs);self.assertEqual(1,c.terminates);self.assertEqual(1,len(c.effects));self.assertEqual(0,c.forces)
        data=c.ctl.store.read();self.assertIsNone(data['reservation']);self.assertEqual(1,data['runs'][legacy.QWEN]['generation']);self.assertNotIn('stop_observation_recovery',data['runs'][legacy.QWEN])
    def test_matching_zombie_always_active_never_resources_released(self):
        c=self.case();self.zombie(c);run={**c.f.run,'state':'STOPPING','cancelled':True}
        for _ in range(3):
            self.assertEqual({'active':True,'engine_active':True,'supervisor_active':True,'engine_pid':10002},c.f.adapter.observe(c.f.row,run));self.assertFalse(c.f.adapter.resources_released(c.f.row,run))
        self.assertEqual([],c.effects);self.assertEqual(0,c.terminates);self.assertEqual(0,c.forces)
    def test_zombie_changed_tick_foreign_pid_and_unknown_stat_refused(self):
        cases=[('reused_tick',10002,'Z','999',None),('foreign_pid',10003,'Z','202',None),('live',10002,'S','202',None),('malformed',10002,'Z','202',b'bad stat'),('wrong_pid_prefix',10002,'Z','202',b'999 (other) '+b' '.join([b'Z']+[b'0']*18+[b'202'])),('oversized',10002,'Z','202',b'x'*4097)]
        for name,pid,state,birth,raw in cases:
            with self.subTest(name=name):
                c=self.case();self.zombie(c);self.stat(c,state,birth,pid,raw);c.f.container['State']['Pid']=pid;run={**c.f.run,'state':'STOPPING','cancelled':True}
                with self.assertRaises(core.ObservationError):c.f.adapter.observe(c.f.row,run)
                self.assertEqual([],c.effects)
        c=self.case();self.zombie(c);run={**c.f.run,'state':'STOPPING','cancelled':True}
        with patch.object(legacy.os,'open',side_effect=PermissionError('unreadable')),self.assertRaises(core.ObservationError):c.f.adapter.observe(c.f.row,run)
    def test_raw_missing_cannot_claim_zombie_and_original_absent_path_remains(self):
        c=self.case();self.zombie(c);(c.proc/'10002/stat').unlink();(c.proc/'10002').rmdir();self.assertFalse(legacy._same_birth_zombie(10002,'202'))
        run={**c.f.run,'state':'STOPPING','cancelled':True}
        with self.assertRaises(core.ObservationError) as caught:c.f.adapter.observe(c.f.row,run)
        self.assertTrue(hasattr(caught.exception,'qwen_stopping_run'))
        self.assertTrue(caught.exception.qwen_stopping_run==run)
    def test_ready_uncancelled_other_model_and_active_unit_not_relaxed(self):
        for change in [{'state':'READY'},{'cancelled':False},{'model_id':legacy.GPT},{'row_id':legacy.GPT},{'unit_active':'active'},{'owner':None}]:
            with self.subTest(change=change):
                c=self.case();self.zombie(c);run={**c.f.run,'state':'STOPPING','cancelled':True};row=copy.deepcopy(c.f.row)
                for k,v in change.items():
                    if k=='row_id':row['id']=v
                    elif k=='unit_active':c.f.unit['ActiveState']=v
                    else:run[k]=v
                with self.assertRaises(core.ObservationError):c.f.adapter.observe(row,run)
                self.assertEqual([],c.effects)
    def test_foreign_full_identity_and_owner_drift_refused(self):
        mutations=[lambda c:c.f.container.update(Id='f'*64),lambda c:c.f.container.update(Created='foreign'),lambda c:c.f.container.update(Image='sha256:'+'f'*64),lambda c:c.f.container.update(Name='/foreign'),lambda c:c.f.container['Config'].update(Cmd=['foreign']),lambda c:c.f.unit.update(InvocationID='f'*32),lambda c:c.f.run['owner'].update(birth='999')]
        for mutation in mutations:
            with self.subTest(mutation=mutation):
                c=self.case();self.zombie(c);mutation(c);run={**c.f.run,'state':'STOPPING','cancelled':True}
                with self.assertRaises(core.ObservationError):c.f.adapter.observe(c.f.row,run)
                self.assertEqual([],c.effects)
    def test_surviving_full_release_resources_still_refuse_after_zombie(self):
        for held in ['owner','cgroup','gpu']:
            with self.subTest(held=held):
                c=self.case();c.f.present=c.f.named=False;c.stage='released';c.f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='');run={**c.f.run,'state':'STOPPING','cancelled':True}
                if held=='owner':self.stat(c,state='S',birth='101',pid=10001)
                elif held=='cgroup':c.f.adapter.owned_cgroups_released=lambda *a:False
                else:c.f.adapter.gpu_pids=lambda:[30003];c.f.adapter.process_cgroups=lambda *a:['/system.slice/docker-'+old.fixtures.old.CID+'.scope']
                self.assertFalse(c.f.adapter.resources_released(c.f.row,run))
                self.assertEqual([],c.effects)

if __name__=='__main__':unittest.main(verbosity=2)
