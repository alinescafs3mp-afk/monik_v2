"""Two frozen legacy deployments within the existing V4 Controller.

Only fixed local data is accepted. Qualification/adoption are explicit root-local
steps, separate from telemetry. No original GPT shell is ever executed.
"""
from __future__ import annotations
import copy
import hashlib
import json
import os
from pathlib import Path
import re
import stat
import time
import uuid

IDS = ('qwen-existing-observation', 'gpt-oss-120b-existing-configured')
QWEN, GPT = IDS
SECRET = Path('/etc/spark-sglang-api.env')
QIMAGE = 'sha256:9d2a843c706c74bc259c0d9abf360551eb2734e1e7d255ab012a6965f10480b6'
GIMAGE = 'sha256:e07b3d4e9c4555d456758abf1f986811e180380a4c07f8d8d517c6ae5f66bf3c'
IMAGE_CONFIG_PINS = {QIMAGE:'bd4211b00d1d78ff48c2641bd53c66901a9ea3a0c93f70175cac0d64962a0525', GIMAGE:'99a2410ecb9f0c64087a1737cd0b57e2596f137088e9ea5901b9627608842a00'}
# Actual root metadata-only Docker fixture (never started/no GPU/no mounts).
# All observed fields must be present. These current approved defaults never
# substitute for missing historical container evidence.
RESOURCE_DEFAULTS = {
 'Memory':0,'MemorySwap':0,'MemoryReservation':0,'NanoCpus':0,
 'CpuQuota':0,'CpuPeriod':0,'CpuShares':0,'CpusetCpus':'','CpusetMems':'',
 'PidsLimit':None,'CgroupnsMode':'private','DeviceCgroupRules':None,
 'SecurityOpt':None,'CapDrop':None,'CapAdd':None,'Runtime':'runc',
}
SOURCE_PINS = {
 '/usr/local/lib/sglang-qwen38/tokenizer_manager.py': 'dd44027271b33a2bd34ffce9970c6f72ee9a5697eb9be8f3a8cb11601caa64af',
 '/usr/local/lib/sglang-qwen38/qsa_indexer.py': 'abfa6b6f3e21071a12ba4d34183bdcad432cf4965a8c16fddf309c1968ea1c06',
 '/usr/local/lib/spark-gptoss/openai_harmony_init.py': '18eeaa3dbbaf9b0516da89b0b62e3763f770c6a14cd8d42aec28205f132a636f',
 '/usr/local/lib/spark-gptoss/serving_chat_stream_harmony.py': '443bcfc4b8f98d03072b01d6edd72cfec50ec81fd1973c9d2cab9272d5815616',
 '/usr/local/sbin/spark-sglang-prepare': '5795ae170df3f2005381babb483117f3ba1085bcdfa2c6c6e033a9f430579b78',
}
ORIGINAL_QWEN_UNIT_SHA = '0adf04aee52395ccabeda14971c98581d25d385a446ab4b724b0081d3706641c'
# A root-local installer writes these compiled fixed units only after review.
QWEN_UNIT = '[Unit]\nDescription=SGLang Qwen3.8 Flash Next NVFP4\nAfter=docker.service spark-nvidia.service spark-fetch-model.service spark-fetch-image.service network-online.target\nRequires=docker.service spark-nvidia.service\nWants=network-online.target\n\n[Service]\nType=simple\nRestart=always\nRestartSec=20\nTimeoutStartSec=infinity\nTimeoutStopSec=180\nKillMode=mixed\nExecStartPre=/usr/local/sbin/spark-sglang-prepare\nExecStart=/usr/local/sbin/spark-sglang-run\nExecStop=/usr/bin/docker stop -t 90 spark-sglang\n\n[Install]\nWantedBy=multi-user.target\n'
QWEN_DROP90 = '[Service]\nRestart=no\n# Preserve the native normal stop: docker90, systemd180, KillMode=mixed.\n'
QWEN_DROP99 = '[Service]\nExecCondition=/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-can-launch\nExecStart=\nExecStart=/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-start\nExecStop=\nExecStop=/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-exact-stop\n'
GPT_UNIT = '''[Unit]
Description=Spark exact GPT OSS 120B model owner %i
After=docker.service network-online.target
Requires=docker.service

[Service]
Type=simple
Restart=no
User=root
Group=root
TimeoutStartSec=infinity
TimeoutStopSec=180
KillMode=mixed
ExecStart=/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/supervisor.py gpt-oss-120b-existing-configured %i
'''

def digest(value):
    return hashlib.sha256(json.dumps(value, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()).hexdigest()

def definitions():
    env = {'HF_HUB_OFFLINE':'1','TRANSFORMERS_OFFLINE':'1','HF_HOME':'/srv/spark/hf-home','HF_HUB_DISABLE_TELEMETRY':'1'}
    common = ['--host','0.0.0.0','--port','30000','--api-key','@SPARK_API_KEY@']
    qargs = ['sglang','serve','--model-path','/models','--served-model-name','qwen38-flash-next-abliterated',*common,
      '--tp','1','--quantization','modelopt_fp4','--moe-runner-backend','flashinfer_cutlass','--fp4-gemm-backend','flashinfer_cudnn',
      '--ple-offload-embedding','--ple-offload-backend','file','--ple-offload-dir','/srv/spark/ple','--context-length','262144',
      '--mem-fraction-static','0.852','--page-size','64','--chunked-prefill-size','8192','--max-running-requests','2',
      '--max-mamba-cache-size','8','--cuda-graph-backend-decode','full','--cuda-graph-max-bs-decode','2','--cuda-graph-backend-prefill','disabled',
      '--speculative-algorithm','NEXTN','--speculative-num-steps','2','--speculative-eagle-topk','1','--speculative-num-draft-tokens','3',
      '--mamba-radix-cache-strategy','extra_buffer','--disable-overlap-schedule','--mamba-ssm-dtype','float32',
      '--linear-attn-prefill-backend','triton','--linear-attn-decode-backend','triton','--grammar-backend','xgrammar',
      '--reasoning-parser','qwen3','--tool-call-parser','qwen3_coder','--default-chat-template-kwargs',
      '{"enable_thinking": true, "reasoning_effort": "xhigh"}','--enable-cache-report']
    gargs = ['vllm','serve','/models','--served-model-name','gpt-oss-120b',*common,
      '--tool-call-parser','openai','--reasoning-parser','openai_gptoss','--enable-auto-tool-choice',
      '--gpu-memory-utilization','0.70','--max-model-len','131072','--max-num-seqs','8','--max-num-batched-tokens','8192',
      '--enable-prefix-caching','--load-format','fastsafetensors','--quantization','mxfp4','--mxfp4-backend','CUTLASS',
      '--mxfp4-layers','moe,qkv,o,lm_head','--attention-backend','FLASHINFER','--kv-cache-dtype','fp8','--tensor-parallel-size','1']
    qmounts = [('/srv/spark/models/Qwen3.8-Flash-Next-ABLITERATED-NVFP4','/models',True),('/srv/spark/ple','/srv/spark/ple',False),
      ('/srv/spark/hf-home','/srv/spark/hf-home',False),('/usr/local/lib/sglang-qwen38/tokenizer_manager.py','/sgl-workspace/sglang/python/sglang/srt/managers/tokenizer_manager.py',True),
      ('/usr/local/lib/sglang-qwen38/qsa_indexer.py','/sgl-workspace/sglang/python/sglang/srt/layers/attention/qsa/qsa_indexer.py',True)]
    gmounts = [('/srv/spark/models/Huihui-gpt-oss-120b-mxfp4-abliterated','/models',True),('/srv/spark/hf-home','/srv/spark/hf-home',False),
      ('/usr/local/lib/spark-gptoss/openai_harmony_init.py','/usr/local/lib/python3.12/dist-packages/openai_harmony/__init__.py',True)]
    rows = []
    for mid,name,engine,quant,context,alias,image,args,mounts in [
      (QWEN,'Qwen3.8 Flash Next ABLITERATED NVFP4','SGLang','modelopt_fp4',262144,'qwen38-flash-next-abliterated',QIMAGE,qargs,qmounts),
      (GPT,'GPT OSS 120B MXFP4 ABLITERATED','vLLM','mxfp4',131072,'gpt-oss-120b',GIMAGE,gargs,gmounts)]:
        e=copy.deepcopy(env)
        if mid==GPT: e['VLLM_USE_FLASHINFER_MOE_MXFP4_MXFP8']='1'
        rows.append({'id':mid,'name':name,'engine':engine,'quant':quant,'context':context,'endpoint':'http://127.0.0.1:30000/v1',
          'prepared':True,'legacy_fixed':True,'required_memory_bytes':40000000*1024 if mid==GPT else None,
          'memory_policy':'Existing MemAvailable >=40000000 KiB, no global cache flush' if mid==GPT else 'Existing SGLang mem-fraction-static=0.852; no invented host RAM threshold',
          'startup_timeout_seconds':4320 if mid==GPT else 1800,'stop_timeout_seconds':180,
          'runtime':{'image':image,'image_id':image,'argv':args,'env':e,'mounts':[{'source':s,'target':t,'readonly':ro} for s,t,ro in mounts],
            'secret_reference':str(SECRET),'served_model_alias':alias,'container_name':'spark-sglang' if mid==QWEN else 'spark-gptoss',
            'ipc':'host','network':'host','shm_bytes':16*1024**3,'ulimits':{'memlock':-1,'stack':67108864},'auto_remove':mid==QWEN},
          'artifacts':[],'evidence_level':'ROOT_METADATA_PINNED_NOT_RUNTIME_VALIDATED'})
    return rows

def approved_qwen_declarations():
    """Three exact local Qwen declarations; no manifest-supplied overrides.

    Keep definitions() as the frozen historical deployment. The two reviewed
    endpoint variants let the client select reasoning effort, with the same
    NEXTN configuration or with speculation entirely absent. Each candidate is
    a complete compiled row and still requires its own full-integrity approval.
    """
    original = definitions()[0]
    client_effort = copy.deepcopy(original)
    argv = client_effort['runtime']['argv']
    argv[argv.index('--default-chat-template-kwargs') + 1] = '{"enable_thinking": true}'
    no_speculation = copy.deepcopy(client_effort)
    argv = no_speculation['runtime']['argv']
    start = argv.index('--speculative-algorithm')
    del argv[start:start + 8]
    return original, client_effort, no_speculation

def approved_gpt_declarations():
    """Seven exact GPT precision/cache/stream controls, not runtime qualifications.

    definitions() remains the frozen historical deployment. Preserve its image,
    CUTLASS selection, parsers, limits and mounts in every complete candidate.
    Cache controls use only the existing read-write HF_HOME mount. Each row
    requires its own normal full-integrity approval before a tracked launch.
    """
    original = definitions()[1]
    moe = copy.deepcopy(original)
    argv = moe['runtime']['argv']
    argv[argv.index('--mxfp4-layers') + 1] = 'moe'
    scaled_original = copy.deepcopy(original)
    scaled_original['runtime']['argv'].append('--calculate-kv-scales')
    scaled_moe = copy.deepcopy(moe)
    scaled_moe['runtime']['argv'].append('--calculate-kv-scales')
    cached_moe = copy.deepcopy(moe)
    cached_moe['runtime']['env']['XDG_CACHE_HOME'] = '/srv/spark/hf-home/gptoss-runtime-cache'
    cached_scaled_moe = copy.deepcopy(scaled_moe)
    cached_scaled_moe['runtime']['env']['XDG_CACHE_HOME'] = '/srv/spark/hf-home/gptoss-runtime-cache'
    # Preserve the six original declarations byte-for-byte. The seventh pins
    # only the exact streaming header-transition fix; no numerical/kernel delta.
    cached_moe_streamfix = copy.deepcopy(cached_moe)
    cached_moe_streamfix['runtime']['mounts'].append({
        'source': '/usr/local/lib/spark-gptoss/serving_chat_stream_harmony.py',
        'target': '/usr/local/lib/python3.12/dist-packages/vllm/entrypoints/openai/serving_chat_stream_harmony.py',
        'readonly': True})
    return original, moe, scaled_original, scaled_moe, cached_moe, cached_scaled_moe, cached_moe_streamfix

def validate_row(row):
    expected = next(x for x in definitions() if x['id']==row['id'])
    approved = (approved_qwen_declarations() if row['id']==QWEN else
                approved_gpt_declarations() if row['id']==GPT else (expected,))
    if not any(row == candidate for candidate in approved):
        raise Refusal('Legacy definition differs from exact compiled approved declaration')

def read_secret():
    protect(SECRET)
    st=SECRET.stat(follow_symlinks=False)
    if stat.S_IMODE(st.st_mode)!=0o600 or st.st_size>128:
        raise Refusal('Legacy secret reference requires protected root0600 data')
    fd=os.open(SECRET,os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
    with os.fdopen(fd,'rb') as f: raw=f.read(129)
    if not re.fullmatch(rb'SPARK_API_KEY=[A-Za-z0-9]{48}\n?',raw):
        raise Refusal('Legacy credential must be exact data-only syntax')
    return raw.partition(b'=')[2].rstrip(b'\n').decode('ascii')

def command_argv(row, key):
    return [key if x=='@SPARK_API_KEY@' else x for x in row['runtime']['argv']]

def container_argv(row,run):
    r=row['runtime']; validate_row(row); key=read_secret()
    args=['/usr/bin/docker','create','--name',r['container_name'],'--gpus','all','--ipc','host','--network','host',
      '--shm-size','16g','--ulimit','memlock=-1','--ulimit','stack=67108864','--restart','no',
      '--label','spark.v4.model='+row['id'],'--label','spark.v4.run='+run['run_id'],
      '--label','spark.v4.generation='+str(run['generation']),'--label','spark.v4.attempt='+run['attempt'],
      '--log-driver','local','--log-opt','max-size=10m','--log-opt','max-file=3']
    if r['auto_remove']: args+=['--rm']
    for m in r['mounts']: args+=['-v',m['source']+':'+m['target']+(':ro' if m['readonly'] else '')]
    for k,v in sorted(r['env'].items()): args+=['-e',k+'='+v]
    # Preserve original image ENTRYPOINT and original command. No host shell.
    return args+[r['image_id'],*command_argv(row,key)]

def confirm_empty_command_properties(adapter,name,properties):
    """systemctl omits empty Exec arrays; confirm absence from typed D-Bus."""
    allowed={'ExecStart','ExecStartPre','ExecStop','ExecCondition'}
    if (not isinstance(name,str) or not re.fullmatch(r'spark-sglang\.service|spark-v4-gpt-oss-120b-existing-configured@[a-f0-9]{32}\.service',name)
        or not properties or any(p not in allowed for p in properties) or len(set(properties))!=len(properties)):
        raise Refusal('Invalid fixed legacy command property query')
    encoded=''.join(c if c.isascii() and c.isalnum() else '_'+format(ord(c),'02x') for c in name)
    raw=adapter.command(['/usr/bin/busctl','--system','--json=short','get-property',
        'org.freedesktop.systemd1','/org/freedesktop/systemd1/unit/'+encoded,
        'org.freedesktop.systemd1.Service',*properties],.65)
    def unique(pairs):
        result={}
        for key,value in pairs:
            if key in result:raise ValueError('Duplicate typed property key')
            result[key]=value
        return result
    lines=raw.splitlines()
    if len(lines)!=len(properties):raise Refusal('Incomplete typed empty command evidence')
    for line in lines:
        value=json.loads(line,object_pairs_hook=unique)
        if (type(value) is not dict or set(value)!={'type','data'}
            or value['type']!='a(sasbttttuii)' or value['data']!=[]):
            raise Refusal('Legacy omitted command is not a confirmed empty array')

def effective_unit(adapter,row,run=None,allow_original=False):
    # A bare @.service is a unit file, not a loadable systemd runtime identity.
    # This fixed instance is read only; no process/run/authority is invented.
    inspected_run=run['run_id'] if run else '0'*32
    name=unit_name(row['id'],inspected_run)
    raw=adapter.command(['/usr/bin/systemctl','show',name,'--all','--property=FragmentPath,DropInPaths,NeedDaemonReload,User,Group,Restart,RestartUSec,TimeoutStartUSec,TimeoutStopUSec,KillMode,ExecStart,ExecStartPre,ExecStop,ExecCondition'],.65)
    values=dict(line.split('=',1) for line in raw.splitlines() if '=' in line)
    command_properties=('ExecStart','ExecStartPre','ExecStop','ExecCondition')
    missing=[p for p in command_properties if p not in values]
    if missing:
        confirm_empty_command_properties(adapter,name,missing)
        values.update({p:'' for p in missing})
    path=Path('/etc/systemd/system')/unit_name(row['id'])
    if values.get('FragmentPath')!=str(path) or values.get('NeedDaemonReload')!='no':
        raise Refusal('Legacy effective unit fragment/reload drift')
    protect(path)
    raw_unit=path.read_bytes()
    if len(raw_unit)>65536: raise Refusal('Legacy unit bound')
    unit_sha=hashlib.sha256(raw_unit).hexdigest()
    source_pins={str(path):unit_sha}
    if row['id']==QWEN:
        if unit_sha!=ORIGINAL_QWEN_UNIT_SHA: raise Refusal('Actual Qwen base unit changed')
        drop90=path.parent/'spark-sglang.service.d/90-sol-cache2.conf'
        drop99=path.parent/'spark-sglang.service.d/99-monik-shared-controller.conf'
        wanted90=str(drop90); wanted99=wanted90+' '+str(drop99)
        if values.get('DropInPaths') not in ({wanted90,wanted99} if allow_original else {wanted99}):
            raise Refusal('Qwen effective drop-in population differs')
        safe=values.get('DropInPaths')==wanted99
        original=not safe and allow_original
        for drop,wanted in [(drop90,QWEN_DROP90)]+([(drop99,QWEN_DROP99)] if safe else []):
            protect(drop)
            if drop.read_bytes()!=wanted.encode(): raise Refusal('Qwen protected drop-in bytes differ')
            source_pins[str(drop)]=hashlib.sha256(wanted.encode()).hexdigest()
    else:
        if values.get('DropInPaths') or unit_sha!=hashlib.sha256(GPT_UNIT.encode()).hexdigest(): raise Refusal('GPT exact supervisor unit differs')
        safe=True; original=False
    # Loaded policy can diverge from disk even before NeedDaemonReload catches up.
    expected={'User':'','Group':'','Restart':'no','RestartUSec':'20s','TimeoutStartUSec':'infinity','TimeoutStopUSec':'3min','KillMode':'mixed'} if row['id']==QWEN else {'User':'root','Group':'root','Restart':'no','TimeoutStartUSec':'infinity','TimeoutStopUSec':'3min','KillMode':'mixed'}
    if any(values.get(k)!=v for k,v in expected.items()): raise Refusal('Legacy loaded supervisor policy differs')
    if original:
        required={'ExecStart':'/usr/local/sbin/spark-sglang-run','ExecStartPre':'/usr/local/sbin/spark-sglang-prepare','ExecStop':'/usr/bin/docker stop -t 90 spark-sglang'}
    elif row['id']==QWEN:
        required={'ExecStart':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-start','ExecStartPre':'/usr/local/sbin/spark-sglang-prepare','ExecStop':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-exact-stop','ExecCondition':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/legacy_entry.py qwen-can-launch'}
    else:
        required={'ExecStart':'/usr/bin/python3 -I -B /opt/spark/model-collection-v4/bin/supervisor.py gpt-oss-120b-existing-configured '+inspected_run}
    if not {'ExecStart','ExecStartPre','ExecStop','ExecCondition'}<=values.keys(): raise Refusal('Legacy command policy observation incomplete')
    for k,argv in required.items():
        # systemctl's parsed command projection includes exact argv[]=; reject
        # multiple commands, alternate prefixes and hidden executable wrappers.
        value=values.get(k,'')
        if (value.count('argv[]=')!=1 or ('argv[]='+argv+' ;') not in value or value.count('path=')!=1
            or ('path='+argv.split()[0]+' ;') not in value or 'ignore_errors=no' not in value):
            raise Refusal('Legacy loaded exact command differs: '+k)
    for k in {'ExecStartPre','ExecStop','ExecCondition'}-required.keys():
        if values.get(k): raise Refusal('Unexpected legacy unit command')
    # systemctl Exec* includes invocation times/PIDs/status. Only the parsed
    # static command fields belong in configuration approval/revision.
    stable = {k:v for k,v in values.items() if not k.startswith('Exec')}
    for k in ('ExecStart','ExecStartPre','ExecStop','ExecCondition'):
        value=values.get(k,'')
        stable[k]=[(field, re.findall(pattern,value)) for field,pattern in (
          ('path',r'path=([^ ;]+)'),('argv',r'argv\[\]=(.+?) ;'),
          ('ignore_errors',r'ignore_errors=([^ ;}]+)'),('flags',r'flags=([^ ;}]+)'))]
    return {'unit_sha256':unit_sha,'effective_sha256':digest(stable),'source_pins':source_pins,'control_safe':safe}

def profile_binding(ctl,row):
    pins={}
    paths=[MANIFEST, PROJECT/'bin/modelctl.py',PROJECT/'bin/supervisor.py',PROJECT/'bin/monik_bridge.py',PROJECT/'bin/legacy_runtime.py',PROJECT/'bin/legacy_entry.py',Path('/usr/local/sbin/spark-modelctl-v4')]
    for p in paths:
        protect(p)
        if p.stat().st_size>2*1024**2: raise Refusal('Legacy effect source bound')
        pins[str(p)]=hashlib.sha256(p.read_bytes()).hexdigest()
    return {'protected_source_pins':pins,'effective_unit':effective_unit(ctl.adapter,row,allow_original=True)}

def qualify(adapter,row,allow_original=False):
    validate_row(row)
    unit=effective_unit(adapter,row,allow_original=allow_original)
    key=read_secret() # Parse bytes as data, never source the .env shell file.
    if row['id']==QWEN and not unit['control_safe']:
        launch=Path('/usr/local/sbin/spark-sglang-run');protect(launch)
        if hashlib.sha256(launch.read_bytes()).hexdigest()!='85e40927fa7ca1244754f1b010bc47453a475f4a1237b8aa81e913ea846a4363':
            raise Refusal('Original protected Qwen launch source differs')
    selected=[p for p in SOURCE_PINS if ('sglang' in p)==(row['id']==QWEN)]
    witnesses=[]
    for p in selected:
        path=Path(p);protect(path)
        if path.stat().st_size>2*1024**2 or hashlib.sha256(path.read_bytes()).hexdigest()!=SOURCE_PINS[p]:
            raise Refusal('Pinned legacy code dependency changed')
        st=path.stat();witnesses.append((path,(st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns,st.st_ctime_ns)))
    for m in row['runtime']['mounts']:
        path=Path(m['source']); protect(path,directory=path.is_dir())
    model=Path(row['runtime']['mounts'][0]['source'])
    markers=[model/'config.json']+([model/'.download-complete'] if row['id']==GPT else [])
    for path in markers:
        protect(path);st=path.stat()
        if path.name=='config.json' and st.st_size<1: raise Refusal('Empty legacy model metadata')
        witnesses.append((path,(st.st_dev,st.st_ino,st.st_size,st.st_mtime_ns,st.st_ctime_ns)))
    if row['id']==QWEN:
        tag=json.loads(adapter.command(['/usr/bin/docker','image','inspect','lmsysorg/sglang:dev-qwen38-next-local'],2))
        if len(tag)!=1 or tag[0].get('Id')!=QIMAGE: raise Refusal('Existing Qwen prepare image-reference dependency missing/drifted')
    image=json.loads(adapter.command(['/usr/bin/docker','image','inspect',row['runtime']['image_id']],2))
    if len(image)!=1 or image[0].get('Id')!=row['runtime']['image_id'] or image[0].get('Architecture')!='arm64' or image[0].get('Os')!='linux' or digest(image[0].get('Config'))!=IMAGE_CONFIG_PINS[row['runtime']['image_id']]:
        raise Refusal('Legacy pinned local ARM64 image missing/mismatched')
    return unit,witnesses,key

def verify_bundle(adapter,row):
    unit,witnesses,key=qualify(adapter,row)
    if not unit['control_safe']: raise Refusal('Legacy original supervisor is observation-only until explicit local migration')
    return witnesses

def inspect_name(adapter,row):
    ids=adapter.command(['/usr/bin/docker','ps','--all','--no-trunc','--quiet','--filter','name=^/'+row['runtime']['container_name']+'$'],.65).split()
    if len(ids)>1 or any(not re.fullmatch(r'[a-f0-9]{64}',x) for x in ids): raise ObservationError('Ambiguous exact legacy named identity')
    return ids[0] if ids else None

def declaration_digest(container,normalize_oom=True):
    # Secret-bearing raw inspect stays local; only a one-way digest is durable.
    declaration={k:container.get(k) for k in ('Name','Image','Config','HostConfig','Mounts')}
    mounts=declaration['Mounts']
    if isinstance(mounts,list):
        # Docker inspect may enumerate the same mounts in a different order.
        # Keep every field and duplicate; normalize only this unordered list.
        declaration['Mounts']=sorted(mounts,key=lambda m:json.dumps(m,sort_keys=True,separators=(',',':'),allow_nan=False))
    hc=declaration['HostConfig']
    if normalize_oom and isinstance(hc,dict) and 'OomKillDisable' in hc and hc['OomKillDisable'] is None:
        # Docker start changes this default from false to null. Both leave the
        # OOM killer enabled; keep every other host field exact.
        declaration['HostConfig']={**hc,'OomKillDisable':False}
    return digest(declaration)

def valid_oom_default(hc):
    return (isinstance(hc,dict) and 'OomKillDisable' in hc and
            (hc['OomKillDisable'] is False or hc['OomKillDisable'] is None))

def declaration_matches(container,frozen):
    # Old immutable records keep their exact original representation/hash.
    # Never rewrite them or guess a historical mount order after a mismatch.
    if not valid_oom_default(container.get('HostConfig')):
        return False
    raw={k:container.get(k) for k in ('Name','Image','Config','HostConfig','Mounts')}
    normalized_raw={**raw,'HostConfig':{**raw['HostConfig'],'OomKillDisable':False}}
    return (declaration_digest(container)==frozen or
            declaration_digest(container,normalize_oom=False)==frozen or
            digest(raw)==frozen or digest(normalized_raw)==frozen)

def validate_resource_defaults(hc):
    if not valid_oom_default(hc):
        raise ObservationError('Legacy OOM killer default differs or unavailable')
    for field,expected in RESOURCE_DEFAULTS.items():
        # Docker may add this exact SELinux label default for host IPC. Other
        # security options, combinations and missing fields remain unqualified.
        if field == 'SecurityOpt' and hc.get(field) == ['label=disable']:
            continue
        if field not in hc or type(hc[field]) is not type(expected) or hc[field]!=expected:
            raise ObservationError('Legacy explicit resource/default field differs or unavailable: '+field)

def check_declaration(row,c,key,adapter,allow_original_logs=False):
    r=row['runtime']; config=c.get('Config',{}); hc=c.get('HostConfig',{})
    if c.get('Image')!=r['image_id'] or config.get('Cmd')!=command_argv(row,key): raise ObservationError('Legacy image/command differs')
    image=json.loads(adapter.command(['/usr/bin/docker','image','inspect',r['image_id']],2))
    if len(image)!=1 or image[0].get('Id')!=r['image_id'] or digest(image[0].get('Config'))!=IMAGE_CONFIG_PINS[r['image_id']]: raise ObservationError('Legacy image declaration missing/changed')
    defaults=image[0].get('Config',{})
    if (c.get('Name')!='/'+r['container_name']
        or any(config.get(k)!=defaults.get(k) for k in ('Entrypoint','WorkingDir'))
        or config.get('User')!=(defaults.get('User') or '')):
        raise ObservationError('Legacy image entrypoint/user/workdir differs')
    env=config.get('Env')
    if not isinstance(env,list) or any(not isinstance(x,str) or '=' not in x for x in env) or len(env)>256: raise ObservationError('Legacy environment unavailable')
    parsed=dict(x.split('=',1) for x in env)
    inherited=defaults.get('Env') or []
    if not isinstance(inherited,list) or any(not isinstance(x,str) or '=' not in x for x in inherited): raise ObservationError('Pinned image environment malformed')
    expected_env=dict(x.split('=',1) for x in inherited);expected_env.update(r['env'])
    if len(parsed)!=len(env) or parsed!=expected_env: raise ObservationError('Legacy effective environment differs')
    mounts=c.get('Mounts')
    if not isinstance(mounts,list) or len(mounts)!=len(r['mounts']): raise ObservationError('Legacy mount population differs')
    actual=sorted((m.get('Source'),m.get('Destination'),m.get('RW'),m.get('Type')) for m in mounts)
    expected=sorted((m['source'],m['target'],not m['readonly'],'bind') for m in r['mounts'])
    if actual!=expected: raise ObservationError('Legacy effective mounts differ')
    ulimits=hc.get('Ulimits') or []
    ul={x.get('Name'):(x.get('Soft'),x.get('Hard')) for x in ulimits if isinstance(x,dict)}
    if (hc.get('NetworkMode')!='host' or hc.get('IpcMode')!='host' or hc.get('ShmSize')!=r['shm_bytes'] or ul!={'memlock':(-1,-1),'stack':(67108864,67108864)}
      or hc.get('Privileged') is not False or hc.get('CgroupParent') or hc.get('AutoRemove') is not r['auto_remove']
      or hc.get('RestartPolicy',{}).get('Name') not in {'','no'} or hc.get('ReadonlyRootfs') is not False
      or any(hc.get(k) for k in ('CapAdd','Devices','Sysctls','ExtraHosts','Dns','PidMode','UTSMode','UsernsMode'))): raise ObservationError('Legacy effective Docker resources/policy differ')
    logging=hc.get('LogConfig')
    fixed_logging={'Type':'local','Config':{'max-size':'10m','max-file':'3'}}
    if allow_original_logs:
        if (not isinstance(logging,dict) or logging.get('Type') not in {'local','json-file','journald','none'}
            or not isinstance(logging.get('Config'),dict) or not set(logging['Config']).issubset({'max-size','max-file','compress'})):
            raise ObservationError('Original legacy local logging declaration not qualified')
    elif logging!=fixed_logging:
        raise ObservationError('New legacy Docker log originals must have exact bounded local policy')
    validate_resource_defaults(hc)
    requests=hc.get('DeviceRequests')
    if not isinstance(requests,list) or len(requests)!=1 or requests[0].get('Count')!=-1 or requests[0].get('Capabilities')!=[['gpu']] or requests[0].get('Driver') not in {'','nvidia'}:
        raise ObservationError('Legacy exact GPU request differs')
    return declaration_digest(c)

def container_ids(adapter):
    result=adapter.command(['/usr/bin/docker','ps','--all','--no-trunc','--quiet'],.65).split()
    if len(result)>128 or len(set(result))!=len(result) or any(not re.fullmatch(r'[a-f0-9]{64}',x) for x in result):
        raise ObservationError('Legacy Docker inventory bound/identity')
    return result

def reap_binding(row,run):
    cid=run.get('container_id');created=run.get('container_created');declaration=run.get('legacy_declaration_sha256')
    if (not isinstance(cid,str) or not re.fullmatch(r'[a-f0-9]{64}',cid)
        or not isinstance(created,str) or not created or not isinstance(declaration,str)
        or not re.fullmatch(r'[a-f0-9]{64}',declaration) or not run.get('owner')):
        raise ObservationError('Exact owned legacy cleanup identity incomplete')
    return {**{k:copy.deepcopy(run.get(k)) for k in ('model_id','run_id','generation','attempt','owner','container_id','container_created','engine_pid','engine_birth','legacy_declaration_sha256')},'image_id':row['runtime']['image_id']}

def reap_marker(row,run):
    marker=run.get('container_reap')
    if not marker:return None
    if (not isinstance(marker,dict) or marker.get('binding')!=reap_binding(row,run)
        or marker.get('phase') not in {'prepared','effect_attempted','confirmed'}
        or marker.get('resources_verified') is not True
        or not isinstance(marker.get('started_at'),str) or not marker['started_at']
        or (marker['started_at']!='0001-01-01T00:00:00Z' and not run.get('engine_birth'))):
        raise ObservationError('Legacy cleanup journal identity/proof differs')
    return marker

def reap_stopped(ctl,data,row,run,before_effect=None):
    """Same-lock exact stopped-container cleanup; never forced, named or replayed.

    Durable evidence remains in legacy_reaps after a new run replaces the current
    slot. Original operation receipts and physical instance fields are untouched.
    """
    cid=run.get('container_id')
    if not cid:return False
    if run['state']=='STARTING' and not run.get('cancelled'):
        raise Refusal('Uncancelled legacy launch permission cannot be reaped')
    if run['state'] not in {'STOPPED','FAILED','STOPPING','STARTING'}:
        raise Refusal('Legacy lifecycle is not eligible for stopped cleanup')
    adapter=ctl.adapter;marker=reap_marker(row,run)
    present=cid in container_ids(adapter)
    if not present:
        if marker is None:
            # A previously running --rm Qwen can disappear naturally. The existing
            # observer requires captured birth and no replacement; absence alone
            # cannot certify an inert/unknown create or a non-auto-remove GPT.
            if not row['runtime']['auto_remove'] or not run.get('engine_birth'):
                raise ObservationError('Legacy missing CID has no exact removal journal')
            if not adapter.resources_released(row,run):raise Refusal('Legacy removed engine resources still present')
            return False
        if not adapter.resources_released(row,run):raise Refusal('Legacy deleted CID resources still present')
        marker.update(phase='confirmed',confirmed_at_ms=int(ctl.wall_time()*1000))
        data.setdefault('legacy_reaps',{})[run['run_id']]=copy.deepcopy(marker)
        ctl.store.write(data)
        return True
    if marker and marker['phase']=='confirmed':
        raise ObservationError('Confirmed reaped legacy full CID reappeared')
    if marker and marker['phase']=='effect_attempted':
        raise Refusal('legacy_reap_unresolved: exact CID still present; deletion is never replayed')
    # Check the real adapter's exact CID/Created/image/declaration/labels and
    # stopped process state, then independently account for proc/cgroup/GPU.
    if adapter.observe(row,run)['active'] or not adapter.resources_released(row,run):
        raise Refusal('Legacy engine/supervisor/resources not fully released; no removal')
    container=adapter.inspect_container(cid);state=container.get('State',{})
    if state.get('Running') is not False or state.get('Pid')!=0 or type(state.get('Pid')) is not int:
        raise ObservationError('Legacy non-running exact container evidence incomplete')
    validate_resource_defaults(container.get('HostConfig',{}))
    binding=reap_binding(row,run)
    if (container.get('Id')!=cid or container.get('Created')!=run['container_created']
        or container.get('Image')!=row['runtime']['image_id']
        or not declaration_matches(container,run['legacy_declaration_sha256'])):
        raise ObservationError('Legacy immutable container replaced before removal')
    if not marker:
        marker={'phase':'prepared','binding':binding,'resources_verified':True,
                'started_at':state.get('StartedAt'),'created_at_ms':int(ctl.wall_time()*1000),
                'reason':'exact_stopped_owned_container_before_next_admission'}
        run['container_reap']=marker
        data.setdefault('legacy_reaps',{})[run['run_id']]=copy.deepcopy(marker)
        ctl.store.write(data)
    if before_effect is not None:before_effect()
    if adapter.observe(row,run)['active'] or not adapter.resources_released(row,run):
        raise Refusal('Legacy resources changed before exact removal')
    marker['phase']='effect_attempted'
    data['legacy_reaps'][run['run_id']]=copy.deepcopy(marker)
    ctl.store.write(data)
    # The lease/authority can expire during fsync; check again immediately before
    # this independent effect. A crash after the marker never repeats docker rm.
    if before_effect is not None:
        try:before_effect()
        except Exception:
            # This process knows no rm was invoked. A fresh independently
            # authorized command may continue; a crash leaves attempted/unknown.
            marker.update(phase='prepared',effect_invoked=False,error_code='admission_recheck_refused')
            data['legacy_reaps'][run['run_id']]=copy.deepcopy(marker)
            ctl.store.write(data)
            raise
    try:
        adapter.command(['/usr/bin/docker','rm',cid],2.0)
    except Exception as exc:
        marker['error_code']=type(exc).__name__
        data['legacy_reaps'][run['run_id']]=copy.deepcopy(marker)
        ctl.store.write(data)
        raise Refusal('legacy_reap_unresolved: exact removal outcome unknown; no replay') from exc
    if cid in container_ids(adapter):raise Refusal('legacy_reap_unresolved: exact CID remains after removal')
    if not adapter.resources_released(row,run):raise Refusal('Legacy removal completed but resources remain unconfirmed')
    marker.update(phase='confirmed',confirmed_at_ms=int(ctl.wall_time()*1000))
    data['legacy_reaps'][run['run_id']]=copy.deepcopy(marker)
    ctl.store.write(data)
    return True

def _same_birth_zombie(pid,birth):
    """Positive bounded observation only; a zombie is not a released process."""
    try:
        fd=os.open(Path('/proc')/str(pid)/'stat',os.O_RDONLY|os.O_NOFOLLOW|os.O_NONBLOCK)
        with os.fdopen(fd,'rb') as stream:
            if not stat.S_ISREG(os.fstat(stream.fileno()).st_mode): return False
            raw=stream.read(4097)
        if len(raw)>4096 or not raw.startswith((str(pid)+' (').encode()): return False
        fields=raw.rsplit(b')',1)[1].split()
        return len(fields)>=20 and fields[0]==b'Z' and fields[19]==birth.encode('ascii')
    except (OSError,IndexError,ValueError,UnicodeError):
        return False

def observe(adapter,row,run):
    unit=adapter.unit(row['id'],run['run_id']); active=unit.get('ActiveState') in {'active','activating','deactivating','reloading'}
    if not {'LoadState','ActiveState','InvocationID','MainPID','ControlGroup'}<=unit.keys(): raise ObservationError('Legacy supervisor evidence unavailable')
    owner=run.get('owner')
    handoff=run.get('legacy_handoff_invocation')
    if handoff and unit.get('InvocationID') and unit['InvocationID']!=handoff:
        raise ObservationError('Legacy preparation invocation replaced')
    if active:
        if owner and (unit.get('InvocationID')!=owner['invocation_id'] or int(unit.get('MainPID') or 0)!=owner['pid'] or proc_birth(owner['pid'])!=owner['birth']):
            # Native ExecStop may run after the cancelled Qwen supervisor exits.
            # Permit only that exact deactivating invocation, with positive proof
            # the old PID is gone and its full running-container capture intact.
            stopping_owner_gone=(row['id']==QWEN and run.get('model_id')==QWEN
                and run.get('state')=='STOPPING' and run.get('cancelled') is True
                and unit.get('LoadState')=='loaded' and unit.get('ActiveState')=='deactivating'
                and unit.get('MainPID')=='0' and owner.get('unit')==unit_name(row['id'],run['run_id'])
                and isinstance(owner.get('invocation_id'),str) and re.fullmatch('[0-9a-f]{32}',owner['invocation_id'])
                and unit.get('InvocationID')==owner['invocation_id']
                and isinstance(owner.get('birth'),str) and re.fullmatch('[0-9]+',owner['birth'])
                and all(isinstance(run.get(k),str) and re.fullmatch('[0-9a-f]{64}',run[k]) for k in ('container_id','legacy_declaration_sha256'))
                and isinstance(run.get('container_created'),str) and bool(run['container_created'])
                and type(run.get('engine_pid')) is int and run['engine_pid']>0
                and isinstance(run.get('engine_birth'),str) and re.fullmatch('[0-9]+',run['engine_birth'])
                and adapter.process_released(owner.get('pid'),owner['birth']) is True)
            if not stopping_owner_gone:
                raise ObservationError('Legacy supervisor invocation/PID birth replaced')
        if not owner and not unit.get('InvocationID'): raise ObservationError('Legacy supervisor invocation unavailable')
    elif owner and unit.get('InvocationID') and unit['InvocationID']!=owner['invocation_id']:
        raise ObservationError('Legacy stopped invocation replaced')
    engine=False;epid=0;cid=run.get('container_id')
    if cid:
        present=container_ids(adapter)
        if cid not in present:
            marker=reap_marker(row,run)
            natural=(row['runtime']['auto_remove'] and run.get('container_created') and run.get('engine_birth'))
            if (not natural and marker is None) or inspect_name(adapter,row):
                raise ObservationError('Legacy exact container missing/replaced')
        else:
            c=adapter.inspect_container(cid)
            if c.get('Created')!=run.get('container_created') or c.get('Image')!=row['runtime']['image_id']: raise ObservationError('Legacy immutable creation/image replaced')
            frozen=run.get('legacy_declaration_sha256')
            if frozen:
                if not declaration_matches(c,frozen): raise ObservationError('Legacy effective declaration replaced')
            else:
                labels=c.get('Config',{}).get('Labels') or {}
                if any(labels.get(k)!=v for k,v in {'spark.v4.model':row['id'],'spark.v4.run':run['run_id'],'spark.v4.generation':str(run['generation']),'spark.v4.attempt':run['attempt']}.items()):
                    raise ObservationError('Legacy newly created run labels differ')
            state=c.get('State',{})
            if type(state.get('Running')) is not bool or type(state.get('Pid')) is not int: raise ObservationError('Legacy process state unavailable')
            engine=state['Running'];epid=state['Pid']
            if not engine and (epid!=0 or not isinstance(state.get('StartedAt'),str) or not state['StartedAt']): raise ObservationError('Legacy stopped process evidence unavailable')
            if not engine and state.get('StartedAt')!='0001-01-01T00:00:00Z' and not run.get('engine_birth'): raise ObservationError('Legacy previously started engine birth unavailable')
            if engine:
                if not frozen: raise ObservationError('Legacy engine active before immutable declaration capture')
                birth=proc_birth(epid)
                if not birth or (run.get('engine_pid') and (epid!=run['engine_pid'] or birth!=run.get('engine_birth'))):
                    # Preserve ACTIVE during the exact cancelled Qwen teardown.
                    # proc_birth intentionally hides zombies; only a fresh raw Z
                    # with the same captured tick may wait for the normal full
                    # release proof. Never call this process released/READY.
                    stopping_zombie=(row['id'] in IDS and run.get('model_id')==row['id']
                        and run.get('state')=='STOPPING' and run.get('cancelled') is True
                        and not birth and type(epid) is int and epid>0
                        and epid==run.get('engine_pid')
                        and isinstance(run.get('engine_birth'),str)
                        and re.fullmatch('[0-9]+',run['engine_birth'])
                        and c.get('Id')==cid and isinstance(frozen,str)
                        and re.fullmatch('[0-9a-f]{64}',frozen)
                        and isinstance(owner,dict)
                        and owner.get('unit')==unit_name(row['id'],run['run_id'])
                        and unit.get('LoadState')=='loaded'
                        and unit.get('ActiveState')=='deactivating'
                        and unit.get('InvocationID')==owner.get('invocation_id')
                        and isinstance(owner.get('invocation_id'),str)
                        and re.fullmatch('[0-9a-f]{32}',owner['invocation_id'])
                        and _same_birth_zombie(epid,run['engine_birth']))
                    if not stopping_zombie:
                        error=ObservationError('Legacy engine PID birth replaced')
                        # Still refuse this running snapshot. Only a positively gone
                        # exact PID in an already cancelled Qwen stop may request a
                        # separate full release proof; PID reuse never gets this tag.
                        if (row['id'] in IDS and run.get('model_id')==row['id']
                                and run.get('state')=='STOPPING' and run.get('cancelled') is True
                                and not birth and type(epid) is int and epid>0
                                and epid==run.get('engine_pid')
                                and isinstance(run.get('engine_birth'),str)
                                and re.fullmatch('[0-9]+',run['engine_birth'])
                                and adapter.process_released(epid,run['engine_birth']) is True):
                            setattr(error,'qwen_stopping_run' if row['id']==QWEN else 'gpt_stopping_run',copy.deepcopy(run))
                        raise error
    return {'active':active or engine,'engine_active':engine,'supervisor_active':active,'engine_pid':epid}

def idle(adapter,row):
    if row['id']==GPT:
        active=adapter.command(['/usr/bin/systemctl','list-units','--all','--plain','--no-legend','--no-pager','--state=active,activating,deactivating,reloading','spark-v4-'+row['id']+'@*.service'],.4)
        if active.strip(): raise ObservationError('Unrecorded GPT supervisor exists')
    unit=adapter.unit(row['id'],'0'*32 if row['id']==GPT else None)
    if unit.get('ActiveState') not in {'inactive','failed'} or int(unit.get('MainPID') or 0): raise ObservationError('Unadopted legacy supervisor active/unknown')
    if inspect_name(adapter,row): raise ObservationError('Unadopted legacy container exists; explicit local adoption/recovery required')

def request_terminate(adapter,row,run):
    policy=effective_unit(adapter,row,run)
    if not policy['control_safe']: raise Refusal('Unsafe original legacy ExecStop must be locally migrated first')
    observed=observe(adapter,row,run)
    if observed['supervisor_active'] and (run.get('owner') or (row['id']==QWEN and run.get('legacy_handoff_invocation'))):
        adapter.command(['/usr/bin/systemctl','stop','--no-block',unit_name(row['id'],run['run_id'])],2)
    elif observed['engine_active']: raise Refusal('Legacy orphan requires exact local recovery')

def resources_released(adapter,row,run):
    observed=observe(adapter,row,run)
    if observed['active']: return False
    unit=adapter.unit(row['id'],run['run_id']);owner=run.get('owner')
    if unit.get('ActiveState') not in {'inactive','failed'} or int(unit.get('MainPID') or 0): raise ObservationError('Legacy supervisor release unavailable')
    if run.get('container_create_attempted') and not run.get('container_id'): raise ObservationError('Legacy create outcome unknown')
    if run.get('container_id') and not owner: raise ObservationError('Legacy engine lacks captured supervisor')
    if owner and (owner.get('unit')!=unit_name(row['id'],run['run_id']) or not adapter.process_released(owner.get('pid'),owner.get('birth'))): return False
    if run.get('engine_pid') and not adapter.process_released(run['engine_pid'],run.get('engine_birth')): return False
    cid=run.get('container_id'); names={unit_name(row['id'],run['run_id'])}
    if cid: names|={cid,'docker-'+cid+'.scope'}
    if unit.get('ControlGroup') and not adapter.cgroup_released(unit['ControlGroup']): return False
    if not adapter.owned_cgroups_released(names): return False
    pids=adapter.gpu_pids()
    if len(pids)>128 or len(set(pids))!=len(pids): raise ObservationError('Legacy GPU observation bound')
    for pid in pids:
        groups=adapter.process_cgroups(pid)
        if groups and any(names.intersection(adapter.cgroup_parts(g)) for g in groups): return False
    if observed!=observe(adapter,row,run) or unit!=adapter.unit(row['id'],run['run_id']): raise ObservationError('Legacy physical identity changed during release proof')
    return True

def original_qwen_owner_argv(row,key):
    expected=['docker','run','--rm','--name','spark-sglang','--gpus','all','--ipc=host','--network','host','--shm-size=16g','--ulimit','memlock=-1','--ulimit','stack=67108864']
    for m in row['runtime']['mounts']: expected+=['-v',m['source']+':'+m['target']+(':ro' if m['readonly'] else '')]
    for k in ('HF_HUB_OFFLINE','TRANSFORMERS_OFFLINE','HF_HOME','HF_HUB_DISABLE_TELEMETRY'): expected+=['-e',k+'='+row['runtime']['env'][k]]
    return expected+[row['runtime']['image_id'],*command_argv(row,key)]

def adopt(ctl,mid):
    row=ctl.row(mid)
    if mid!=QWEN: raise Refusal('GPT original shell is never adoptable/executable; only tracked approved worker can create it')
    policy,witnesses,key=qualify(ctl.adapter,row,allow_original=True)
    with ctl.store.lock():
        data=ctl.store.read();previous=data['runs'].get(mid)
        if data.get('reservation') not in {None,mid}: raise Refusal('Shared collection reservation belongs to another deployment')
        for other,run in data['runs'].items():
            if other!=mid and ctl.adapter.observe(ctl.row(other),run)['active']: raise Refusal('Shared collection has another active owner')
        unit=ctl.adapter.unit(mid,None);cid=inspect_name(ctl.adapter,row)
        if unit.get('ActiveState')!='active' or not cid or not unit.get('InvocationID'): raise Refusal('Only exact active legacy Qwen can be adopted')
        c=ctl.adapter.inspect_container(cid)
        if previous:
            captured=ctl.captured_legacy_declaration(previous)
            if captured is None or not declaration_matches(c,captured):
                raise Refusal('Existing adopted immutable declaration changed; never rebind same CID')
        original=not policy['control_safe'] or bool(previous and previous.get('legacy_original_adopted'))
        if policy['control_safe'] and not previous:
            raise Refusal('An original running Qwen must be adopted before safe-unit migration')
        frozen=check_declaration(row,c,key,ctl.adapter,allow_original_logs=original)
        if previous:
            frozen=captured  # Qualification never rewrites a historical binding.
        pid=int(unit.get('MainPID') or 0);epid=c.get('State',{}).get('Pid')
        if not pid or not proc_birth(pid) or c.get('State',{}).get('Running') is not True or not proc_birth(epid): raise Refusal('Legacy physical process birth unavailable')
        if not policy['control_safe']:
            exe=Path('/proc')/str(pid)/'exe'
            if os.readlink(exe)!='/usr/bin/docker': raise Refusal('Original Qwen owner executable differs')
            raw=(Path('/proc')/str(pid)/'cmdline').read_bytes()
            if len(raw)>16384: raise Refusal('Original Qwen owner argv bound')
            argv=[x.decode('utf-8') for x in raw.rstrip(b'\0').split(b'\0')]
            expected=original_qwen_owner_argv(row,key)
            if argv!=expected: raise Refusal('Original Qwen supervisor argv differs from protected declaration')
        owner={'unit':unit_name(mid),'invocation_id':unit['InvocationID'],'pid':pid,'birth':proc_birth(pid)}
        # Bind the exact engine PID's Docker full-ID cgroup and actual supervisor
        # cgroup; a merely matching named container is not ownership proof.
        groups=ctl.adapter.process_cgroups(epid)
        if not groups or not any({cid,'docker-'+cid+'.scope'}.intersection(ctl.adapter.cgroup_parts(g)) for g in groups): raise Refusal('Legacy engine cgroup does not bind full CID')
        if not unit.get('ControlGroup') or unit_name(mid) not in ctl.adapter.cgroup_parts(unit['ControlGroup']): raise Refusal('Legacy supervisor cgroup differs')
        if previous:
            if any(previous.get(k)!=v for k,v in {'container_id':cid,'container_created':c.get('Created'),'owner':owner,'engine_pid':epid,'engine_birth':proc_birth(epid)}.items()): raise Refusal('Existing adopted immutable identity changed; never overwrite')
            run=previous
        else:
            count=data['counters'].get(mid,0)+1;data['counters'][mid]=count
            run={'model_id':mid,'generation':count,'attempt':uuid.uuid4().hex,'run_id':uuid.uuid4().hex,'created_at':time.time(),'state':'STARTING','cancelled':False}
        run.update(owner=owner,container_id=cid,container_created=c.get('Created'),engine_pid=epid,engine_birth=proc_birth(epid),legacy_declaration_sha256=frozen,legacy_unit_qualification=policy,legacy_original_adopted=original,message='Explicit local immutable legacy adoption; readiness independently observed')
        observe(ctl.adapter,row,run)
        ctl.adapter.recheck_bundle(witnesses)
        if ctl.adapter.unit(mid,None)!=unit or not declaration_matches(ctl.adapter.inspect_container(cid),frozen): raise Refusal('Legacy identity changed while adopting')
        data['runs'][mid]=run;data['reservation']=mid
        ctl.store.write(data)
    return {'profile_id':mid,'run_id':run['run_id'],'generation':run['generation'],'adopted':True,'control_safe':policy['control_safe']}
