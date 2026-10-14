#!/usr/bin/python3
"""Shared Spark V4 controller. Production entrypoint has no configurable paths.

No inference happens at import, manifest validation, or status. The constructor's
injected adapters are solely an offline test seam; they are not CLI options.
"""
from __future__ import annotations

import contextlib
import importlib.util
import fcntl
import hashlib
import json
import os
from pathlib import Path
import re
import socket
import signal
import stat
import subprocess
import sys
import time
import threading
import urllib.error
import urllib.request
import uuid

PROJECT = Path('/opt/spark/model-collection-v4')
MANIFEST = PROJECT / 'config/controller-manifest.json'
STATE_ROOT = Path('/var/lib/spark-model-collection-v4')
LOG_ROOT = Path('/var/log/spark-model-collection-v4')
IDS = ('qwen3-coder-next-80b-huihui', 'ling-3-flash-124b-heretic-text',
       'mistral-small-4-119b-heretic', 'nemotron-3-super-120b-heretic-v2',
       'minimax-m27-229b-heretic', 'qwen-existing-observation',
       'gpt-oss-120b-existing-configured')
HEX64 = re.compile(r'[a-f0-9]{64}\Z')
ENV_KEYS = {'VLLM_ATTENTION_BACKEND', 'VLLM_USE_FLASHINFER_MOE_FP4',
            'VLLM_ENABLE_CUDAGRAPH_GC', 'CUDA_DEVICE_ORDER', 'NCCL_P2P_DISABLE',
            'OMP_NUM_THREADS', 'TOKENIZERS_PARALLELISM', 'HF_HUB_OFFLINE',
            'TRANSFORMERS_OFFLINE', 'VLLM_LOGGING_LEVEL', 'CUDA_CACHE_PATH',
            'XDG_CACHE_HOME', 'HF_HOME', 'TORCHINDUCTOR_CACHE_DIR', 'TRITON_CACHE_DIR',
            'FLASHINFER_WORKSPACE_BASE', 'VLLM_NO_USAGE_STATS',
            'VLLM_ENABLE_CUDA_COMPATIBILITY', 'VLLM_USE_V2_MODEL_RUNNER',
            'GGML_CUDA_ENABLE_UNIFIED_MEMORY', 'GGML_CUDA_NO_PINNED'}
BASE_ENV = {'PATH': '/usr/sbin:/usr/bin:/sbin:/bin', 'LANG': 'C.UTF-8',
            'LC_ALL': 'C.UTF-8', 'HOME': '/root'}
BRIDGE_JOURNAL_CAP = 256
BRIDGE_RETIRED_BYTES = 16 * 1024
STATE_MAX_BYTES = 1024 * 1024


class Refusal(RuntimeError):
    pass


class ObservationError(RuntimeError):
    pass


_LEGACY_SUPPORT = None

def legacy_support():
    global _LEGACY_SUPPORT
    if _LEGACY_SUPPORT is None:
        source = Path(__file__).resolve().parent / 'legacy_runtime.py'
        if source.parent == PROJECT / 'bin':
            protect(source)
        spec = importlib.util.spec_from_file_location('spark_fixed_legacy_runtime', source)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        for key in ('Refusal', 'ObservationError', 'protect', 'unit_name', 'proc_birth', 'PROJECT', 'MANIFEST'):
            setattr(module, key, globals()[key])
        _LEGACY_SUPPORT = module
    # proc_birth may be replaced at the explicit offline adapter seam.
    _LEGACY_SUPPORT.proc_birth = proc_birth
    return _LEGACY_SUPPORT


def unit_name(model_id, run_id=None):
    if model_id not in IDS:
        raise Refusal('Unknown fixed model ID')
    if run_id is not None and not re.fullmatch(r'[a-f0-9]{32}', run_id):
        raise Refusal('Invalid immutable run ID')
    if model_id == 'qwen-existing-observation':
        return 'spark-sglang.service'
    return 'spark-v4-' + model_id + '@' + (run_id or '') + '.service'


def protect(path, directory=False):
    """Reject symlinks/user-writable ancestors before any privileged read/write."""
    path = Path(path)
    if not path.is_absolute() or '..' in path.parts:
        raise Refusal('Nonabsolute privileged path')
    for current in reversed((path, *path.parents)):
        st = current.lstat()
        if stat.S_ISLNK(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
            raise Refusal('Privileged path must be root owned and not writable: ' + str(current))
        if current != path and not stat.S_ISDIR(st.st_mode):
            raise Refusal('Privileged ancestor is not a directory')
    st = path.lstat()
    if directory and not stat.S_ISDIR(st.st_mode):
        raise Refusal('Expected protected directory')
    if not directory and not stat.S_ISREG(st.st_mode):
        raise Refusal('Expected protected regular file')


def validate_manifest(data):
    if not isinstance(data, dict) or data.get('schema_version') != 4:
        raise Refusal('Controller manifest schema must be 4')
    if data.get('project_root') != str(PROJECT) or data.get('state_root') != str(STATE_ROOT) or data.get('log_root') != str(LOG_ROOT):
        raise Refusal('Manifest root paths differ from compiled fixed paths')
    if data.get('verified_uid') != 1000 or data.get('verified_home') != '/home/Dest':
        raise Refusal('Verified Spark account must be Dest UID 1000 /home/Dest')
    exclusions = data.get('excluded_units')
    if not isinstance(exclusions, list) or 'spark-sglang.service' not in exclusions or not exclusions:
        raise Refusal('Excluded baseline/GPT units must be explicitly recorded')
    if any(not re.fullmatch(r'[a-zA-Z0-9_.@-]+\.service', x) or x.startswith('spark-v4-') for x in exclusions):
        raise Refusal('Invalid excluded service')
    rows = data.get('models')
    if not isinstance(rows, list) or len(rows) not in (5, 7) or {r.get('id') for r in rows if isinstance(r, dict)} not in (set(IDS[:5]), set(IDS)):
        raise Refusal('Exactly five V4 or seven shared fixed model IDs required')
    ports = set()
    for row in rows:
        mid = row['id']
        if mid in IDS[5:]:
            legacy_support().validate_row(row)
            continue
        if type(row.get('prepared')) is not bool:
            raise Refusal(mid + ': prepared must be boolean')
        for k in ('name', 'engine', 'quant'):
            if not isinstance(row.get(k), str) or not row[k] or len(row[k]) > 180:
                raise Refusal(mid + ': invalid ' + k)
        supported = ({('vLLM', 'FP8'), ('vLLM', 'FP8 static'), ('llama.cpp CUDA', 'Q6_K_L')} if mid == IDS[0]
                     else {('vLLM', 'NVFP4'), ('vLLM', 'NVFP4 compressed-tensors')} if mid == IDS[2]
                     else {('llama.cpp CUDA', 'IQ3_XS')} if mid == IDS[4]
                     else {('llama.cpp CUDA', 'MXFP4_MOE')})
        if (row['engine'], row['quant']) not in supported:
            raise Refusal(mid + ': selected artifact-to-engine mapping differs from V4')
        context = row.get('context')
        if context is not None and (type(context) is not int or context < 1):
            raise Refusal(mid + ': invalid context')
        ep = row.get('endpoint', '')
        match = re.fullmatch(r'http://127\.0\.0\.1:([0-9]{4,5})/v1', ep)
        if not match or not 1024 <= int(match[1]) <= 65535 or int(match[1]) in ports:
            raise Refusal(mid + ': distinct loopback API port required')
        ports.add(int(match[1]))
        if not row['prepared']:
            if not row.get('block_reason'):
                raise Refusal(mid + ': incomplete bundle needs block_reason')
            continue
        if type(row.get('required_memory_bytes')) is not int or row['required_memory_bytes'] <= 8 * 1024**3:
            raise Refusal(mid + ': full memory requirement including reserve required')
        budget = row.get('memory_budget', {})
        required = ('resident_weights_bytes', 'cache_state_bytes', 'runtime_workspace_bytes',
                    'additional_loading_peak_bytes', 'safety_headroom_bytes')
        if any(type(budget.get(k)) is not int or budget[k] < 0 for k in required) or budget['safety_headroom_bytes'] < 8 * 1024**3:
            raise Refusal(mid + ': itemized budget with >=8 GiB headroom required')
        if sum(budget[k] for k in required) != row['required_memory_bytes'] or not context:
            raise Refusal(mid + ': memory budget/context inconsistent')
        artifacts = row.get('artifacts')
        if not isinstance(artifacts, list) or not artifacts:
            raise Refusal(mid + ': complete artifact table required')
        seen = set()
        for item in artifacts:
            p = Path(item.get('path', ''))
            if not p.is_absolute() or '..' in p.parts or str(p) in seen or type(item.get('size')) is not int or item['size'] < 1 or not HEX64.fullmatch(item.get('sha256', '')):
                raise Refusal(mid + ': invalid artifact entry')
            seen.add(str(p))
        runtime = row.get('runtime', {})
        image = runtime.get('image', '')
        pinned_repository = re.fullmatch(r'[a-zA-Z0-9][a-zA-Z0-9_./:-]*@sha256:[a-f0-9]{64}', image)
        pinned_local = re.fullmatch(r'sha256:[a-f0-9]{64}', image) and image == runtime.get('image_id')
        if not (pinned_repository or pinned_local) or not re.fullmatch(r'sha256:[a-f0-9]{64}', runtime.get('image_id', '')):
            raise Refusal(mid + ': local exact image ID and pinned digest required')
        args = runtime.get('argv')
        if not isinstance(args, list) or not args or len(args) > 128 or any(not isinstance(x, str) or not x or '\x00' in x for x in args):
            raise Refusal(mid + ': runtime argv array required')
        # Executed inside unprivileged readonly container, never by root host shell.
        if not args[0].startswith('/') or any(x in {'--trust-remote-code', '--cpu-offload-gb'} for x in args):
            raise Refusal(mid + ': absolute audited entrypoint/no CPU offload required')
        expected_binary = 'vllm' if row['engine'] == 'vLLM' else 'llama-server'
        if Path(args[0]).name != expected_binary or (expected_binary == 'vllm' and (len(args) < 2 or args[1] != 'serve')):
            raise Refusal(mid + ': exact mapped runtime entrypoint required')
        alias_flag = '--served-model-name' if expected_binary == 'vllm' else '--alias'
        if argument_value(args, alias_flag) != mid or argument_value(args, '--port') != match[1] or argument_value(args, '--host') != '127.0.0.1':
            raise Refusal(mid + ': argv API model alias/loopback endpoint differs from manifest')
        env = runtime.get('env', {})
        if not isinstance(env, dict) or not set(env).issubset(ENV_KEYS) or any(not isinstance(v, str) or '\x00' in v or '\n' in v for v in env.values()):
            raise Refusal(mid + ': unsupported environment key/value')
        for key, default in [('shm_bytes', 2 * 1024**3), ('tmpfs_bytes', 2 * 1024**3)]:
            value = runtime.get(key, default)
            if type(value) is not int or not 64 * 1024**2 <= value <= 8 * 1024**3:
                raise Refusal(mid + ': bounded cache/shm cap required')
        mounts = runtime.get('mounts')
        if not isinstance(mounts, list) or not mounts:
            raise Refusal(mid + ': explicit readonly artifact mounts required')
        for mount in mounts:
            source = Path(mount.get('source', ''))
            target = Path(mount.get('target', ''))
            if not source.is_absolute() or not target.is_absolute() or '..' in source.parts or '..' in target.parts or mount.get('readonly') is not True:
                raise Refusal(mid + ': invalid readonly mount')
            if str(source) in {'/', '/proc', '/sys', '/dev', '/run', '/var/run', '/etc', '/root', '/home/Dest'} or 'docker.sock' in str(source) or ',' in str(source) or ',' in str(target):
                raise Refusal(mid + ': unsafe mount')
        for art in artifacts:
            if not any(Path(art['path']).is_relative_to(Path(m['source'])) for m in mounts):
                raise Refusal(mid + ': artifact is not in a readonly mount')
        for key, default, lower, upper in [('startup_timeout_seconds', 1800, 1, 7200), ('stop_timeout_seconds', 90, 1, 600)]:
            value = row.get(key, default)
            if type(value) not in (int, float) or not lower <= value <= upper:
                raise Refusal(mid + ': invalid bounded timeout')
    return data


def argument_value(args, key):
    found = []
    for index, item in enumerate(args):
        if item == key:
            if index + 1 >= len(args):
                raise Refusal('Missing runtime argument value: ' + key)
            found.append(args[index + 1])
        elif item.startswith(key + '='):
            found.append(item[len(key) + 1:])
    if len(found) != 1:
        raise Refusal('Exactly one fixed runtime argument required: ' + key)
    return found[0]


def load_production_manifest():
    protect(Path(__file__).resolve())
    protect(MANIFEST)
    if MANIFEST.stat().st_size > 2 * 1024**2:
        raise Refusal('Manifest too large')
    return validate_manifest(json.loads(MANIFEST.read_text()))


def proc_birth(pid):
    """PID plus kernel start ticks, robust to spaces/parentheses in comm."""
    if type(pid) is not int or pid < 1:
        return None
    try:
        text = Path('/proc', str(pid), 'stat').read_text()
        tail = text[text.rindex(')') + 2:].split()
        if tail[0] == 'Z':
            return None
        return tail[19]
    except FileNotFoundError:
        return None
    except (OSError, ValueError, IndexError) as exc:
        raise ObservationError('Cannot observe process birth: ' + str(exc))


class Command:
    def __call__(self, argv, timeout=0.65):
        try:
            result = subprocess.run(argv, stdin=subprocess.DEVNULL, stdout=subprocess.PIPE,
                                    stderr=subprocess.PIPE, env=BASE_ENV, cwd='/',
                                    timeout=timeout, check=False, close_fds=True)
        except (OSError, subprocess.TimeoutExpired) as exc:
            raise ObservationError(type(exc).__name__ + ': bounded command failed')
        if len(result.stdout) > 1024**2 or len(result.stderr) > 65536:
            raise ObservationError('Command output exceeds bound')
        if result.returncode:
            # No raw argv/env/error output; may contain credentials in unrelated services.
            raise ObservationError('Command failed rc=' + str(result.returncode) + ' (' + Path(argv[0]).name + ')')
        return result.stdout.decode('utf-8', 'replace')


class NoRedirect(urllib.request.HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        # Readiness belongs to the configured exact endpoint, never another
        # address reached through a redirect with a coincidentally matching alias.
        return None


@contextlib.contextmanager
def total_api_deadline(seconds=.45):
    """One-shot hard request deadline, including drip-fed headers and bodies.

Production helper is a standalone main-thread CLI. A direct threaded importer
must fail closed rather than perform an unbounded socket probe. This is a
disposable request timeout, not a periodic timer/watch service.
"""
    if threading.current_thread() is not threading.main_thread() or signal.getitimer(signal.ITIMER_REAL) != (0.0, 0.0):
        raise ObservationError('Cannot install absolute API request deadline')
    previous = signal.getsignal(signal.SIGALRM)
    def expired(signum, frame):
        raise TimeoutError('Absolute API request deadline expired')
    signal.signal(signal.SIGALRM, expired)
    signal.setitimer(signal.ITIMER_REAL, seconds)
    try:
        yield
    finally:
        signal.setitimer(signal.ITIMER_REAL, 0)
        signal.signal(signal.SIGALRM, previous)


class SystemAdapter:
    """Audited read-only status + exact disabled-unit/container actions."""
    def __init__(self, manifest, command=None):
        self.manifest = manifest
        self.command = command or Command()
        self.http = urllib.request.build_opener(urllib.request.ProxyHandler({}), NoRedirect())

    def unit(self, mid, run_id):
        raw = self.command(['/usr/bin/systemctl', 'show', unit_name(mid, run_id),
                            '--property=LoadState,ActiveState,SubState,InvocationID,MainPID,ControlGroup'], .55)
        result = dict(line.split('=', 1) for line in raw.splitlines() if '=' in line)
        if result.get('LoadState') not in {'loaded', 'not-found'}:
            raise ObservationError('Cannot determine exact service identity')
        return result

    def validate_run_definition(self, row, run):
        if row['id'] in IDS[5:]:
            return legacy_support().effective_unit(self, row, run)
        # An instance can have drop-ins that do not appear on its template.
        raw = self.command(['/usr/bin/systemctl', 'show', unit_name(row['id'], run['run_id']),
            '--property=FragmentPath,DropInPaths,NeedDaemonReload'], .55)
        effective = dict(line.split('=', 1) for line in raw.splitlines() if '=' in line)
        expected = str(Path('/etc/systemd/system') / unit_name(row['id']))
        if (effective.get('FragmentPath') != expected or effective.get('DropInPaths')
                or effective.get('NeedDaemonReload') != 'no'):
            raise Refusal('Unapproved exact-instance unit override/reload drift')

    def inspect_container(self, cid):
        if not HEX64.fullmatch(cid or ''):
            raise ObservationError('Invalid recorded full container ID')
        # Not-found is ambiguous (deletion/replacement); never quietly means STOPPED.
        text = self.command(['/usr/bin/docker', 'inspect', '--type', 'container', cid], .65)
        try:
            rows = json.loads(text)
            if len(rows) != 1 or rows[0]['Id'] != cid:
                raise ValueError('wrong identity')
            return rows[0]
        except (ValueError, KeyError, TypeError) as exc:
            raise ObservationError('Invalid exact container observation') from exc

    def observe(self, row, run):
        if row['id'] in IDS[5:]:
            return legacy_support().observe(self, row, run)
        unit = self.unit(row['id'], run['run_id'])
        if not {'LoadState', 'ActiveState', 'InvocationID', 'MainPID', 'ControlGroup'} <= unit.keys():
            raise ObservationError('Exact supervisor release evidence incomplete')
        active_unit = unit.get('ActiveState') in {'active', 'activating', 'deactivating', 'reloading'}
        owner = run.get('owner')
        if owner and active_unit:
            pid = int(unit.get('MainPID') or '0')
            if unit.get('InvocationID') != owner.get('invocation_id') or pid != owner.get('pid') or proc_birth(pid) != owner.get('birth'):
                raise ObservationError('Service invocation/PID birth replaced; refuse ownership')
        elif active_unit and not owner:
            # Worker cannot be READY until it durably captures exact invocation.
            if not unit.get('InvocationID'):
                raise ObservationError('Active supervisor identity unavailable')
        cid = run.get('container_id')
        container_active = False
        engine_pid = 0
        if cid:
            container = self.inspect_container(cid)
            labels = container.get('Config', {}).get('Labels', {}) or {}
            if labels.get('spark.v4.model') != row['id'] or labels.get('spark.v4.run') != run['run_id'] or labels.get('spark.v4.generation') != str(run['generation']) or labels.get('spark.v4.attempt') != run['attempt'] or container.get('Image') != row['runtime']['image_id']:
                raise ObservationError('Container labels/image/run do not match exact ownership')
            if run.get('container_created') and container.get('Created') != run['container_created']:
                raise ObservationError('Container creation identity replaced')
            container_active = container.get('State', {}).get('Running') is True
            engine_pid = int(container.get('State', {}).get('Pid') or 0)
            if container_active:
                birth = proc_birth(engine_pid)
                if not birth:
                    raise ObservationError('Container process birth unavailable')
                if run.get('engine_pid') and (engine_pid != run['engine_pid'] or birth != run.get('engine_birth')):
                    raise ObservationError('Container PID birth replaced')
        return {'active': active_unit or container_active, 'engine_active': container_active,
                'supervisor_active': active_unit, 'engine_pid': engine_pid}

    def idle(self, row):
        if row['id'] in IDS[5:]:
            return legacy_support().idle(self, row)
        raw = self.command(['/usr/bin/systemctl', 'list-units', '--all', '--plain', '--no-legend',
                            '--no-pager', '--state=active,activating,deactivating,reloading',
                            'spark-v4-' + row['id'] + '@*.service'], .4)
        if raw.strip():
            raise ObservationError('Unrecorded active supervisor; cannot claim ownership')
        containers = self.command(['/usr/bin/docker', 'ps', '--no-trunc', '--quiet',
                                  '--filter', 'label=spark.v4.model=' + row['id']], .65)
        if containers.strip():
            raise ObservationError('Unrecorded active profile container; cannot prove idle')

    def api_ready(self, row):
        try:
            with total_api_deadline(.45):
                return self._api_ready(row)
        except TimeoutError as exc:
            raise ObservationError('API identity absolute request deadline expired') from exc

    def _api_ready(self, row):
        headers = {'Accept': 'application/json'}
        if row['id'] in IDS[5:]:
            headers['Authorization'] = 'Bearer ' + legacy_support().read_secret()
        req = urllib.request.Request(row['endpoint'] + '/models', headers=headers)
        try:
            if row['id'] in IDS[5:]:
                health = urllib.request.Request(row['endpoint'].removesuffix('/v1') + '/health', headers=headers)
                with self.http.open(health, timeout=.45) as response:
                    health_body = response.read(4097)
                    if len(health_body) > 4096:
                        raise ObservationError('Legacy passive health body bound')
                    if response.status != 200:
                        return False, 'Legacy engine health is not ready'
                    if health_body.strip() not in {b'', b'{}'}:
                        raise ObservationError('Legacy passive health body differs from approved schema')
            with self.http.open(req, timeout=.45) as response:
                if response.status != 200:
                    return False, 'API is not ready'
                raw = response.read(65537)
                if len(raw) > 65536:
                    raise ObservationError('API identity exceeds bound')
                data = json.loads(raw)
            names = [x.get('id') for x in data.get('data', []) if isinstance(x, dict)]
            if names != [row.get('runtime', {}).get('served_model_alias', row['id'])]:
                raise ObservationError('API returned a foreign/multiple model identity')
            return True, 'Exact owned engine and API model ID verified'
        except urllib.error.HTTPError as exc:
            code = exc.code
            exc.close()
            if code == 503:
                return False, 'Engine reports loading (503)'
            raise ObservationError('API identity HTTP error ' + str(code))
        except urllib.error.URLError as exc:
            if isinstance(exc.reason, (TimeoutError, socket.timeout)):
                raise ObservationError('API identity probe timed out') from exc
            if isinstance(exc.reason, ConnectionRefusedError):
                return False, 'API not yet reachable'
            raise ObservationError('API identity probe failed: ' + type(exc.reason).__name__) from exc
        except TimeoutError as exc:
            raise ObservationError('API identity probe timed out') from exc
        except ConnectionError:
            return False, 'API not yet reachable'
        except (ValueError, TypeError, AttributeError) as exc:
            raise ObservationError('Invalid API identity JSON') from exc

    def verify_bundle(self, row):
        if row['id'] in IDS[5:]:
            return legacy_support().verify_bundle(self, row)
        witnesses = []
        for item in row['artifacts']:
            path = Path(item['path'])
            # Artifact contents are never executed on host. Frozen stats rechecked at handoff.
            fd = os.open(path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(fd, 'rb') as stream:
                before = os.fstat(stream.fileno())
                if not stat.S_ISREG(before.st_mode) or before.st_size != item['size']:
                    raise Refusal('Artifact missing/wrong size: ' + path.name)
                digest = hashlib.file_digest(stream, 'sha256').hexdigest() if hasattr(hashlib, 'file_digest') else self._hash(stream)
                after = os.fstat(stream.fileno())
            witness = (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns)
            if digest != item['sha256'] or witness != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns):
                raise Refusal('Artifact checksum/change refused: ' + path.name)
            witnesses.append((path, witness))
        runtime = row['runtime']
        image = json.loads(self.command(['/usr/bin/docker', 'image', 'inspect', runtime['image']], 2.0))
        if len(image) != 1 or image[0].get('Id') != runtime['image_id'] or image[0].get('Architecture') != 'arm64' or image[0].get('Os') != 'linux':
            raise Refusal('Pinned local image ID/linux/arm64 mismatch; no pull attempted')
        return witnesses

    @staticmethod
    def _hash(stream):
        digest = hashlib.sha256()
        for block in iter(lambda: stream.read(8 * 1024**2), b''):
            digest.update(block)
        return digest.hexdigest()

    def recheck_bundle(self, witnesses):
        for path, witness in witnesses:
            st = path.stat(follow_symlinks=False)
            if witness != (st.st_dev, st.st_ino, st.st_size, st.st_mtime_ns, st.st_ctime_ns):
                raise Refusal('Artifact changed after verification: ' + path.name)

    def gpu_pids(self):
        raw = self.command(['/usr/bin/nvidia-smi', '--query-compute-apps=pid', '--format=csv,noheader,nounits'], .8)
        result = []
        for line in raw.splitlines():
            if line.strip() and not line.strip().isdigit():
                raise ObservationError('Cannot classify all GPU workloads')
            if line.strip():
                result.append(int(line.strip()))
        return result

    def foreign_gpu_containers(self):
        ids = self.command(['/usr/bin/docker', 'ps', '--no-trunc', '--quiet'], .65).split()
        if not ids:
            return []
        if len(ids) > 128 or len(set(ids)) != len(ids) or any(not HEX64.fullmatch(cid) for cid in ids):
            raise ObservationError('Cannot classify all running container identities')
        # Docker's Go template emits only a boolean marker for NVIDIA env;
        # no raw Config.Env values, private prompts, or credentials leave inspect.
        projection = ('{{json .Id}}\t{{json .HostConfig.DeviceRequests}}\t{{json .HostConfig.Runtime}}\t'
                      '{{range .Config.Env}}{{if and (ge (len .) 23) (eq (slice . 0 23) "NVIDIA_VISIBLE_DEVICES=")}}'
                      '{{if and (ne . "NVIDIA_VISIBLE_DEVICES=none") (ne . "NVIDIA_VISIBLE_DEVICES=void") (ne . "NVIDIA_VISIBLE_DEVICES=")}}GPU{{end}}{{end}}{{end}}\t'
                      '{{json .HostConfig.Devices}}\t{{json .HostConfig.Privileged}}')
        raw = self.command(['/usr/bin/docker', 'inspect', '--type', 'container', '--format', projection, *ids], .65)
        seen, capable = set(), []
        try:
            for line in raw.splitlines():
                fields = line.split('\t')
                if len(fields) != 6:
                    raise ValueError('invalid projection')
                cid, requests, runtime = (json.loads(x) for x in fields[:3])
                devices, privileged = (json.loads(x) for x in fields[4:])
                if cid not in ids or cid in seen or type(privileged) is not bool or not isinstance(runtime, str):
                    raise ValueError('invalid container identity')
                seen.add(cid)
                if requests is not None and (not isinstance(requests, list) or any(not isinstance(x, dict) for x in requests)):
                    raise ValueError('invalid device requests')
                for request in (requests or []):
                    caps = request.get('Capabilities')
                    if not isinstance(caps, list) or any(not isinstance(group, list) or any(not isinstance(cap, str) for cap in group) for group in caps):
                        raise ValueError('unclassified device request capabilities')
                if devices is not None and (not isinstance(devices, list) or any(not isinstance(x, dict) for x in devices)):
                    raise ValueError('invalid devices')
                gpu_requests = any(r.get('Driver') == 'nvidia' or any('gpu' in cap for cap in r.get('Capabilities', [])) for r in (requests or []))
                gpu_devices = any(str(d.get('PathOnHost', '')).startswith(('/dev/nvidia', '/dev/dri/')) for d in (devices or []))
                if fields[3] not in {'', 'GPU', 'GPUGPU'}:
                    raise ValueError('invalid NVIDIA env marker')
                if gpu_requests or runtime == 'nvidia' or fields[3] or gpu_devices or privileged:
                    capable.append(cid)
        except (ValueError, TypeError, AttributeError) as exc:
            raise ObservationError('Cannot classify running GPU-capable containers') from exc
        if seen != set(ids):
            raise ObservationError('Not every running container was inspected')
        return capable

    def preflight(self, row):
        units = [unit for unit in self.manifest['excluded_units']
                 if not (row['id'] == IDS[5] and unit == 'spark-sglang.service')]
        if units:
            raw = self.command(['/usr/bin/systemctl', 'show', *units, '--property=Id,ActiveState,LoadState'], .8)
            seen = set()
            for block in raw.strip().split('\n\n'):
                item = dict(line.split('=', 1) for line in block.splitlines() if '=' in line)
                if item.get('Id') not in units or item.get('LoadState') not in {'loaded', 'not-found'}:
                    raise Refusal('Excluded baseline/GPT identity cannot be observed')
                seen.add(item['Id'])
                if item.get('ActiveState') in {'active', 'activating', 'reloading', 'deactivating'}:
                    raise Refusal('Foreign baseline/GPT active: ' + item['Id'] + '; release it separately')
            if seen != set(units):
                raise Refusal('Not all excluded services were observed')
        if self.foreign_gpu_containers():
            raise Refusal('Foreign running GPU-capable container (including CPU preload); no container stopped')
        if self.gpu_pids():
            raise Refusal('Foreign GPU workload active; no signals sent')
        port = int(row['endpoint'].split(':')[2].split('/')[0])
        with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as sock:
            sock.settimeout(.1)
            try:
                sock.bind(('127.0.0.1', port))
            except OSError as exc:
                raise Refusal('Selected API port is occupied: ' + str(port)) from exc
        memory = dict((line.split(':')[0], int(line.split()[1]) * 1024)
                      for line in Path('/proc/meminfo').read_text().splitlines()
                      if line.startswith(('MemTotal:', 'MemAvailable:')))
        gpu_raw = self.command(['/usr/bin/nvidia-smi', '--query-gpu=memory.free', '--format=csv,noheader,nounits'], .8)
        try:
            # GB10 may report N/A for unified memory. Never add host/GPU pools.
            cuda_available = memory['MemAvailable'] if gpu_raw.strip() in {'N/A', '[N/A]'} else int(gpu_raw.strip()) * 1024**2
        except ValueError as exc:
            raise Refusal('CUDA/shared free memory cannot be classified') from exc
        available = min(memory['MemAvailable'], cuda_available)
        if row.get('required_memory_bytes') is not None and available < row['required_memory_bytes']:
            raise Refusal('Insufficient shared/CUDA-addressable free memory: ' + str(available) + ' < ' + str(row['required_memory_bytes']))

    def handoff(self, row, run):
        # Disabled unit, Restart=no. Never enable, restart, image pull, or start baseline.
        self.validate_run_definition(row, run)
        self.command(['/usr/bin/systemctl', 'start', '--no-block', unit_name(row['id'], run['run_id'])], 2.0)

    def terminate(self, row, run):
        if row['id'] in IDS[5:]:
            return legacy_support().request_terminate(self, row, run)
        observation = self.observe(row, run)  # Verify before each effect.
        if run.get('container_id') and observation['engine_active']:
            self.command(['/usr/bin/docker', 'stop', '--time', '20', run['container_id']], 25.0)
        # Container stop can take seconds; revalidate service immediately before
        # its separate signal instead of reusing the preceding observation.
        observation = self.observe(row, run)
        if observation['supervisor_active']:
            # observe() verified the exact invocation; a noncaptured STARTING worker
            # handles cancellation itself rather than signaling an unknown unit.
            if run.get('owner'):
                self.command(['/usr/bin/systemctl', 'stop', '--no-block', unit_name(row['id'], run['run_id'])], 2.0)

    def request_terminate(self, row, run):
        if row['id'] in IDS[5:]:
            return legacy_support().request_terminate(self, row, run)
        """Bounded bridge effect. The existing independent supervisor owns shutdown.

        A STARTING worker without captured owner sees its durable cancellation before
        any docker start. An orphan active engine is refused, never silently adopted.
        Existing CLI's broader observed stop/recovery implementation is preserved.
        """
        self.validate_run_definition(row, run)
        observation = self.observe(row, run)
        if observation['supervisor_active'] and run.get('owner'):
            self.command(['/usr/bin/systemctl', 'stop', '--no-block', unit_name(row['id'], run['run_id'])], 2.0)
        elif observation['engine_active']:
            raise Refusal('orphan_engine_requires_existing_local_recovery: no owned supervisor')

    def force(self, row, run):
        observation = self.observe(row, run)
        if observation['engine_active'] and run.get('container_id'):
            self.command(['/usr/bin/docker', 'kill', '--signal=KILL', run['container_id']], 2.0)
        observation = self.observe(row, run)
        if observation['supervisor_active'] and run.get('owner'):
            self.command(['/usr/bin/systemctl', 'kill', '--kill-whom=all', '--signal=KILL', unit_name(row['id'], run['run_id'])], 2.0)

    def resources_released(self, row, run):
        if row['id'] in IDS[5:]:
            return legacy_support().resources_released(self, row, run)
        if not Path('/proc').is_dir():
            raise ObservationError('Process resource namespace unavailable')
        if (run.get('container_id') or run.get('container_create_attempted')) and not run.get('owner'):
            raise ObservationError('Created engine lacks captured supervisor identity')
        if run.get('container_create_attempted') and not run.get('container_id'):
            raise ObservationError('Container create outcome unknown; no release proof')
        if run.get('container_id') and not run.get('container_created'):
            raise ObservationError('Container creation identity unavailable for release proof')
        observation = self.observe(row, run)
        if observation['active']:
            return False
        # Inactive labels alone do not account for escaped/remaining processes.
        # Validate the stopped invocation too; PID reuse is ambiguous, not release.
        unit = self.unit(row['id'], run['run_id'])
        if unit.get('ActiveState') not in {'inactive', 'failed'}:
            raise ObservationError('Exact supervisor inactivity cannot be classified')
        owner = run.get('owner')
        if owner:
            if (owner.get('unit') != unit_name(row['id'], run['run_id'])
                    or not isinstance(owner.get('invocation_id'), str) or not owner['invocation_id']):
                raise ObservationError('Recorded owner unit differs from exact run')
            invocation = unit.get('InvocationID')
            if invocation and invocation != owner.get('invocation_id'):
                raise ObservationError('Stopped supervisor invocation replaced')
            if int(unit.get('MainPID') or 0):
                raise ObservationError('Inactive supervisor still reports a PID')
            if not self.process_released(owner.get('pid'), owner.get('birth')):
                return False
        if run.get('engine_pid'):
            if not self.process_released(run['engine_pid'], run.get('engine_birth')):
                return False
        cid = run.get('container_id')
        if cid:
            container = self.inspect_container(cid)
            state = container.get('State', {})
            if (type(state.get('Running')) is not bool or type(state.get('Pid')) is not int
                    or state['Pid'] != 0 or not isinstance(state.get('StartedAt'), str) or not state['StartedAt']):
                raise ObservationError('Stopped container process state unavailable')
            if (state['StartedAt'] != '0001-01-01T00:00:00Z'
                    and not (run.get('engine_pid') and run.get('engine_birth'))):
                raise ObservationError('Previously started engine lacks pinned PID birth')
            # V4 creates without a custom cgroup parent. Reject a changed layout;
            # both supported Docker drivers retain their exact full-ID cgroup.
            if container.get('HostConfig', {}).get('CgroupParent'):
                raise ObservationError('Unapproved container cgroup parent')
        group = unit.get('ControlGroup')
        if group:
            parts = self.cgroup_parts(group)
            if not parts or parts[-1] != unit_name(row['id'], run['run_id']):
                raise ObservationError('Stopped supervisor cgroup replaced')
            if not self.cgroup_released(group):
                return False
        names = {unit_name(row['id'], run['run_id'])}
        if cid:
            names.update({cid, 'docker-' + cid + '.scope'})
        # A stopped manager can clear ControlGroup. Exhaustively bounded cgroup-v2
        # enumeration still finds exact descendant groups even without that label.
        if not self.owned_cgroups_released(names):
            return False
        # Query all GPU PIDs, but release is scoped to this exact unit/container.
        # A legitimate different profile's GPU work must not veto an inactive stop.
        pids = self.gpu_pids()
        if len(pids) > 128 or len(set(pids)) != len(pids):
            raise ObservationError('GPU resource observation bound exceeded')
        exact_unit = unit_name(row['id'], run['run_id'])
        for pid in pids:
            groups = self.process_cgroups(pid)
            if groups is None:  # Process exited during the bounded observation.
                continue
            for path in groups:
                parts = self.cgroup_parts(path)
                if exact_unit in parts or (cid and any(x in {cid, 'docker-' + cid + '.scope'} for x in parts)):
                    return False
        if self.observe(row, run) != observation or self.unit(row['id'], run['run_id']) != unit:
            raise ObservationError('Physical ownership changed during resource observation')
        return True

    @staticmethod
    def process_released(pid, birth):
        if type(pid) is not int or pid <= 0 or not isinstance(birth, str) or not birth:
            raise ObservationError('Recorded PID birth unavailable for release proof')
        try:
            raw = Path('/proc') / str(pid) / 'stat'
            text = raw.read_text()
            observed_birth = text.rsplit(')', 1)[1].split()[19]
        except FileNotFoundError:
            if raw.parent.exists():
                raise ObservationError('Recorded process exists but PID birth is unavailable')
            return True
        except (OSError, IndexError, ValueError) as exc:
            raise ObservationError('PID birth resource observation unavailable') from exc
        if observed_birth != birth:
            raise ObservationError('Recorded PID reused; release identity ambiguous')
        return False

    @staticmethod
    def cgroup_parts(group):
        if not isinstance(group, str) or not group.startswith('/') or any(x in {'.', '..'} for x in group.split('/')):
            raise ObservationError('Invalid owned cgroup path')
        return tuple(x for x in group.split('/') if x)

    def cgroup_released(self, group):
        parts = self.cgroup_parts(group)
        path = Path('/sys/fs/cgroup').joinpath(*parts)
        try:
            raw = (path / 'cgroup.events').read_text()
        except FileNotFoundError:
            if path.exists():
                raise ObservationError('Owned cgroup exists but population is unavailable')
            return True
        except OSError as exc:
            raise ObservationError('Owned cgroup resource observation unavailable') from exc
        fields = dict(line.split() for line in raw.splitlines())
        if fields.get('populated') not in {'0', '1'}:
            raise ObservationError('Owned cgroup population unavailable')
        return fields['populated'] == '0'

    def owned_cgroups_released(self, names):
        root = Path('/sys/fs/cgroup')
        if not (root / 'cgroup.controllers').is_file():
            raise ObservationError('Unified cgroup resource observation unavailable')
        pending, count = [(root, 0)], 0
        try:
            while pending:
                path, depth = pending.pop()
                count += 1
                if count > 2048 or depth > 32:
                    raise ObservationError('Owned cgroup discovery bound exceeded')
                with os.scandir(path) as entries:
                    for entry in entries:
                        if entry.is_symlink():
                            raise ObservationError('Unexpected symlink in cgroup namespace')
                        if not entry.is_dir(follow_symlinks=False):
                            continue
                        child = Path(entry.path)
                        if entry.name in names and not self.cgroup_released('/' + str(child.relative_to(root))):
                            return False
                        pending.append((child, depth + 1))
        except FileNotFoundError as exc:
            # A disappearing unrelated subtree makes this observation incomplete;
            # retry belongs to a new bounded observation, never assumed zero.
            raise ObservationError('Cgroup namespace changed during release observation') from exc
        except OSError as exc:
            raise ObservationError('Cgroup namespace resource observation unavailable') from exc
        return True

    def process_cgroups(self, pid):
        if type(pid) is not int or pid <= 0:
            raise ObservationError('Invalid GPU PID')
        try:
            raw = (Path('/proc') / str(pid) / 'cgroup').read_text()
        except FileNotFoundError:
            if (Path('/proc') / str(pid)).exists():
                raise ObservationError('GPU process exists but cgroup ownership unavailable')
            return None
        except OSError as exc:
            raise ObservationError('GPU PID ownership observation unavailable') from exc
        try:
            groups = [line.split(':', 2)[2] for line in raw.splitlines()]
            if not groups:
                raise ValueError('empty process cgroups')
            for group in groups:
                self.cgroup_parts(group)
            return groups
        except (IndexError, ValueError) as exc:
            raise ObservationError('GPU PID cgroup ownership unavailable') from exc


class StateStore:
    def __init__(self, root, protected=True):
        self.root = Path(root)
        self.path = self.root / 'state.json'
        self.lock_path = self.root / 'collection.lock'
        self.protected = protected
        if protected:
            protect(self.root, directory=True)

    def read(self):
        try:
            if self.protected:
                protect(self.path)
            fd = os.open(self.path, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
            with os.fdopen(fd, 'rb') as f:
                if not stat.S_ISREG(os.fstat(f.fileno()).st_mode):
                    raise ObservationError('State is not regular')
                raw = f.read(STATE_MAX_BYTES + 1)
                if len(raw) > STATE_MAX_BYTES:
                    raise ObservationError('Durable state capacity exceeded')
                data = json.loads(raw)
            if data.get('schema_version') != 4 or not isinstance(data.get('runs'), dict) or not isinstance(data.get('counters'), dict):
                raise ObservationError('Invalid durable state schema')
            return data
        except FileNotFoundError:
            # Empty state is safe only at initial install; callers still audit units.
            return {'schema_version': 4, 'reservation': None, 'counters': {}, 'runs': {}}
        except (ValueError, TypeError, OSError) as exc:
            raise ObservationError('Durable state unreadable') from exc

    def write(self, data):
        # Reject oversized state before replacing the last readable durable file.
        encoded = (json.dumps(data, sort_keys=True, allow_nan=False) + '\n').encode()
        if len(encoded) > STATE_MAX_BYTES:
            raise Refusal('Durable state capacity exceeded; unresolved records preserved')
        if self.protected:
            protect(self.root, directory=True)
        tmp = self.root / ('.state-' + uuid.uuid4().hex)
        fd = os.open(tmp, os.O_WRONLY | os.O_CREAT | os.O_EXCL | os.O_NOFOLLOW, 0o600)
        try:
            with os.fdopen(fd, 'wb') as f:
                f.write(encoded); f.flush(); os.fsync(f.fileno())
            os.replace(tmp, self.path)
            directory = os.open(self.root, os.O_RDONLY | os.O_DIRECTORY)
            try:
                os.fsync(directory)
            finally:
                os.close(directory)
        finally:
            if tmp.exists():
                tmp.unlink()

    @contextlib.contextmanager
    def lock(self, timeout=3.0):
        if self.protected:
            protect(self.root, directory=True)
        fd = os.open(self.lock_path, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o600)
        try:
            if not stat.S_ISREG(os.fstat(fd).st_mode):
                raise Refusal('Collection lock is not regular')
            deadline = time.monotonic() + timeout
            while True:
                try:
                    fcntl.flock(fd, fcntl.LOCK_EX | fcntl.LOCK_NB)
                    break
                except BlockingIOError:
                    if time.monotonic() >= deadline:
                        raise Refusal('Collection transaction is busy; retry explicitly')
                    time.sleep(.01)
            yield
        finally:
            os.close(fd)  # Never unlink a collection lock.


class RotatingSink:
    """Open/write/close every chunk: rotation reopens stable path immediately."""
    def __init__(self, path, max_bytes=10 * 1024**2, backups=3, protected=True, gid=1000):
        self.path = Path(path)
        self.max_bytes, self.backups, self.protected, self.gid = max_bytes, backups, protected, gid

    def write(self, raw):
        if isinstance(raw, str):
            raw = raw.encode('utf-8', 'replace')
        if self.protected:
            protect(self.path.parent, directory=True)
        lock = self.path.parent / ('.' + self.path.name + '.lock')
        fd_lock = os.open(lock, os.O_RDWR | os.O_CREAT | os.O_NOFOLLOW, 0o600)
        try:
            fcntl.flock(fd_lock, fcntl.LOCK_EX)
            if self.path.exists() or self.path.is_symlink():
                st = self.path.lstat()
                if not stat.S_ISREG(st.st_mode) or (self.protected and (st.st_uid != 0 or st.st_mode & 0o022)):
                    raise Refusal('Unsafe log destination')
            for offset in range(0, len(raw), self.max_bytes):
                chunk = raw[offset:offset + self.max_bytes]
                if self.path.exists() and self.path.stat().st_size + len(chunk) > self.max_bytes:
                    self._rotate()
                fd = os.open(self.path, os.O_WRONLY | os.O_APPEND | os.O_CREAT | os.O_NOFOLLOW | os.O_NONBLOCK, 0o640)
                try:
                    if not stat.S_ISREG(os.fstat(fd).st_mode):
                        raise Refusal('Log is not a regular file')
                    if self.protected:
                        os.fchown(fd, 0, self.gid)
                    with os.fdopen(fd, 'ab', closefd=False) as stream:
                        stream.write(chunk); stream.flush()
                finally:
                    os.close(fd)
        finally:
            os.close(fd_lock)

    def _rotate(self):
        for index in range(self.backups, 0, -1):
            target = self.path.with_name(self.path.name + '.' + str(index))
            source = self.path if index == 1 else self.path.with_name(self.path.name + '.' + str(index - 1))
            if target.is_symlink() or source.is_symlink():
                raise Refusal('Symlink log rotation refused')
            if source.exists():
                os.replace(source, target)


class Controller:
    def __init__(self, manifest, adapter, store=None, log_root=None, protected=True):
        self.manifest, self.adapter = manifest, adapter
        self.rows = {x['id']: x for x in manifest['models']}
        self.store = store or StateStore(STATE_ROOT, protected)
        self.log_root = Path(log_root or LOG_ROOT)
        self.protected = protected
        self.wall_time = time.time
        self.monotonic_time = time.monotonic

    def profile_revision(self, mid):
        # Runtime PIDs, counters and readiness are absent from approved manifest rows.
        binding = {'model': self.row(mid), 'excluded_units': self.manifest.get('excluded_units', [])}
        if self.protected and mid in IDS[5:]:
            binding.update(legacy_support().profile_binding(self, self.row(mid)))
        elif self.protected:
            # systemd cannot load a bare template as a runtime unit. Inspect a
            # fixed inactive instance to resolve the configured template; the
            # actual run's overrides are checked separately before each effect.
            unit_policy = self.adapter.command(['/usr/bin/systemctl', 'show', unit_name(mid, '0' * 32),
                '--property=FragmentPath,DropInPaths,NeedDaemonReload'], .55)
            effective = dict(line.split('=', 1) for line in unit_policy.splitlines() if '=' in line)
            expected_fragment = str(Path('/etc/systemd/system') / unit_name(mid))
            if (effective.get('FragmentPath') != expected_fragment or effective.get('DropInPaths')
                    or effective.get('NeedDaemonReload') != 'no'):
                raise Refusal('Unapproved unit override/reload drift; local reapproval required')
            sources = (MANIFEST, PROJECT / 'bin/modelctl.py', PROJECT / 'bin/supervisor.py',
                       PROJECT / 'bin/monik_bridge.py', Path('/usr/local/sbin/spark-modelctl-v4'),
                       Path('/etc/systemd/system') / unit_name(mid))
            pins = {}
            for source in sources:
                protect(source)
                fd = os.open(source, os.O_RDONLY | os.O_NOFOLLOW | os.O_NONBLOCK)
                with os.fdopen(fd, 'rb') as stream:
                    before = os.fstat(stream.fileno())
                    if before.st_size > 2 * 1024**2:
                        raise Refusal('Effect source byte bound exceeded')
                    raw = stream.read(2 * 1024**2 + 1)
                    after = os.fstat(stream.fileno())
                if (before.st_dev, before.st_ino, before.st_size, before.st_mtime_ns, before.st_ctime_ns) != (after.st_dev, after.st_ino, after.st_size, after.st_mtime_ns, after.st_ctime_ns):
                    raise Refusal('Effect source changed while binding approval')
                pins[str(source)] = hashlib.sha256(raw).hexdigest()
            binding['protected_source_pins'] = pins
        return hashlib.sha256(json.dumps(binding, sort_keys=True, separators=(',', ':'),
                                         allow_nan=False).encode()).hexdigest()

    @staticmethod
    def captured_legacy_declaration(run):
        value = run.get('legacy_declaration_sha256') if run else None
        if value is None:
            return None
        if run.get('model_id') not in IDS[5:]:
            raise ObservationError('Captured legacy declaration is not valid for this profile')
        if not isinstance(value, str) or not HEX64.fullmatch(value):
            raise ObservationError('Invalid captured immutable legacy declaration')
        return value

    @staticmethod
    def instance_identity(run):
        if not run:
            return ''
        identity = {k: run.get(k) for k in
            ('run_id', 'owner', 'container_id', 'container_created', 'engine_pid', 'engine_birth')}
        declaration = Controller.captured_legacy_declaration(run)
        if declaration is not None:
            identity['legacy_declaration_sha256'] = declaration
        return hashlib.sha256(json.dumps(identity, sort_keys=True, separators=(',', ':'),
                                         allow_nan=False).encode()).hexdigest()

    def adopt_legacy(self, mid):
        return legacy_support().adopt(self, mid)

    def enable_bridge(self, agent_session, boot_id=None, *, agent_id=None, controller_id=None, control_epoch=None):
        """Explicit privileged local approval/session binding, never discovery or wire RPC.

        Existing CLI remains usable with the bridge disabled. Production callers must
        already be the authenticated local root broker/installer. This method alone
        grants no browser, sudo or socket access.
        """
        if not isinstance(agent_session, str) or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', agent_session):
            raise Refusal('Invalid locally authenticated agent session')
        if self.protected and (agent_id is None or controller_id is None or control_epoch is None):
            raise Refusal('Explicit fixed agent/controller approval and broker epoch are required')
        agent_id = agent_id or 'offline-agent'
        controller_id = controller_id or 'offline-controller'
        for value in (agent_id, controller_id):
            if not isinstance(value, str) or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', value):
                raise Refusal('Invalid fixed local identity approval')
        if control_epoch is not None and (not isinstance(control_epoch, str) or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', control_epoch)):
            raise Refusal('Invalid approved broker control epoch')
        boot_id = boot_id or Path('/proc/sys/kernel/random/boot_id').read_text().strip()
        if not re.fullmatch(r'[a-f0-9-]{32,36}', boot_id):
            raise Refusal('Invalid current boot identity')
        with self.store.lock():
            data = self.store.read()
            old = data.get('bridge_authority', {})
            authority = {'control_epoch': control_epoch or old.get('control_epoch') or uuid.uuid4().hex,
                         'agent_session': agent_session, 'boot_id': boot_id}
            data['bridge_authority'] = authority
            data['bridge_approval'] = {'agent_id': agent_id, 'controller_id': controller_id}
            self.store.write(data)
        return dict(authority)

    def bind_current_agent(self, binding):
        """Root broker session refresh after authenticated native worker restart.

        This never creates local approval or affects any already started model. The
        IDs must match the separately approved fixed local agent/controller pair.
        """
        keys = {'agent_id', 'controller_id', 'control_epoch', 'agent_session', 'boot_id'}
        if not isinstance(binding, dict) or set(binding) != keys or any(not isinstance(v, str)
                or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', v) for v in binding.values()):
            raise Refusal('Invalid fixed broker session binding')
        if binding['boot_id'] != Path('/proc/sys/kernel/random/boot_id').read_text().strip():
            raise Refusal('Broker session binding boot differs from current host')
        with self.store.lock():
            data = self.store.read()
            approved = data.get('bridge_approval')
            if not approved or any(binding[k] != approved.get(k) for k in ('agent_id', 'controller_id')):
                raise Refusal('Agent/controller pair has no explicit local approval')
            authority = {k: binding[k] for k in ('control_epoch', 'agent_session', 'boot_id')}
            if authority == data.get('bridge_authority'):
                return dict(authority)
            for operation in data.get('bridge_operations', {}).values():
                if operation['receipt']['phase'] == 'prepared':
                    operation['execution_forbidden'] = True
                    operation['receipt'].update(phase='unresolved', stage='prepared',
                        result={'reason': 'authority_replaced_before_effect', 'effect_invoked': False})
            data['bridge_authority'] = authority
            self.store.write(data)
        return dict(authority)

    def bridge_snapshot(self, mid):
        data = self.store.read()
        row = self.row(mid)
        run = data['runs'].get(mid)
        revision = self.profile_revision(mid)
        snap = self._observed_snapshot(row, data, run)
        if not snap['process_active'] and snap['state'] in {'STOPPED', 'BLOCKED', 'FAILED'}:
            proof = self._shutdown_witness(row, data, run)
            if proof is not None:
                snap.update(shutdown_verified=True, shutdown_witness=proof)
        self._recheck_observed_state(mid, data, revision)
        snap.update(profile_revision=revision,
                    instance_identity=self.instance_identity(run),
                    generation=run['generation'] if run else data['counters'].get(mid, 0),
                    observed_at_ms=int(self.wall_time() * 1000),
                    **data.get('bridge_authority', {}))
        # Exact supervisor/container evidence is local. Do not export raw state/env.
        snap['instance'] = ({k: run.get(k) for k in ('run_id', 'owner', 'container_id',
                            'container_created', 'engine_pid', 'engine_birth')} if run else None)
        declaration = self.captured_legacy_declaration(run)
        if declaration is not None:
            snap['instance']['legacy_declaration_sha256'] = declaration
        return snap

    def _recheck_observed_state(self, mid, data, revision=None):
        after = self.store.read()
        if (data['runs'].get(mid) != after['runs'].get(mid)
                or data['counters'].get(mid, 0) != after['counters'].get(mid, 0)
                or data['reservation'] != after['reservation']
                or data.get('bridge_authority') != after.get('bridge_authority')
                or (revision is not None and revision != self.profile_revision(mid))):
            raise ObservationError('Runtime state/generation/instance changed during bounded observation')

    def _shutdown_witness(self, row, data, run):
        """Bracket exact physical release evidence with unchanged durable identity.

        This returns no certificate from STOPPED/FAILED labels alone. No-run is a
        separate bounded idle observation, not proof for a historical instance.
        """
        self._recheck_observed_state(row['id'], data)
        if run is None:
            if data['reservation'] == row['id']:
                raise ObservationError('Reservation exists without an owned run')
            self.adapter.idle(row)
            kind = 'no_owned_run'
        else:
            # An inactive observation cannot revoke an outstanding worker's
            # durable launch permission. Even an old STARTING label may start late.
            if run['state'] == 'STARTING' and not run.get('cancelled'):
                return None
            before = self.adapter.observe(row, run)
            if before['active'] or not self.adapter.resources_released(row, run):
                return None
            if self.adapter.observe(row, run) != before:
                raise ObservationError('Exact resources changed during shutdown proof')
            kind = 'exact_run_resources_released'
        self._recheck_observed_state(row['id'], data)
        witness = {'kind': kind, 'model_id': row['id'],
                'run_id': run.get('run_id') if run else None,
                'generation': run['generation'] if run else data['counters'].get(row['id'], 0),
                'attempt': run.get('attempt') if run else None,
                'instance_identity': self.instance_identity(run),
                'owner': run.get('owner') if run else None,
                'container_id': run.get('container_id') if run else None,
                'container_created': run.get('container_created') if run else None,
                'engine_pid': run.get('engine_pid') if run else None,
                'engine_birth': run.get('engine_birth') if run else None}
        declaration = self.captured_legacy_declaration(run)
        if declaration is not None:
            witness['legacy_declaration_sha256'] = declaration
        return witness

    def prepare_bundle(self, mid):
        """Explicit install/approval preparation; full original integrity check, no launch.

        The seal can only be used when every exact stat witness remains unchanged.
        Changed/missing files fail closed and need fresh full preparation. Monitoring
        never invokes this method. Existing local start still does its original hash.
        """
        row = self.row(mid)
        if not row['prepared']:
            raise Refusal('Bundle is not prepared')
        if self.protected and mid not in IDS[5:]:
            for artifact in row['artifacts']:
                protect(Path(artifact['path']))
            for mount in row['runtime']['mounts']:
                protect(Path(mount['source']), directory=True)
        witnesses = self.adapter.verify_bundle(row)
        with self.store.lock():
            data = self.store.read()
            self.adapter.recheck_bundle(witnesses)
            data.setdefault('bundle_seals', {})[mid] = {
                'profile_revision': self.profile_revision(mid),
                'witnesses': [[str(p), list(w)] for p, w in witnesses]}
            self.store.write(data)
        return {'profile_id': mid, 'profile_revision': self.profile_revision(mid),
                'artifacts_verified': len(witnesses)}

    def _sealed_bundle(self, row):
        seal = self.store.read().get('bundle_seals', {}).get(row['id'])
        if not seal or seal.get('profile_revision') != self.profile_revision(row['id']):
            raise Refusal('bundle_not_prepared: explicit local full-integrity approval required')
        witnesses = [(Path(p), tuple(w)) for p, w in seal['witnesses']]
        if not witnesses:
            raise Refusal('Empty sealed bundle refused')
        if self.protected:
            for path, _ in witnesses:
                protect(path)
            for mount in row['runtime']['mounts']:
                path = Path(mount['source'])
                protect(path, directory=path.is_dir() if row['id'] in IDS[5:] else True)
            # Original image identity check is preserved without weight rehashing.
            image = json.loads(self.adapter.command(['/usr/bin/docker', 'image', 'inspect', row['runtime']['image']], 2.0))
            if len(image) != 1 or image[0].get('Id') != row['runtime']['image_id'] or image[0].get('Architecture') != 'arm64' or image[0].get('Os') != 'linux':
                raise Refusal('Approved pinned local image identity changed')
        self.adapter.recheck_bundle(witnesses)
        return witnesses

    def _validate_typed(self, request):
        required = {'schema_version', 'operation_id', 'profile_id', 'action',
                    'profile_revision', 'expected_generation', 'expected_instance',
                    'control_epoch', 'agent_session', 'boot_id', 'issued_at_ms', 'deadline_ms'}
        if not isinstance(request, dict) or set(request) not in (required, required | {'parent_operation_id'}):
            raise Refusal('Unknown/missing typed request fields')
        if request['schema_version'] != 1 or type(request['schema_version']) is not int:
            raise Refusal('Unsupported typed request version')
        for key in ('operation_id', 'agent_session', 'control_epoch', 'boot_id'):
            if not isinstance(request[key], str) or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', request[key]):
                raise Refusal('Invalid typed identity ' + key)
        if request['profile_id'] not in IDS or request['action'] not in {'start', 'stop'}:
            raise Refusal('Only exact fixed profile start/stop stages are permitted')
        if (not isinstance(request['profile_revision'], str) or not HEX64.fullmatch(request['profile_revision'])
                or not isinstance(request['expected_instance'], str)
                or (request['expected_instance'] and not HEX64.fullmatch(request['expected_instance']))):
            raise Refusal('Invalid revision/instance identity')
        if type(request['expected_generation']) is not int or request['expected_generation'] < 0:
            raise Refusal('Invalid runtime generation')
        if any(type(request[k]) is not int for k in ('issued_at_ms', 'deadline_ms')):
            raise Refusal('Invalid typed dispatch times')
        if not 0 < request['deadline_ms'] - request['issued_at_ms'] <= 30000:
            raise Refusal('Dispatch lease must be at most 30 seconds')
        if 'parent_operation_id' in request and (not isinstance(request['parent_operation_id'], str)
                or not re.fullmatch(r'[A-Za-z0-9_.:-]{1,128}', request['parent_operation_id'])):
            raise Refusal('Invalid linked stage parent identity')
        encoded = json.dumps(request, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
        if len(encoded) > 4096:
            raise Refusal('Typed request size exceeded')
        return hashlib.sha256(encoded).hexdigest()

    def _check_typed_authority(self, data, request):
        authority = data.get('bridge_authority', {})
        if not authority or any(request[k] != authority.get(k) for k in ('control_epoch', 'agent_session', 'boot_id')):
            raise Refusal('Stale/unapproved local control authority')
        # Authenticated broker binds the transport session; boot is verified locally.
        if request['boot_id'] != Path('/proc/sys/kernel/random/boot_id').read_text().strip():
            raise Refusal('Boot changed; old command rejected')

    def _existing_receipt(self, data, request, digest, current_authority=None):
        self._check_typed_authority(data, request if current_authority is None else current_authority)
        operation = data.get('bridge_operations', {}).get(request['operation_id'])
        if not operation:
            return None
        if operation['digest'] != digest:
            raise Refusal('operation_id_conflict: immutable request digest differs')
        # No replay of attempted or ambiguous effects, including crash before call.
        receipt = dict(operation['receipt'])
        if receipt['phase'] in {'prepared', 'effect_attempted'}:
            receipt.update(stage=receipt['phase'], phase='unresolved')
        return receipt

    def _retention_state(self, data):
        state = data.get('bridge_retention')
        if state is None:
            return {'schema_version': 1, 'deadline_floor_ms': 0,
                    'retired_ids': '00' * BRIDGE_RETIRED_BYTES, 'retired_count': 0}
        if (not isinstance(state, dict) or set(state) != {
                'schema_version', 'deadline_floor_ms', 'retired_ids', 'retired_count'}
                or type(state['schema_version']) is not int or state['schema_version'] != 1
                or type(state['deadline_floor_ms']) is not int
                or not 0 <= state['deadline_floor_ms'] < 2**63
                or type(state['retired_count']) is not int
                or not 0 <= state['retired_count'] < 2**63
                or not isinstance(state['retired_ids'], str)
                or not re.fullmatch('[a-f0-9]{' + str(BRIDGE_RETIRED_BYTES * 2) + '}', state['retired_ids'])
                or (state['retired_count'] == 0) != (set(state['retired_ids']) <= {'0'})
                or (state['retired_count'] == 0) != (state['deadline_floor_ms'] == 0)):
            raise Refusal('Durable retention metadata invalid; records preserved')
        return state

    @staticmethod
    def _retired_bits(operation_id):
        # Domain separated, stable hashes. A Bloom false positive only refuses
        # a fresh ID; no false negative can permit a retired ID to execute again.
        raw = hashlib.sha256(b'monik-v4-retired-operation-v1\0' + operation_id.encode()).digest()
        return tuple(int.from_bytes(raw[i:i+8], 'big') % (BRIDGE_RETIRED_BYTES * 8)
                     for i in range(0, 32, 8))

    def _check_retired_request(self, data, request):
        state = self._retention_state(data)
        if request['deadline_ms'] <= state['deadline_floor_ms']:
            raise Refusal('Original dispatch expired before durable retention floor')
        retired = bytes.fromhex(state['retired_ids'])
        if all(retired[bit // 8] & (1 << (bit % 8)) for bit in self._retired_bits(request['operation_id'])):
            raise Refusal('Operation ID retired or bounded retention capacity reached; use a distinct ID')

    def _completed_retention_candidate(self, operation_id, operation, now_ms):
        # Validate the ENTIRE immutable record before any eligibility return.
        # Known optional evidence is checked, never silently ignored. Unknown,
        # malformed, ambiguous, unexpired and pending evidence is preserved.
        try:
            if not isinstance(operation, dict) or set(operation) != {'digest', 'request', 'receipt', 'bound_instance'}:
                return False
            request, receipt = operation['request'], operation['receipt']
            if self._validate_typed(request) != operation['digest'] or request['operation_id'] != operation_id:
                return False
            if request['issued_at_ms'] <= 0 or request['deadline_ms'] > now_ms:
                return False
            required = {'operation_id', 'profile_id', 'action', 'phase', 'run_id', 'generation', 'attempt', 'result'}
            if (not isinstance(receipt, dict) or set(receipt) != required
                    or receipt['phase'] != 'completed'
                    or any(receipt[k] != request[k] for k in ('operation_id', 'profile_id', 'action'))
                    or type(receipt['generation']) is not int or receipt['generation'] < 0
                    or receipt['run_id'] is not None and not re.fullmatch('[a-f0-9]{32}', receipt['run_id'])
                    or receipt['attempt'] is not None and not re.fullmatch('[a-f0-9]{32}', receipt['attempt'])
                    or (receipt['run_id'] is None) != (receipt['attempt'] is None)):
                return False
            bound = operation['bound_instance']
            if receipt['run_id'] is None:
                if bound is not None:
                    return False
            else:
                bound_keys = {'model_id', 'run_id', 'generation', 'attempt', 'owner',
                              'container_id', 'container_created', 'engine_pid', 'engine_birth'}
                optional_bound = {'legacy_declaration_sha256'} if receipt['profile_id'] in {
                    'qwen-existing-observation', 'gpt-oss-120b-existing-configured'} else set()
                if (not isinstance(bound, dict) or not bound_keys <= set(bound) <= bound_keys | optional_bound
                        or type(bound['generation']) is not int
                        or any(bound[k] != receipt[k] for k in ('run_id', 'generation', 'attempt'))
                        or bound['model_id'] != receipt['profile_id']
                        or 'legacy_declaration_sha256' in bound and (
                            not isinstance(bound['legacy_declaration_sha256'], str)
                            or HEX64.fullmatch(bound['legacy_declaration_sha256']) is None)):
                    return False
                owner = bound['owner']
                if owner is not None and (not isinstance(owner, dict)
                        or set(owner) != {'unit', 'invocation_id', 'pid', 'birth'}
                        or owner['unit'] != unit_name(receipt['profile_id'], receipt['run_id'])
                        or not isinstance(owner['invocation_id'], str)
                        or re.fullmatch('[a-f0-9]{32}', owner['invocation_id']) is None
                        or type(owner['pid']) is not int or owner['pid'] <= 0
                        or not isinstance(owner['birth'], str) or not owner['birth'].isdigit()):
                    return False
                if ((bound['container_id'] is None) != (bound['container_created'] is None)
                        or bound['container_id'] is not None and (not isinstance(bound['container_id'], str)
                            or HEX64.fullmatch(bound['container_id']) is None
                            or not isinstance(bound['container_created'], str) or not 1 <= len(bound['container_created']) <= 128)
                        or (bound['engine_pid'] is None) != (bound['engine_birth'] is None)
                        or bound['engine_pid'] is not None and (type(bound['engine_pid']) is not int or bound['engine_pid'] <= 0
                            or not isinstance(bound['engine_birth'], str) or not bound['engine_birth'].isdigit())):
                    return False
            result = receipt['result']
            if not isinstance(result, dict):
                return False
            if set(result) == {'reason', 'effect_invoked'}:
                # Bound-instance validation above also applies to no-effect rows.
                return result['reason'] == 'dispatch_expired' and result['effect_invoked'] is False
            required_result = {'model_id', 'state', 'process_active', 'api_ready', 'context',
                               'endpoint', 'engine_log', 'action_log', 'message',
                               'generation', 'attempt', 'run_id'}
            optional_result = {'instance_identity', 'shutdown_verified', 'shutdown_witness'}
            if (not required_result <= set(result) <= required_result | optional_result
                    or result['model_id'] != request['profile_id']
                    or result['state'] not in {'READY', 'STARTING', 'STOPPED', 'BLOCKED', 'FAILED'}
                    or type(result['process_active']) is not bool or type(result['api_ready']) is not bool
                    or type(result['generation']) is not int or result['generation'] != receipt['generation']
                    or result['run_id'] != receipt['run_id'] or result['attempt'] != receipt['attempt']
                    or result['context'] is not None and (type(result['context']) is not int or result['context'] < 1)
                    or not all(isinstance(result[k], str) for k in ('endpoint', 'engine_log', 'action_log', 'message'))
                    or len(result['message']) > 400 or len(result['endpoint']) > 512
                    or len(result['engine_log']) > 4096 or len(result['action_log']) > 4096):
                return False
            identity = self.instance_identity(bound)
            if 'instance_identity' in result and (not isinstance(result['instance_identity'], str)
                    or result['instance_identity'] != identity):
                return False
            if 'shutdown_verified' in result and type(result['shutdown_verified']) is not bool:
                return False
            verified = result.get('shutdown_verified', False)
            if verified != ('shutdown_witness' in result):
                return False
            if 'shutdown_witness' in result:
                witness = result['shutdown_witness']
                keys = {'kind', 'model_id', 'run_id', 'generation', 'attempt', 'instance_identity',
                        'owner', 'container_id', 'container_created', 'engine_pid', 'engine_birth'}
                optional_witness = {'legacy_declaration_sha256'} if receipt['profile_id'] in {
                    'qwen-existing-observation', 'gpt-oss-120b-existing-configured'} else set()
                if (not isinstance(witness, dict) or not keys <= set(witness) <= keys | optional_witness
                        or type(witness['generation']) is not int
                        or 'instance_identity' not in result
                        or any(witness[k] != result[k] for k in ('model_id', 'run_id', 'generation', 'attempt', 'instance_identity'))
                        or witness['instance_identity'] != identity):
                    return False
                if ('legacy_declaration_sha256' in witness and (
                        not isinstance(witness['legacy_declaration_sha256'], str)
                        or HEX64.fullmatch(witness['legacy_declaration_sha256']) is None)
                        or witness.get('legacy_declaration_sha256') != (bound or {}).get('legacy_declaration_sha256')):
                    return False
                physical = ('owner', 'container_id', 'container_created', 'engine_pid', 'engine_birth')
                if bound is None:
                    if witness['kind'] != 'no_owned_run' or any(witness[k] is not None for k in physical):
                        return False
                elif (witness['kind'] != 'exact_run_resources_released'
                        or any(witness[k] != bound[k] for k in physical)):
                    return False
            if request['action'] == 'start':
                return (not verified and result['state'] in {'READY', 'STARTING'} and result['process_active']
                        and result['api_ready'] == (result['state'] == 'READY')
                        and receipt['run_id'] is not None and receipt['attempt'] is not None)
            return (verified and not result['process_active'] and not result['api_ready']
                    and result['state'] in {'STOPPED', 'BLOCKED', 'FAILED'})
        except (KeyError, TypeError, ValueError, Refusal):
            return False

    def _prune_completed_operations(self, data, keep_operation_id):
        state = dict(self._retention_state(data))
        retired = bytearray.fromhex(state['retired_ids'])
        now_ms = int(self.wall_time() * 1000)
        operations = data.setdefault('bridge_operations', {})
        for operation_id, operation in list(operations.items()):
            if operation_id == keep_operation_id or not self._completed_retention_candidate(operation_id, operation, now_ms):
                continue
            for bit in self._retired_bits(operation_id):
                retired[bit // 8] |= 1 << (bit % 8)
            state['deadline_floor_ms'] = max(state['deadline_floor_ms'], operation['request']['deadline_ms'])
            state['retired_count'] += 1
            del operations[operation_id]
        if state['retired_count']:
            state['retired_ids'] = retired.hex()
            data['bridge_retention'] = state

    def _check_typed_effect(self, data, row, request, monotonic_deadline):
        self._check_typed_authority(data, request)
        self._check_retired_request(data, request)
        if request['profile_revision'] != self.profile_revision(row['id']):
            raise Refusal('Approved profile revision changed')
        run = data['runs'].get(row['id'])
        generation = run['generation'] if run else data['counters'].get(row['id'], 0)
        if request['expected_generation'] != generation or request['expected_instance'] != self.instance_identity(run):
            raise Refusal('Runtime generation/instance changed; obtain fresh state')
        if run:
            validate_definition = getattr(self.adapter, 'validate_run_definition', None)
            if validate_definition is not None:
                validate_definition(row, run)
        if self.monotonic_time() >= monotonic_deadline or self.wall_time() * 1000 >= request['deadline_ms']:
            raise Refusal('Dispatch lease expired before effect')
        if request['issued_at_ms'] > self.wall_time() * 1000 + 1000:
            raise Refusal('Dispatch issue time is in the future')

    def _record_typed(self, data, request, digest, phase, run=None, result=None):
        self._prune_completed_operations(data, request['operation_id'])
        operations = data.setdefault('bridge_operations', {})
        # Keep unresolved intents. Bound admission without deleting dedup evidence.
        if request['operation_id'] not in operations:
            self._check_retired_request(data, request)
            if len(operations) >= BRIDGE_JOURNAL_CAP:
                raise Refusal('Operation journal capacity reached; unresolved records preserved')
        receipt = {'operation_id': request['operation_id'], 'profile_id': request['profile_id'],
                   'action': request['action'], 'phase': phase,
                   'run_id': run.get('run_id') if run else None,
                   'generation': run.get('generation') if run else data['counters'].get(request['profile_id'], 0),
                   'attempt': run.get('attempt') if run else None}
        old = operations.get(request['operation_id'])
        if old:
            if old['digest'] != digest:
                raise Refusal('operation_id_conflict: immutable request digest differs')
            # The operation stays bound to its original exact run forever. Reporting
            # replacement/absence must never adopt the replacement into its receipt.
            receipt['run_id'] = old['receipt']['run_id']
            receipt['generation'] = old['receipt']['generation']
            receipt['attempt'] = old['receipt'].get('attempt')
        if result is not None:
            receipt['result'] = result
        bound_instance = old.get('bound_instance') if old else ({k: run.get(k) for k in
            ('model_id', 'run_id', 'generation', 'attempt', 'owner', 'container_id',
             'container_created', 'engine_pid', 'engine_birth')} if run else None)
        declaration = self.captured_legacy_declaration(run)
        same_bound_run = (run is not None and bound_instance is not None
                          and all(run.get(k) == bound_instance.get(k) for k in
                                  ('model_id', 'run_id', 'generation', 'attempt')))
        if same_bound_run and declaration is not None:
            captured = bound_instance.get('legacy_declaration_sha256')
            if captured is not None and captured != declaration:
                raise ObservationError('Receipt immutable legacy declaration changed')
            # A STARTING receipt can precede capture. Add the exact field once;
            # never replace it or adopt a replacement run into an old receipt.
            bound_instance = dict(bound_instance)
            bound_instance['legacy_declaration_sha256'] = declaration
        operations[request['operation_id']] = {'digest': digest, 'request': dict(request),
                                              'receipt': receipt, 'bound_instance': bound_instance}
        return dict(receipt)

    def submit_typed(self, request):
        digest = self._validate_typed(request)
        request = dict(request)
        remaining = (request['deadline_ms'] - self.wall_time() * 1000) / 1000
        monotonic_deadline = self.monotonic_time() + max(0, min(30, remaining))
        with self.store.lock():
            data = self.store.read()
            receipt = self._existing_receipt(data, request, digest)
            if receipt is not None:
                return receipt
            self._check_typed_effect(data, self.row(request['profile_id']), request, monotonic_deadline)
        context = (request, digest, monotonic_deadline)
        if request['action'] == 'start':
            return self._start(request['profile_id'], typed=context, wait=False)
        return self._stop(request['profile_id'], typed=context, wait=False)

    def reconcile_typed(self, request, current_authority=None):
        """Resolve a previously attempted effect from exact evidence, never replay it."""
        digest = self._validate_typed(request)
        with self.store.lock():
            data = self.store.read()
            receipt = self._existing_receipt(data, request, digest, current_authority)
            if receipt is None:
                raise Refusal('Unknown operation; receipt lookup never dispatches')
            if receipt['phase'] == 'completed':
                return receipt
            operation = data['bridge_operations'][request['operation_id']]
            if operation.get('execution_forbidden') or operation['receipt']['phase'] == 'prepared':
                # Actual out-of-band activity cannot turn a never-attempted or revoked
                # request into an executed request. Its original receipt stays clear.
                return receipt
            mid = request['profile_id']
            run = data['runs'].get(mid)
            bound = operation.get('bound_instance')
            if (not run or not bound or run['run_id'] != receipt['run_id']
                    or run['generation'] != receipt['generation']
                    or run.get('attempt') != receipt.get('attempt')
                    or any(value is not None and run.get(key) != value for key, value in bound.items())):
                receipt = self._record_typed(data, request, digest, 'unresolved', run,
                                            {'reason': 'original_runtime_replaced_or_missing'})
                self.store.write(data)
                return receipt
            row = self.row(mid)
            observed = self.adapter.observe(row, run)
            if request['action'] == 'start' and observed['active'] and run.get('owner'):
                snap = self.status(mid)
                if snap['state'] != 'UNKNOWN':
                    receipt = self._record_typed(data, request, digest, 'completed', run, snap)
                    self.store.write(data)
            elif request['action'] == 'stop' and not observed['active']:
                proof = self._shutdown_witness(row, data, run)
                if proof is None:
                    return receipt
                failure = run.get('shutdown_failure')
                state = 'FAILED' if run['state'] == 'FAILED' or failure else 'STOPPED'
                message = (failure['message'] if failure else run.get('message')) if state == 'FAILED' else 'Exact stopped effect reconciled from supervisor/resource evidence'
                run.update(state=state, message=message)
                if data['reservation'] == mid:
                    data['reservation'] = None
                snap = self.snapshot(row, state, False, False, run['message'], run)
                snap.update(shutdown_verified=True, shutdown_witness=proof,
                            instance_identity=proof['instance_identity'])
                receipt = self._record_typed(data, request, digest, 'completed', run, snap)
                self.store.write(data)
            return receipt

    def row(self, mid):
        if mid not in IDS or mid not in self.rows:
            raise Refusal('Unknown fixed model ID')
        return self.rows[mid]

    def log(self, mid, message):
        stamp = time.strftime('%Y-%m-%dT%H:%M:%S%z')
        RotatingSink(self.log_root / mid / 'actions.log', protected=self.protected).write(stamp + ' ' + message + '\n')

    def snapshot(self, row, state, active, ready, message, run=None):
        return {'model_id': row['id'], 'state': state, 'process_active': bool(active),
                'api_ready': bool(ready), 'context': row.get('context'), 'endpoint': row['endpoint'],
                'engine_log': str(self.log_root / row['id'] / 'engine.log'),
                'action_log': str(self.log_root / row['id'] / 'actions.log'),
                'message': message[:400], 'generation': run.get('generation') if run else None,
                'attempt': run.get('attempt') if run else None, 'run_id': run.get('run_id') if run else None}

    def status(self, mid):
        row = self.row(mid)
        try:
            data = self.store.read()  # Strictly read-only: no locks/state/log writes.
            run = data['runs'].get(mid)
            return self._observed_snapshot(row, data, run)
        except (ObservationError, Refusal, OSError, ValueError, KeyError, TypeError) as exc:
            return self.snapshot(row, 'UNKNOWN', False, False, str(exc))

    def _observed_snapshot(self, row, data, run):
        """One pinned state input, shared by legacy status and bridge observation."""
        mid = row['id']
        if not run:
            self.adapter.idle(row)
            return self.snapshot(row, 'STOPPED' if row['prepared'] else 'BLOCKED', False, False,
                                 'No owned instance' if row['prepared'] else row['block_reason'])
        observation = self.adapter.observe(row, run)
        active = observation['active']
        saved = run['state']
        failure = run.get('shutdown_failure')
        if saved == 'FAILED' or failure:
            message = run.get('message') if saved == 'FAILED' else failure['message']
            return self.snapshot(row, 'FAILED', active, False, message or 'Failed start; no automatic retry', run)
        if run.get('cancelled') or saved == 'STOPPING':
            return self.snapshot(row, 'STOPPING' if active or data['reservation'] == mid else 'STOPPED', active, False,
                                 'Cancellation/termination recorded for this run', run)
        if saved == 'STOPPED':
            if active:
                raise ObservationError('Stopped record still has an active owner')
            return self.snapshot(row, 'STOPPED' if row['prepared'] else 'BLOCKED', False, False,
                                 'Owned instance stopped; logs retained', run)
        if saved in {'STARTING', 'READY'}:
            if observation.get('engine_active'):
                ready, reason = self.adapter.api_ready(row)
                if not observation.get('supervisor_active'):
                    return self.snapshot(row, 'FAILED', True, False, 'Engine alive but persistent supervisor/log collector stopped', run)
                return self.snapshot(row, 'READY' if ready else 'STARTING', True, ready, reason, run)
            if active or (saved == 'STARTING' and time.time() - run['created_at'] < 15):
                return self.snapshot(row, 'STARTING', active, False, 'Reserved; persistent supervisor preparing engine', run)
            return self.snapshot(row, 'FAILED', False, False, 'Persistent engine exited before/after readiness', run)
        raise ObservationError('Unknown durable run state')

    def start(self, mid):
        try:
            return self._start(mid)
        except (Refusal, ObservationError, OSError, ValueError, KeyError, TypeError) as exc:
            if mid in self.rows:
                self.log(mid, 'START REFUSED/FAILED: ' + str(exc)[:400])
            raise

    def _start(self, mid, typed=None, wait=True):
        row = self.row(mid)
        # Same target already active is a no-op BEFORE launch memory/port/GPU checks.
        with self.store.lock():
            data = self.store.read()
            if typed:
                request, digest, lease = typed
                receipt = self._existing_receipt(data, request, digest)
                if receipt is not None:
                    return receipt
                self._check_typed_effect(data, row, request, lease)
            current = data['runs'].get(mid)
            if current and self.adapter.observe(row, current)['active']:
                result = self.status(mid)
                if result['state'] == 'UNKNOWN':
                    raise Refusal('Current exact instance cannot be verified for start no-op')
                if typed:
                    receipt = self._record_typed(data, request, digest, 'completed', current, result)
                    self.store.write(data)
                    return receipt
                return result
        if not row['prepared']:
            self.log(mid, 'REFUSED bundle incomplete: ' + row['block_reason'])
            raise Refusal(row['block_reason'])
        # Cheap read-only conflicts first: do not hash 80-93 GB only to discover
        # that the baseline, GPT, port, or shared-memory budget forbids launch.
        self.adapter.preflight(row)
        # Bridge launch uses an explicitly approved full-integrity stat seal. Existing
        # CLI still performs its original full hash, always outside the effect lock.
        witnesses = self._sealed_bundle(row) if typed else self.adapter.verify_bundle(row)
        with self.store.lock():
            data = self.store.read()
            if typed:
                receipt = self._existing_receipt(data, request, digest)
                if receipt is not None:
                    return receipt
                self._check_typed_effect(data, row, request, lease)
            current = data['runs'].get(mid)
            if current and self.adapter.observe(row, current)['active']:
                result = self.status(mid)
                if result['state'] == 'UNKNOWN':
                    raise Refusal('Concurrent exact instance cannot be verified')
                if typed:
                    receipt = self._record_typed(data, request, digest, 'completed', current, result)
                    self.store.write(data)
                    return receipt
                return result
            for ident, run in data['runs'].items():
                observation = self.adapter.observe(self.rows[ident], run)
                if observation['active']:
                    raise Refusal('Collection slot reserved/active by ' + ident + ' generation ' + str(run['generation']))
                if data['reservation'] == ident or run['state'] in {'STARTING', 'STOPPING'}:
                    if run['state'] == 'STARTING' and time.time() - run['created_at'] < 15:
                        raise Refusal('Collection startup handoff pending for ' + ident)
                    if not self.adapter.resources_released(self.rows[ident], run):
                        raise Refusal('Collection resources not released by ' + ident)
                    run.update(state='STOPPED' if run.get('cancelled') else 'FAILED',
                               message='Reconciled inactive exact owner/resources; old transaction released')
                    if data['reservation'] == ident:
                        data['reservation'] = None
                    self.store.write(data)
                    self.log(ident, 'RECONCILED inactive run=' + run['run_id'] + '; resources released')
            for ident, historical in data['runs'].items():
                if ident in IDS[5:] and historical.get('container_id'):
                    before_reap = (lambda: self._check_typed_effect(data, row, request, lease)) if typed else None
                    legacy_support().reap_stopped(self, data, self.rows[ident], historical, before_reap)
            for item in self.rows.values():
                self.adapter.idle(item)
            self.adapter.recheck_bundle(witnesses)
            self.adapter.preflight(row)
            approved_revision = request['profile_revision'] if typed else self.profile_revision(mid)
            if approved_revision != self.profile_revision(mid):
                raise Refusal('Accepted profile revision changed during final launch admission')
            count = data['counters'].get(mid, 0) + 1
            data['counters'][mid] = count
            run = {'model_id': mid, 'generation': count, 'attempt': uuid.uuid4().hex,
                   'run_id': uuid.uuid4().hex, 'created_at': time.time(), 'state': 'STARTING',
                   'cancelled': False, 'owner': None, 'container_id': None,
                   'artifact_witnesses': [[str(path), list(witness)] for path, witness in witnesses],
                   'message': 'Durable reservation; no readiness yet'}
            # Freeze effect definition for the asynchronous existing supervisor.
            # A later manifest/code/unit change cannot reuse the accepted launch.
            run['approved_profile_revision'] = approved_revision
            if typed:
                run['bridge_operation_id'] = request['operation_id']
            data['runs'][mid] = run
            data['reservation'] = mid
            if typed:
                self._record_typed(data, request, digest, 'prepared', run)
            self.store.write(data)  # Reserve before systemd can start anything.
            self.log(mid, 'START run=' + run['run_id'] + ' generation=' + str(count) + ' quant=' + row['quant'] + ' engine=' + row['engine'] + ' context=' + str(row['context']))
            try:
                if typed:
                    self._record_typed(data, request, digest, 'effect_attempted', run)
                    self.store.write(data)
                    # Recheck after durable writes; no wall jump can extend this lease.
                    self._check_typed_authority(data, request)
                    if self.monotonic_time() >= lease or self.wall_time() * 1000 >= request['deadline_ms']:
                        run.update(state='STOPPED', cancelled=True, message='Dispatch expired; no effect invoked')
                        data['reservation'] = None
                        receipt = self._record_typed(data, request, digest, 'completed', run,
                                                    {'reason': 'dispatch_expired', 'effect_invoked': False})
                        self.store.write(data)
                        return receipt
                if run['approved_profile_revision'] != self.profile_revision(mid):
                    raise Refusal('Accepted profile revision changed before exact supervisor handoff')
                self.adapter.handoff(row, run)
                if typed:
                    # Command acceptance is not model readiness. Observation comes later.
                    receipt = self._record_typed(data, request, digest, 'dispatched', run)
                    self.store.write(data)
            except Exception as exc:
                run.update(state='FAILED', message='Persistent handoff failed: ' + str(exc))
                # Keep reservation if partial handoff may own a process; reconciliation required.
                try:
                    if self.adapter.resources_released(row, run):
                        data['reservation'] = None
                except Exception:
                    pass
                self.store.write(data)
                self.log(mid, run['message'])
                raise Refusal(run['message']) from exc
        if typed:
            return receipt
        if not wait:
            return self.snapshot(row, 'STARTING', False, False, 'Exact supervisor handoff dispatched', run)
        return self.wait_ready(mid, run['run_id'], row.get('startup_timeout_seconds', 1800))

    def wait_ready(self, mid, run_id, timeout):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            data = self.store.read()
            run = data['runs'].get(mid)
            if not run or run['run_id'] != run_id or run.get('cancelled') or run['state'] == 'STOPPING':
                raise Refusal('This startup was cancelled/replaced; late waiter cannot publish READY')
            snap = self.status(mid)
            if snap['state'] == 'READY':
                with self.store.lock():
                    data = self.store.read(); latest = data['runs'].get(mid)
                    if not latest or latest['run_id'] != run_id or latest.get('cancelled') or latest['state'] not in {'STARTING', 'READY'}:
                        raise Refusal('Cancelled/replaced startup waiter rejected')
                    # Exact identity/API verified again inside publish boundary.
                    if self.status(mid)['state'] != 'READY':
                        continue
                    latest.update(state='READY', message='Exact owned engine/API identity ready')
                    self.store.write(data)
                self.log(mid, 'READY run=' + run_id)
                return snap
            if snap['state'] in {'FAILED', 'UNKNOWN', 'BLOCKED', 'STOPPED'}:
                self._fail(mid, run_id, snap['message'])
                raise Refusal('Start failed: ' + snap['message'] + '; engine log=' + snap['engine_log'])
            time.sleep(.15)
        self._fail(mid, run_id, 'Readiness timeout; explicit stop required if owned engine remains')
        raise Refusal('Readiness timeout; logs retained; no automatic retry')

    def _fail(self, mid, run_id, message):
        with self.store.lock():
            data = self.store.read(); run = data['runs'].get(mid)
            if run and run['run_id'] == run_id and not run.get('cancelled'):
                run.update(state='FAILED', message=message)
                try:
                    if self.adapter.resources_released(self.row(mid), run):
                        if data['reservation'] == mid:
                            data['reservation'] = None
                except Exception:
                    pass
                self.store.write(data)
        self.log(mid, 'FAILED run=' + run_id + ': ' + message)

    def stop(self, mid):
        try:
            return self._stop(mid)
        except (Refusal, ObservationError, OSError, ValueError, KeyError, TypeError) as exc:
            if mid in self.rows:
                self.log(mid, 'STOP REFUSED/FAILED: ' + str(exc)[:400])
            raise

    def _stop(self, mid, typed=None, wait=True):
        row = self.row(mid)
        with self.store.lock():
            data = self.store.read(); run = data['runs'].get(mid)
            if typed:
                request, digest, lease = typed
                receipt = self._existing_receipt(data, request, digest)
                if receipt is not None:
                    return receipt
                self._check_typed_effect(data, row, request, lease)
            if not run:
                self.adapter.idle(row)
                result = self.snapshot(row, 'STOPPED' if row['prepared'] else 'BLOCKED', False, False, 'No owned instance')
                if typed:
                    proof = self._shutdown_witness(row, data, None)
                    result.update(shutdown_verified=True, shutdown_witness=proof,
                                  instance_identity='', generation=proof['generation'])
                    receipt = self._record_typed(data, request, digest, 'completed', result=result)
                    self.store.write(data)
                    return receipt
                return result
            if mid in IDS[5:]:
                legacy_support().effective_unit(self.adapter, row, run)
            # Refuse stale/replaced identities before cancellation/effects.
            observation = self.adapter.observe(row, run)
            if run['state'] in ({'STOPPED', 'FAILED'} if typed else {'STOPPED'}) and not observation['active']:
                state = 'FAILED' if typed and run.get('shutdown_failure') else run['state']
                message = ((run.get('shutdown_failure') or {}).get('message') or run.get('message')) if state == 'FAILED' else 'Already stopped'
                result = self.snapshot(row, state, False, False, message or 'Already inactive', run)
                if typed:
                    proof = self._shutdown_witness(row, data, run)
                    if proof is not None:
                        result.update(shutdown_verified=True, shutdown_witness=proof,
                                      instance_identity=proof['instance_identity'])
                        receipt = self._record_typed(data, request, digest, 'completed', run, result)
                        self.store.write(data)
                        return receipt
                else:
                    return result
            failure = None
            if typed:
                current_state = self._observed_snapshot(row, data, run)
                if current_state['state'] == 'FAILED':
                    failure = {'message': current_state['message']}
            if typed:
                self._record_typed(data, request, digest, 'prepared', run)
                self.store.write(data)
                # Cancellation itself is observed by the supervisor and is therefore
                # an effect. Check the lease BEFORE committing cancelled=True.
                self._check_typed_effect(data, row, request, lease)
            run.update(cancelled=True, state='STOPPING', message='Cancellation requested for exact run')
            if failure:
                # Supervisor cancellation may change its lifecycle label later.
                # Retain the original failed outcome while proving physical stop.
                run['shutdown_failure'] = failure
            # Stopping historical inactive A must not overwrite pending B's
            # reservation. Cancellation is attached to A's exact run regardless.
            if data['reservation'] in {None, mid}:
                data['reservation'] = mid
            if typed:
                self._record_typed(data, request, digest, 'effect_attempted', run)
            self.store.write(data)
            run_id = run['run_id']
            if typed:
                # With a healthy exact supervisor, one nonblocking systemd action is
                # enough: its existing SIGTERM handler stops its owned container.
                request_stop = getattr(self.adapter, 'request_terminate', None)
                if request_stop is None:
                    # Explicit offline test adapters may implement the original seam.
                    if self.protected:
                        raise Refusal('Bounded exact stop adapter unavailable')
                    self.adapter.terminate(row, run)
                else:
                    request_stop(row, run)
                receipt = self._record_typed(data, request, digest, 'dispatched', run)
                self.store.write(data)
                return receipt
        self.log(mid, 'STOP run=' + run_id + ' (late readiness waiters invalidated)')
        self.adapter.terminate(row, run)
        deadline = time.monotonic() + row.get('stop_timeout_seconds', 90)
        forced = False
        while True:
            current = self.store.read()['runs'].get(mid)
            if not current or current['run_id'] != run_id:
                raise Refusal('Stop identity replaced; no further effects')
            if self.adapter.resources_released(row, current):
                with self.store.lock():
                    data = self.store.read(); latest = data['runs'].get(mid)
                    if not latest or latest['run_id'] != run_id:
                        raise Refusal('Stop run replaced before release')
                    if not self.adapter.resources_released(row, latest):
                        continue
                    latest.update(state='STOPPED', message='Exact owner and resources stopped; logs retained')
                    if data['reservation'] == mid:
                        data['reservation'] = None
                    self.store.write(data)
                self.log(mid, 'STOPPED run=' + run_id)
                return self.snapshot(row, 'STOPPED', False, False, 'Exact owned instance stopped', latest)
            if time.monotonic() >= deadline:
                if forced:
                    raise Refusal('STOP_UNCONFIRMED: exact owner/resources still active; reservation retained')
                self.adapter.force(row, current)
                forced = True
                deadline = time.monotonic() + 10
            time.sleep(.15)


def parse_cli(args):
    # No root argv passthrough, user manifest, profiles, paths, unit/container names.
    if len(args) not in (2, 3) or args[0] not in {'start', 'stop', 'status', 'adopt-legacy', 'prepare-legacy'} or args[1] not in IDS:
        raise Refusal('Usage: spark-modelctl-v4 {start|stop|status|adopt-legacy|prepare-legacy} FIXED_MODEL_ID [--json for status]')
    if len(args) == 3 and (args[0] != 'status' or args[2] != '--json'):
        raise Refusal('Only status accepts --json; no other arguments allowed')
    return args[0], args[1], len(args) == 3


def main(args=None):
    mid = None
    json_mode = False
    ctl = None
    action = None
    try:
        action, mid, json_mode = parse_cli(list(sys.argv[1:] if args is None else args))
        if os.geteuid() != 0:
            raise Refusal('Root helper authorization required; use installed sudo -n allowlist')
        manifest = load_production_manifest()
        ctl = Controller(manifest, SystemAdapter(manifest))
        if action == 'status':
            result = ctl.status(mid)
        elif action == 'prepare-legacy':
            if mid not in IDS[5:]:
                raise Refusal('prepare-legacy accepts only fixed legacy IDs')
            print(json.dumps(ctl.prepare_bundle(mid), sort_keys=True))
            return 0
        elif action == 'adopt-legacy':
            print(json.dumps(ctl.adopt_legacy(mid), sort_keys=True))
            return 0
        elif action == 'start':
            result = ctl.start(mid)
        else:
            result = ctl.stop(mid)
        if json_mode:
            print(json.dumps(result, ensure_ascii=False))
        else:
            print(result['model_id'] + ': ' + result['state'] + ' | ' + result['message'])
            print('context=' + str(result['context']) + ' endpoint=' + result['endpoint'])
            print('engine_log=' + result['engine_log'])
        return 1 if result['state'] == 'UNKNOWN' else 0
    except (Refusal, ObservationError, OSError, ValueError, KeyError, TypeError) as exc:
        message = str(exc)[:400]
        if json_mode and mid:
            print(json.dumps({'model_id': mid, 'state': 'UNKNOWN', 'process_active': False,
                              'api_ready': False, 'message': message}))
        else:
            print('Spark V4: ' + message, file=sys.stderr)
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
