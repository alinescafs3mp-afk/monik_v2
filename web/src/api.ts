// Presentation clock only, never used for authorization or operation deadlines.
let clockAnchor: {server:number; received:number; request:number} | null=null;
export function observeServerTime(value: unknown, request: number, received=performance.now()) {
  const server=typeof value==='string'?Date.parse(value):NaN;
  if(!Number.isFinite(server)||!Number.isFinite(request)||!Number.isFinite(received)||received<request)return;
  if(clockAnchor&&request<clockAnchor.request)return; // late older response
  clockAnchor={server,received,request};
}
export function serverNow(monotonic=performance.now()): number {
  return clockAnchor ? clockAnchor.server+Math.max(0,monotonic-clockAnchor.received) : Date.now();
}
export type ApiError = { error: string; message: string; status: number; operation_id?: string };
const state = { csrf: "", stream: "unknown" as "live" | "paused" | "unknown" };
export const csrf = () => state.csrf;
export const setCsrf = (v: string) => { state.csrf = v; };
export const streamState = () => state.stream;
export const setStream = (v: typeof state.stream) => { state.stream = v; };
export function newKey() { return crypto.randomUUID(); }
let reauthHandler: ((action:string)=>Promise<boolean>) | null=null;
export function setReauthHandler(handler:typeof reauthHandler){reauthHandler=handler;}
const activeSubmissions = new Set<string>();

export async function api<T>(path: string, init: RequestInit = {}): Promise<T> {
  const requestStarted=performance.now();
  const headers = new Headers(init.headers);
  if (init.body && !headers.has("Content-Type")) headers.set("Content-Type", "application/json");
  if (init.method && !["GET","HEAD"].includes(init.method) && state.csrf) headers.set("X-CSRF-Token", state.csrf);
  const ctl = new AbortController(), timeout = setTimeout(() => ctl.abort(), 15000);
  const abort = () => ctl.abort(); init.signal?.addEventListener("abort", abort, { once: true });
  if (init.signal?.aborted) ctl.abort();
  try {
    let res: Response;
    try { res = await fetch(path, { ...init, headers, credentials: "same-origin", signal: ctl.signal }); }
    catch { throw { error: "network", message: "Связь с сервером потеряна или истекло время ожидания", status: 0 } satisfies ApiError; }
    let data: any;
    try { data = await res.json(); }
    catch { throw { error: "invalid_response", message: "Сервер вернул неполный или некорректный ответ", status: res.ok ? 0 : res.status } satisfies ApiError; }
    if (!res.ok) throw { error: data?.error || "http", message: data?.message || res.statusText, status: res.status } satisfies ApiError;
    observeServerTime(data?.server_time,requestStarted);
    return data as T;
  } finally { clearTimeout(timeout); init.signal?.removeEventListener("abort", abort); }
}
export const get = <T>(path: string) => api<T>(path);
export const post = <T>(path: string, body: unknown, headers: HeadersInit = {}) => api<T>(path, { method: "POST", body: JSON.stringify(body), headers });

type Pending = { key: string; signature: string; action: string; created: string };
const pendingKey = "monik:pending-operations:v1";
export function pendingRequests(): Pending[] { try { const p = JSON.parse(sessionStorage.getItem(pendingKey) || "[]"); return Array.isArray(p) ? p : []; } catch { return []; } }
function persist(p: Pending[]) { try { sessionStorage.setItem(pendingKey, JSON.stringify(p)); } catch { throw { error: "storage", status: 400, message: "Хранилище браузера недоступно. Запрос не отправлен: невозможно сохранить ключ безопасного повтора." }; } }
function forget(key: string) { persist(pendingRequests().filter(p => p.key !== key)); }
function announceError(e: ApiError) { window.dispatchEvent(new CustomEvent("monik:error", { detail: e })); }
export async function reconcileOperation(key: string) {
  const result = await post<{ found: boolean; operation?: Record<string, unknown> }>("/api/v1/operations/lookup", { client_request_key: key });
  if (result.found && result.operation) forget(key);
  return result;
}
function requireSuccessfulResult(op: Record<string, unknown>) {
  if (["completed_with_errors","attention_required"].includes(String(op.status))) {
    const reasons = (op.targets as Array<{message?:string}> || []).map(t => t.message).filter(Boolean).join("; ");
    throw { error: "operation_attention", message: reasons || "Операция требует внимания", status: 409, operation_id: String(op.operation_id) } satisfies ApiError;
  }
}
export async function submitOp(action: string, params: Record<string, unknown> = {}, targetIds: string[] = [], targetMode = "") {
  let activeSignature="";
  try {
    // Persist metadata and a digest, never plaintext secret parameters.
    const body = JSON.stringify({ action, params, target_ids: [...targetIds].sort(), target_mode: targetMode });
    const digest = await crypto.subtle.digest("SHA-256", new TextEncoder().encode(body));
    const signature = Array.from(new Uint8Array(digest), b => b.toString(16).padStart(2,"0")).join("");
    if(activeSubmissions.has(signature))throw {error:'action_busy',status:409,message:'Этот запрос уже выполняется. Дождитесь его результата.'};
    activeSubmissions.add(signature); activeSignature=signature;
    const old = pendingRequests().find(p => p.signature === signature);
    const key = old?.key || newKey();
    if (old) {
      const looked = await reconcileOperation(key);
      if (looked.found && looked.operation) { requireSuccessfulResult(looked.operation); return { key, op: looked.operation, unknown: false }; }
      throw { error:"outcome_unknown", status:409, message:`Исход операции ${key} ещё неизвестен. Повторная команда не отправлена; проверьте её в Центре операций.` } satisfies ApiError;
    }
    const pending = pendingRequests(); if (pending.length >= 100) throw { error:"pending_limit", status:409, message:"Сначала проверьте незавершённые запросы в Центре операций." } satisfies ApiError;
    persist([...pending, { key, signature, action, created: new Date().toISOString() }]);
    let op: Record<string, unknown>;
    try {
      const request={ action, client_request_key:key, params, target_ids:targetIds, target_mode:targetMode || undefined };
      try {op = await post("/api/v1/operations", request);}
      catch(e){
        const err=e as ApiError;
        if(err.error!=='recent_auth_required' || ![401,403].includes(err.status) || !reauthHandler)throw e;
        // This exact response proves no operation was accepted. Forget the
        // pending marker while asking, then retry once using the SAME key.
        forget(key);
        if(!await reauthHandler(action))throw {error:'action_cancelled',status:400,message:'Действие отменено. Команда не выполнялась.'} satisfies ApiError;
        persist([...pendingRequests(),{key,signature,action,created:new Date().toISOString()}]);
        op=await post("/api/v1/operations",request);
      }
    } catch (e) {
      const err = e as ApiError;
      if (err.status === 0 || err.status >= 500 && err.status !== 501) {
        try { const found = await reconcileOperation(key); if (found.found && found.operation) { requireSuccessfulResult(found.operation); return { key, op: found.operation, unknown:false }; } } catch (lookupError) { if ((lookupError as ApiError).error === "operation_attention") throw lookupError; }
        throw { error:"outcome_unknown", status:409, message:`Ответ потерян. Исход операции ${key} проверяется по исходному ключу. Повторная команда не отправлена.` } satisfies ApiError;
      }
      forget(key); throw err;
    }
    forget(key); requireSuccessfulResult(op);
    return { key, op, unknown:false };
  } catch (e) { if((e as ApiError).error!=="action_cancelled")announceError(e as ApiError); throw e; } finally {if(activeSignature)activeSubmissions.delete(activeSignature);}
}

// Ticket creation has no remote effect. Only a definite recent-auth rejection
// may be retried; never persist the SSH password/private key in pending jobs.
export async function consoleTicket(path:string,targetRevision:string):Promise<{ticket:string}> {
 try{return await post(path,{target_revision:targetRevision});}catch(e){
  if((e as ApiError).error==='recent_auth_required'&&[401,403].includes((e as ApiError).status)&&reauthHandler){
   if(!await reauthHandler('Открыть консоль машины'))throw {message:'Подключение отменено.'};
   return await post(path,{target_revision:targetRevision});
  }throw e;
 }
}

// Read state is idempotent, version-bound server metadata, never an agent job.
export async function markOperationsRead(targets: Array<{operation_id:string;attention_revision:number}>, read:boolean) {
 const result=await post<{saved:boolean;count:number}>("/api/v1/operations/read",{targets,read});
 if(result?.saved!==true)throw {error:"invalid_response",status:0,message:"Подтверждение сохранения не получено. Перечитайте операции."} satisfies ApiError;
 window.dispatchEvent(new Event("monik:refresh"));
 return result;
}

// A prepared executable is a secret-bearing download, not a new fleet command.
// Only a definite recent-auth rejection is retried. A lost download is NOT
// repeated automatically; an unused one-machine code expires server-side.
export async function downloadInstaller(platform:string,controllerURL:string,enableAgentConsole=false):Promise<{blob:Blob;expires:string}> {
 if(!['linux-amd64','linux-arm64'].includes(platform))throw {message:'Неподдерживаемая платформа установщика.'};
 const request=async()=>{
  const ctl=new AbortController(), timer=setTimeout(()=>ctl.abort(),120000);
  try{
   const r=await fetch(`/api/v1/installers/${platform}`,{method:'POST',credentials:'same-origin',headers:{'Content-Type':'application/json','X-CSRF-Token':state.csrf},body:JSON.stringify({controller_url:controllerURL,...(enableAgentConsole?{enable_agent_console:true}:{})}),signal:ctl.signal});
   if(!r.ok){let data:any;try{data=await r.json();}catch{data={};}throw {status:r.status,error:data.error||'http',message:data.message||'Не удалось подготовить установщик.'};}
   const length=Number(r.headers.get('Content-Length'));
   if(r.headers.get('Content-Type')?.split(';')[0]!=='application/octet-stream'||!Number.isSafeInteger(length)||length<64||length>193*1024*1024)throw {error:'invalid_download',message:'Некорректный размер или тип установщика.'};
   const expires=r.headers.get('X-Monik-Profile-Expires')||'';
   if(!Number.isFinite(Date.parse(expires)))throw {message:'Срок профиля не подтверждён.'};
   const blob=await r.blob();if(blob.size!==length)throw {message:'Файл получен не полностью. Установка не готова.'};
   return {blob,expires};
  }catch(e){if(e instanceof TypeError || (e as Error).name==='AbortError')throw {error:'network',message:'Загрузка прервалась. Готовый файл не подтверждён; автоматического повтора нет.'};throw e;}
  finally{clearTimeout(timer);}
 };
 try{return await request();}catch(e){
  if((e as ApiError).error==='recent_auth_required'&&[401,403].includes((e as ApiError).status)&&reauthHandler){
   if(!await reauthHandler('Скачать установщик для одной машины'))throw {message:'Загрузка отменена.'};return request();
  }throw e;
 }
}

// Configure a fixed SSH destination, not a command. Only a definite pre-effect
// authentication rejection is retried. Unknown saves require an explicit read.
export async function saveConsoleConfiguration(id:string,body:unknown):Promise<void> {
 const path=`/api/v1/agents/${encodeURIComponent(id)}/console-config`;
 let result:{saved:boolean};
 try{result=await post(path,body);}catch(e){
  const err=e as ApiError;
  if(err.error==='recent_auth_required'&&[401,403].includes(err.status)&&reauthHandler){
   if(!await reauthHandler('Изменить SSH-настройки'))throw {error:'action_cancelled',message:'Изменение отменено. SSH-настройки не отправлялись.'};
   result=await post(path,body);
  }else throw e;
 }
 if(result?.saved!==true)throw {error:'outcome_unknown',status:0,message:'Подтверждение сохранения не получено. Перечитайте SSH-настройки; автоматического повтора нет.'};
}
