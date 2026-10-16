"""Pure draft construction for existing root-local approve; no I/O or effects.

Root supplies current protected policy/catalog and real controller/profile/file
readers after source staging. This function never approves, publishes, enables,
binds an agent, changes credentials, or edits a Monik database.
"""
import copy
import hashlib
import json

BASE_IDS = ('qwen3-coder-next-80b-huihui', 'ling-3-flash-124b-heretic-text',
            'mistral-small-4-119b-heretic', 'nemotron-3-super-120b-heretic-v2',
            'minimax-m27-229b-heretic', 'qwen-existing-observation',
            'gpt-oss-120b-existing-configured')
NEW_IDS = ('glm45-air-106b-derestricted', 'gemma4-31b-deckard-heretic',
           'nemotron-super-49b-v15-abliterated')


def definition_hash(binding):
    # Exact encoding recipe from reviewed activation_primitives.py.
    ordered = {key: binding[key] for key in ('python_path', 'backend_revision', 'code_pins')}
    for key in ('log_root', 'redaction_file', 'redaction_sha256'):
        if binding.get(key): ordered[key] = binding[key]
    if set(ordered) != set(binding): raise ValueError('unexpected binding field')
    raw = json.dumps(ordered, ensure_ascii=False, separators=(',', ':'), allow_nan=False)
    raw = raw.replace('&', '\\u0026').replace('<', '\\u003c').replace('>', '\\u003e')
    raw = raw.replace('\u2028', '\\u2028').replace('\u2029', '\\u2029')
    return 'sha256:' + hashlib.sha256(raw.encode()).hexdigest()


def draft_policy(before, catalog, backend_revision, source_sha256):
    """Return a draft; mandatory ordinary worker approve computes revisions.

    backend_revision(mid) must call the installed exact ctl.profile_revision.
    source_sha256(path) must use the existing protected, race-checked reader.
    Disabled/prepared=false new rows receive no remote allowed_actions.
    """
    if [p['profile_id'] for p in before['profiles']] != list(BASE_IDS):
        raise ValueError('exact seven-profile current policy required')
    if [r['id'] for r in catalog['models']] != list(BASE_IDS + NEW_IDS):
        raise ValueError('exact seven plus three fixed catalog required')
    if any(type(r.get('prepared')) is not bool for r in catalog['models']):
        raise ValueError('typed prepared flag required')
    result = copy.deepcopy(before)
    template = before['profiles'][1]
    if template['target']['kind'] != 'existing-coordinator':
        raise ValueError('existing coordinator template required')
    rows = {r['id']:r for r in catalog['models']}
    for index, mid in enumerate(NEW_IDS):
        row = rows[mid]; p = copy.deepcopy(template)
        expected = [('llama.cpp CUDA','MXFP4_MOE'), ('vLLM','NVFP4_AWQ'),
                    ('llama.cpp CUDA','Q5_K_M')][index]
        if (row['engine'],row['quant']) != expected or row['endpoint'] != f'http://127.0.0.1:{31006+index}/v1':
            raise ValueError('fixed new engine/quant/loopback port differs')
        p.update(profile_id=mid, display_name=row['name'], model_ref=mid,
                 engine='vllm' if row['engine'] == 'vLLM' else 'llama.cpp',
                 allowed_actions=copy.deepcopy(template['allowed_actions']) if row['prepared'] else [],
                 readiness_seconds=row.get('startup_timeout_seconds',1800),
                 stop_seconds=row.get('stop_timeout_seconds',90))
        p.pop('engine_version', None)
        configured = {'context_length':row['context'], 'quantization':row['quant'],
                      'port':31006+index}
        for setting in p['settings']:
            setting.update(value=configured.get(setting['name']),
                           provenance='configured' if setting['name'] in configured else 'unknown')
        pins = p['target']['existing_coordinator']['code_pins']
        expected = '/etc/systemd/system/spark-v4-'+BASE_IDS[1]+'@.service'
        if len(pins) != 9 or pins[6]['path'] != expected:
            raise ValueError('exact native nine-source template required')
        pins[6]['path'] = '/etc/systemd/system/spark-v4-'+mid+'@.service'
        result['profiles'].append(p)
    for p in result['profiles']:
        if p['target']['kind'] != 'existing-coordinator': raise ValueError('unexpected target kind')
        binding = p['target']['existing_coordinator']
        revision = backend_revision(p['profile_id'])
        if len(revision) != 64 or any(c not in '0123456789abcdef' for c in revision):
            raise ValueError('actual bare native backend SHA required')
        binding['backend_revision'] = revision
        for pin in binding['code_pins']:
            sha = source_sha256(pin['path'])
            if len(sha) != 64 or any(c not in '0123456789abcdef' for c in sha):
                raise ValueError('actual protected source SHA required')
            pin['sha256'] = sha
        p['target']['definition_hash'] = definition_hash(binding)
        p['profile_revision'] = ''
    return result
