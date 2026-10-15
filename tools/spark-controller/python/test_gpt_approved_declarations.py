"""Finite GPT controls at the real manifest, container and integrity boundaries.

All host observations/effects are injected or absent. Declaration acceptance
does not establish model quality, cache reuse or native runtime acceptance.
"""
import copy
import itertools
import json
from pathlib import Path
import tempfile
import unittest
from unittest.mock import Mock, patch

import test_bridge_candidate as base

core = base.modelctl
legacy = core.legacy_support()
CACHE_ROOT = '/srv/spark/hf-home/gptoss-runtime-cache'


class ApprovedGptDeclarations(unittest.TestCase):
    def setUp(self):
        self.rows = legacy.approved_gpt_declarations()

    def test_six_distinct_complete_rows_reach_existing_manifest_validation(self):
        self.assertEqual(7, len(self.rows))
        self.assertEqual(7, len({legacy.digest(row) for row in self.rows}))
        self.assertEqual(legacy.definitions()[1], self.rows[0])
        for row in self.rows:
            with self.subTest(variant=legacy.digest(row)):
                self.assertIsNone(legacy.validate_row(row))
                manifest = json.loads((base.SOURCE_ROOT / 'config/controller-manifest.json').read_text())
                manifest['models'] += [legacy.definitions()[0], row]
                self.assertIs(manifest, core.validate_manifest(manifest))

    def test_historical_definitions_and_qwen_declarations_unchanged(self):
        self.assertEqual('9a8348d8e8744999f23573577b96059bb4bf22e647f47fa047fb25be2018074f',
                         legacy.digest(legacy.definitions()))
        self.assertEqual('8bf68b5edbcd50d806aba91ec8e7ba3191591c73065fec537adfec165870eea0',
                         legacy.digest(legacy.approved_qwen_declarations()))
        for row in legacy.approved_qwen_declarations():
            self.assertIsNone(legacy.validate_row(row))

    def test_only_exact_layer_scale_and_cache_differences(self):
        # Independent expected rows assert the entire row, including metadata,
        # source/image pins, mounts, limits, parsers and supervisor stop policy.
        for index, (layers, scales, cache) in enumerate([
                ('moe,qkv,o,lm_head', False, False), ('moe', False, False),
                ('moe,qkv,o,lm_head', True, False), ('moe', True, False),
                ('moe', False, True), ('moe', True, True)]):
            expected = copy.deepcopy(legacy.definitions()[1])
            args = expected['runtime']['argv']
            args[args.index('--mxfp4-layers') + 1] = layers
            if scales:
                args.append('--calculate-kv-scales')
            if cache:
                expected['runtime']['env']['XDG_CACHE_HOME'] = CACHE_ROOT
            with self.subTest(index=index):
                self.assertEqual(expected, self.rows[index])

    def test_returned_rows_never_alias_each_other_or_historical_data(self):
        before = legacy.definitions()
        expected = copy.deepcopy(self.rows)
        for index in range(len(self.rows)):
            rows = legacy.approved_gpt_declarations()
            rows[index]['runtime']['argv'].append('--unapproved')
            rows[index]['runtime']['env']['XDG_CACHE_HOME'] = '/unapproved'
            rows[index]['runtime']['mounts'][0]['readonly'] = False
            for other in range(len(rows)):
                if other != index:
                    self.assertEqual(expected[other], rows[other])
        self.assertEqual(before, legacy.definitions())
        self.assertEqual(expected, legacy.approved_gpt_declarations())

    def test_adjacent_unapproved_precision_context_gpu_parser_and_flags_denied(self):
        changes = [('--mxfp4-layers', 'moe,qkv'), ('--mxfp4-layers', 'moe,lm_head'),
                   ('--mxfp4-backend', 'TRTLLM'), ('--quantization', 'fp8'),
                   ('--kv-cache-dtype', 'auto'), ('--attention-backend', 'FLASH_ATTN'),
                   ('--max-model-len', '262144'), ('--max-num-seqs', '2'),
                   ('--max-num-batched-tokens', '16384'), ('--gpu-memory-utilization', '0.75'),
                   ('--tensor-parallel-size', '2'), ('--reasoning-parser', 'qwen3'),
                   ('--tool-call-parser', 'hermes'), ('--served-model-name', 'other'),
                   ('--api-key', 'literal-secret'), ('--host', '127.0.0.1'), ('--port', '31000')]
        for row, (flag, value) in itertools.product(self.rows, changes):
            changed = copy.deepcopy(row)
            args = changed['runtime']['argv']
            args[args.index(flag) + 1] = value
            with self.subTest(variant=legacy.digest(row), flag=flag), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)
        for row in self.rows:
            for extra in (['--enforce-eager'], ['--calculate-kv-scales', 'true'],
                          ['--mxfp4-layers', 'moe'], ['--max-model-len', '131072'],
                          ['--default-chat-template-kwargs', '{"enable_thinking": true}']):
                changed = copy.deepcopy(row)
                changed['runtime']['argv'] += extra
                with self.subTest(extra=extra), self.assertRaises(core.Refusal):
                    legacy.validate_row(changed)
            changed = copy.deepcopy(row)
            args = changed['runtime']['argv']
            position = args.index('--mxfp4-backend')
            del args[position:position + 2]
            with self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_extra_environment_cache_paths_partial_combinations_and_mounts_denied(self):
        mutations = [lambda r: r['runtime']['env'].update(BASH_ENV='/tmp/code'),
                     lambda r: r['runtime']['env'].update(TORCHINDUCTOR_CACHE_DIR=CACHE_ROOT + '/torchinductor'),
                     lambda r: r['runtime']['env'].update(TRITON_CACHE_DIR=CACHE_ROOT + '/triton'),
                     lambda r: r['runtime']['env'].update(VLLM_CACHE_ROOT=CACHE_ROOT + '/vllm'),
                     lambda r: r['runtime']['env'].update(XDG_CACHE_HOME='/root/.cache'),
                     lambda r: r['runtime']['env'].update(XDG_CACHE_HOME=CACHE_ROOT + '/'),
                     lambda r: r['runtime']['env'].update(HF_HOME='/other-cache'),
                     lambda r: r['runtime']['env'].update(VLLM_USE_FLASHINFER_MOE_MXFP4_MXFP8='0'),
                     lambda r: r['runtime']['mounts'][0].update(readonly=False),
                     lambda r: r['runtime']['mounts'][1].update(readonly=True),
                     lambda r: r['runtime']['mounts'][1].update(source='/other-cache'),
                     lambda r: r['runtime']['mounts'].append(
                         {'source': '/root/.cache', 'target': '/root/.cache', 'readonly': False})]
        for row, mutate in itertools.product(self.rows, mutations):
            changed = copy.deepcopy(row)
            mutate(changed)
            with self.subTest(variant=legacy.digest(row), changed=changed), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)
        # Persisted cache is approved only with the two exact moe controls.
        for row in self.rows[:1] + self.rows[2:3]:
            changed = copy.deepcopy(row)
            changed['runtime']['env']['XDG_CACHE_HOME'] = CACHE_ROOT
            with self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_identity_image_metadata_secret_and_resource_drift_denied(self):
        mutations = [lambda r: r.update(context=262144), lambda r: r.update(prepared=False),
                     lambda r: r.update(required_memory_bytes=1),
                     lambda r: r.update(startup_timeout_seconds=8000), lambda r: r.update(stop_timeout_seconds=90),
                     lambda r: r.update(evidence_level='RUNTIME_ACCEPTED'), lambda r: r.update(extra='unapproved'),
                     lambda r: r['runtime'].update(image='tag:latest'),
                     lambda r: r['runtime'].update(image_id='sha256:' + 'f' * 64),
                     lambda r: r['runtime'].update(secret_reference='/tmp/key'),
                     lambda r: r['runtime'].update(network='none'), lambda r: r['runtime'].update(ipc='private'),
                     lambda r: r['runtime'].update(shm_bytes=1), lambda r: r['runtime'].update(gpus='all'),
                     lambda r: r['runtime']['ulimits'].update(memlock=0),
                     lambda r: r['runtime'].update(container_name='other'),
                     lambda r: r['runtime'].update(auto_remove=True)]
        for row, mutate in itertools.product(self.rows, mutations):
            changed = copy.deepcopy(row)
            mutate(changed)
            with self.subTest(variant=legacy.digest(row)), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_qwen_cannot_adopt_gpt_flags_or_cache_exception(self):
        for row in legacy.approved_qwen_declarations():
            changes = [copy.deepcopy(row) for _ in range(3)]
            changes[0]['runtime']['argv'] += ['--calculate-kv-scales']
            changes[1]['runtime']['argv'] += ['--mxfp4-layers', 'moe']
            changes[2]['runtime']['env']['XDG_CACHE_HOME'] = CACHE_ROOT
            for changed in changes:
                with self.assertRaises(core.Refusal):
                    legacy.validate_row(changed)

    def test_each_row_needs_new_integrity_seal_and_cannot_reuse_any_other_row(self):
        with tempfile.TemporaryDirectory() as tmp:
            adapter = Mock()
            store = core.StateStore(tmp, False)
            ctl = core.Controller({'models': [self.rows[0]]}, adapter, store=store,
                                  log_root=tmp, protected=False)
            revisions = []
            for row in self.rows:
                ctl.rows[legacy.GPT] = row
                revisions.append(ctl.profile_revision(legacy.GPT))
                with self.assertRaisesRegex(core.Refusal, 'bundle_not_prepared'):
                    ctl._sealed_bundle(row)
            self.assertEqual(7, len(set(revisions)))
            for old_index, new_index in itertools.permutations(range(7), 2):
                data = store.read()
                data['bundle_seals'] = {legacy.GPT: {'profile_revision': revisions[old_index],
                                                    'witnesses': [['/compiled/artifact', [1]]]}}
                store.write(data)
                row = self.rows[new_index]
                ctl.rows[legacy.GPT] = row
                with self.subTest(old=old_index, new=new_index), \
                     self.assertRaisesRegex(core.Refusal, 'bundle_not_prepared'):
                    ctl._sealed_bundle(row)
            adapter.recheck_bundle.assert_not_called()

    def test_container_argv_preserves_fixed_gpu_supervisor_and_exact_cache_mount(self):
        for index, row in enumerate(self.rows):
            with patch.object(legacy, 'read_secret', return_value='K' * 48):
                args = legacy.container_argv(row, {'run_id': 'a' * 32, 'generation': 2, 'attempt': 'b' * 32})
            image_position = args.index(row['runtime']['image_id'])
            self.assertEqual(legacy.command_argv(row, 'K' * 48), args[image_position + 1:])
            self.assertEqual(['--gpus', 'all'], args[args.index('--gpus'):args.index('--gpus') + 2])
            self.assertIn('/srv/spark/hf-home:/srv/spark/hf-home', args)
            self.assertIn('max-size=10m', args)
            self.assertNotIn('--entrypoint', args)
            self.assertNotIn('--rm', args)
            self.assertEqual(index >= 4, 'XDG_CACHE_HOME=' + CACHE_ROOT in args)

    def test_deployment_source_equality(self):
        deployment = Path(legacy.__file__).parent.parent / 'deployment/opt/spark/model-collection-v4/bin/legacy_runtime.py'
        self.assertEqual(Path(legacy.__file__).read_bytes(), deployment.read_bytes())


if __name__ == '__main__':
    unittest.main()
