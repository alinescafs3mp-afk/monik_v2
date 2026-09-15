import {test,beforeEach} from 'node:test';
import assert from 'node:assert/strict';
import {readFile} from 'node:fs/promises';
import ts from 'typescript';
const source=await readFile(new URL('../src/api.ts',import.meta.url),'utf8');
const js=ts.transpileModule(source,{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.ESNext}}).outputText;
const api=await import('data:text/javascript;base64,'+Buffer.from(js).toString('base64'));
const expiry='2030-01-01T00:00:00Z';
function binary(extra={}){return new Response(new Uint8Array(128),{status:200,headers:{'Content-Type':'application/octet-stream','Content-Length':'128','X-Monik-Profile-Expires':expiry,...extra}});}
beforeEach(()=>{api.setCsrf('csrf');api.setReauthHandler(null);globalThis.window=new EventTarget();globalThis.sessionStorage={getItem(){throw new Error('installer must not use browser credential storage')},setItem(){throw new Error('installer must not persist credential')}};});
test('one-file download has CSRF and fixed platform endpoint with exact bytes',async()=>{
 globalThis.fetch=async(path,r)=>{assert.equal(path,'/api/v1/installers/linux-amd64');assert.equal(r.method,'POST');assert.equal(r.credentials,'same-origin');assert.equal(r.headers['X-CSRF-Token'],'csrf');assert.deepEqual(JSON.parse(r.body),{controller_url:'https://controller.example:8777'});return binary();};
 const r=await api.downloadInstaller('linux-amd64','https://controller.example:8777');assert.equal(r.blob.size,128);assert.equal(r.expires,expiry);
});
test('download rejects invalid platform before request',async()=>{let n=0;globalThis.fetch=async()=>{n++;return binary();};await assert.rejects(api.downloadInstaller('../anything','https://c'));assert.equal(n,0);});
test('HTML login fallback and truncated installer cannot look downloaded',async()=>{
 for(const headers of [{'Content-Type':'text/html'},{'Content-Length':'129'},{'Content-Length':'0'},{'Content-Length':'999999999'},{'X-Monik-Profile-Expires':'invalid'}]){globalThis.fetch=async()=>binary(headers);await assert.rejects(api.downloadInstaller('linux-amd64','https://c'));}
});
test('interrupted installer download does not silently mint another code',async()=>{let n=0;globalThis.fetch=async()=>{n++;throw new TypeError('connection dropped')};await assert.rejects(api.downloadInstaller('linux-amd64','https://c'),e=>e.error==='network');assert.equal(n,1);});
test('only definite recent-auth denial permits a single repeat',async()=>{let n=0,p=0;api.setReauthHandler(async()=>{p++;return true});globalThis.fetch=async()=>{n++;return n===1?new Response(JSON.stringify({error:'recent_auth_required'}),{status:401}):binary()};await api.downloadInstaller('linux-amd64','https://c');assert.equal(n,2);assert.equal(p,1);});
test('cancelled or repeatedly denied recent-auth never loops',async()=>{
 for(const approve of [false,true]){let n=0,p=0;api.setReauthHandler(async()=>{p++;return approve});globalThis.fetch=async()=>{n++;return new Response(JSON.stringify({error:'recent_auth_required'}),{status:401})};await assert.rejects(api.downloadInstaller('linux-amd64','https://c'));assert.equal(n,approve?2:1);assert.equal(p,1);}
});
test('failed server download is visible rather than operation success',async()=>{let n=0;globalThis.fetch=async()=>{n++;return new Response(JSON.stringify({error:'installer_unavailable',message:'missing template'}),{status:409})};await assert.rejects(api.downloadInstaller('linux-arm64','https://c'),e=>e.error==='installer_unavailable');assert.equal(n,1);});
test('simple setup is primary and older profile path remains accessible',async()=>{
 const component=await readFile(new URL('../src/components/InstallerDownload.vue',import.meta.url),'utf8'),page=await readFile(new URL('../src/pages/AddMachine.vue',import.meta.url),'utf8');
 assert.match(component,/chmod \+x monik-agent/);assert.match(component,/sudo \.\/monik-agent/);assert.match(component,/role="status"/);assert.match(component,/role="alert"/);assert.match(component,/одной новой машины/);assert.match(component,/Это не подтверждение установки/);assert.match(component,/URL\.revokeObjectURL/);
 assert.ok(page.indexOf('<InstallerDownload')<page.indexOf('<details'));assert.match(page,/advanced-enrollment/);assert.match(page,/installerBusy/);
});
