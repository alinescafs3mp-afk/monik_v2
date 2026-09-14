export type OperationRecord = {
 operation_id:string; action:string; actor:string; status:string; created_at:string;
 revision:number; attention_revision:number; attention_required:boolean;
 needs_attention:boolean; read:boolean; read_at?:string; read_by?:string;
 targets:Array<{agent_id:string;status:string;stage:string;message:string;error_code?:string;evidence?:Record<string,unknown>}>;
};
export type OperationReadTarget={operation_id:string;attention_revision:number};
export type OperationCounts={total:number;running:number;attention:number;unread_attention:number;read:number};
export type OperationPage={operations:OperationRecord[];next_cursor?:string;counts:OperationCounts};
const targetProblems=new Set(['waiting_offline','failed','rejected','expired','unknown_result','rolled_back','unsupported']);
export function requiresAttention(op:Partial<OperationRecord>):boolean {
 // Fallback permits reading older responses, but acknowledgement requires the
 // server's versioned contract. A read receipt never means execution succeeded.
 const required=typeof op.attention_required==='boolean'?op.attention_required:
  ['attention_required','completed_with_errors'].includes(op.status||'')||(op.targets||[]).some(t=>targetProblems.has(t.status));
 return required&&op.read!==true;
}
export function readTarget(op:Pick<OperationRecord,'operation_id'|'attention_revision'>):OperationReadTarget {
 if(!op.operation_id||!Number.isSafeInteger(op.attention_revision)||op.attention_revision<1)throw new Error('Сервер не предоставил версию результата. Обновите данные перед отметкой.');
 return {operation_id:op.operation_id,attention_revision:op.attention_revision};
}
export function operationQuery(filter:string,read:string,q:string,before=''):string {
 const p=new URLSearchParams({filter,read,q,limit:'50'});if(before)p.set('before',before);
 return '/api/v1/operations?'+p.toString();
}
