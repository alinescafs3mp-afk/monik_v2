"""Offline negative controls for native shutdown evidence; no API/effects."""
import copy
import datetime
import unittest

from controller_run import AGENT_ID, shutdown_witness


class ShutdownWitnessTest(unittest.TestCase):
    def setUp(self):
        self.now = datetime.datetime.now(datetime.timezone.utc)
        observed = self.now.isoformat()
        self.profile = 'synthetic-starting'
        self.detail = {'agent_session': 'current-session', 'spark': {'inventory': {'profiles': [{'profile_id': self.profile, 'profile_revision': 'current-revision'}]}, 'control_state': {'enabled': True, 'epoch': 'current-epoch'}}}
        self.state = {'profile_id': self.profile, 'quality': 'ok', 'readiness': 'not_applicable', 'supervisor_state': 'failed', 'reason': 'failed_service_shutdown_verified', 'generation': 12, 'observed_at': observed}
        self.target = {'agent_id': AGENT_ID, 'status': 'succeeded', 'stage': 'model_stopped', 'evidence': {'profile_id': self.profile, 'profile_revision': 'current-revision', 'agent_session': 'current-session', 'control_epoch': 'current-epoch', 'generation': 12, 'observed_at': observed, 'instance_identity': 'sha256:'+'a'*64, 'supervisor_state': 'failed', 'readiness': 'not_applicable', 'reason': 'failed_service_shutdown_verified', 'shutdown_verified': True}}

    def proved(self, detail=None, state=None, target=None):
        return shutdown_witness(detail or self.detail, state or self.state, self.profile, target or self.target, now=self.now)

    def test_complete_failed_proof_retains_failure(self):
        before = copy.deepcopy((self.detail, self.state, self.target))
        self.assertTrue(self.proved())
        self.assertEqual((self.detail, self.state, self.target), before)

    def test_complete_inactive_proof(self):
        self.state.update(supervisor_state='stopped', reason='')
        self.target['evidence'].update(supervisor_state='stopped', instance_identity='stopped', reason='')
        self.assertTrue(self.proved())

    def test_missing_each_receipt_proof_field_is_rejected(self):
        for key in self.target['evidence']:
            with self.subTest(key=key):
                target = copy.deepcopy(self.target)
                del target['evidence'][key]
                self.assertFalse(self.proved(target=target))

    def test_ambiguous_and_unbound_receipts_are_rejected(self):
        for key, value in [('shutdown_verified', 1), ('shutdown_verified', 'true'), ('shutdown_verified', False), ('generation', 11), ('generation', True), ('instance_identity', 'stopped'), ('instance_identity', ''), ('profile_id', 'synthetic-ready'), ('profile_revision', 'old-revision'), ('agent_session', 'old-session'), ('control_epoch', 'old-epoch'), ('supervisor_state', 'stopped'), ('reason', 'failed'), ('readiness', 'unknown')]:
            with self.subTest(key=key, value=value):
                target = copy.deepcopy(self.target)
                target['evidence'][key] = value
                self.assertFalse(self.proved(target=target))
        for key, value in [('status', 'unknown_result'), ('status', 'failed'), ('stage', 'attempt_unresolved'), ('agent_id', 'different-agent')]:
            with self.subTest(key=key, value=value):
                target = copy.deepcopy(self.target)
                target[key] = value
                self.assertFalse(self.proved(target=target))

    def test_stale_unverified_and_resurrected_current_state_is_rejected(self):
        for key, value in [('quality', 'stale'), ('quality', 'unknown'), ('readiness', 'unknown'), ('readiness', 'ready'), ('supervisor_state', 'starting'), ('supervisor_state', 'running'), ('reason', 'service_shutdown_unverified'), ('reason', ''), ('generation', 13), ('profile_id', 'foreign-profile'), ('observed_at', (self.now-datetime.timedelta(seconds=31)).isoformat()), ('observed_at', (self.now+datetime.timedelta(seconds=6)).isoformat()), ('observed_at', (self.now-datetime.timedelta(seconds=1)).isoformat())]:
            with self.subTest(key=key, value=value):
                state = copy.deepcopy(self.state)
                state[key] = value
                self.assertFalse(self.proved(state=state))

    def test_inactive_label_without_physical_proof_is_rejected(self):
        self.state.update(supervisor_state='stopped', reason='')
        self.target['evidence'].update(supervisor_state='stopped', instance_identity='stopped', reason='')
        del self.target['evidence']['shutdown_verified']
        self.assertFalse(self.proved())


if __name__ == '__main__':
    unittest.main()
