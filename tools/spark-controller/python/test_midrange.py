"""Fixed native catalog additions; real controller, injected host effects only."""
import copy
import itertools
import json
import unittest
from unittest.mock import patch
import test_bridge_candidate as base

core = base.modelctl


def fixture_rows(manifest, prepared=False):
    """Synthetic rows exercise schema/routing, never qualify physical models."""
    rows = []
    for index, mid in enumerate(core.MIDRANGE_IDS):
        row = copy.deepcopy(manifest['models'][2 if index == 1 else 1])
        row.update(id=mid, name='OFFLINE fixture ' + mid,
                   engine='vLLM' if index == 1 else 'llama.cpp CUDA',
                   quant=['MXFP4_MOE', 'NVFP4_AWQ', 'Q5_K_M'][index],
                   endpoint='http://127.0.0.1:' + str(31006 + index) + '/v1',
                   prepared=prepared, block_reason='OFFLINE synthetic; no physical acceptance')
        args = row['runtime']['argv']
        flag = '--served-model-name' if index == 1 else '--alias'
        args[args.index(flag) + 1] = mid
        args[args.index('--port') + 1] = str(31006 + index)
        if index == 1:
            # Synthetic host effects retain the exact production Gemma authority.
            row['runtime']['env'].update(core.GEMMA_FIXED_ENV)
            for source, item in core.GEMMA_FIXED_FILES.items():
                row['runtime']['mounts'].append(dict(source=source,
                    target=item['target'], readonly=True))
                row['artifacts'].append(dict(path=source, size=item['size'],
                    sha256=item['sha256']))
        rows.append(row)
    return rows


class MidrangeCatalog(unittest.TestCase):
    def setUp(self):
        self.manifest = json.loads((base.SOURCE_ROOT / 'config/controller-manifest.json').read_text())
        self.base5 = copy.deepcopy(self.manifest)
        self.manifest['models'] += core.legacy_support().definitions()

    def test_old5_old7_and_every_fixed_subset_of_three(self):
        self.assertIs(self.base5, core.validate_manifest(self.base5))
        added = fixture_rows(self.manifest)
        for n in range(4):
            for subset in itertools.combinations(added, n):
                m = copy.deepcopy(self.manifest)
                m['models'] += copy.deepcopy(subset)
                with self.subTest(ids=[r['id'] for r in subset]):
                    self.assertIs(m, core.validate_manifest(m))

    def test_catalog_duplicates_unknown_and_missing_old_row_refused(self):
        m = copy.deepcopy(self.manifest)
        m['models'] += fixture_rows(m)
        for case in ['duplicate', 'unknown', 'missing_legacy', 'nonrow']:
            bad = copy.deepcopy(m)
            if case == 'duplicate': bad['models'].append(copy.deepcopy(bad['models'][-1]))
            if case == 'unknown': bad['models'][-1]['id'] = 'arbitrary-user-model'
            if case == 'missing_legacy': del bad['models'][5]
            if case == 'nonrow': bad['models'][-1] = None
            with self.subTest(case=case), self.assertRaises(core.Refusal):
                core.validate_manifest(bad)

    def test_wrong_engine_quant_and_colliding_loopback_port_refused(self):
        for index, row in enumerate(fixture_rows(self.manifest)):
            for key, value in [('engine', 'arbitrary'), ('quant', 'BF16'),
                               ('endpoint', self.manifest['models'][0]['endpoint'])]:
                m = copy.deepcopy(self.manifest)
                r = copy.deepcopy(row); r[key] = value; m['models'].append(r)
                with self.subTest(mid=row['id'], key=key), self.assertRaises(core.Refusal):
                    core.validate_manifest(m)

    def test_native_supervisor_does_not_request_legacy_secret_or_privileges(self):
        run = {'run_id':'a'*32, 'generation':1, 'attempt':'b'*32}
        with patch.object(base.supervisor, 'legacy_support', side_effect=AssertionError('legacy path forbidden')):
            for row in fixture_rows(self.manifest, prepared=True):
                args = base.supervisor.container_argv(row, run)
                with self.subTest(mid=row['id']):
                    self.assertIn('--user', args)
                    self.assertIn('1000:1000', args)
                    self.assertIn('--read-only', args)
                    self.assertIn('no-new-privileges=true', args)
                    self.assertIn('--entrypoint', args)
                    self.assertNotIn('--privileged', args)
                    self.assertEqual('no', args[args.index('--restart') + 1])

    def test_bridge_fixed10_observations_and_unknown_duplicate_refusals(self):
        body = {'schema_version':1, 'method':'observe_many', 'profile_ids':list(core.IDS)}
        self.assertEqual(body, base.bridge.decode_envelope(json.dumps(body).encode()))
        self.assertEqual(tuple(core.IDS), tuple(base.bridge.IDS))
        for ids in [list(core.IDS)+['unknown'], [core.MIDRANGE_IDS[0]]*2, list(core.IDS)+[core.IDS[0]]]:
            with self.assertRaises(ValueError):
                base.bridge.decode_envelope(json.dumps({**body, 'profile_ids':ids}).encode())

    def test_new_models_cannot_capture_legacy_declaration(self):
        for mid in core.MIDRANGE_IDS:
            with self.subTest(mid=mid), self.assertRaises(core.ObservationError):
                core.Controller.captured_legacy_declaration({'model_id':mid, 'legacy_declaration_sha256':'a'*64})
        for mid in core.LEGACY_IDS:
            self.assertEqual('a'*64, core.Controller.captured_legacy_declaration({'model_id':mid, 'legacy_declaration_sha256':'a'*64}))


class MidrangeTransactions(base.CandidateFixture):
    def setUp(self):
        super().setUp()
        self.added = fixture_rows(self.manifest, prepared=True)
        self.manifest['models'] += core.legacy_support().definitions() + self.added
        for row in self.added:
            (self.logs / row['id']).mkdir()
        self.ctl = self.new_controller()

    def test_exact_new_start_stop_and_foreign_reservation_retained(self):
        for mid in core.MIDRANGE_IDS:
            with self.subTest(mid=mid):
                snap = self.ctl.start(mid)
                self.assertEqual('READY', snap['state'])
                self.assertEqual(mid, self.store.read()['reservation'])
                before = len(self.adapter.handoffs)
                with self.assertRaises(core.Refusal): self.ctl.start(self.mid)
                self.assertEqual(before, len(self.adapter.handoffs))
                self.ctl.stop(mid)
                self.assertIsNone(self.store.read()['reservation'])
        self.assertEqual(3, len(self.adapter.handoffs))
        self.assertEqual(3, len(self.adapter.terminations))
        self.assertEqual([], self.adapter.forces)

    def test_disabled_and_stale_seal_refuse_before_any_handoff(self):
        self.ctl.enable_bridge('offline-midrange-only')
        for row in self.added:
            mid = row['id']; row['prepared'] = False
            with self.assertRaises(core.Refusal): self.ctl.start(mid)
            row['prepared'] = True
            self.ctl.prepare_bundle(mid)
            self.bundle_file.write_bytes(self.bundle_file.read_bytes() + b'!')
            with self.assertRaises(core.Refusal): self.ctl._sealed_bundle(row)
        self.assertEqual([], self.adapter.handoffs)
        self.assertEqual([], self.adapter.terminations)


if __name__ == '__main__':
    unittest.main()
