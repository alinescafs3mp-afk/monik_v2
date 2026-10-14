"""Same retention invariants on both fixed legacy registries, no host effects."""
from unittest.mock import patch
import test_retention as retention
from test_bridge_candidate import modelctl

legacy = modelctl.legacy_support()


class GPTRetentionTests(retention.RetentionTests):
    model_id = legacy.GPT

    def setUp(self):
        super().setUp()
        self.manifest['models'] += legacy.definitions()
        for row in legacy.definitions():
            (self.logs/row['id']).mkdir()
        self.mid = self.model_id
        self.other = legacy.QWEN if self.mid == legacy.GPT else legacy.GPT
        self.ctl = self.new_controller()
        self.ctl.wall_time = lambda: self.now
        protected = patch.object(legacy, 'effective_unit', return_value={'control_safe': True})
        protected.start()
        self.addCleanup(protected.stop)


class QwenRetentionTests(GPTRetentionTests):
    model_id = legacy.QWEN
