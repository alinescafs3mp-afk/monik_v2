#!/usr/bin/env python3
"""Offline plan by default; explicit copy creates a NEW GGUF, never modifies source.
Pinned tokenizer provenance is mandatory. Intended only for this Nemotron snapshot.
No network, subprocess, inference, or runtime/configuration operations.
"""
import argparse,hashlib,json,os,struct,uuid,pathlib,signal
SOURCE_SHA='870e367f1ae025cba5ad3709d9bbb05444d9b4a51e89ca162021736cefcdd0cf'
TOKENIZER_SHA='623c34567aebb18582765289fbe23d901c62704d6518d71866e0e58db892b5b7'
TOKENIZER_CONFIG_SHA='10f93eabcb9b1602fbb991d6308e787ce1df28ee9cd7a1c6d1e8c3f338b957bc'
FORMATS={0:'B',1:'b',2:'H',3:'h',4:'I',5:'i',6:'f',7:'?',10:'Q',11:'q',12:'d'}
def need(ok,msg):
 if not ok:raise ValueError(msg)
def sha(b):return hashlib.sha256(b).hexdigest()
def encstr(s):
 b=s.encode('utf-8');return struct.pack('<Q',len(b))+b

def scan_header(path):
 with open(path,'rb') as f:
  size=os.fstat(f.fileno()).st_size
  def rd(n):
   need(0<=n<=max(size,1),'invalid read length');b=f.read(n);need(len(b)==n,'truncated GGUF header');return b
  def u64():return struct.unpack('<Q',rd(8))[0]
  def st():return rd(u64()).decode('utf-8')
  def val(t):
   if t in FORMATS:return struct.unpack('<'+FORMATS[t],rd(struct.calcsize('<'+FORMATS[t])))[0]
   if t==8:return st()
   if t==9:
    ty=struct.unpack('<I',rd(4))[0];n=u64();need(n<=size,'invalid array length');return [val(ty) for _ in range(n)]
   raise ValueError('unknown metadata type '+str(t))
  need(rd(4)==b'GGUF','GGUF magic mismatch');version=struct.unpack('<I',rd(4))[0];need(version==3,'requires little-endian GGUFv3')
  nt,nkv=struct.unpack('<QQ',rd(16));need(nt<=1000000 and nkv<=100000,'implausible counts')
  kv=[];meta={}
  for _ in range(nkv):
   start=f.tell();key=st();need(key not in meta,'duplicate metadata key');ty=struct.unpack('<I',rd(4))[0];value=val(ty);end=f.tell()
   f.seek(start);raw=rd(end-start);kv.append({'key':key,'type':ty,'value':value,'raw':raw,'start':start,'end':end});meta[key]=value
  metadata_end=f.tell();tensor_start=f.tell();tensors=[]
  for _ in range(nt):
   name=st();nd=struct.unpack('<I',rd(4))[0];need(nd<=8,'implausible tensor rank');dims=[u64() for _ in range(nd)];ty=struct.unpack('<I',rd(4))[0];offset=u64();tensors.append({'name':name,'dims':dims,'type':ty,'offset':offset})
  tensor_end=f.tell();f.seek(tensor_start);tensor_raw=rd(tensor_end-tensor_start)
  align=meta.get('general.alignment',32);need(isinstance(align,int) and 0<align<=1048576 and align&(align-1)==0,'invalid alignment')
  payload=(tensor_end+align-1)//align*align;need(payload<=size,'truncated alignment padding');f.seek(0);prefix=rd(payload)
 return {'size':size,'nt':nt,'nkv':nkv,'kv':kv,'meta':meta,'tensors':tensors,'tensor_raw':tensor_raw,'metadata_end':metadata_end,'tensor_end':tensor_end,'alignment':align,'payload_offset':payload,'prefix':prefix}

def official(tokenizer,config):
 b=pathlib.Path(tokenizer).read_bytes();need(sha(b)==TOKENIZER_SHA,'tokenizer hash mismatch');hf=json.loads(b)
 cb=pathlib.Path(config).read_bytes();need(sha(cb)==TOKENIZER_CONFIG_SHA,'tokenizer_config hash mismatch');cfg=json.loads(cb)
 need(cfg.get('add_bos_token') is False and cfg.get('add_eos_token') is False,'unexpected special insertion policy')
 need(hf['model'].get('ignore_merges') is True and hf['normalizer'] is None,'unexpected BPE semantics');need(hf['pre_tokenizer']['pretokenizers'][0]['type']=='Split' and hf['pre_tokenizer']['pretokenizers'][0]['pattern']['Regex'].find('\\p{Lu}\\p{Lt}')>=0,'unexpected Tekken pretokenizer');vocab=hf['model']['vocab'];n=len(vocab);need(n==131072 and set(vocab.values())==set(range(n)),'vocab IDs must be complete and unique')
 expected=['']*n;types=[1]*n
 for token,idx in vocab.items():expected[idx]=token
 for tok in hf['added_tokens']:
  idx=tok['id'];need(0<=idx<n,'added token out of range');expected[idx]=tok['content'];types[idx]=3 if tok['special'] else 4
  need(all(tok[k] is False for k in ['single_word','lstrip','rstrip','normalized']),'unsupported added token boundary behavior')
 need(all(expected) and len(set(expected))==n,'empty or duplicate official tokens')
 merges=[' '.join(x) if isinstance(x,list) else x for x in hf['model']['merges']]
 return expected,types,merges

def plan(src,tokenizer,config,strict_snapshot=True):
 h=scan_header(src);m=h['meta'];need(m.get('general.architecture')=='nemotron_h_moe','wrong architecture');need(m.get('tokenizer.ggml.pre')=='pixtral','unexpected pretokenizer snapshot')
 expected,types,merges=official(tokenizer,config);actual=m['tokenizer.ggml.tokens']
 need(len(actual)==len(expected),'vocab length mismatch');need(m['tokenizer.ggml.merges']==merges,'ordered BPE merges differ');need(m['tokenizer.ggml.token_type']==types,'token types differ from official special mapping')
 diff=[{'id':i,'before':a,'after':b} for i,(a,b) in enumerate(zip(actual,expected)) if a!=b]
 need(all(x['before']=='' and x['after'] for x in diff),'nonempty token drift; refuse automatic rewrite')
 if strict_snapshot:need([x['id'] for x in diff]==list(range(12))+list(range(18,1000)),'unexpected defect set')
 need(m.get('tokenizer.ggml.bos_token_id')==1 and m.get('tokenizer.ggml.eos_token_id')==11 and m.get('tokenizer.ggml.unknown_token_id')==0,'unexpected token IDs')
 need(m.get('tokenizer.ggml.padding_token_id')==11,'unexpected padding ID')
 changes={'tokenizer.ggml.pre':(8,encstr('tekken')),'tokenizer.ggml.tokens':(9,struct.pack('<IQ',8,len(expected))+b''.join(encstr(s) for s in expected)),'tokenizer.ggml.add_bos_token':(7,b'\x00'),'tokenizer.ggml.add_eos_token':(7,b'\x00')}
 kvbytes=[];seen=set()
 for item in h['kv']:
  k=item['key'];seen.add(k)
  if k in changes:
   ty,data=changes[k];kvbytes.append(encstr(k)+struct.pack('<I',ty)+data)
  else:kvbytes.append(item['raw'])
 for k,(ty,data) in changes.items():
  if k not in seen:kvbytes.append(encstr(k)+struct.pack('<I',ty)+data)
 prefix=b'GGUF'+struct.pack('<IQQ',3,h['nt'],len(kvbytes))+b''.join(kvbytes)+h['tensor_raw'];prefix+=b'\x00'*((-len(prefix))%h['alignment'])
 summary={'source_size':h['size'],'source_metadata_end':h['metadata_end'],'source_tensor_info_end':h['tensor_end'],'source_payload_offset':h['payload_offset'],'alignment':h['alignment'],'tensor_count':h['nt'],'old_kv_count':h['nkv'],'new_kv_count':len(kvbytes),'replacement_count':len(diff),'replacement_ids_first12':diff[:12],'new_payload_offset':len(prefix),'payload_shift':len(prefix)-h['payload_offset'],'new_file_size':h['size']+len(prefix)-h['payload_offset'],'tensor_directory_sha256':sha(h['tensor_raw']),'all_ordered_merges_equal':True,'all_token_types_equal':True,'ordinary_tokens_unchanged':True,'preserved_kv_keys':[x['key'] for x in h['kv'] if x['key'] not in changes],'changes':list(changes)}
 return h,prefix,summary,expected

def copy_candidate(src,dest,tokenizer,config,expected_source_sha=SOURCE_SHA,min_free_after=8*1024**3,strict_snapshot=True):
 src=pathlib.Path(src);dest=pathlib.Path(dest);need(not dest.exists(),'destination exists');need(src.resolve()!=dest.resolve(),'destination equals source')
 h,prefix,result,expected=plan(src,tokenizer,config,strict_snapshot)
 fs=os.statvfs(dest.parent);need(fs.f_bavail*fs.f_frsize>=result['new_file_size']+min_free_after,'insufficient disk reserve')
 tmp=dest.with_name(dest.name+'.partial-'+uuid.uuid4().hex);result['temporary_path']=str(tmp)
 source_hash=hashlib.sha256(h['prefix']);payload_hash=hashlib.sha256();written_hash=hashlib.sha256(prefix)
 try:
  fd=os.open(tmp,os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW,0o600)
  with os.fdopen(fd,'wb') as out,open(src,'rb') as inp:
   before=os.fstat(inp.fileno());need(inp.read(h['payload_offset'])==h['prefix'],'source header changed after planning');out.write(prefix);copied=0
   while True:
    b=inp.read(8*1024*1024)
    if not b:break
    source_hash.update(b);payload_hash.update(b);written_hash.update(b);out.write(b);copied+=len(b)
   after=os.fstat(inp.fileno());need((before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns,before.st_ctime_ns)==(after.st_dev,after.st_ino,after.st_size,after.st_mtime_ns,after.st_ctime_ns),'source changed during copy')
   need(copied==h['size']-h['payload_offset'],'source length changed');need(source_hash.hexdigest()==expected_source_sha,'source full SHA256 mismatch');out.flush();os.fsync(out.fileno())
  verify=scan_header(tmp);need(verify['meta']['tokenizer.ggml.tokens']==expected,'written token table mismatch');need(verify['meta']['tokenizer.ggml.pre']=='tekken','written pretokenizer mismatch');need(verify['tensor_raw']==h['tensor_raw'],'tensor descriptors changed')
  need(verify['meta']['tokenizer.ggml.add_bos_token'] is False and verify['meta']['tokenizer.ggml.add_eos_token'] is False,'written insertion policy mismatch')
  for item in h['kv']:
   if item['key'] not in result['changes']:need(next(x['raw'] for x in verify['kv'] if x['key']==item['key'])==item['raw'],'unrelated metadata bytes changed')
  check_payload=hashlib.sha256();check_whole=hashlib.sha256(verify['prefix'])
  with open(tmp,'rb') as inp:
   inp.seek(verify['payload_offset'])
   while True:
    b=inp.read(8*1024*1024)
    if not b:break
    check_payload.update(b);check_whole.update(b)
  need(check_payload.digest()==payload_hash.digest(),'written payload differs');need(check_whole.digest()==written_hash.digest(),'written full hash differs');need(verify['size']==result['new_file_size'],'written size differs')
  current=os.stat(src);need((current.st_dev,current.st_ino,current.st_size,current.st_mtime_ns,current.st_ctime_ns)==(before.st_dev,before.st_ino,before.st_size,before.st_mtime_ns,before.st_ctime_ns),'source path changed before publication')
  # Atomic no-overwrite publication on the same filesystem, including race with another writer.
  os.link(tmp,dest);os.unlink(tmp)
  directory_fd=os.open(dest.parent,os.O_RDONLY|os.O_DIRECTORY)
  try:os.fsync(directory_fd)
  finally:os.close(directory_fd)
  result.update({'candidate':str(dest),'source_sha256':source_hash.hexdigest(),'candidate_sha256':check_whole.hexdigest(),'payload_sha256':check_payload.hexdigest(),'payload_bytes':copied,'payload_verified_by_reread':True,'source_unchanged':True})
  return result
 finally:
  if tmp.exists():tmp.unlink()

def main():
 a=argparse.ArgumentParser(description=__doc__);a.add_argument('mode',choices=['plan','copy']);a.add_argument('--source',required=True);a.add_argument('--tokenizer',required=True);a.add_argument('--tokenizer-config',required=True);a.add_argument('--destination');a.add_argument('--min-free-after-bytes',type=int,default=8*1024**3);args=a.parse_args()
 need(args.min_free_after_bytes>=0,'negative disk reserve')
 if args.mode=='plan':result=plan(args.source,args.tokenizer,args.tokenizer_config)[2]
 else:
  need(args.destination,'copy requires --destination');result=copy_candidate(args.source,args.destination,args.tokenizer,args.tokenizer_config,min_free_after=args.min_free_after_bytes)
 print(json.dumps(result,ensure_ascii=False,indent=2))
if __name__=='__main__':
 def stop(signum,frame):raise KeyboardInterrupt('signal '+str(signum))
 signal.signal(signal.SIGTERM,stop)
 main()
