"""Offline exact-command controls for Mistral's bounded executable cache."""
import copy
import hashlib
import json
from types import SimpleNamespace
import unittest
from unittest.mock import patch

import test_bridge_candidate as base


CACHE_TMPFS = '/tmp/spark-compiled-cache:rw,exec,nosuid,nodev,size=2147483648,uid=1000,gid=1000'
CACHE_ENV = {
    'XDG_CACHE_HOME': '/tmp/spark-compiled-cache/cache',
    'TORCHINDUCTOR_CACHE_DIR': '/tmp/spark-compiled-cache/torchinductor',
    'TRITON_CACHE_DIR': '/tmp/spark-compiled-cache/triton',
    'FLASHINFER_WORKSPACE_BASE': '/tmp/spark-compiled-cache/flashinfer',
}
# Exact argv captured from the unchanged source fixtures before this correction.
GENERIC_ARGV_SHA256 = {
    'qwen3-coder-next-80b-huihui': 'c74ddc6135d13d63626a12946126d03c35f270708e1e53ef760d163f2acf836e',
    'ling-3-flash-124b-heretic-text': 'dceaf566e7b39c88e9f6299074f6fc1b320bd30d94c4ff2b20723877ae83346d',
    'nemotron-3-super-120b-heretic-v2': 'f052b45bfc90ac88c148afe216d4001de6b2fb08e9c60db1022163295d37d234',
    'minimax-m27-229b-heretic': '8f03dce38e19feacdebf5750a25aaf8de32028ff676eef20b1af633ef4772a0c',
}


def option_values(argv, option):
    return [argv[i + 1] for i, value in enumerate(argv[:-1]) if value == option]


class MistralCompilerCache(unittest.TestCase):
    def setUp(self):
        self.rows = json.loads((base.SOURCE_ROOT / 'config/controller-manifest.json').read_text())['models']
        self.row = copy.deepcopy(next(row for row in self.rows if row['id'] == base.modelctl.IDS[2]))
        self.run = {'run_id': 'a' * 32, 'generation': 7, 'attempt': 'b' * 32}

    def test_mistral_has_only_fixed_bounded_exec_cache(self):
        self.row['runtime']['env'].update(CACHE_ENV)
        argv = base.supervisor.container_argv(self.row, self.run)
        self.assertEqual(['/tmp:rw,nosuid,nodev,size=2147483648', CACHE_TMPFS], option_values(argv, '--tmpfs'))
        self.assertEqual(['1000:1000'], option_values(argv, '--user'))
        self.assertEqual(['ALL'], option_values(argv, '--cap-drop'))
        self.assertEqual(['no-new-privileges=true'], option_values(argv, '--security-opt'))
        self.assertEqual(1, argv.count('--read-only'))
        self.assertEqual(['4096'], option_values(argv, '--pids-limit'))
        self.assertEqual([str(self.row['runtime']['shm_bytes'])], option_values(argv, '--shm-size'))
        self.assertEqual(['no'], option_values(argv, '--restart'))
        self.assertNotIn('--privileged', argv)
        self.assertNotIn('--volume', argv)
        self.assertNotIn('-v', argv)
        self.assertEqual([
            'type=bind,source=' + mount['source'] + ',target=' + mount['target'] + ',readonly'
            for mount in self.row['runtime']['mounts']
        ], option_values(argv, '--mount'))
        self.assertEqual([
            key + '=' + value for key, value in sorted(self.row['runtime']['env'].items())
        ], option_values(argv, '--env'))
        self.assertEqual('/tmp/hf', self.row['runtime']['env']['HF_HOME'])
        engine_index = argv.index('--entrypoint')
        runtime = self.row['runtime']
        self.assertEqual(['--entrypoint', runtime['argv'][0], runtime['image'], *runtime['argv'][1:]], argv[engine_index:])
        self.assertNotIn('--enforce-eager', argv)
        self.assertIn('--compilation-config', argv)
        self.assertEqual(['{"cudagraph_capture_sizes":[1]}'], option_values(argv, '--compilation-config'))

    def test_four_other_generic_profiles_keep_exact_argv(self):
        for row in self.rows:
            if row['id'] not in GENERIC_ARGV_SHA256:
                continue
            with self.subTest(model=row['id']):
                argv = base.supervisor.container_argv(row, self.run)
                digest = hashlib.sha256(json.dumps(argv, separators=(',', ':')).encode()).hexdigest()
                self.assertEqual(GENERIC_ARGV_SHA256[row['id']], digest)
                self.assertEqual(1, len(option_values(argv, '--tmpfs')))
                self.assertFalse(any(',exec,' in mount for mount in option_values(argv, '--tmpfs')))

    def test_legacy_profiles_still_delegate_exact_row_and_run(self):
        for mid in base.modelctl.IDS[5:]:
            row = {'id': mid}  # No generic runtime may be read on this path.
            expected = ['unchanged-legacy-command']
            def dispatch(actual_row, actual_run):
                self.assertIs(row, actual_row)
                self.assertIs(self.run, actual_run)
                return expected
            delegate = SimpleNamespace(container_argv=dispatch)
            with self.subTest(model=mid), patch.object(base.supervisor, 'legacy_support', return_value=delegate):
                self.assertIs(expected, base.supervisor.container_argv(row, self.run))

    def test_env_delta_uses_existing_all_seven_manifest_contract(self):
        manifest = json.loads((base.SOURCE_ROOT / 'config/controller-manifest.json').read_text())
        manifest['models'] += base.modelctl.legacy_support().definitions()
        row = next(row for row in manifest['models'] if row['id'] == base.modelctl.IDS[2])
        old_env = copy.deepcopy(row['runtime']['env'])
        row['runtime']['env'].update(CACHE_ENV)
        self.assertIs(manifest, base.modelctl.validate_manifest(manifest))
        self.assertEqual(set(old_env), set(row['runtime']['env']))
        self.assertEqual(set(CACHE_ENV), {key for key in old_env if old_env[key] != row['runtime']['env'][key]})

    def test_engine_env_and_unrecognized_fields_cannot_grant_other_profiles_exec(self):
        self.row['runtime']['env'].update(CACHE_ENV)
        self.row['runtime'].update(compiled_cache_bytes=8 * 1024**3, compiled_cache_exec=True,
                                   compiled_cache_path='/tmp', tmpfs_options='exec')
        for mid in base.modelctl.IDS[:5]:
            self.row['id'] = mid
            with self.subTest(model=mid):
                mounts = option_values(base.supervisor.container_argv(self.row, self.run), '--tmpfs')
                self.assertEqual(2 if mid == base.modelctl.IDS[2] else 1, len(mounts))
                if mid == base.modelctl.IDS[2]:
                    self.assertEqual(CACHE_TMPFS, mounts[1])

    def test_existing_tmpfs_cap_cannot_resize_exec_cache(self):
        for cap in [64 * 1024**2, 8 * 1024**3]:
            with self.subTest(cap=cap):
                self.row['runtime']['tmpfs_bytes'] = cap
                argv = base.supervisor.container_argv(self.row, self.run)
                self.assertEqual(['/tmp:rw,nosuid,nodev,size=' + str(cap), CACHE_TMPFS], option_values(argv, '--tmpfs'))

    def test_deployment_mirror_and_install_pin_match_source(self):
        root = base.CANDIDATE.parent
        source = (base.CANDIDATE / 'supervisor.py').read_bytes()
        mirror = root / 'deployment/opt/spark/model-collection-v4/bin/supervisor.py'
        self.assertEqual(source, mirror.read_bytes())
        installation = json.loads((root / 'INSTALL-MANIFEST.json').read_text())
        entries = [item for item in installation['files'] if item['path'] == '/opt/spark/model-collection-v4/bin/supervisor.py']
        self.assertEqual(1, len(entries))
        self.assertEqual(hashlib.sha256(source).hexdigest(), entries[0]['sha256'])


if __name__ == '__main__':
    unittest.main()
