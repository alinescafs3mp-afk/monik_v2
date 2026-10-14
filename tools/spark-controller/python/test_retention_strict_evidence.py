import copy
import test_retention as retention


class StrictRetentionEvidence(retention.RetentionTests):
    def test_noeffect_also_validates_complete_bound_instance(self):
        self.ctl.start(self.mid)
        request=self.request('complete-noeffect',action='start')
        run=self.store.read()['runs'][self.mid]
        data=self.store.read()
        self.ctl._record_typed(data,request,self.ctl._validate_typed(request),'completed',run,
                               {'reason':'dispatch_expired','effect_invoked':False})
        source=data['bridge_operations'][request['operation_id']]
        expired=request['deadline_ms']+1
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],source,expired))
        for change in (
                lambda o:o.update(bound_instance={'unknown':True}),
                lambda o:o['bound_instance'].update(extra=True),
                lambda o:o['bound_instance'].update(generation=True),
                lambda o:o['bound_instance'].update(owner={'unit':'unexpected'}),
                lambda o:o['bound_instance'].update(container_id='a'*64),
                lambda o:o['bound_instance'].update(engine_pid=True,engine_birth='1'),
                lambda o:o['bound_instance'].update(legacy_declaration_sha256='a'*64),
                lambda o:o['receipt'].update(attempt=None)):
            operation=copy.deepcopy(source);change(operation)
            self.assertFalse(self.ctl._completed_retention_candidate(request['operation_id'],operation,expired))

    def test_every_optional_runtime_evidence_field_is_checked_before_start_eligibility(self):
        self.ctl.start(self.mid)
        request=self.request('complete-start',action='start')
        self.ctl.submit_typed(request)
        source=self.store.read()['bridge_operations'][request['operation_id']]
        expired=request['deadline_ms']+1
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],source,expired))
        for fields in ({'instance_identity':True},{'instance_identity':'f'*64},
                       {'shutdown_verified':'ambiguous'},{'shutdown_verified':1},
                       {'shutdown_witness':{'unknown':True}},
                       {'shutdown_verified':False,'shutdown_witness':None},
                       {'shutdown_verified':True}, {'arbitrary_optional':False}):
            operation=copy.deepcopy(source);operation['receipt']['result'].update(fields)
            self.assertFalse(self.ctl._completed_retention_candidate(request['operation_id'],operation,expired),fields)
        valid=copy.deepcopy(source)
        valid['receipt']['result'].update(instance_identity=self.ctl.instance_identity(valid['bound_instance']),
                                         shutdown_verified=False)
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],valid,expired))

    def test_malformed_optionals_stay_byte_equal_through_actual_prune_transaction(self):
        self.ctl.start(self.mid)
        request=self.request('stay-immutable',action='start')
        self.ctl.submit_typed(request)
        data=self.store.read()
        bad=data['bridge_operations'][request['operation_id']]
        bad['receipt']['result']['instance_identity']=False
        saved=copy.deepcopy(bad)
        self.store.write(data)
        self.now+=2
        self.ctl.submit_typed(self.request('new-distinct',action='start'))
        self.assertEqual(self.store.read()['bridge_operations'][request['operation_id']],saved)
