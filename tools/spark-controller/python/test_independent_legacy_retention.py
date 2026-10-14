from test_legacy_retention import GPTRetentionTests, QwenRetentionTests
class IndependentLegacyDeclarationRetention(GPTRetentionTests):
    def test_independent_immutable_legacy_binding_can_expire(self):
        self.ctl.start(self.mid)
        data=self.store.read()
        data['runs'][self.mid]['legacy_declaration_sha256']='a'*64
        self.store.write(data)
        request=self.request('completed-with-legacy-declaration',action='start')
        self.assertEqual(self.ctl.submit_typed(request)['phase'],'completed')
        operation=self.store.read()['bridge_operations'][request['operation_id']]
        self.assertEqual(operation['bound_instance']['legacy_declaration_sha256'],'a'*64)
        self.now+=2
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],operation,int(self.now*1000)),
            'MUST_FIX: actual bound_instance carries legacy_declaration_sha256 but retention rejects its known shape')
class IndependentQwenDeclarationRetention(IndependentLegacyDeclarationRetention):
    model_id=QwenRetentionTests.model_id

class IndependentOriginalQwenOwnerRetention(QwenRetentionTests):
    def test_independent_original_qwen_owner_unit_can_expire(self):
        self.ctl.start(self.mid)
        data=self.store.read()
        data['runs'][self.mid]['owner']={'unit':'spark-sglang.service','invocation_id':'e'*32,'pid':10001,'birth':'10001'}
        self.store.write(data)
        request=self.request('completed-with-original-qwen-owner',action='start')
        self.assertEqual(self.ctl.submit_typed(request)['phase'],'completed')
        operation=self.store.read()['bridge_operations'][request['operation_id']]
        self.now+=2
        self.assertTrue(self.ctl._completed_retention_candidate(request['operation_id'],operation,int(self.now*1000)),
            'MUST_FIX: original Qwen spark-sglang.service owner is rejected by template unit_name assumption')
