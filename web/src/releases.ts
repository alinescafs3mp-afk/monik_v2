// UI preflight is explanatory only. The server and agent repeat all checks.
export function releaseReason(r:any, now:unknown):string {
 if(r?.immutable!==true)return 'Старый формат: повторно импортируйте подписанный комплект';
 if(r?.trust_ok!==true)return 'Нет подтверждённого доверия к релизу';
 const expiry=Date.parse(r?.metadata_expires_at||''), time=Date.parse(String(now||''));
 if(!Number.isFinite(expiry)||!Number.isFinite(time))return 'Не удалось проверить срок действия метаданных';
 if(expiry<=time)return 'Подписанные метаданные истекли: нужен новый подписанный комплект';
 return '';
}
export function agentUpdateReason(a:any):string {
 if(a?.revoked||a?.archived)return 'Машина отозвана или архивирована';
 if(a?.managed_ready!==true)return 'Не подтверждена управляемая установка';
 if(a?.capabilities?.immutable_release_v1?.status!=='supported')return 'Сначала обновите агент: нет immutable_release_v1';
 return '';
}
export function selectedUpdateTargets(selected:string[],agents:any[]):string[]{
 const valid=new Set(agents.filter(a=>!agentUpdateReason(a)).map(a=>a.id));return [...new Set(selected)].filter(id=>valid.has(id));
}

export function importNotice(op:any):string {
 const verified=op?.status==='completed' && Array.isArray(op.targets) && op.targets.some((t:any)=>t.agent_id==='server' && t.status==='succeeded' && t.stage==='verified_catalog_commit');
 return verified ? 'Подписанный комплект проверен и опубликован отдельно от других релизов.' : 'Операция импорта сохранена. Дождитесь подтверждения публикации в Центре операций.';
}
