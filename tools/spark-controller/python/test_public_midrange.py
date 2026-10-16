"""Public source/mount authority checks; no model or host effects."""
import copy, hashlib, importlib.util, json, unittest
from pathlib import Path
import test_bridge_candidate as base
T=Path(__file__).resolve().parents[1];D=T/'deployment';M=D/'opt/spark/model-collection-v4';core=base.modelctl
def read(p):return json.loads(p.read_text())
def sha(p):return hashlib.sha256(p.read_bytes()).hexdigest()
def canonical(row):return hashlib.sha256(json.dumps(row,ensure_ascii=False,sort_keys=True,separators=(',',':')).encode()).hexdigest()
class PublicMidrange(unittest.TestCase):
 def setUp(self):self.catalog=read(M/'config/controller-manifest.json');self.row=copy.deepcopy(self.catalog['models'][8])
 def document(self,row):
  doc=copy.deepcopy(self.catalog);doc['models'][8]=row;return doc
 def test_mirrors_and_complete_deployment_hash_table(self):
  for name in ('modelctl.py','legacy_runtime.py','supervisor.py','monik_bridge.py'):
   self.assertEqual((T/'python'/name).read_bytes(),(M/'bin'/name).read_bytes())
  table={i['source']:i for i in read(T/'INSTALL-MANIFEST.json')['files']};pins=read(T/'midrange/SOURCE-PINS.json')
  for path in D.rglob('*'):
   if path.is_file():
    rel=str(path.relative_to(T));self.assertEqual(sha(path),table[rel]['sha256']);self.assertEqual(sha(path),pins['files'][rel])
  for path in (T/'gemma4-source-fix/upstream').glob('*.py'):self.assertEqual(sha(path),pins['gemma_image_source_files'][path.name])
 def test_old_seven_canonical_and_three_disabled_rows(self):
  pins=read(T/'midrange/SOURCE-PINS.json');self.assertEqual([r['id'] for r in self.catalog['models']],list(core.IDS));core.validate_manifest(self.catalog)
  for row in self.catalog['models'][:7]:self.assertEqual(canonical(row),pins['old7_configuration_canonical_sha256'][row['id']])
  for row in self.catalog['models'][7:]:
   self.assertIs(row['prepared'],False);self.assertEqual(row,read(T/'midrange/rows'/(row['id']+'.json')))
   for text in ('/home/jericho','physical_raw_verified','source_receipt_sha256','actual_artifact_report_sha256'):self.assertNotIn(text,json.dumps(row))
 def test_exact_five_env_values_and_no_global_authority(self):
  self.row['prepared']=True # schema-only fixture, never a preparation receipt
  core.validate_manifest(self.document(self.row));self.assertFalse(set(core.GEMMA_FIXED_ENV)&core.ENV_KEYS)
  for key in core.GEMMA_FIXED_ENV:
   for value in (None,'wrong'):
    bad=copy.deepcopy(self.row)
    if value is None:del bad['runtime']['env'][key]
    else:bad['runtime']['env'][key]=value
    with self.subTest(key=key,value=value),self.assertRaises(core.Refusal):core.validate_manifest(self.document(bad))
  doc=self.document(self.row);doc['models'][7]['prepared']=True;doc['models'][7]['runtime']['env']['NVIDIA_DISABLE_REQUIRE']='1'
  with self.assertRaises(core.Refusal):core.validate_manifest(doc)
 def test_exact_two_readonly_files_and_artifact_bindings(self):
  self.row['prepared']=True
  for source,fixed in core.GEMMA_FIXED_FILES.items():
   for mode in ('target','writable','hash','size','missing','duplicate'):
    bad=copy.deepcopy(self.row);mount=next(m for m in bad['runtime']['mounts'] if m['source']==source);art=next(a for a in bad['artifacts'] if a['path']==source)
    if mode=='target':mount['target']='/foreign/parser.py'
    elif mode=='writable':mount['readonly']=False
    elif mode=='hash':art['sha256']='0'*64
    elif mode=='size':art['size']+=1
    elif mode=='missing':bad['runtime']['mounts'].remove(mount)
    else:bad['runtime']['mounts'].append(copy.deepcopy(mount))
    with self.subTest(source=source,mode=mode),self.assertRaises(core.Refusal):core.validate_manifest(self.document(bad))
   self.assertFalse(core.gemma_file_mount(dict(self.row,id=core.MIDRANGE_IDS[0]),dict(source=source,target=fixed['target'],readonly=True)))
   p=D/source.lstrip('/');self.assertEqual(p.stat().st_size,fixed['size']);self.assertEqual(sha(p),fixed['sha256'])
 def test_direct_argv_window_reserves_and_sampler(self):
  a=self.row['runtime']['argv'];self.assertEqual(a[:3],['/usr/local/bin/vllm','serve','/models/deckard'])
  for flag,value in [('--max-model-len','131072'),('--block-size','16'),('--max-num-seqs','1'),('--max-num-batched-tokens','8192'),('--gpu-memory-utilization','0.65')]:self.assertEqual(core.argument_value(a,flag),value)
  self.assertEqual((self.row['context'],self.row['main_output_budget'],self.row['history_aux_reserve']),(131072,8192,2048));self.assertEqual(self.row['context']-8192-2048,120832)
  self.assertEqual(sum(self.row['memory_budget'].values()),self.row['required_memory_bytes']);self.assertNotIn('/bin/bash',a);self.assertNotIn('--trust-remote-code',a)
  self.assertEqual(json.loads(core.argument_value(a,'--speculative-config')),dict(method='dflash',model='/models/gemma-dflash',num_speculative_tokens=15,attention_backend='flash_attn'))
  self.assertEqual(read(T/'midrange/gemma4.params.json'),dict(temperature=1.0,top_p=0.95,top_k=64))
 def test_private_2g_exec_cache_only_mistral_and_gemma(self):
  run=dict(run_id='a'*32,generation=1,attempt='b'*32)
  for row in self.catalog['models'][:5]+self.catalog['models'][7:]:
   a=base.supervisor.container_argv(row,run);c=[v for v in a if v.startswith('/tmp/spark-compiled-cache:')]
   self.assertEqual(len(c),int(row['id'] in (core.IDS[2],core.MIDRANGE_IDS[1])))
   if c:self.assertEqual(c[0],'/tmp/spark-compiled-cache:rw,exec,nosuid,nodev,size=2147483648,uid=1000,gid=1000')
   self.assertIn('--read-only',a);self.assertEqual(a[a.index('--user')+1],'1000:1000');self.assertNotIn('--privileged',a)
 def test_e_profile_and_gpt_streaming_source_mount(self):
  row=self.catalog['models'][2];a=row['runtime']['argv']
  for flag,value in [('--kv-cache-dtype','float16'),('--kv-cache-memory-bytes','8589934592'),('--moe-backend','flashinfer_cutlass')]:self.assertEqual(core.argument_value(a,flag),value)
  self.assertNotIn('--calculate-kv-scales',a);self.assertNotIn('VLLM_NVFP4_GEMM_BACKEND',row['runtime']['env']);self.assertIn('VLLM_NVFP4_GEMM_BACKEND',core.ENV_KEYS)
  mounts=self.catalog['models'][6]['runtime']['mounts']
  self.assertEqual(len(mounts),4)
  self.assertIn(dict(source='/usr/local/lib/spark-gptoss/serving_chat_stream_harmony.py',target='/usr/local/lib/python3.12/dist-packages/vllm/entrypoints/openai/serving_chat_stream_harmony.py',readonly=True),mounts)
 def test_tui_and_launcher_exact_disabled_additions(self):
  tui=read(M/'config/tui-models.json');record=read(M/'config/launcher-installation.json');self.assertEqual([r['id'] for r in tui['models']],list(core.IDS))
  for row in tui['models'][7:]:
   self.assertIs(row['prepared'],False);self.assertEqual(row['context'],131072)
   for action in ('start','status','stop'):
    path=record['wrappers'][row['id']][action];self.assertEqual(row[action][0],path);self.assertIn(row['id'],(D/path.lstrip('/')).read_text())
   unit=(D/'etc/systemd/system'/('spark-v4-'+row['id']+'@.service')).read_text();self.assertIn('supervisor.py '+row['id']+' %i',unit);self.assertIn('Restart=no',unit)
 def test_policy_draft_preserves_identity_and_requires_later_native_approval(self):
  spec=importlib.util.spec_from_file_location('public_midrange_policy',T/'midrange/policy_draft.py');policy=importlib.util.module_from_spec(spec);spec.loader.exec_module(policy)
  binding=dict(python_path='/usr/bin/python3',backend_revision='0'*64,code_pins=[dict(path='/fixed/'+str(i),sha256='0'*64) for i in range(9)])
  binding['code_pins'][6]['path']='/etc/systemd/system/spark-v4-'+core.IDS[1]+'@.service'
  profile=dict(profile_id='placeholder',allowed_actions=['start','stop','status'],target=dict(kind='existing-coordinator',existing_coordinator=binding),settings=[dict(name=n,value=None,provenance='unknown') for n in ['context_length','port','quantization']])
  before=dict(identity='synthetic-test-identity',trust='synthetic-test-trust',profiles=[dict(copy.deepcopy(profile),profile_id=mid) for mid in core.IDS[:7]])
  original=copy.deepcopy(before);after=policy.draft_policy(before,self.catalog,lambda mid:'1'*64,lambda path:'2'*64)
  self.assertEqual(before,original);self.assertEqual(after['identity'],before['identity']);self.assertEqual(after['trust'],before['trust'])
  self.assertTrue(all(p['allowed_actions']==['start','stop','status'] for p in after['profiles'][:7]));self.assertTrue(all(p['allowed_actions']==[] for p in after['profiles'][7:]))
  self.assertTrue(all(p['profile_revision']=='' for p in after['profiles']))
  bad=copy.deepcopy(self.catalog);bad['models'][8]['prepared']='false'
  with self.assertRaises(ValueError):policy.draft_policy(before,bad,lambda mid:'1'*64,lambda path:'2'*64)
  with self.assertRaises(ValueError):policy.draft_policy(before,self.catalog,lambda mid:'not-an-actual-digest',lambda path:'2'*64)
 def test_public_32_source_inventory_has_no_host_attestation(self):
  doc=read(T/'gemma4-source-fix/SOURCE-INVENTORY.json');files=doc['source_files'];self.assertEqual(len(files),32)
  self.assertEqual(len({f['container_path'] for f in files.values()}),32)
  for f in files.values():self.assertEqual(set(f),{'container_path','sha256','bytes'});self.assertRegex(f['sha256'],r'^[a-f0-9]{64}$');self.assertGreater(f['bytes'],0)
  for name in ['vllm/model_executor/model_loader/weight_utils.py','vllm/model_executor/models/gemma4_mm.py','vllm/model_executor/models/utils.py']:self.assertIn(name,files)
if __name__=='__main__':unittest.main()
