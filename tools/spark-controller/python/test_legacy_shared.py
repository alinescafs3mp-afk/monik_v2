"""Private offline controls of the real shared Controller and legacy adapter.
All host commands are injected; no Docker/systemd/model effects are possible.
"""
import copy
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import patch
import test_bridge_candidate as base
core=base.modelctl
legacy=core.legacy_support()
KEY='K'*48
CID='c'*64

def qwen_worker_info_fixture():
    alias=legacy.definitions()[0]['runtime']['served_model_alias']
    return {'served_model_name':alias,'context_length':262144,'quantization':'modelopt_fp4',
            'tp_size':1,'dp_size':1,'version':'0.0.0.dev1+g4ccff141d',
            'startup_time':{'tokenizer_e2e':835.18},
            'internal_states':[{'served_model_name':alias,'model_path':'/models','tp_size':1,'dp_size':1,
                'memory_usage':{'token_capacity':267904},
                'startup_time':{'load_weight':726.48,'kv_cache_allocation':4.64,'scheduler_e2e':824.82}}]}

class LegacyDefinitions(unittest.TestCase):
    def test_seven_ids_same_manifest_and_bridge(self):
        m=json.loads((base.SOURCE_ROOT/'config/controller-manifest.json').read_text())
        m['models']+=legacy.definitions()
        self.assertIs(m,core.validate_manifest(m))
        self.assertEqual(tuple(core.IDS),tuple(base.bridge.IDS))
        env={'schema_version':1,'method':'observe_many','profile_ids':list(core.IDS)}
        self.assertEqual(env,base.bridge.decode_envelope(json.dumps(env).encode()))
    def test_reject_arbitrary_and_duplicate_profiles(self):
        for ids in [list(core.IDS)+['other'],[legacy.QWEN,legacy.QWEN],['spark-sglang.service']]:
            with self.assertRaises(ValueError): base.bridge.decode_envelope(json.dumps({'schema_version':1,'method':'observe_many','profile_ids':ids}).encode())
    def test_fixed_declarations_refuse_each_engine_change(self):
        row=legacy.definitions()[1]
        mutations=[('image','tag:latest'),('argv',['sh','-c','true']),('secret_reference','/tmp/key'),('mounts',[]),('env',{'BASH_ENV':'/tmp/code'})]
        for key,value in mutations:
            with self.subTest(key=key):
                changed=copy.deepcopy(row);changed['runtime'][key]=value
                with self.assertRaises(core.Refusal):legacy.validate_row(changed)
    def test_frozen_original_gpt_and_qwen_arguments(self):
        import shlex
        sources=base.CANDIDATE.parent/'copied-source/usr/local/sbin'
        for row,source,entry in [(legacy.definitions()[0],'spark-sglang-run','sglang serve'),(legacy.definitions()[1],'spark-start-gpt','vllm serve')]:
            raw=(sources/source).read_text().split(entry,1)[1]
            raw=raw.split('>/dev/null',1)[0].replace('\\\n',' ')
            actual=shlex.split(entry+raw)
            actual=['@SPARK_API_KEY@' if x in {'${SPARK_API_KEY}','$key'} else x for x in actual]
            self.assertEqual(row['runtime']['argv'],actual)
    def test_original_gpt_is_not_executable_effect_source(self):
        text=(base.CANDIDATE/'modelctl.py').read_text()+(base.CANDIDATE/'legacy_runtime.py').read_text()+(base.CANDIDATE/'supervisor.py').read_text()
        self.assertNotIn('drop_caches',text)
        self.assertNotIn('rm -f',text)
        self.assertNotIn('spark-start-gpt',text)
        self.assertNotIn('sparkarena/spark-vllm-docker:mxfp4',text)
        self.assertEqual('no',legacy.QWEN_DROP90.split('Restart=',1)[1].splitlines()[0])
    def test_legacy_create_has_frozen_image_no_shell_no_victims(self):
        for row in legacy.definitions():
            with patch.object(legacy,'read_secret',return_value=KEY):
                args=base.supervisor.container_argv(row,{'run_id':'a'*32,'generation':2,'attempt':'b'*32})
            self.assertEqual(['/usr/bin/docker','create'],args[:2])
            self.assertIn(row['runtime']['image_id'],args)
            self.assertIn('16g',args)
            self.assertIn('memlock=-1',args)
            self.assertIn('--log-driver',args);self.assertIn('max-size=10m',args);self.assertIn('max-file=3',args)
            self.assertNotIn('--entrypoint',args)
            self.assertNotIn('--user',args)
            self.assertNotIn('systemctl',args)
            self.assertNotIn('-c',args)
            self.assertEqual(row['id']==legacy.QWEN,'--rm' in args)

class LegacyAdapter(unittest.TestCase):
    def setUp(self):
        self.row=legacy.definitions()[0]
        self.defaults={'Env':['BASE=immutable'],'Entrypoint':['/opt/nvidia/nvidia_entrypoint.sh'],'User':'','WorkingDir':'/workspace'}
        self.image={'Id':self.row['runtime']['image_id'],'Architecture':'arm64','Os':'linux','Config':self.defaults}
        r=self.row['runtime']
        self.container={'Id':CID,'Created':'2026-10-14T01:00:00Z','Image':r['image_id'],'Name':'/'+r['container_name'],
          'Config':{'Cmd':legacy.command_argv(self.row,KEY),'Env':['BASE=immutable']+[k+'='+v for k,v in r['env'].items()],**{k:self.defaults[k] for k in ('Entrypoint','User','WorkingDir')},'Labels':{}},
          'HostConfig':{'NetworkMode':'host','IpcMode':'host','ShmSize':16*1024**3,'Ulimits':[{'Name':'memlock','Soft':-1,'Hard':-1},{'Name':'stack','Soft':67108864,'Hard':67108864}],
            'Privileged':False,'ReadonlyRootfs':False,'CgroupParent':'','AutoRemove':True,'RestartPolicy':{'Name':'no'},'DeviceRequests':[{'Count':-1,'Capabilities':[['gpu']],'Driver':'nvidia'}],'LogConfig':{'Type':'local','Config':{'max-size':'10m','max-file':'3'}}},
          'Mounts':[{'Source':m['source'],'Destination':m['target'],'RW':not m['readonly'],'Type':'bind'} for m in r['mounts']],
          'State':{'Running':True,'Pid':10002,'StartedAt':'2026-10-14T01:00:01Z'}}
        self.unit={'LoadState':'loaded','ActiveState':'active','SubState':'running','InvocationID':'e'*32,'MainPID':'10001','ControlGroup':'/system.slice/spark-sglang.service'}
        self.present=True;self.named=True;self.commands=[]
        self.adapter=core.SystemAdapter({'models':legacy.definitions()},command=self.command)
        self.run={'model_id':self.row['id'],'run_id':'a'*32,'generation':1,'attempt':'b'*32,'state':'STARTING','created_at':1,'cancelled':False,
          'owner':{'unit':'spark-sglang.service','invocation_id':'e'*32,'pid':10001,'birth':'birth10001'},'container_id':CID,'container_created':self.container['Created'],'engine_pid':10002,'engine_birth':'birth10002','legacy_declaration_sha256':legacy.declaration_digest(self.container)}
        self.addCleanup(patch.stopall)
        patch.object(core,'proc_birth',side_effect=lambda pid:'birth'+str(pid)).start()
        patch.dict(legacy.IMAGE_CONFIG_PINS,{r['image_id']:legacy.digest(self.defaults)}).start()
    def command(self,args,timeout=.65):
        self.commands.append(args)
        if args[:3]==['/usr/bin/docker','image','inspect']:return json.dumps([self.image])
        if args[:2]==['/usr/bin/systemctl','show']:return '\n'.join(k+'='+v for k,v in self.unit.items())
        if args[:2]==['/usr/bin/docker','ps']:
            if '--filter' in args:return CID if self.named else ''
            return CID if self.present else ''
        if args[:3]==['/usr/bin/docker','inspect','--type']:return json.dumps([self.container])
        raise AssertionError('Unapproved real command: '+repr(args))
    def test_qualification_actual_declaration_positive(self):
        self.assertEqual(legacy.declaration_digest(self.container),legacy.check_declaration(self.row,self.container,KEY,self.adapter))
    def test_wrong_image_entrypoint_environment_mount_gpu_policy_refused(self):
        mutations=[lambda c:c.update(Image='sha256:'+'f'*64),lambda c:c['Config'].update(Entrypoint=['/bin/sh']),lambda c:c['Config']['Env'].append('BASH_ENV=/evil'),lambda c:c['Mounts'][0].update(RW=True),lambda c:c['HostConfig'].update(Privileged=True),lambda c:c['HostConfig'].update(DeviceRequests=[]),lambda c:c['HostConfig'].update(CapAdd=['SYS_ADMIN']),lambda c:c['HostConfig'].update(IpcMode='private'),lambda c:c.update(Name='/other')]
        for mutate in mutations:
            c=copy.deepcopy(self.container);mutate(c)
            with self.assertRaises(core.ObservationError):legacy.check_declaration(self.row,c,KEY,self.adapter)
    def test_new_unbounded_or_remote_logging_refused_original_local_only(self):
        c=copy.deepcopy(self.container);c['HostConfig']['LogConfig']={'Type':'json-file','Config':{}}
        with self.assertRaises(core.ObservationError):legacy.check_declaration(self.row,c,KEY,self.adapter)
        self.assertTrue(legacy.check_declaration(self.row,c,KEY,self.adapter,allow_original_logs=True))
        c['HostConfig']['LogConfig']={'Type':'fluentd','Config':{'fluentd-address':'remote'}}
        with self.assertRaises(core.ObservationError):legacy.check_declaration(self.row,c,KEY,self.adapter,allow_original_logs=True)

    def test_exact_observe_positive(self):
        self.assertEqual({'active':True,'engine_active':True,'supervisor_active':True,'engine_pid':10002},self.adapter.observe(self.row,self.run))
    def test_replaced_cid_creation_pid_unit_and_declaration_fail(self):
        for key,value in [('Created','other'),('Image','sha256:'+'f'*64)]:
            old=self.container[key];self.container[key]=value
            with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)
            self.container[key]=old
        self.container['Config']['Cmd']=['other']
        with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)
        self.container['Config']['Cmd']=legacy.command_argv(self.row,KEY)
        self.unit['InvocationID']='f'*32
        with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)
    def test_qwen_rm_absence_is_observed_only_with_captured_birth_and_no_replacement(self):
        self.present=False;self.named=False;self.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
        self.assertFalse(self.adapter.observe(self.row,self.run)['active'])
        self.named=True
        with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)
        self.named=False;self.run['engine_birth']=None
        with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)
    def test_qwen_missing_cid_requires_release_of_process_cgroup_gpu(self):
        self.present=False;self.named=False;self.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
        patch.object(self.adapter,'process_released',return_value=True).start()
        patch.object(self.adapter,'owned_cgroups_released',return_value=True).start()
        patch.object(self.adapter,'gpu_pids',return_value=[]).start()
        self.assertTrue(self.adapter.resources_released(self.row,self.run))
        self.adapter.process_released=lambda *a:False
        self.assertFalse(self.adapter.resources_released(self.row,self.run))
    def test_stopped_process_state_and_unknown_create_have_no_certificate(self):
        self.unit.update(ActiveState='inactive',MainPID='0',ControlGroup='')
        self.container['State'].update(Running=False,Pid=0)
        self.run.pop('engine_birth')
        with self.assertRaises(core.ObservationError):self.adapter.resources_released(self.row,self.run)
        self.run.update(container_id=None,container_create_attempted=True)
        with self.assertRaises(core.ObservationError):self.adapter.resources_released(self.row,self.run)
    def test_typed_stop_never_invokes_named_docker(self):
        seen=[]
        with patch.object(legacy,'effective_unit',return_value={'control_safe':True}),patch.object(self.adapter,'command',side_effect=lambda a,t:seen.append(a)),patch.object(legacy,'observe',return_value={'supervisor_active':True,'engine_active':True,'active':True}):
            self.adapter.request_terminate(self.row,self.run)
        self.assertEqual([['/usr/bin/systemctl','stop','--no-block','spark-sglang.service']],seen)
    def test_unadopted_live_is_unknown_not_stopped(self):
        with self.assertRaises(core.ObservationError):self.adapter.idle(self.row)
    def test_explicit_shared_adoption_idempotent_and_refuses_other_reservation(self):
        with tempfile.TemporaryDirectory() as td:
            ctl=core.Controller({'models':legacy.definitions()},self.adapter,store=core.StateStore(td,False),log_root=td,protected=False)
            with patch.object(legacy,'qualify',return_value=({'control_safe':False},[],KEY)),patch.object(self.adapter,'process_cgroups',return_value=['/system.slice/docker-'+CID+'.scope']),patch.object(legacy.os,'readlink',return_value='/usr/bin/docker'),patch.object(Path,'read_bytes',return_value=b'\0'.join(x.encode() for x in legacy.original_qwen_owner_argv(self.row,KEY))+b'\0'):
                adopted=ctl.adopt_legacy(legacy.QWEN)
                data=ctl.store.read();self.assertEqual(legacy.QWEN,data['reservation']);self.assertEqual(CID,data['runs'][legacy.QWEN]['container_id'])
                self.assertEqual(adopted,ctl.adopt_legacy(legacy.QWEN))
                data=ctl.store.read();data['reservation']=legacy.GPT;ctl.store.write(data)
                with self.assertRaises(core.Refusal):ctl.adopt_legacy(legacy.QWEN)
            self.assertTrue(all(a[1] not in {'stop','start','kill','create','rm'} for a in self.commands))
    def test_adoption_cannot_overwrite_replacement(self):
        with tempfile.TemporaryDirectory() as td:
            ctl=core.Controller({'models':legacy.definitions()},self.adapter,store=core.StateStore(td,False),log_root=td,protected=False)
            with patch.object(legacy,'qualify',return_value=({'control_safe':False},[],KEY)),patch.object(self.adapter,'process_cgroups',return_value=['/system.slice/docker-'+CID+'.scope']),patch.object(legacy.os,'readlink',return_value='/usr/bin/docker'),patch.object(Path,'read_bytes',return_value=b'\0'.join(x.encode() for x in legacy.original_qwen_owner_argv(self.row,KEY))+b'\0'):
                ctl.adopt_legacy(legacy.QWEN);old=ctl.store.read();self.container['Created']='replaced'
                with self.assertRaises(core.Refusal):ctl.adopt_legacy(legacy.QWEN)
                self.assertEqual(old,ctl.store.read())
    def test_original_gpt_can_never_be_adopted(self):
        class C:
            def row(self,mid):return legacy.definitions()[1]
        with self.assertRaises(core.Refusal):legacy.adopt(C(),legacy.GPT)

    def test_preparation_stop_pins_invocation_without_main_owner(self):
        self.run.update(owner=None,container_id=None,legacy_handoff_invocation='e'*32)
        self.unit.update(ActiveState='activating',MainPID='0')
        observed=self.adapter.observe(self.row,self.run)
        self.assertTrue(observed['supervisor_active']);self.assertFalse(observed['engine_active'])
        seen=[]
        with patch.object(legacy,'effective_unit',return_value={'control_safe':True}),patch.object(self.adapter,'command',side_effect=lambda a,t:seen.append(a)),patch.object(legacy,'observe',return_value=observed):
            self.adapter.request_terminate(self.row,self.run)
        self.assertEqual([['/usr/bin/systemctl','stop','--no-block','spark-sglang.service']],seen)
        self.unit['InvocationID']='f'*32
        with self.assertRaises(core.ObservationError):self.adapter.observe(self.row,self.run)

class LegacyEffectiveUnit(unittest.TestCase):
    def setUp(self):
        self.row=legacy.definitions()[0]
        self.props={'FragmentPath':'/etc/systemd/system/spark-sglang.service','DropInPaths':'/etc/systemd/system/spark-sglang.service.d/90-sol-cache2.conf /etc/systemd/system/spark-sglang.service.d/99-monik-shared-controller.conf','NeedDaemonReload':'no','User':'','Group':'','Restart':'no','RestartUSec':'20s','TimeoutStartUSec':'infinity','TimeoutStopUSec':'3min','KillMode':'mixed'}
        for k,cmd in {'ExecStart':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-start','ExecStartPre':'/usr/local/sbin/spark-sglang-prepare','ExecStop':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-exact-stop','ExecCondition':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-can-launch'}.items():
            self.props[k]='{ path='+cmd.split()[0]+' ; argv[]='+cmd+' ; ignore_errors=no ; start_time=[n/a] ; pid=0 ; status=0/0 }'
        self.adapter=core.SystemAdapter({},command=lambda a,t:'\n'.join(k+'='+v for k,v in self.props.items()))
        self.files={'/etc/systemd/system/spark-sglang.service':legacy.QWEN_UNIT,'/etc/systemd/system/spark-sglang.service.d/90-sol-cache2.conf':legacy.QWEN_DROP90,'/etc/systemd/system/spark-sglang.service.d/99-monik-shared-controller.conf':legacy.QWEN_DROP99}
        self.addCleanup(patch.stopall)
        patch.object(legacy,'protect',return_value=None).start()
        files=self.files
        patch.object(Path,'read_bytes',lambda p:files[str(p)].encode()).start()
    def test_exact_base_and_actual_restart_no_dropins_qualified(self):
        self.assertTrue(legacy.effective_unit(self.adapter,self.row)['control_safe'])
    def test_effective_revision_excludes_dynamic_exec_status(self):
        before=legacy.effective_unit(self.adapter,self.row)
        self.props['ExecStart']=self.props['ExecStart'].replace('pid=0','pid=125').replace('start_time=[n/a]','start_time=Wed 2026-10-14').replace('status=0/0','status=1/FAILURE')
        self.assertEqual(before,legacy.effective_unit(self.adapter,self.row))
    def test_restart_always_extra_dropin_or_effective_command_drift_refused(self):
        for k,v in [('Restart','always'),('DropInPaths',self.props['DropInPaths']+' /etc/systemd/system/evil.conf'),('NeedDaemonReload','yes'),('ExecStop','{ path=/usr/bin/docker ; argv[]=/usr/bin/docker stop spark-sglang ; ignore_errors=no ; }'),('ExecCondition','')]:
            old=self.props[k];self.props[k]=v
            with self.assertRaises(core.Refusal):legacy.effective_unit(self.adapter,self.row)
            self.props[k]=old
    def test_dropin_file_drift_refused(self):
        self.files['/etc/systemd/system/spark-sglang.service.d/90-sol-cache2.conf']='[Service]\nRestart=always\n'
        with self.assertRaises(core.Refusal):legacy.effective_unit(self.adapter,self.row)
    def test_original_policy_is_observation_only(self):
        self.props['DropInPaths']='/etc/systemd/system/spark-sglang.service.d/90-sol-cache2.conf'
        for k,cmd in {'ExecStart':'/usr/local/sbin/spark-sglang-run','ExecStop':'/usr/bin/docker stop -t 90 spark-sglang'}.items():self.props[k]='{ path='+cmd.split()[0]+' ; argv[]='+cmd+' ; ignore_errors=no ; }'
        self.props['ExecCondition']=''
        self.assertFalse(legacy.effective_unit(self.adapter,self.row,allow_original=True)['control_safe'])
        with self.assertRaises(core.Refusal):legacy.effective_unit(self.adapter,self.row)

class LegacyPassiveHealth(unittest.TestCase):
    def test_exact_passive_routes_alias_and_key_reference(self):
        from unittest.mock import MagicMock
        for row in legacy.definitions():
            adapter=core.SystemAdapter({},command=lambda *a:None)
            class Reply:
                status=200
                def __init__(self,body):self.body=body
                def __enter__(self):return self
                def __exit__(self,*a):pass
                def read(self,size):return self.body
            passive=json.dumps(qwen_worker_info_fixture()).encode() if row['id']==legacy.QWEN else b''
            adapter.http=MagicMock();adapter.http.open.side_effect=[Reply(passive),Reply(json.dumps({'data':[{'id':row['runtime']['served_model_alias']}]}).encode())]
            with patch.object(legacy,'read_secret',return_value=KEY):self.assertTrue(adapter.api_ready(row)[0])
            requests=[c.args[0] for c in adapter.http.open.call_args_list]
            path='/server_info' if row['id']==legacy.QWEN else '/health'
            self.assertEqual(['http://127.0.0.1:30000'+path,'http://127.0.0.1:30000/v1/models'],[r.full_url for r in requests])
            self.assertTrue(all(r.get_header('Authorization')=='Bearer '+KEY for r in requests))
    def test_arbitrary_html_health_never_ready(self):
        from unittest.mock import MagicMock
        adapter=core.SystemAdapter({})
        response=MagicMock();response.__enter__.return_value=response;response.status=200;response.read.return_value=b'<html>ok</html>'
        adapter.http=MagicMock();adapter.http.open.return_value=response
        with patch.object(legacy,'read_secret',return_value=KEY),self.assertRaises(core.ObservationError):adapter.api_ready(legacy.definitions()[0])
        self.assertEqual(1,adapter.http.open.call_count)

class LegacyCredentialAndAdmission(unittest.TestCase):
    def test_secret_reference_data_only_and_mode(self):
        with tempfile.TemporaryDirectory() as td:
            p=Path(td)/'key.env';p.write_text('SPARK_API_KEY='+KEY+'\n');p.chmod(0o600)
            with patch.object(legacy,'SECRET',p),patch.object(legacy,'protect',return_value=None):
                self.assertEqual(KEY,legacy.read_secret())
                for body in ['SPARK_API_KEY=$(id)\n','SPARK_API_KEY='+KEY+'\nX=1\n','export SPARK_API_KEY='+KEY+'\n','SPARK_API_KEY="'+KEY+'"\n']:
                    p.write_text(body)
                    with self.assertRaises(core.Refusal):legacy.read_secret()
                p.write_text('SPARK_API_KEY='+KEY);p.chmod(0o644)
                with self.assertRaises(core.Refusal):legacy.read_secret()
    def test_qwen_self_supervisor_is_not_foreign_but_gpt_cannot_stop_it(self):
        calls=[]
        def command(argv,timeout):
            calls.append(argv)
            if argv[:2]==['/usr/bin/systemctl','show']:
                return 'Id=spark-sglang.service\nActiveState=active\nLoadState=loaded'
            if argv[0]=='/usr/bin/nvidia-smi':return 'N/A'
            raise AssertionError(argv)
        adapter=core.SystemAdapter({'excluded_units':['spark-sglang.service']},command=command)
        with patch.object(adapter,'foreign_gpu_containers',return_value=[]),patch.object(adapter,'gpu_pids',return_value=[]),patch.object(core.socket,'socket') as sock,patch.object(Path,'read_text',return_value='MemTotal: 128000000 kB\nMemAvailable: 100000000 kB\n'):
            adapter.preflight(legacy.definitions()[0])
            self.assertFalse(any(c[0]=='/usr/bin/systemctl' for c in calls))
            with self.assertRaises(core.Refusal):adapter.preflight(legacy.definitions()[1])
        self.assertTrue(all(c[1] not in {'stop','kill','rm','start','create'} for c in calls))
    def test_gpt_original_memory_threshold_preserved(self):
        row=legacy.definitions()[1];self.assertEqual(40000000*1024,row['required_memory_bytes'])
        adapter=core.SystemAdapter({'excluded_units':['spark-sglang.service']},command=lambda a,t:'N/A' if a[0]=='/usr/bin/nvidia-smi' else 'Id=spark-sglang.service\nActiveState=inactive\nLoadState=loaded')
        with patch.object(adapter,'foreign_gpu_containers',return_value=[]),patch.object(adapter,'gpu_pids',return_value=[]),patch.object(core.socket,'socket'),patch.object(Path,'read_text',return_value='MemTotal: 128000000 kB\nMemAvailable: 39999999 kB\n'):
            with self.assertRaises(core.Refusal):adapter.preflight(row)

class LegacySharedTransactions(base.TypedBridgeTests):
    # Reuse original transaction controls for each new fixed registry entry.
    # Inherited tests exercise the real Controller/StateStore/typed receipt code.
    def setUp(self):
        super().setUp()
        self.manifest['models']+=legacy.definitions()
        for row in legacy.definitions():(self.logs/row['id']).mkdir()
        self.mid=legacy.GPT;self.other=legacy.QWEN;self.ctl=self.new_controller()
        self.ctl.prepare_bundle(self.mid);self.ctl.prepare_bundle(self.other)
        p=patch.object(legacy,'effective_unit',return_value={'control_safe':True});p.start();self.addCleanup(p.stop)

class QwenSharedTransactions(LegacySharedTransactions):
    def setUp(self):
        super().setUp()
        self.mid=legacy.QWEN;self.other=legacy.GPT

class QwenPreparationCondition(base.CandidateFixture):
    def setUp(self):
        super().setUp()
        self.manifest['models']+=legacy.definitions()
        for row in legacy.definitions():(self.logs/row['id']).mkdir()
        self.mid=legacy.QWEN;self.ctl=self.new_controller();self.ctl._start(self.mid,wait=False)
        self.invocation='c'*32
        self.adapter.unit=lambda *a:{'InvocationID':self.invocation}
        self.adapter.validate_run_definition=lambda *a:None
        spec=base.importlib.util.spec_from_file_location('candidate_legacy_entry',base.CANDIDATE/'legacy_entry.py')
        self.entry=base.importlib.util.module_from_spec(spec)
        saved=base.sys.path[:]
        try:
            with patch.dict(base.sys.modules,{'modelctl':core,'supervisor':base.supervisor}):spec.loader.exec_module(self.entry)
        finally:base.sys.path[:]=saved
    def invoke(self):
        from types import SimpleNamespace
        with patch.object(self.entry.os,'geteuid',return_value=0),patch.object(self.entry.sys,'flags',SimpleNamespace(isolated=True)),patch.object(self.entry.sys,'argv',['legacy_entry.py','qwen-can-launch']),patch.dict(self.entry.os.environ,{'INVOCATION_ID':self.invocation}),patch.object(self.entry,'protect',return_value=None),patch.object(self.entry,'load_production_manifest',return_value=self.manifest),patch.object(self.entry,'SystemAdapter',return_value=self.adapter),patch.object(self.entry,'Controller',return_value=self.ctl):
            return self.entry.main()
    def test_condition_captures_invocation_before_prep_without_engine_effect(self):
        self.assertEqual(0,self.invoke());run=self.store.read()['runs'][self.mid]
        self.assertEqual(self.invocation,run['legacy_handoff_invocation']);self.assertIsNone(run['owner']);self.assertIsNone(run['container_id'])
        self.assertEqual(1,len(self.adapter.handoffs))
    def test_cancelled_condition_cannot_prepare_or_write(self):
        data=self.store.read();data['runs'][self.mid]['cancelled']=True;self.store.write(data);before=self.store.path.read_bytes()
        self.assertEqual(1,self.invoke());self.assertEqual(before,self.store.path.read_bytes())
    def test_replaced_native_preparation_invocation_refused(self):
        data=self.store.read();data['runs'][self.mid]['legacy_handoff_invocation']='f'*32;self.store.write(data)
        self.assertEqual(1,self.invoke())

class GPTSupervisorStages(base.CandidateFixture):
    model_id=legacy.GPT
    def setUp(self):
        super().setUp()
        self.manifest['models']+=legacy.definitions()
        for row in legacy.definitions():(self.logs/row['id']).mkdir()
        self.mid=self.model_id;self.other=legacy.QWEN if self.mid==legacy.GPT else legacy.GPT;self.ctl=self.new_controller()
        self.now=1800000000.0;self.ctl.wall_time=lambda:self.now
        self.ctl.enable_bridge('offline-test-session');self.ctl.prepare_bundle(self.mid)
        self.ctl._start(self.mid,wait=False)
        self.run=self.store.read()['runs'][self.mid]
        self.invocation='d'*32;self.cid='e'*64;self.created='2026-10-14T18:00:00.000000Z'
        self.docker_commands=[];self.running=False
        self.adapter.unit=lambda mid,run_id:{'InvocationID':self.invocation,'MainPID':str(base.os.getpid())}
        self.adapter.inspect_container=lambda cid:{'Created':self.created,'State':{'Running':self.running,'Pid':base.os.getpid() if self.running else 0}}
        for target,name,value in [(legacy,'effective_unit',{'control_safe':True}),(legacy,'read_secret',KEY),(legacy,'check_declaration','f'*64)]:
            p=patch.object(target,name,return_value=value);p.start();self.addCleanup(p.stop)
    stop_request=base.SupervisorStageTests.stop_request
    docker_command=base.SupervisorStageTests.docker_command
    test_persist_cid_before_start=base.SupervisorStageTests.test_actual_supervisor_persists_exact_cid_before_engine_start
    test_stop_during_create_prevents_start=base.SupervisorStageTests.test_actual_supervisor_create_releases_lock_and_stop_prevents_engine_start
    test_definition_changed_before_create=base.SupervisorStageTests.test_changed_manifest_before_supervisor_create_has_no_container_effect
    test_definition_changed_during_create=base.SupervisorStageTests.test_changed_manifest_during_create_prevents_engine_start
    test_revoked_prepared_never_launches=base.SupervisorStageTests.test_revoked_prepared_operation_cannot_launch_actual_supervisor

class QwenSupervisorStages(GPTSupervisorStages):
    model_id=legacy.QWEN

if __name__=='__main__':unittest.main()
