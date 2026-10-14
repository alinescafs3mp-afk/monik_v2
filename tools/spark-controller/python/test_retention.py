"""Finite real StateStore/Controller checks; all host effects stay injected."""
import copy
import json
import unittest

from test_bridge_candidate import CandidateFixture, modelctl


class RetentionTests(CandidateFixture):
    def setUp(self):
        self.now = 1800000000.0
        super().setUp()
        self.ctl.wall_time = lambda: self.now
        self.ctl.enable_bridge('offline-retention-session')

    def request(self, operation_id, action='stop', **changes):
        snap = self.ctl.bridge_snapshot(self.mid)
        request = {'schema_version': 1, 'operation_id': operation_id,
                   'profile_id': self.mid, 'action': action,
                   'profile_revision': snap['profile_revision'],
                   'expected_generation': snap['generation'],
                   'expected_instance': snap['instance_identity'],
                   **self.current_authority(),
                   'issued_at_ms': int(self.now * 1000),
                   'deadline_ms': int(self.now * 1000) + 1000}
        request.update(changes)
        return request

    def submit_stop(self, name, **changes):
        request = self.request(name, **changes)
        receipt = self.ctl.submit_typed(request)
        self.assertEqual(receipt['phase'], 'completed')
        return request, receipt

    def test_600_completed_expired_requests_bound_state_and_no_original_replay(self):
        originals = []
        for i in range(600):
            request, _ = self.submit_stop('retained-' + str(i))
            originals.append(request)
            self.now += 2
        data = self.store.read()
        self.assertEqual(len(data['bridge_operations']), 1)
        self.assertEqual(data['bridge_retention']['retired_count'], 599)
        self.assertEqual(len(data['bridge_retention']['retired_ids']), modelctl.BRIDGE_RETIRED_BYTES * 2)
        self.assertLess(self.store.path.stat().st_size, 64 * 1024)
        before = self.store.path.read_bytes()
        for request in originals[:-1]:
            with self.assertRaises(modelctl.Refusal):
                self.ctl.submit_typed(request)
            with self.assertRaisesRegex(modelctl.Refusal, 'lookup never dispatches'):
                self.ctl.reconcile_typed(request)
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual(self.adapter.handoffs, [])
        self.assertEqual(self.adapter.terminations, [])

    def test_original_deadline_protects_fresh_completed_and_digest_conflict(self):
        first, receipt = self.submit_stop('valid-first', deadline_ms=int(self.now * 1000)+10000)
        self.now += 1
        self.submit_stop('valid-second', deadline_ms=int(self.now * 1000)+1)
        data = self.store.read()
        self.assertEqual(set(data['bridge_operations']), {'valid-first', 'valid-second'})
        self.assertEqual(self.ctl.submit_typed(first), receipt)
        changed = {**first, 'deadline_ms': first['deadline_ms']+1}
        with self.assertRaisesRegex(modelctl.Refusal, 'digest differs'):
            self.ctl.submit_typed(changed)
        self.assertEqual(data, self.store.read())

    def test_pruned_id_changed_digest_refuses_but_distinct_new_id_succeeds(self):
        first, _ = self.submit_stop('original-id')
        self.now += 2
        self.submit_stop('trigger-prune')
        self.assertNotIn('original-id', self.store.read()['bridge_operations'])
        changed = self.request('original-id', action='start')
        with self.assertRaisesRegex(modelctl.Refusal, 'retired or bounded retention capacity'):
            self.ctl.submit_typed(changed)
        self.submit_stop('explicit-distinct-id')
        self.assertEqual(self.adapter.handoffs, [])
        self.assertEqual(self.adapter.terminations, [])

    def test_wall_clock_rollback_cannot_revive_original_expired_lease(self):
        first, _ = self.submit_stop('before-clock-rollback')
        self.now += 2
        self.submit_stop('after-prune')
        saved = self.store.path.read_bytes()
        self.now -= 3
        with self.assertRaisesRegex(modelctl.Refusal, 'durable retention floor'):
            self.ctl.submit_typed(first)
        self.assertEqual(saved, self.store.path.read_bytes())
        self.now += 4
        fresh, _ = self.submit_stop('new-short-valid', deadline_ms=int(self.now * 1000)+1)
        self.assertIn(fresh['operation_id'], self.store.read()['bridge_operations'])

    def test_all_unresolved_prepared_attempted_dispatched_and_unknown_preserved(self):
        saved = {}
        with self.store.lock():
            data = self.store.read()
            for phase in ('prepared', 'effect_attempted', 'dispatched', 'unresolved', 'unknown', 'unexpected'):
                request = self.request('keep-' + phase)
                digest = self.ctl._validate_typed(request)
                self.ctl._record_typed(data, request, digest, phase)
            saved = copy.deepcopy(data['bridge_operations'])
            self.store.write(data)
        self.now += 5
        self.submit_stop('new-completed')
        after = self.store.read()['bridge_operations']
        self.assertTrue(all(after[k] == v for k, v in saved.items()))

    def test_malformed_completed_proof_and_nonimmutable_records_never_pruned(self):
        first, _ = self.submit_stop('source')
        with self.store.lock():
            data = self.store.read()
            source = data['bridge_operations']['source']
            cases = {}
            for name in ('missing-result', 'digest-drift', 'wrong-operation', 'wrong-bool',
                         'no-release-proof', 'partial-witness', 'foreign-witness', 'extra-field'):
                row = copy.deepcopy(source)
                row['request']['operation_id'] = name
                row['receipt']['operation_id'] = name
                row['digest'] = self.ctl._validate_typed(row['request'])
                if name == 'missing-result': del row['receipt']['result']
                if name == 'digest-drift': row['digest'] = '0'*64
                if name == 'wrong-operation': row['receipt']['operation_id'] = 'foreign'
                if name == 'wrong-bool': row['receipt']['result']['process_active'] = 0
                if name == 'no-release-proof': row['receipt']['result']['shutdown_verified'] = False
                if name == 'partial-witness': del row['receipt']['result']['shutdown_witness']['owner']
                if name == 'foreign-witness': row['receipt']['result']['shutdown_witness']['model_id'] = self.other
                if name == 'extra-field': row['extra'] = True
                cases[name] = row
            data['bridge_operations'].update(cases)
            self.store.write(data)
        self.now += 5
        self.submit_stop('trigger')
        after = self.store.read()['bridge_operations']
        self.assertNotIn('source', after)
        self.assertTrue(all(after[k] == v for k, v in cases.items()))

    def test_256_unresolved_entries_fail_closed_without_deleting_or_effect(self):
        with self.store.lock():
            data = self.store.read()
            for i in range(modelctl.BRIDGE_JOURNAL_CAP):
                request = self.request('unresolved-' + str(i))
                self.ctl._record_typed(data, request, self.ctl._validate_typed(request), 'unresolved')
            self.store.write(data)
        before = self.store.path.read_bytes()
        with self.assertRaisesRegex(modelctl.Refusal, 'journal capacity'):
            self.submit_stop('one-too-many')
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual(self.adapter.handoffs, [])
        self.assertEqual(self.adapter.terminations, [])

    def test_retention_metadata_strict_shape_bounds_and_saturation_fail_closed(self):
        valid = {'schema_version': 1, 'deadline_floor_ms': int(self.now*1000)-1,
                 'retired_ids': 'ff' * modelctl.BRIDGE_RETIRED_BYTES, 'retired_count': 100000}
        for field, value in [('schema_version', True), ('deadline_floor_ms', -1),
                             ('retired_count', True), ('retired_ids', '00'),
                             ('retired_ids', 'FF' * modelctl.BRIDGE_RETIRED_BYTES),
                             ('extra', 1)]:
            with self.subTest(field=field, value=str(value)[:20]):
                malformed = {**valid, field:value}
                data = self.store.read(); data['bridge_retention'] = malformed; self.store.write(data)
                before = self.store.path.read_bytes()
                with self.assertRaisesRegex(modelctl.Refusal, 'metadata invalid'):
                    self.submit_stop('malformed-' + field)
                self.assertEqual(before, self.store.path.read_bytes())
        data = self.store.read(); data['bridge_retention'] = valid; self.store.write(data)
        before = self.store.path.read_bytes()
        with self.assertRaisesRegex(modelctl.Refusal, 'retention capacity'):
            self.submit_stop('saturated-distinct-id')
        self.assertEqual(before, self.store.path.read_bytes())

    def test_state_size_cap_preserves_last_readable_bytes_and_all_intents(self):
        self.submit_stop('existing-completed')
        before = self.store.path.read_bytes()
        data = self.store.read(); data['unexpected-large'] = 'x' * modelctl.STATE_MAX_BYTES
        with self.assertRaisesRegex(modelctl.Refusal, 'state capacity'):
            self.store.write(data)
        self.assertEqual(before, self.store.path.read_bytes())
        self.assertEqual(list(self.root.glob('.state-*')), [])

    def test_actual_started_noop_and_exact_stopped_release_receipts_can_expire(self):
        self.ctl.start(self.mid)
        start = self.request('existing-start-noop', action='start')
        self.assertEqual(self.ctl.submit_typed(start)['phase'], 'completed')
        stop = self.request('exact-stop')
        self.assertEqual(self.ctl.submit_typed(stop)['phase'], 'dispatched')
        completed = self.ctl.reconcile_typed(stop)
        self.assertEqual(completed['phase'], 'completed')
        self.assertEqual(completed['result']['shutdown_witness']['kind'], 'exact_run_resources_released')
        before = self.store.read()
        self.assertTrue(self.ctl._completed_retention_candidate(start['operation_id'], before['bridge_operations'][start['operation_id']], int(self.now*1000)+2000))
        self.assertTrue(self.ctl._completed_retention_candidate(stop['operation_id'], before['bridge_operations'][stop['operation_id']], int(self.now*1000)+2000))
        self.now += 2
        self.submit_stop('prune-after-exact-release')
        operations = self.store.read()['bridge_operations']
        self.assertNotIn(start['operation_id'], operations)
        self.assertNotIn(stop['operation_id'], operations)
        effects = (len(self.adapter.handoffs), len(self.adapter.terminations))
        for original in (start, stop):
            with self.assertRaises(modelctl.Refusal):
                self.ctl.submit_typed(original)
        self.assertEqual(effects, (len(self.adapter.handoffs), len(self.adapter.terminations)))


if __name__ == '__main__':
    unittest.main()
