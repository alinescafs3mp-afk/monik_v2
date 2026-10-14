#!/usr/bin/python3
"""Fixed systemd-only Qwen entrypoints, using the same durable Controller."""
import os
from pathlib import Path
import signal
import stat
import sys
def protected_bootstrap(path):
    for current in reversed((path,*path.parents)):
        st=current.lstat()
        if stat.S_ISLNK(st.st_mode) or st.st_uid!=0 or st.st_mode&0o022:
            raise RuntimeError('Unprotected fixed legacy entrypoint source')
        if current!=path and not stat.S_ISDIR(st.st_mode): raise RuntimeError('Invalid source ancestor')
    if not stat.S_ISREG(path.lstat().st_mode): raise RuntimeError('Invalid fixed source')

BIN=Path('/opt/spark/model-collection-v4/bin')
if Path(__file__).absolute().parent==BIN:
    for path in [Path(__file__),*(BIN/name for name in ('modelctl.py','supervisor.py','legacy_runtime.py'))]: protected_bootstrap(path)
sys.path.insert(0,str(BIN))
from modelctl import (Controller, SystemAdapter, Refusal, load_production_manifest,
                      protect, legacy_support)
from supervisor import Supervisor

def main():
    if os.geteuid()!=0 or not sys.flags.isolated or len(sys.argv)!=2 or sys.argv[1] not in {'qwen-can-launch','qwen-start','qwen-exact-stop'}:
        raise Refusal('Fixed isolated root systemd entrypoint required')
    protect(Path(__file__).resolve())
    manifest=load_production_manifest();ctl=Controller(manifest,SystemAdapter(manifest));legacy=legacy_support();mid=legacy.QWEN
    data=ctl.store.read();run=data['runs'].get(mid)
    if not run: raise Refusal('No exact Qwen run exists')
    if sys.argv[1]=='qwen-can-launch':
        # Capture the exact native preparation invocation before ExecStartPre.
        # A concurrent stop can cancel even before the persistent MainPID exists.
        # This uses the same collection.lock and never creates an engine.
        with ctl.store.lock():
            data=ctl.store.read();run=data['runs'].get(mid)
            if not run or run.get('cancelled') or run['state']!='STARTING' or data['reservation']!=mid or run.get('owner') or run.get('container_id'):
                return 1
            if run.get('approved_profile_revision')!=ctl.profile_revision(mid): return 1
            operation_id=run.get('bridge_operation_id')
            if operation_id and data.get('bridge_operations',{}).get(operation_id,{}).get('execution_forbidden'): return 1
            ctl.adapter.validate_run_definition(ctl.row(mid),run)
            invocation=os.environ.get('INVOCATION_ID','')
            if len(invocation)!=32 or any(c not in '0123456789abcdef' for c in invocation): return 1
            if ctl.adapter.unit(mid,run['run_id']).get('InvocationID')!=invocation: return 1
            if run.get('legacy_handoff_invocation') not in {None,invocation}: return 1
            run['legacy_handoff_invocation']=invocation
            ctl.store.write(data)
        return 0
    if sys.argv[1]=='qwen-exact-stop':
        with ctl.store.lock():
            data=ctl.store.read();current=data['runs'].get(mid)
            if not current or current['run_id']!=run['run_id']: raise Refusal('Qwen stop generation replaced')
            ctl.adapter.observe(ctl.row(mid),current)
            current.update(cancelled=True,state='STOPPING',message='Native unit exact-CID stop')
            ctl.store.write(data)
        seen=ctl.adapter.observe(ctl.row(mid),current)
        if seen['engine_active']:
            ctl.adapter.command(['/usr/bin/docker','stop','--time','90',current['container_id']],95)
        return 0
    invocation=os.environ.get('INVOCATION_ID','')
    if len(invocation)!=32 or any(c not in '0123456789abcdef' for c in invocation): raise Refusal('Missing exact systemd invocation')
    worker=Supervisor(ctl,mid,run['run_id'],invocation)
    def requested(signum,frame): worker.signal_requested=True
    signal.signal(signal.SIGTERM,requested);signal.signal(signal.SIGINT,requested)
    return worker.run()

if __name__=='__main__':
    try: raise SystemExit(main())
    except Exception: print('Exact Qwen systemd action refused',file=sys.stderr);raise SystemExit(1)
