"""Exact Qwen endpoint variants at the real manifest/approval boundary.

All host effects are absent. The original declarations and source pins remain
the historical reference; each new full row needs a distinct integrity seal.
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

SPECULATIVE_PAIRS = (
    ('--speculative-algorithm', 'NEXTN'),
    ('--speculative-num-steps', '2'),
    ('--speculative-eagle-topk', '1'),
    ('--speculative-num-draft-tokens', '3'),
)


def without_pairs(argv, pairs):
    """Build independent negative fixtures, never production declarations."""
    result = list(argv)
    for name, value in pairs:
        position = result.index(name)
        assert result[position + 1] == value
        del result[position:position + 2]
    return result


class ApprovedQwenDeclarations(unittest.TestCase):
    def setUp(self):
        self.original, self.client_effort, self.no_speculation = legacy.approved_qwen_declarations()

    def test_only_three_complete_distinct_declarations_and_original_unchanged(self):
        self.assertEqual(3, len(legacy.approved_qwen_declarations()))
        self.assertEqual(self.original, legacy.definitions()[0])
        self.assertEqual(3, len({legacy.digest(row) for row in legacy.approved_qwen_declarations()}))
        for row in legacy.approved_qwen_declarations():
            with self.subTest(row=legacy.digest(row)):
                self.assertIsNone(legacy.validate_row(row))
                manifest = json.loads((base.SOURCE_ROOT / 'config/controller-manifest.json').read_text())
                manifest['models'] += [row, legacy.definitions()[1]]
                self.assertIs(manifest, core.validate_manifest(manifest))

    def test_effort_variant_differs_only_in_exact_server_default(self):
        original = copy.deepcopy(self.original)
        args = original['runtime']['argv']
        position = args.index('--default-chat-template-kwargs') + 1
        self.assertEqual('{"enable_thinking": true, "reasoning_effort": "xhigh"}', args[position])
        args[position] = '{"enable_thinking": true}'
        self.assertEqual(original, self.client_effort)
        self.assertEqual(['--speculative-algorithm', 'NEXTN', '--speculative-num-steps', '2',
                          '--speculative-eagle-topk', '1', '--speculative-num-draft-tokens', '3'],
                         self.client_effort['runtime']['argv'][
                             self.client_effort['runtime']['argv'].index('--speculative-algorithm'):
                             self.client_effort['runtime']['argv'].index('--speculative-algorithm') + 8])

    def test_no_speculation_differs_only_by_all_four_exact_pairs(self):
        expected = copy.deepcopy(self.client_effort)
        expected['runtime']['argv'] = without_pairs(expected['runtime']['argv'], SPECULATIVE_PAIRS)
        self.assertEqual(expected, self.no_speculation)
        self.assertFalse([arg for arg in self.no_speculation['runtime']['argv']
                          if arg.startswith('--speculative-')])

    def test_returned_rows_are_independent_and_never_modify_historical_reference(self):
        before = legacy.definitions()
        rows = legacy.approved_qwen_declarations()
        rows[1]['runtime']['argv'].append('--unapproved')
        rows[2]['runtime']['mounts'][0]['readonly'] = False
        self.assertEqual(before[0], rows[0])
        self.assertEqual(before, legacy.definitions())
        self.assertEqual(self.client_effort, legacy.approved_qwen_declarations()[1])
        self.assertEqual(self.no_speculation, legacy.approved_qwen_declarations()[2])

    def test_partial_speculation_removals_and_original_effort_without_speculation_refused(self):
        for count in range(1, 5):
            for pairs in itertools.combinations(SPECULATIVE_PAIRS, count):
                for original in (self.original, self.client_effort):
                    changed = copy.deepcopy(original)
                    changed['runtime']['argv'] = without_pairs(changed['runtime']['argv'], pairs)
                    if changed == self.no_speculation:
                        continue
                    with self.subTest(pairs=pairs, effort=original == self.original):
                        with self.assertRaises(core.Refusal):
                            legacy.validate_row(changed)

    def test_alternative_effort_thinking_and_semantically_equivalent_json_refused(self):
        values = ['{}', '{"enable_thinking": false}', '{"enable_thinking": true, "reasoning_effort": "medium"}',
                  '{"enable_thinking":true}', '{"enable_thinking": true, "reasoning_effort": "none"}']
        for row, value in itertools.product(legacy.approved_qwen_declarations(), values):
            changed = copy.deepcopy(row)
            args = changed['runtime']['argv']
            args[args.index('--default-chat-template-kwargs') + 1] = value
            with self.subTest(value=value), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_every_non_approved_flag_change_refused(self):
        mutations = [('--context-length', '131072'), ('--mem-fraction-static', '0.90'),
                     ('--chunked-prefill-size', '16384'), ('--max-running-requests', '3'),
                     ('--mamba-ssm-dtype', 'bfloat16'), ('--quantization', 'fp8'),
                     ('--model-path', '/other-model'), ('--api-key', 'literal-secret'),
                     ('--reasoning-parser', 'other'), ('--tool-call-parser', 'other'),
                     ('--host', '127.0.0.1'), ('--port', '31002')]
        for row, (flag, value) in itertools.product(legacy.approved_qwen_declarations(), mutations):
            changed = copy.deepcopy(row)
            args = changed['runtime']['argv']
            args[args.index(flag) + 1] = value
            with self.subTest(flag=flag, variant=legacy.digest(row)), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_alternative_speculation_values_extra_reordered_or_duplicate_flags_refused(self):
        for row in legacy.approved_qwen_declarations():
            changes = [row['runtime']['argv'] + ['--speculative-algorithm', 'NONE'],
                       row['runtime']['argv'] + ['--context-length', '262144'],
                       ['--enable-cache-report'] + row['runtime']['argv'][:-1]]
            for flag, value in SPECULATIVE_PAIRS:
                if flag in row['runtime']['argv']:
                    args = list(row['runtime']['argv'])
                    args[args.index(flag) + 1] = '1' if value != '1' else '2'
                    changes.append(args)
            for args in changes:
                changed = copy.deepcopy(row)
                changed['runtime']['argv'] = args
                with self.subTest(args=args), self.assertRaises(core.Refusal):
                    legacy.validate_row(changed)

    def test_non_argv_metadata_mount_image_env_secret_and_resource_drift_refused(self):
        mutations = [lambda r: r.update(context=131072), lambda r: r.update(prepared=False),
                     lambda r: r.update(startup_timeout_seconds=2000), lambda r: r.update(stop_timeout_seconds=90),
                     lambda r: r.update(required_memory_bytes=1), lambda r: r.update(evidence_level='ACCEPTED'),
                     lambda r: r.update(extra='unapproved'), lambda r: r['runtime'].update(image='tag:latest'),
                     lambda r: r['runtime'].update(image_id='sha256:' + 'f' * 64),
                     lambda r: r['runtime']['mounts'][0].update(source='/other-model'),
                     lambda r: r['runtime']['mounts'][0].update(readonly=False),
                     lambda r: r['runtime']['env'].update(BASH_ENV='/tmp/code'),
                     lambda r: r['runtime'].update(secret_reference='/tmp/key'),
                     lambda r: r['runtime'].update(network='none'),
                     lambda r: r['runtime'].update(shm_bytes=1),
                     lambda r: r['runtime']['ulimits'].update(memlock=0),
                     lambda r: r['runtime'].update(container_name='other'),
                     lambda r: r['runtime'].update(auto_remove=False)]
        for row, mutate in itertools.product(legacy.approved_qwen_declarations(), mutations):
            changed = copy.deepcopy(row)
            mutate(changed)
            with self.subTest(variant=legacy.digest(row), changed=changed), self.assertRaises(core.Refusal):
                legacy.validate_row(changed)

    def test_gpt_cannot_adopt_qwen_exceptions(self):
        original = legacy.definitions()[1]
        self.assertIsNone(legacy.validate_row(original))
        for row in legacy.approved_qwen_declarations():
            changed = copy.deepcopy(row)
            changed['id'] = legacy.GPT
            with self.assertRaises(core.Refusal):
                legacy.validate_row(changed)
        for extra in (['--default-chat-template-kwargs', '{"enable_thinking": true}'],
                      ['--speculative-algorithm', 'NEXTN']):
            changed = copy.deepcopy(original)
            changed['runtime']['argv'] += extra
            with self.assertRaises(core.Refusal):
                legacy.validate_row(changed)
        self.assertEqual(original, legacy.definitions()[1])

    def test_profile_revisions_distinct_and_previous_or_missing_seal_refused(self):
        with tempfile.TemporaryDirectory() as tmp:
            adapter = Mock()
            store = core.StateStore(tmp, False)
            ctl = core.Controller({'models': [self.original]}, adapter, store=store,
                                  log_root=tmp, protected=False)
            revisions = []
            for row in legacy.approved_qwen_declarations():
                ctl.rows[legacy.QWEN] = row
                revisions.append(ctl.profile_revision(legacy.QWEN))
                with self.assertRaisesRegex(core.Refusal, 'bundle_not_prepared'):
                    ctl._sealed_bundle(row)
            self.assertEqual(3, len(set(revisions)))
            for old_index, new_index in itertools.permutations(range(3), 2):
                data = store.read()
                data['bundle_seals'] = {legacy.QWEN: {'profile_revision': revisions[old_index],
                                                    'witnesses': [['/compiled/artifact', [1]]]}}
                store.write(data)
                row = legacy.approved_qwen_declarations()[new_index]
                ctl.rows[legacy.QWEN] = row
                with self.subTest(old=old_index, new=new_index), \
                     self.assertRaisesRegex(core.Refusal, 'bundle_not_prepared'):
                    ctl._sealed_bundle(row)
            adapter.recheck_bundle.assert_not_called()

    def test_exact_variant_reaches_container_argv_without_secret_or_runtime_override(self):
        for row in legacy.approved_qwen_declarations():
            with patch.object(legacy, 'read_secret', return_value='K' * 48):
                args = legacy.container_argv(row, {'run_id': 'a' * 32, 'generation': 2, 'attempt': 'b' * 32})
            image_position = args.index(row['runtime']['image_id'])
            self.assertEqual(legacy.command_argv(row, 'K' * 48), args[image_position + 1:])
            self.assertEqual(1, args.count(row['runtime']['image_id']))
            self.assertIn('--gpus', args)
            self.assertIn('max-size=10m', args)
            self.assertNotIn('--entrypoint', args)

    def test_deployment_source_equality(self):
        source = Path(legacy.__file__)
        deployment = source.parent.parent / 'deployment/opt/spark/model-collection-v4/bin/legacy_runtime.py'
        self.assertEqual(source.read_bytes(), deployment.read_bytes())


if __name__ == '__main__':
    unittest.main()
