import {test} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const text=await readFile(new URL('../src/inventory.ts',import.meta.url),'utf8');
const m=await import('data:text/javascript;base64,'+Buffer.from(ts.transpileModule(text,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText).toString('base64'));
test('inventory hides only explicitly inactive items, not failed watched services or older agents',()=>{
 const rows=[{id:1,state:'app_fail',inventory_state:'missing',inventory_archived:false},{id:2,inventory_archived:true},{id:3},{id:4,pinned:false,monitoring_enabled:false}];
 assert.deepEqual(m.inventoryRows(rows).map(x=>x.id),[1,3,4]);assert.equal(rows.length,4);assert.deepEqual(m.inventoryRows(rows,true),rows);assert.notEqual(m.inventoryRows(rows,true),rows);
});
test('presence vocabulary separates absence from failure and unknown capability',()=>{
 assert.match(m.inventoryNote({inventory_state:'missing'}),/повторных/);assert.match(m.inventoryNote({inventory_state:'unconfirmed'}),/подтверждения/);assert.match(m.inventoryNote({}),/не подтверждено/);assert.equal(m.inventoryNote({inventory_state:'present'}),'Порт обнаружен');
});
test('profile uses chosen public origin, accepts trailing slash and preserves IPv6',()=>{
 assert.equal(m.normalizedProfileURL(' https://46.150.103.61:8777/ '),'https://46.150.103.61:8777');assert.equal(m.normalizedProfileURL('https://[::1]:8777/'),'https://[::1]:8777');
});
test('profile rejects URL credentials, query, fragment, non-HTTPS and paths',()=>{
 for(const x of ['x','http://localhost','https://u:p@host','https://host/path','https://host/?token=x','https://host/#x','https://host:99999'])assert.throws(()=>m.normalizedProfileURL(x));
});
test('discovery profile carries CA and chosen address but no shared credential',()=>{
 const ca='-----BEGIN CERTIFICATE-----\nfixture\n-----END CERTIFICATE-----';
 const p=m.discoveryProfile('https://46.150.103.61:8777/',ca);assert.deepEqual(p,{controller_url:'https://46.150.103.61:8777',ca_cert_pem:ca,auto_discover:true});assert.throws(()=>m.discoveryProfile('https://host',ca+'\n-----BEGIN PRIVATE KEY-----'));assert.throws(()=>m.discoveryProfile('https://host','invalid'));
});
// Source contracts complement the separate actual-browser scenarios.
test('both enrollment paths use selected address and public-only trust',async()=>{
 const s=await readFile(new URL('../src/pages/AddMachine.vue',import.meta.url),'utf8');assert.ok(s.includes('controller_url: ${chosen}'));assert.ok(s.includes('makeDiscoveryProfile(profileURL.value'));assert.ok(s.includes('await checkAddress()'));assert.ok(s.includes('result.ca_cert_pem!==info.value?.ca_cert_pem'));assert.ok(!s.includes('ev.advertised_url ||'));
});
test('services offers explicit inactive history while the normal view filters it',async()=>{
 const s=await readFile(new URL('../src/pages/Services.vue',import.meta.url),'utf8');assert.ok(s.includes('inventoryRows(rows.value,showInactive.value)'));assert.ok(s.includes('Показывать исчезнувшие'));assert.ok(s.includes('/api/v1/services?inventory=all'));
});
