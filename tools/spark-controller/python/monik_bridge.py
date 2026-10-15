#!/usr/bin/python3
"""Fixed local stdio transport for the existing Spark V4 Controller.

No network, shell, alternate manifest, argv, unit, container or path input. This
program does not enable control, bind sessions, prepare weights, or run discovery
scripts. A root broker authenticates peer credentials and existing owner/session
policy before invoking submit/receipt. Observation remains separately read-only.
"""
from __future__ import annotations

import importlib.util
import json
import os
from pathlib import Path
import stat
import sys

PROJECT = Path('/opt/spark/model-collection-v4')
MAX_INPUT = 8192
MAX_OUTPUT = 128 * 1024
IDS = ('qwen3-coder-next-80b-huihui', 'ling-3-flash-124b-heretic-text',
       'mistral-small-4-119b-heretic', 'nemotron-3-super-120b-heretic-v2',
       'minimax-m27-229b-heretic', 'qwen-existing-observation',
       'gpt-oss-120b-existing-configured')
MIDRANGE_IDS = ('glm45-air-106b-derestricted', 'gemma4-31b-deckard-heretic',
                'nemotron-super-49b-v15-abliterated')
IDS = (*IDS, *MIDRANGE_IDS)


def strict_object(pairs):
    result = {}
    for key, value in pairs:
        if key in result:
            raise ValueError('Duplicate typed JSON key')
        result[key] = value
    return result


def decode_envelope(raw):
    if not isinstance(raw, bytes) or len(raw) > MAX_INPUT:
        raise ValueError('Typed input byte limit')
    body = json.loads(raw, object_pairs_hook=strict_object,
                      parse_constant=lambda _: (_ for _ in ()).throw(ValueError('Non-finite JSON')))
    if not isinstance(body, dict) or body.get('schema_version') != 1 or type(body.get('schema_version')) is not int:
        raise ValueError('Typed bridge schema must be 1')
    method = body.get('method')
    if method == 'observe_many':
        if set(body) != {'schema_version', 'method', 'profile_ids'}:
            raise ValueError('Unknown observation selectors')
        profiles = body['profile_ids']
        if not isinstance(profiles, list) or not 1 <= len(profiles) <= len(IDS) or any(not isinstance(p, str) or p not in IDS for p in profiles) or len(set(profiles)) != len(profiles):
            raise ValueError('Only distinct fixed profile IDs permitted')
    elif method == 'bind_current_agent':
        if set(body) != {'schema_version', 'method', 'binding'} or not isinstance(body['binding'], dict):
            raise ValueError('Only a fixed authenticated broker binding is permitted')
        binding = body['binding']
        if set(binding) != {'agent_id', 'controller_id', 'control_epoch', 'agent_session', 'boot_id'} or any(not isinstance(v, str) or not 1 <= len(v) <= 128 for v in binding.values()):
            raise ValueError('Invalid authenticated local broker binding')
    elif method in {'submit', 'receipt'}:
        expected = {'schema_version', 'method', 'request'}
        if method == 'receipt':
            expected.add('authority')
        if set(body) != expected or not isinstance(body['request'], dict):
            raise ValueError('Only a bounded typed request is permitted')
        if method == 'receipt':
            authority = body['authority']
            if not isinstance(authority, dict) or set(authority) != {'control_epoch', 'agent_session', 'boot_id'} or any(not isinstance(v, str) or not 1 <= len(v) <= 128 for v in authority.values()):
                raise ValueError('Current authenticated receipt authority is required')
    else:
        raise ValueError('Unknown typed method')
    return body


def protected_source(path):
    for current in reversed((path, *path.parents)):
        st = current.lstat()
        if stat.S_ISLNK(st.st_mode) or st.st_uid != 0 or st.st_mode & 0o022:
            raise ValueError('Unsafe protected bridge source')
        if current != path and not stat.S_ISDIR(st.st_mode):
            raise ValueError('Unsafe bridge source ancestor')
    if not stat.S_ISREG(path.lstat().st_mode):
        raise ValueError('Bridge source must be regular')


def dispatch(controller, body):
    method = body['method']
    if method == 'observe_many':
        return {'schema_version': 1, 'models': [controller.bridge_snapshot(mid) for mid in body['profile_ids']]}
    if method == 'bind_current_agent':
        return {'schema_version': 1, 'authority': controller.bind_current_agent(body['binding'])}
    if method == 'submit':
        return {'schema_version': 1, 'receipt': controller.submit_typed(body['request'])}
    return {'schema_version': 1, 'receipt': controller.reconcile_typed(body['request'], body['authority'])}


def main():
    try:
        if len(sys.argv) != 1 or os.geteuid() != 0 or not sys.flags.isolated:
            raise ValueError('Fixed isolated root broker entrypoint required')
        source = PROJECT / 'bin/modelctl.py'
        protected_source(Path(__file__))
        protected_source(source)
        # Import only the already verified fixed root source; no PYTHONPATH/site.
        spec = importlib.util.spec_from_file_location('spark_v4_bridge_modelctl', source)
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        raw = sys.stdin.buffer.read(MAX_INPUT + 1)
        body = decode_envelope(raw)
        manifest = module.load_production_manifest()
        controller = module.Controller(manifest, module.SystemAdapter(manifest))
        result = dispatch(controller, body)
        encoded = json.dumps(result, sort_keys=True, separators=(',', ':'), allow_nan=False).encode()
        if len(encoded) > MAX_OUTPUT:
            raise ValueError('Typed output byte limit')
        sys.stdout.buffer.write(encoded + b'\n')
        return 0
    except Exception as exc:
        # No exception text, raw argv, environment or underlying command output.
        code = 'typed_bridge_' + type(exc).__name__.lower()
        sys.stdout.write(json.dumps({'schema_version': 1, 'error': code}) + '\n')
        return 1


if __name__ == '__main__':
    raise SystemExit(main())
