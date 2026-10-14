#!/usr/bin/env python3
"""Fixed isolated native staging. Only the native-run owner executes apply."""
import hashlib
import json
import os
import pathlib
import platform
import pwd
import stat
import subprocess
import sys
import time

BASE = pathlib.Path('/opt/monik-spark-native-control-canary-20261014')
STATE = pathlib.Path('/var/lib/monik-spark-native-control-canary-20261014')
POLICY = pathlib.Path('/etc/monik-spark-native-control-canary-20261014/policy.json')
SOCKET = pathlib.Path('/run/monik-spark/control.sock')
NAMES = ['ready', 'starting', 'preflight']
UNITS = [f'spark-native-control-{name}-canary.service' for name in NAMES]
BROKER = 'spark-native-control-broker-canary.service'
SECRET = 'MONIK_SYNTHETIC_LITERAL_REDACTION_20261014'
ENV = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C', 'LC_ALL': 'C'}


def call(args, timeout=10):
    proc = subprocess.run(args, env=ENV, capture_output=True, timeout=timeout)
    if proc.returncode:
        raise RuntimeError(f'fixed command failed: {args[0]} {args[1] if len(args)>1 else ""}; exit={proc.returncode}')
    return proc.stdout.decode()


def protected(path, directory=False):
    path = pathlib.Path(path)
    info = path.lstat()
    good_type = stat.S_ISDIR(info.st_mode) if directory else stat.S_ISREG(info.st_mode)
    if info.st_uid != 0 or info.st_mode & 0o022 or not good_type or not directory and info.st_nlink != 1:
        raise RuntimeError(f'root-protected path required: {path}')
    for parent in path.parents:
        info = parent.lstat()
        if info.st_uid != 0 or info.st_mode & 0o022 or not stat.S_ISDIR(info.st_mode):
            raise RuntimeError('root-protected ancestors required')


def service(name, port):
    delay = name == 'starting'
    pre = 'delay-synthetic' if delay else 'emit-synthetic-log'
    ready_after = '3s'
    return f'''[Unit]
Description=Monik explicitly synthetic CPU control canary ({name}; no model)

[Service]
Type=simple
User=monik-canary
Group=monik-canary
ExecStartPre={BASE}/spark-control-canary {pre}
ExecStart={BASE}/fake-engine --listen 127.0.0.1:{port} --ready-after {ready_after}
Restart=no
TimeoutStartSec=25s
TimeoutStopSec=5s
KillMode=control-group
SendSIGKILL=yes
StandardOutput=journal
StandardError=journal
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes
CapabilityBoundingSet=
RestrictSUIDSGID=yes
LockPersonality=yes
RestrictAddressFamilies=AF_INET AF_UNIX
MemoryMax=128M
CPUQuota=50%
TasksMax=16
'''


def broker():
    return f'''[Unit]
Description=Monik isolated synthetic control broker canary

[Service]
Type=simple
User=root
Group=root
ExecStart={BASE}/monik-spark-agent broker --policy {POLICY} --socket {SOCKET}
Restart=no
TimeoutStopSec=10s
KillMode=control-group
NoNewPrivileges=yes
ProtectHome=yes
MemoryMax=256M
CPUQuota=100%
TasksMax=64
'''


def all_assets():
    return {**{unit: service(name, 18971+n) for n, (name, unit) in enumerate(zip(NAMES, UNITS))}, BROKER: broker()}


def properties(unit):
    text = call(['/usr/bin/systemctl', 'show', '--no-pager', '--property=LoadState,ActiveState,FragmentPath,DropInPaths,Transient', '--', unit])
    return dict(line.split('=', 1) for line in text.splitlines() if '=' in line)


def no_prospective_dropins():
    dirs = call(['/usr/bin/systemctl', 'show', '--no-pager', '--property=UnitPath', '--value']).split()
    if not dirs:
        raise RuntimeError('unit search paths unavailable')
    candidates = {'service.d'}
    for unit in UNITS+[BROKER]:
        candidates.add(unit+'.d')
        stem = unit[:-len('.service')]
        for n, ch in enumerate(stem):
            if ch == '-':
                candidates.add(stem[:n+1]+'.service.d')
    for directory in dirs:
        if not pathlib.Path(directory).is_absolute():
            raise RuntimeError('canonical unit paths required')
        for name in candidates:
            candidate = pathlib.Path(directory)/name
            if candidate.exists() and any(candidate.glob('*.conf')):
                raise RuntimeError(f'prospective drop-in conflicts with isolated unit: {candidate}')


def apply():
    if os.geteuid() != 0 or platform.machine() not in ('aarch64', 'arm64'):
        raise RuntimeError('native Linux ARM64 root required')
    for path in [BASE/'CANARY_ONLY', BASE/'spark-control-canary', BASE/'fake-engine', BASE/'monik-spark-agent']:
        protected(path)
    if (BASE/'CANARY_ONLY').read_text().strip() != 'MONIK_NATIVE_CONTROL_CANARY_20261014':
        raise RuntimeError('exact owner-staged marker required')
    for path in [STATE, POLICY.parent, SOCKET, BASE/'redaction.txt', BASE/'synthetic-metadata', *[pathlib.Path('/etc/systemd/system')/u for u in UNITS+[BROKER]]]:
        if path.exists() or path.is_symlink():
            raise RuntimeError(f'first application path occupied: {path}')
    account = pwd.getpwnam('monik-canary')
    if account.pw_uid == 0 or account.pw_gid == 0:
        raise RuntimeError('nonroot account/group required')
    for unit in UNITS+[BROKER]:
        p = properties(unit)
        if p.get('LoadState') != 'not-found' or p.get('ActiveState') != 'inactive' or p.get('FragmentPath') or p.get('DropInPaths') or p.get('Transient') != 'no':
            raise RuntimeError(f'unit already exists or cannot be verified: {unit}')
    no_prospective_dropins()
    # Derives fixed fixture IDs and account IDs; emits no credential material.
    draft = json.loads(call([str(BASE/'spark-control-canary'), 'draft']))
    if draft['enabled'] or draft['agent_id'] != 'c7966ecd-e521-4e18-9142-dff2bc9d218e':
        raise RuntimeError('wrong isolated draft')
    # Check all three literal loopback ports before publishing any unit.
    import socket as sockets
    probes = []
    try:
        for port in range(18971, 18974):
            probe = sockets.socket(sockets.AF_INET, sockets.SOCK_STREAM)
            probes.append(probe)
            probe.bind(('127.0.0.1', port))
    finally:
        for probe in probes:
            probe.close()
    STATE.mkdir(mode=0o700)
    POLICY.parent.mkdir(mode=0o700)
    runtime = SOCKET.parent
    if not runtime.exists():
        runtime.mkdir(mode=0o755)
    protected(runtime, directory=True)
    metadata = BASE/'synthetic-metadata'
    if metadata.exists() or metadata.is_symlink():
        raise RuntimeError('synthetic metadata path already exists')
    metadata.mkdir(mode=0o700)
    # 4096 empty sparse metadata fixtures, no model bytes, finite scanner budget.
    for n in range(4096):
        fd = os.open(metadata/f'{n:04d}.bin', os.O_WRONLY|os.O_CREAT|os.O_EXCL, 0o600)
        os.close(fd)
    fd = os.open(BASE/'redaction.txt', os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o600)
    with os.fdopen(fd, 'w') as f:
        f.write(SECRET+'\n')
    draft_path = POLICY.parent/'draft.json'
    draft_path.write_text(json.dumps(draft, indent=2)+'\n')
    os.chmod(draft_path, 0o600)
    for unit, text in all_assets().items():
        path = pathlib.Path('/etc/systemd/system')/unit
        fd = os.open(path, os.O_WRONLY|os.O_CREAT|os.O_EXCL|os.O_NOFOLLOW, 0o644)
        with os.fdopen(fd, 'w') as f:
            f.write(text)
    call(['/usr/bin/systemctl', 'daemon-reload'])
    for unit in UNITS+[BROKER]:
        p = properties(unit)
        if p.get('FragmentPath') != '/etc/systemd/system/'+unit or p.get('DropInPaths') or p.get('Transient') != 'no':
            raise RuntimeError('resolved unit did not match exact staged canary; no broker start')
    approval = call([str(BASE/'monik-spark-agent'), 'approve', '--draft', str(draft_path), '--output', str(POLICY), '--enable'], timeout=25)
    verification = call([str(BASE/'monik-spark-agent'), 'verify-policy', '--policy', str(POLICY)])
    # The native-run owner must reserve this standard-socket boundary throughout
    # apply/run/cleanup. The product also locks this socket path and refuses
    # an active or uncertain peer; keep this canary separate from production.
    if SOCKET.exists() or SOCKET.is_symlink():
        raise RuntimeError('standard socket became occupied; broker was not started')
    call(['/usr/bin/systemctl', 'start', '--', BROKER])
    return {'applied': True, 'approval': approval.strip(), 'verification': verification.strip(), 'units': UNITS+[BROKER], 'state_dir': str(STATE), 'policy': str(POLICY), 'socket': str(SOCKET), 'fake_engine_sha256': hashlib.sha256((BASE/'fake-engine').read_bytes()).hexdigest(), 'helper_sha256': hashlib.sha256((BASE/'spark-control-canary').read_bytes()).hexdigest(), 'started_model_units': [], 'limit': 'no model units started; broker can bind existing light collector on next sample'}


def cleanup_plan():
    # Read-only cleanup verification; no stop, delete or daemon reload here.
    for unit, expected in all_assets().items():
        path = pathlib.Path('/etc/systemd/system')/unit
        protected(path)
        if path.read_text() != expected:
            raise RuntimeError('staged unit changed; do not clean it automatically')
        p = properties(unit)
        if p.get('FragmentPath') != str(path) or p.get('DropInPaths') or p.get('Transient') != 'no':
            raise RuntimeError('resolved unit changed; do not clean it automatically')
    return {'executed': False, 'stop_in_order': UNITS+[BROKER], 'remove_only': ['/etc/systemd/system/'+u for u in UNITS+[BROKER]], 'then': 'systemctl daemon-reload; verify inactive/no main PID and standard socket absent; retain all private state and account evidence', 'excluded': ['monik-agent.service', 'all production units, containers, model launchers and identities']}


def main():
    phase = sys.argv[1] if len(sys.argv) == 2 else 'plan'
    started = time.monotonic()
    try:
        if phase == 'plan':
            result = {'executed': False, 'assets': all_assets(), 'root_owned_inputs': [str(BASE/n) for n in ['CANARY_ONLY', 'spark-control-canary', 'fake-engine', 'monik-spark-agent']], 'marker_text': 'MONIK_NATIVE_CONTROL_CANARY_20261014', 'state_dir': str(STATE), 'policy': str(POLICY), 'socket': str(SOCKET)}
        elif phase == 'apply':
            result = apply()
        elif phase == 'cleanup-plan':
            result = cleanup_plan()
        else:
            raise RuntimeError('use plan | apply | cleanup-plan')
        result.update(success=True, phase=phase, duration_ms=int((time.monotonic()-started)*1000))
        print(json.dumps(result, indent=2))
    except Exception as exc:
        print(json.dumps({'success': False, 'phase': phase, 'error': str(exc), 'duration_ms': int((time.monotonic()-started)*1000)}))
        raise SystemExit(1)


if __name__ == '__main__':
    main()
