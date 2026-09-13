import { test, beforeEach } from 'node:test';
import assert from 'node:assert/strict';
import { readFile } from 'node:fs/promises';
import ts from 'typescript';

async function load(name) {
  const text = await readFile(new URL('../src/' + name + '.ts', import.meta.url), 'utf8');
  const js = ts.transpileModule(text, { compilerOptions: { target: ts.ScriptTarget.ES2022, module: ts.ModuleKind.ESNext } }).outputText;
  return import('data:text/javascript;base64,' + Buffer.from(js).toString('base64'));
}
const api = await load('api'), format = await load('format');
function response(data, status = 200) { return new Response(JSON.stringify(data), { status, headers: { 'Content-Type': 'application/json' } }); }
beforeEach(() => {
  const values = new Map();
  globalThis.sessionStorage = { getItem: key => values.get(key) || null, setItem: (key,value) => values.set(key,value), removeItem: key => values.delete(key) };
  globalThis.window = new EventTarget();
  api.setCsrf('csrf-test');
});

test('successful action has an idempotency key and clears pending state', async () => {
  globalThis.fetch = async (path, init) => { assert.equal(path, '/api/v1/operations'); assert.ok(JSON.parse(init.body).client_request_key); assert.equal(init.headers.get('X-CSRF-Token'), 'csrf-test'); return response({ operation_id:'op', status:'completed' }); };
  const r = await api.submitOp('agent.pin', { agent_id:'a', pinned:true });
  assert.equal(r.op.operation_id, 'op'); assert.equal(api.pendingRequests().length, 0);
});
test('lost response recovers the original operation without a second mutation', async () => {
  let writes = 0;
  globalThis.fetch = async path => { if (path === '/api/v1/operations') { writes++; throw new Error('lost response'); } return response({ found:true, operation:{operation_id:'saved', status:'running'} }); };
  const r = await api.submitOp('agent.collect_now', {}, ['a']);
  assert.equal(r.op.operation_id, 'saved'); assert.equal(writes, 1); assert.equal(api.pendingRequests().length, 0);
});
test('unknown outcome survives another click and never blindly resubmits', async () => {
  let writes = 0;
  globalThis.fetch = async path => { if (path === '/api/v1/operations') { writes++; throw new Error('lost'); } return response({found:false}); };
  await assert.rejects(api.submitOp('agent.collect_now', {}, ['a']), e => e.error === 'outcome_unknown');
  const key = api.pendingRequests()[0].key;
  await assert.rejects(api.submitOp('agent.collect_now', {}, ['a']), e => e.error === 'outcome_unknown');
  assert.equal(writes, 1); assert.equal(api.pendingRequests()[0].key, key);
});
test('pending browser state never contains secret request parameters', async () => {
  globalThis.fetch = async () => { throw new Error('offline'); };
  await assert.rejects(api.submitOp('secret.replace', {value:'PRIVATE_TEST_SECRET'}, ['a']));
  const raw = sessionStorage.getItem('monik:pending-operations:v1'); assert.ok(raw); assert.ok(!raw.includes('PRIVATE_TEST_SECRET'));
});
test('definitive 501 is actionable, not ambiguous acceptance', async () => {
  globalThis.fetch = async () => response({error:'not_implemented', message:'Release gate missing'}, 501);
  await assert.rejects(api.submitOp('update.rollout', {}, ['a']), e => e.status === 501 && e.error === 'not_implemented');
  assert.equal(api.pendingRequests().length, 0);
});
test('completed-with-errors cannot generate a successful result', async () => {
  globalThis.fetch = async () => response({operation_id:'failed-op', status:'completed_with_errors', targets:[{message:'Unknown machine'}]});
  await assert.rejects(api.submitOp('agent.pin', {agent_id:'bad'}), e => e.error === 'operation_attention' && e.operation_id === 'failed-op');
});
test('storage failure prevents unsafe mutation submission', async () => {
  let calls=0; sessionStorage.setItem=()=>{throw new Error('denied')}; globalThis.fetch=async()=>{calls++;return response({})};
  await assert.rejects(api.submitOp('agent.pin'), e=>e.error==='storage'); assert.equal(calls,0);
});
test('HTML in a 200 response is not treated as JSON success', async()=>{
  globalThis.fetch=async()=>new Response('<html>login</html>',{status:200});
  await assert.rejects(api.get('/api/v1/overview'), e=>e.error==='invalid_response'&&e.status===0);
});
test('null, unavailable, and zero measurements remain distinct',()=>{
  assert.equal(format.number(null,'%'),'Нет данных'); assert.equal(format.number(undefined),'Нет данных');
  assert.equal(format.number(0,'%'),'0%'); assert.equal(format.bytes(-1),'Нет данных'); assert.equal(format.bytes(1073741824),'1 GiB');
});
