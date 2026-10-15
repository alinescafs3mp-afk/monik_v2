"""FINAL5 regressions; original178 test bytes remain unchanged.

The original metadata fixture gains ONLY root-observed explicit Docker defaults,
which the stricter production qualifier now requires instead of guessing missing
historical fields. The original assertions are not changed or skipped.
"""
import copy
import hashlib
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import test_legacy_shared as old
core=old.core;legacy=old.legacy
_root_defaults=json.loads((Path(__file__).parent.parent/'ROOT-RESOURCE-DEFAULTS.json').read_text())['default_resource_shape']
_original_setup=old.LegacyAdapter.setUp

def _setup_with_observed_resource_shape(self):
    _original_setup(self)
    for key,value in legacy.RESOURCE_DEFAULTS.items():
        assert key in _root_defaults and type(value) is type(_root_defaults[key]) and value==_root_defaults[key]
        self.container['HostConfig'].setdefault(key,copy.deepcopy(_root_defaults[key]))
    # Root's separate 2026-10-15 real create/start fixture observed false -> null.
    # Keep the earlier resource evidence file unchanged.
    self.container['HostConfig'].setdefault('OomKillDisable',False)
    self.run['legacy_declaration_sha256']=legacy.declaration_digest(self.container)
old.LegacyAdapter.setUp=_setup_with_observed_resource_shape

class Fixture(unittest.TestCase):
    def setUp(self):
        self.f=old.LegacyAdapter();self.f.setUp();self.addCleanup(self.f.doCleanups)
        self.temp=tempfile.TemporaryDirectory();self.addCleanup(self.temp.cleanup)
        self.root=Path(self.temp.name)
        for row in legacy.definitions():(self.root/row['id']).mkdir()
        self.ctl=core.Controller({'models':legacy.definitions()},self.f.adapter,store=core.StateStore(self.root,False),log_root=self.root,protected=False)
    def save(self):
        self.ctl.store.write({'schema_version':4,'reservation':self.f.row['id'],'counters':{self.f.row['id']:1},'runs':{self.f.row['id']:self.f.run}})
    def resource_seams(self):
        self.f.adapter.process_released=lambda *a:True
        self.f.adapter.owned_cgroups_released=lambda *a:True
        self.f.adapter.gpu_pids=lambda:[]

class OptionalCapturedDeclaration(Fixture):
    def test_actual_ready_and_stopped_export_hashes_match_exact_optional_field(self):
        f=self.f;f.run['state']='READY';f.adapter.api_ready=lambda r:(True,'passive injected host seam');self.save()
        ready=self.ctl.bridge_snapshot(legacy.QWEN)
        self.assertEqual(f.run['legacy_declaration_sha256'],ready['instance']['legacy_declaration_sha256'])
        self.assertEqual(ready['instance_identity'],legacy.digest(ready['instance']))
        f.present=False;f.named=False;f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='');self.resource_seams()
        data=self.ctl.store.read();data['runs'][legacy.QWEN].update(state='STOPPED',cancelled=True);data['reservation']=None;self.ctl.store.write(data)
        stopped=self.ctl.bridge_snapshot(legacy.QWEN)
        self.assertTrue(stopped['shutdown_verified'])
        self.assertEqual(ready['instance_identity'],stopped['shutdown_witness']['instance_identity'])
        self.assertEqual(f.run['legacy_declaration_sha256'],stopped['shutdown_witness']['legacy_declaration_sha256'])
    def test_before_capture_omits_optional_field_never_exports_null(self):
        self.f.run.update(owner=None,container_id=None,container_created=None,engine_pid=None,engine_birth=None)
        self.f.run.pop('legacy_declaration_sha256');self.save()
        snapshot=self.ctl.bridge_snapshot(legacy.QWEN)
        self.assertNotIn('legacy_declaration_sha256',snapshot['instance'])
        self.assertEqual(snapshot['instance_identity'],legacy.digest(snapshot['instance']))
    def test_receipt_capture_is_monotonic_and_replacement_cannot_rebind(self):
        run=copy.deepcopy(self.f.run);decl=run.pop('legacy_declaration_sha256')
        data={'counters':{legacy.QWEN:1}}
        now=int(self.ctl.wall_time()*1000)
        req={'schema_version':1,'operation_id':'root-capture','profile_id':legacy.QWEN,'action':'start',
             'profile_revision':self.ctl.profile_revision(legacy.QWEN),'expected_generation':run['generation'],
             'expected_instance':self.ctl.instance_identity(run),'control_epoch':'offline-epoch',
             'agent_session':'offline-session','boot_id':'offline-boot','issued_at_ms':now,'deadline_ms':now+10000}
        digest=self.ctl._validate_typed(req)
        self.ctl._record_typed(data,req,digest,'prepared',run)
        self.assertNotIn('legacy_declaration_sha256',data['bridge_operations']['root-capture']['bound_instance'])
        run['legacy_declaration_sha256']=decl;self.ctl._record_typed(data,req,digest,'completed',run)
        self.assertEqual(decl,data['bridge_operations']['root-capture']['bound_instance']['legacy_declaration_sha256'])
        run['legacy_declaration_sha256']='f'*64
        with self.assertRaises(core.ObservationError):self.ctl._record_typed(data,req,digest,'completed',run)
        replacement=copy.deepcopy(run);replacement['run_id']='f'*32
        self.ctl._record_typed(data,req,digest,'unresolved',replacement)
        self.assertEqual(decl,data['bridge_operations']['root-capture']['bound_instance']['legacy_declaration_sha256'])
    def test_active_engine_before_decl_capture_is_unknown(self):
        self.f.run.pop('legacy_declaration_sha256')
        self.f.container['Config']['Labels']={'spark.v4.model':legacy.QWEN,'spark.v4.run':self.f.run['run_id'],'spark.v4.generation':'1','spark.v4.attempt':self.f.run['attempt']}
        with self.assertRaises(core.ObservationError):self.f.adapter.observe(self.f.row,self.f.run)

class FrozenAdoptionAndResources(Fixture):
    def test_each_named_resource_drift_and_missing_field_refused(self):
        cases={'Memory':1024**3,'MemorySwap':2*1024**3,'NanoCpus':1000000000,'CpuQuota':10000,'CpusetCpus':'0','PidsLimit':1}
        for key,value in cases.items():
            with self.subTest(field=key):
                c=copy.deepcopy(self.f.container);c['HostConfig'][key]=value
                with self.assertRaises(core.ObservationError):legacy.check_declaration(self.f.row,c,old.KEY,self.f.adapter)
        for key in legacy.RESOURCE_DEFAULTS:
            c=copy.deepcopy(self.f.container);del c['HostConfig'][key]
            with self.subTest(missing=key),self.assertRaises(core.ObservationError):legacy.check_declaration(self.f.row,c,old.KEY,self.f.adapter)
    def test_readoption_cannot_rewrite_any_same_cid_declaration_bytes(self):
        f=self.f
        with patch.object(legacy,'qualify',return_value=({'control_safe':False},[],old.KEY)),patch.object(f.adapter,'process_cgroups',return_value=['/system.slice/docker-'+old.CID+'.scope']),patch.object(legacy.os,'readlink',return_value='/usr/bin/docker'),patch.object(Path,'read_bytes',return_value=b'\0'.join(x.encode() for x in legacy.original_qwen_owner_argv(f.row,old.KEY))+b'\0'):
            self.ctl.adopt_legacy(legacy.QWEN)
            with open(self.ctl.store.path,'rb') as stream:before=stream.read()
            f.container['Config']['Labels']['unchanged-engine-but-different-declaration']='new'
            with self.assertRaises(core.Refusal):self.ctl.adopt_legacy(legacy.QWEN)
            with open(self.ctl.store.path,'rb') as stream:self.assertEqual(before,stream.read())

class OwnedContainerReaping(Fixture):
    def configure(self,mid,never_started=False):
        f=self.f;f.present=True;f.named=True;f.run.update(engine_pid=10002,engine_birth='birth10002');f.container['Created']=f.run['container_created']
        f.row=next(r for r in legacy.definitions() if r['id']==mid);r=f.row['runtime']
        f.image.update(Id=r['image_id'])
        legacy.IMAGE_CONFIG_PINS[r['image_id']]=legacy.digest(f.defaults)
        f.container.update(Image=r['image_id'],Name='/'+r['container_name'])
        f.container['Config'].update(Cmd=legacy.command_argv(f.row,old.KEY),Env=['BASE=immutable']+[k+'='+v for k,v in r['env'].items()],Labels={'spark.v4.model':mid,'spark.v4.run':f.run['run_id'],'spark.v4.generation':'1','spark.v4.attempt':f.run['attempt']})
        f.container['HostConfig']['AutoRemove']=r['auto_remove']
        f.container['Mounts']=[{'Source':m['source'],'Destination':m['target'],'RW':not m['readonly'],'Type':'bind'} for m in r['mounts']]
        f.container['State'].update(Running=False,Pid=0,StartedAt='0001-01-01T00:00:00Z' if never_started else '2026-10-14T01:00:01Z')
        f.run.update(model_id=mid,state='STOPPED',cancelled=True,container_create_attempted=True)
        f.run['owner']['unit']=core.unit_name(mid,f.run['run_id'])
        if never_started:f.run.update(engine_pid=None,engine_birth=None)
        f.run['legacy_declaration_sha256']=legacy.declaration_digest(f.container)
        f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
        self.resource_seams();self.rm=[];self.handoffs=[];self.mode='normal';self.stop_autoremove=False
        original=f.command
        def command(args,timeout=.65):
            if args[:2]==['/usr/bin/systemctl','list-units']:return ''
            if args[:2]==['/usr/bin/docker','ps'] and '--filter' in args:
                return old.CID if f.named and r['container_name'] in args[-1] else ''
            if args[:2]==['/usr/bin/docker','rm']:
                self.rm.append(copy.deepcopy(args))
                self.assertEqual(['/usr/bin/docker','rm',old.CID],args)
                self.assertIs(f.container['State']['Running'],False)
                self.assertTrue(f.run.get('owner'))
                if self.mode=='fail-before':raise core.ObservationError('bounded ambiguous delete')
                f.present=False;f.named=False
                if self.mode=='fail-after':raise core.ObservationError('lost delete response')
                return old.CID
            if args[:2]==['/usr/bin/systemctl','stop']:
                f.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
                f.container['State'].update(Running=False,Pid=0)
                if self.stop_autoremove:f.present=False;f.named=False
                return ''
            return original(args,timeout)
        f.adapter.command=command;f.adapter.preflight=lambda r:None;f.adapter.verify_bundle=lambda r:[]
        f.adapter.handoff=lambda r,run:self.handoffs.append(copy.deepcopy(run))
        self.ctl.store.write({'schema_version':4,'reservation':None,'counters':{mid:1},'runs':{mid:copy.deepcopy(f.run)},'bridge_operations':{'historical':{'receipt':{'phase':'completed','run_id':f.run['run_id'],'generation':1},'digest':'unchanged','bound_instance':copy.deepcopy(f.run)}}})
        self.old_receipt=copy.deepcopy(self.ctl.store.read()['bridge_operations'])
    def start(self):return self.ctl._start(self.f.row['id'],wait=False)
    def assert_receipts_preserved(self):self.assertEqual(self.old_receipt,self.ctl.store.read()['bridge_operations'])
    def test_stopped_gpt_reaps_full_cid_before_new_generation(self):
        self.configure(legacy.GPT);self.start();data=self.ctl.store.read()
        self.assertEqual(1,len(self.rm));self.assertEqual(1,len(self.handoffs));self.assertEqual(2,data['counters'][legacy.GPT]);self.assert_receipts_preserved()
        self.assertEqual('confirmed',data['legacy_reaps'][self.f.run['run_id']]['phase'])
        self.assertIsNone(data['runs'][legacy.GPT]['container_id'])
    def test_cancelled_never_started_created_cid_reaps_for_both_ids(self):
        for mid in legacy.IDS:
            with self.subTest(mid=mid):
                self.configure(mid,True);data=self.ctl.store.read();data['runs'][mid]['state']='STOPPING';data['reservation']=mid;self.ctl.store.write(data)
                self.start();self.assertEqual(1,len(self.rm));self.assertEqual(2,self.ctl.store.read()['counters'][mid]);self.assert_receipts_preserved()
    def test_actual_stop_then_start_for_both_legacy_ids(self):
        for mid in legacy.IDS:
            with self.subTest(mid=mid):
                self.configure(mid);self.stop_autoremove=mid==legacy.QWEN
                f=self.f;f.unit.update(ActiveState='active',MainPID='10001');f.container['State'].update(Running=True,Pid=10002)
                data=self.ctl.store.read();data['runs'][mid].update(state='READY',cancelled=False);data['reservation']=mid;self.ctl.store.write(data)
                with patch.object(legacy,'effective_unit',return_value={'control_safe':True}):stopped=self.ctl._stop(mid)
                self.assertEqual('STOPPED',stopped['state']);self.start();self.assertEqual(1,len(self.handoffs));self.assertEqual(0 if mid==legacy.QWEN else 1,len(self.rm));self.assert_receipts_preserved()
    def test_linked_typed_restart_and_duplicate_use_same_reap_and_generation(self):
        for mid in legacy.IDS:
            with self.subTest(mid=mid):
                self.configure(mid);self.stop_autoremove=mid==legacy.QWEN
                f=self.f;f.unit.update(ActiveState='active',MainPID='10001');f.container['State'].update(Running=True,Pid=10002)
                f.adapter.api_ready=lambda r:(True,'passive host fixture')
                data=self.ctl.store.read();data['runs'][mid].update(state='READY',cancelled=False);data['reservation']=mid;self.ctl.store.write(data)
                self.ctl.enable_bridge('final5-offline-session')
                artifact=self.root/'sealed-small-fixture';artifact.write_bytes(b'exact offline stat seal')
                st=artifact.stat();f.adapter.verify_bundle=lambda r:[(artifact,(st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns,st.st_ctime_ns))]
                self.ctl.prepare_bundle(mid)
                def request(action,op,**extra):
                    snap=self.ctl.bridge_snapshot(mid);now=int(self.ctl.wall_time()*1000)
                    return {'schema_version':1,'operation_id':op,'profile_id':mid,'action':action,'profile_revision':snap['profile_revision'],'expected_generation':snap['generation'],'expected_instance':snap['instance_identity'],'control_epoch':snap['control_epoch'],'agent_session':snap['agent_session'],'boot_id':snap['boot_id'],'issued_at_ms':now,'deadline_ms':now+10000,**extra}
                stop=request('stop','linked-stop')
                with patch.object(legacy,'effective_unit',return_value={'control_safe':True}):self.assertEqual('dispatched',self.ctl.submit_typed(stop)['phase'])
                stopped=self.ctl.reconcile_typed(stop,self.ctl.store.read()['bridge_authority'])
                self.assertEqual('completed',stopped['phase']);self.assertTrue(stopped['result']['shutdown_verified'])
                original_stop=copy.deepcopy(self.ctl.store.read()['bridge_operations']['linked-stop'])
                start=request('start','linked-start',parent_operation_id='linked-stop')
                with patch.object(legacy,'effective_unit',return_value={'control_safe':True}):
                    first=self.ctl.submit_typed(start);second=self.ctl.submit_typed(start)
                self.assertEqual(first,second);self.assertEqual(2,first['generation']);self.assertEqual(1,len(self.handoffs));self.assertEqual(0 if mid==legacy.QWEN else 1,len(self.rm))
                self.assertEqual(original_stop,self.ctl.store.read()['bridge_operations']['linked-stop'])

    def test_failed_delete_not_replayed_when_exact_cid_remains(self):
        self.configure(legacy.GPT);self.mode='fail-before'
        with self.assertRaises(core.Refusal):self.start()
        with self.assertRaises(core.Refusal):self.start()
        self.assertEqual(1,len(self.rm));self.assertEqual([],self.handoffs);self.assert_receipts_preserved()
        self.assertEqual('effect_attempted',self.ctl.store.read()['runs'][legacy.GPT]['container_reap']['phase'])
    def test_lost_delete_response_reconciles_absent_without_second_delete(self):
        self.configure(legacy.GPT);self.mode='fail-after'
        with self.assertRaises(core.Refusal):self.start()
        self.start();self.assertEqual(1,len(self.rm));self.assertEqual(1,len(self.handoffs));self.assert_receipts_preserved()
    def test_live_replaced_or_unreleased_instance_is_never_removed(self):
        for case in ('running','created','decl','resources'):
            with self.subTest(case=case):
                self.configure(legacy.GPT)
                if case=='running':self.f.container['State'].update(Running=True,Pid=10002)
                elif case=='created':self.f.container['Created']='replaced'
                elif case=='decl':self.f.container['HostConfig']['Memory']=1
                else:self.f.adapter.process_released=lambda *a:False
                try:self.start()
                except (core.Refusal,core.ObservationError):pass
                self.assertEqual([],self.rm);self.assertEqual([],self.handoffs)
    def test_fresh_authority_is_rechecked_after_reap_intent_fsync(self):
        self.configure(legacy.GPT);data=self.ctl.store.read();run=data['runs'][legacy.GPT];checks=[]
        def expires():
            checks.append(True)
            if len(checks)==2:raise core.Refusal('dispatch expired before rm')
        with self.ctl.store.lock():
            with self.assertRaises(core.Refusal):legacy.reap_stopped(self.ctl,data,self.f.row,run,expires)
        self.assertEqual([],self.rm);self.assertEqual('prepared',self.ctl.store.read()['runs'][legacy.GPT]['container_reap']['phase'])
        self.start();self.assertEqual(1,len(self.rm));self.assert_receipts_preserved()
    def test_foreign_same_name_or_missing_historical_resource_field_never_reaped(self):
        self.configure(legacy.GPT);self.f.present=False;self.f.named=True
        with self.assertRaises(core.ObservationError):self.start()
        self.assertEqual([],self.rm)
        self.configure(legacy.GPT);del self.f.container['HostConfig']['Memory'];self.f.run['legacy_declaration_sha256']=legacy.declaration_digest(self.f.container)
        data=self.ctl.store.read();data['runs'][legacy.GPT]['legacy_declaration_sha256']=self.f.run['legacy_declaration_sha256'];self.ctl.store.write(data)
        # The reaper never replaces the recorded declaration with a new host
        # qualification; incomplete historical resources must remain blocked.
        with self.assertRaises(core.ObservationError):legacy.check_declaration(self.f.row,self.f.container,old.KEY,self.f.adapter)
        with self.assertRaises(core.ObservationError):self.start()
        self.assertEqual([],self.rm);self.assertEqual([],self.handoffs)

if __name__=='__main__':unittest.main()
