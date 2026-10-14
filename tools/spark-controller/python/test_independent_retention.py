import copy, unittest
from test_retention import RetentionTests, modelctl

class IndependentRetention(RetentionTests):
    def test_independent_no_effect_malformed_binding_must_be_preserved(self):
        request,_=self.submit_stop('malformed-noeffect')
        data=self.store.read()
        operation=data['bridge_operations']['malformed-noeffect']
        operation['receipt']['result']={'reason':'dispatch_expired','effect_invoked':False}
        operation['bound_instance']={'unknown':'must preserve evidence'}
        self.store.write(data)
        saved=copy.deepcopy(operation)
        self.now+=2
        self.ctl.submit_typed(self.request('trigger-independent'))
        self.assertEqual(self.store.read()['bridge_operations'].get('malformed-noeffect'),saved,
                         'MUST_FIX: malformed bound_instance deleted by no-effect early return')

    def test_independent_start_optional_proof_types_must_be_preserved(self):
        self.ctl.start(self.mid)
        request=self.request('malformed-start-proof',action='start')
        self.assertEqual(self.ctl.submit_typed(request)['phase'],'completed')
        data=self.store.read()
        operation=data['bridge_operations']['malformed-start-proof']
        operation['receipt']['result']['shutdown_verified']='ambiguous'
        operation['receipt']['result']['shutdown_witness']={'unrecognized':True}
        operation['receipt']['result']['instance_identity']=True
        self.store.write(data)
        saved=copy.deepcopy(operation)
        self.now+=2
        self.ctl.submit_typed(self.request('trigger-independent'))
        self.assertEqual(self.store.read()['bridge_operations'].get('malformed-start-proof'),saved,
                         'MUST_FIX: malformed optional runtime evidence deleted')

    def test_independent_retired_bloom_roundtrip_no_false_negative(self):
        request,_=self.submit_stop('template-independent')
        source=copy.deepcopy(self.store.read()['bridge_operations']['template-independent'])
        self.now+=2
        data=self.store.read()
        ids=['independent-bloom-'+str(i) for i in range(2048)]
        for base in range(0,len(ids),128):
            for operation_id in ids[base:base+128]:
                operation=copy.deepcopy(source)
                operation['request']['operation_id']=operation_id
                operation['receipt']['operation_id']=operation_id
                operation['digest']=self.ctl._validate_typed(operation['request'])
                data['bridge_operations'][operation_id]=operation
            self.ctl._prune_completed_operations(data,'keep-absent')
            self.store.write(data)
            data=self.store.read()
        self.assertEqual(len(data['bridge_retention']['retired_ids']),32768)
        for operation_id in ids:
            fresh=self.request(operation_id)
            with self.assertRaises(modelctl.Refusal):
                self.ctl._check_retired_request(data,fresh)
        self.assertEqual(self.adapter.handoffs,[])
        self.assertEqual(self.adapter.terminations,[])
