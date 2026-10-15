"""Real controller/release race; only host seams are injected, no native effects."""
import copy
import hashlib
from pathlib import Path
import unittest
from unittest.mock import patch
import test_final5 as fixtures
core,legacy=fixtures.core,fixtures.legacy

class QwenStopExitRace(fixtures.Fixture):
    def setUp(self):
        super().setUp()
        self.f.run.update(state='READY',cancelled=False,engine_birth='202');self.f.run['owner']['birth']='101'
        self.proc=self.root/'proc';self.proc.mkdir();actual_path=core.Path
        def routed_path(*parts):
            p=actual_path(*parts)
            return self.proc/p.relative_to('/proc') if p==actual_path('/proc') or actual_path('/proc') in p.parents else p
        self.stage='running';self.engine_birth=None;self.early=False;self.after_stop=lambda:None;self.after_absence=lambda:None
        self.terminates=self.proofs=self.forces=0;self.effects=[]
        for p in [patch.object(core,'Path',side_effect=routed_path),patch.object(core,'proc_birth',side_effect=lambda pid:'101' if pid==10001 else ('202' if self.stage=='running' and pid==10002 else self.engine_birth)),patch.object(legacy,'effective_unit',return_value={'control_safe':True})]:p.start();self.addCleanup(p.stop)
        self.f.adapter.cgroup_released=lambda *a:True;self.f.adapter.owned_cgroups_released=lambda *a:True;self.f.adapter.gpu_pids=lambda:[]
        actual_process=core.SystemAdapter.process_released
        def process(pid,birth):
            result=actual_process(pid,birth)
            if pid==10002 and result and self.stage=='race':
                self.stage='released';self.f.present=self.f.named=False;self.after_absence()
            return result
        self.f.adapter.process_released=process;actual_command=self.f.command
        def command(argv,timeout=.65):
            if argv[:2]==['/usr/bin/systemctl','stop']:
                self.effects.append(argv);self.transition();return ''
            return actual_command(argv,timeout)
        self.f.adapter.command=command;actual_terminate=self.f.adapter.terminate;actual_proof=self.f.adapter.resources_released
        def terminate(row,run):
            self.terminates+=1
            if self.early:self.transition()
            return actual_terminate(row,run)
        def proof(row,run):self.proofs+=1;return actual_proof(row,run)
        def force(*a):self.forces+=1;raise AssertionError('No force/replayed effect allowed')
        self.f.adapter.terminate=terminate;self.f.adapter.resources_released=proof;self.f.adapter.force=force;self.save()
        self.original_container=copy.deepcopy(self.f.container);self.original_unit=copy.deepcopy(self.f.unit)
    def transition(self):
        self.stage='race';self.f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='');self.after_stop()
    def reset(self):
        self.f.run.update(state='READY',cancelled=False);self.f.container=copy.deepcopy(self.original_container);self.f.unit=copy.deepcopy(self.original_unit);self.f.present=self.f.named=True
        self.stage='running';self.engine_birth=None;self.terminates=self.proofs=self.forces=0;self.after_stop=lambda:None;self.after_absence=lambda:None
        self.f.adapter.owned_cgroups_released=lambda *a:True;self.f.adapter.gpu_pids=lambda:[];self.save()
    def retained(self):
        d=self.ctl.store.read();self.assertEqual('STOPPING',d['runs'][legacy.QWEN]['state']);self.assertEqual(legacy.QWEN,d['reservation']);self.assertEqual(1,self.terminates);self.assertEqual(0,self.forces)
    def test_exact_original_error_digest(self):
        self.assertEqual('f66c71783f8c1d20b1b4fe3e3ece121b3ff1006ee09102e1a1d8bd2edee216ce',hashlib.sha256(b'Spark V4: Legacy engine PID birth replaced\n').hexdigest())
    def test_real_teardown_exit_one_fresh_full_proof_without_effect_replay(self):
        self.assertEqual('STOPPED',self.ctl.stop(legacy.QWEN)['state']);d=self.ctl.store.read();r=d['runs'][legacy.QWEN]
        self.assertIsNone(d['reservation']);self.assertEqual(self.f.run['run_id'],r['run_id']);self.assertEqual(self.f.run['generation'],r['generation']);self.assertEqual('Legacy engine PID birth replaced',r['stop_observation_recovery']['original_error'])
        self.assertEqual(2,self.proofs);self.assertEqual(1,self.terminates);self.assertEqual(1,len(self.effects));self.assertEqual(0,self.forces)
    def test_exit_before_termination_observation_needs_no_stop_effect(self):
        self.early=True;self.assertEqual('STOPPED',self.ctl.stop(legacy.QWEN)['state']);self.assertEqual(1,self.proofs);self.assertEqual([],self.effects);self.assertEqual(0,self.forces)
    def test_reused_birth_wrong_pid_created_image_name_argv_invocation_refused(self):
        mutations=[lambda:setattr(self,'engine_birth','999'),lambda:self.f.container['State'].update(Pid=10003),lambda:self.f.container.update(Created='other'),lambda:self.f.container.update(Image='sha256:'+'f'*64),lambda:self.f.container.update(Name='/other'),lambda:self.f.container['Config'].update(Cmd=['other']),lambda:self.f.unit.update(InvocationID='f'*32)]
        for mutate in mutations:
            with self.subTest(mutate=mutate):
                self.reset();self.after_stop=mutate
                with self.assertRaises(core.ObservationError):self.ctl.stop(legacy.QWEN)
                self.assertEqual(1,self.proofs);self.retained()
    def test_still_deactivating_active_cgroup_or_gpu_cannot_release(self):
        for held in ['deactivating','cgroup','gpu']:
            with self.subTest(held=held):
                self.reset()
                if held=='deactivating':self.after_stop=lambda:self.f.unit.update(ActiveState='deactivating')
                if held=='cgroup':self.f.adapter.owned_cgroups_released=lambda *a:False
                if held=='gpu':self.f.adapter.gpu_pids=lambda:[30003];self.f.adapter.process_cgroups=lambda *a:['/system.slice/docker-'+fixtures.old.CID+'.scope']
                with self.assertRaisesRegex(core.ObservationError,'fresh exact Qwen release proof still active') as c:self.ctl.stop(legacy.QWEN)
                self.assertEqual('Legacy engine PID birth replaced',str(c.exception.__cause__));self.assertEqual(2,self.proofs);self.retained()
    def test_fresh_pid_reuse_and_named_replacement_are_refused(self):
        def reuse():
            p=self.proc/'10002';p.mkdir();(p/'stat').write_text('10002 (reused) '+' '.join(['S']+['0']*18+['999']))
        for mutate in [reuse,lambda:setattr(self.f,'named',True)]:
            with self.subTest(mutate=mutate):
                self.reset();self.after_absence=mutate
                with self.assertRaises(core.ObservationError):self.ctl.stop(legacy.QWEN)
                self.assertEqual(2,self.proofs);self.retained()
                if (self.proc/'10002/stat').exists():(self.proc/'10002/stat').unlink();(self.proc/'10002').rmdir()
    def test_durable_identity_and_cancel_drift_rejected_before_fresh_proof(self):
        for k,v in [('cancelled',False),('run_id','f'*32),('attempt','f'*32),('container_created','other'),('legacy_declaration_sha256','f'*64)]:
            with self.subTest(key=k):
                self.reset()
                def drift():
                    d=self.ctl.store.read();d['runs'][legacy.QWEN][k]=v;self.ctl.store.write(d)
                self.after_absence=drift
                with self.assertRaisesRegex(core.Refusal,'identity/cancellation changed'):self.ctl.stop(legacy.QWEN)
                self.assertEqual(1,self.proofs);self.assertEqual(0,self.forces)
    def test_untagged_same_error_noncancelled_ready_and_other_model_refuse(self):
        expected={**self.f.run,'state':'STOPPING','cancelled':True}
        with self.assertRaises(core.ObservationError):self.ctl._finish_qwen_stop_exit(self.f.row,expected,core.ObservationError('Legacy engine PID birth replaced'))
        self.assertEqual(0,self.proofs);self.stage='race';self.f.unit.update(ActiveState='inactive',MainPID='0')
        for delta in [{'cancelled':False},{'state':'READY'},{'model_id':legacy.GPT}]:
            with self.subTest(delta=delta),self.assertRaises(core.ObservationError) as c:self.f.adapter.observe(self.f.row,{**expected,**delta})
            self.assertFalse(hasattr(c.exception,'qwen_stopping_run'))

if __name__=='__main__':unittest.main(verbosity=2)
