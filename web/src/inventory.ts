/** Inventory absence is distinct from check health, pause and screen selection. */
export function inventoryRows<T extends {inventory_archived?:boolean}>(rows:T[] = [], includeInactive=false):T[]{
 return includeInactive ? [...rows] : rows.filter(s=>s.inventory_archived!==true);
}
export function inventoryNote(service:any):string{
 if(service.inventory_state==='missing')return 'Порт не найден в повторных полных обходах';
 if(service.inventory_state==='unconfirmed')return 'Порт не найден в последнем обходе; ждём повторного подтверждения';
 if(service.inventory_state==='present')return 'Порт обнаружен';
 return 'Наличие порта ещё не подтверждено новым агентом';
}
export function normalizedProfileURL(value:string):string{
 let u:URL;try{u=new URL(value.trim());}catch{throw new Error('Укажите полный HTTPS-адрес контроллера');}
 if(u.protocol!=='https:'||!u.hostname||u.username||u.password||u.search||u.hash||u.pathname!=='/')throw new Error('Нужен HTTPS-адрес без пути, query и учётных данных');
 return u.origin;
}
export function discoveryProfile(address:string,ca:string){
 if(!ca.includes('-----BEGIN CERTIFICATE-----')||ca.includes('PRIVATE KEY'))throw new Error('Требуется публичный сертификат CA, не приватный ключ');
 return {controller_url:normalizedProfileURL(address),ca_cert_pem:ca,auto_discover:true};
}
