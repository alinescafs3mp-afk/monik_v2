"""Generate exact records through corrected real producer; no fabricated shape."""
import copy
import test_legacy_retention as fixture
from test_bridge_candidate import modelctl


class ActualGPTRetention(fixture.GPTRetentionTests):
    def test_actual_declaration_owner_start_and_release_producers_expire(self):
        self.ctl.start(self.mid)
        # Capture is injected at the existing adapter seam; the immutable record
        # and witness objects are produced by the actual corrected Controller.
        data=self.store.read();run=data['runs'][self.mid]
        run.update(legacy_declaration_sha256='a'*64,
                   owner={'unit':modelctl.unit_name(self.mid,run['run_id']),
                          'invocation_id':'e'*32,'pid':10001,'birth':'10001'},
                   container_id='c'*64,container_created='2026-10-14T20:00:00Z',
                   engine_pid=10002,engine_birth='10002')
        self.store.write(data)
        start=self.request('actual-declaration-start',action='start')
        self.ctl.submit_typed(start)
        first=self.store.read()['bridge_operations'][start['operation_id']]
        self.assertEqual(first['bound_instance']['legacy_declaration_sha256'],'a'*64)
        self.assertTrue(self.ctl._completed_retention_candidate(start['operation_id'],first,start['deadline_ms']+1))
        stop=self.request('actual-declaration-stop')
        self.ctl.submit_typed(stop)
        self.ctl.reconcile_typed(stop)
        stopped=self.store.read()['bridge_operations'][stop['operation_id']]
        self.assertEqual(stopped['receipt']['phase'],'completed')
        witness=stopped['receipt']['result']['shutdown_witness']
        self.assertEqual(witness['legacy_declaration_sha256'],'a'*64)
        self.assertTrue(self.ctl._completed_retention_candidate(stop['operation_id'],stopped,stop['deadline_ms']+1))
        for value in (True,'bad','B'*64,'b'*64,None):
            bad=copy.deepcopy(stopped)
            bad['receipt']['result']['shutdown_witness']['legacy_declaration_sha256']=value
            self.assertFalse(self.ctl._completed_retention_candidate(stop['operation_id'],bad,stop['deadline_ms']+1))
        missing=copy.deepcopy(stopped)
        del missing['receipt']['result']['shutdown_witness']['legacy_declaration_sha256']
        self.assertFalse(self.ctl._completed_retention_candidate(stop['operation_id'],missing,stop['deadline_ms']+1))
        self.now+=2
        self.submit_stop('actual-trigger')
        after=self.store.read()['bridge_operations']
        self.assertNotIn(start['operation_id'],after);self.assertNotIn(stop['operation_id'],after)

    def test_actual_pre_capture_bound_without_declaration_remains_known(self):
        self.ctl.start(self.mid)
        request=self.request('actual-pre-capture',action='start')
        self.ctl.submit_typed(request)
        source=self.store.read()['bridge_operations'][request['operation_id']]
        self.assertNotIn('legacy_declaration_sha256',source['bound_instance'])
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],source,request['deadline_ms']+1))
        for value in (True,None,'B'*64,'bad'):
            bad=copy.deepcopy(source);bad['bound_instance']['legacy_declaration_sha256']=value
            self.assertFalse(self.ctl._completed_retention_candidate(request['operation_id'],bad,request['deadline_ms']+1))


class ActualQwenRetention(ActualGPTRetention):
    model_id=fixture.QwenRetentionTests.model_id
